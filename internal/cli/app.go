// Package cli implements the project-aware facets command-line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/status"
	"facets.barnlab.dev/internal/store"
)

var errProjectNotDiscovered = errors.New("project not discovered")

// App contains the dependencies needed to run the CLI. Provider is convenient
// for a single-provider embedding; Registry is used when more than one provider
// is available.
type App struct {
	Registry                    *project.Registry
	Provider                    project.Provider
	Summary                     *status.Builder
	ProjectStore                *store.Store
	Stdout                      io.Writer
	Stderr                      io.Writer
	Stdin                       io.Reader
	Interactive                 func() bool
	Cwd                         string
	Env                         map[string]string
	Getenv                      func(string) string
	Executable                  string
	Serve                       func(context.Context, string) error
	TaskDaemonInterval          time.Duration
	TaskDaemonRefreshTimeout    time.Duration
	TaskDaemonHeartbeatInterval time.Duration
	TaskDaemonLockPath          string
}

type runConfig struct {
	project   string
	provider  string
	format    string
	formatSet bool
}

type trackingWriter struct {
	writer io.Writer
	err    error
}

func (w *trackingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

// Run executes args and returns an AXI exit code: 0 for success, 1 for an
// operational failure, and 2 for invalid usage.
func (a *App) Run(ctx context.Context, args []string) (code int) {
	stdout := a.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	trackedStdout := &trackingWriter{writer: stdout}
	stdout = trackedStdout
	defer func() {
		if trackedStdout.err != nil {
			code = 1
		}
	}()
	stderr := a.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	global := flag.NewFlagSet("facets", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	var cfg runConfig
	var jsonAlias bool
	global.StringVar(&cfg.project, "project", "", "project ID (default: discovered from the workspace)")
	global.StringVar(&cfg.provider, "provider", "kata", "provider name")
	global.StringVar(&cfg.format, "format", "toon", "output format: human, json, or toon")
	global.BoolVar(&jsonAlias, "json", false, "alias for --format json")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.write(stdout, normalizedFormat(cfg.format, jsonAlias), topHelp())
			return 0
		}
		a.usageError(stdout, normalizedFormat(cfg.format, jsonAlias), err.Error(), "Run `facets help` for command usage")
		return 2
	}
	global.Visit(func(flag *flag.Flag) {
		if flag.Name == "format" {
			cfg.formatSet = true
		}
	})
	if jsonAlias {
		if cfg.format != "toon" && cfg.format != "json" {
			a.usageError(stdout, "toon", "--json cannot be combined with a non-JSON --format", "Use `--format json` or omit `--format`")
			return 2
		}
		cfg.format = "json"
	}
	if !validFormat(cfg.format) {
		a.usageError(stdout, "toon", fmt.Sprintf("invalid --format %q", cfg.format), "Use `--format human`, `--format json`, or `--format toon`")
		return 2
	}

	rest := global.Args()
	if len(rest) == 0 {
		return a.runHome(ctx, stdout, stderr, cfg)
	}

	switch rest[0] {
	case "help":
		if len(rest) != 1 {
			a.usageError(stdout, cfg.format, "help does not accept arguments", "Run `facets help`")
			return 2
		}
		a.write(stdout, cfg.format, topHelp())
		return 0
	case "tasks":
		return a.runTasks(ctx, stdout, stderr, cfg, rest[1:])
	case "projects":
		return a.runProjects(ctx, stdout, stderr, cfg, rest[1:])
	case "focus":
		return a.runFocus(ctx, stdout, stderr, cfg, rest[1:])
	case "today":
		return a.runToday(ctx, stdout, stderr, cfg, rest[1:])
	case "serve":
		return a.runServe(ctx, stdout, stderr, cfg, rest[1:])
	default:
		a.usageError(stdout, cfg.format, fmt.Sprintf("unknown command %q", rest[0]), "Run `facets help` to list commands")
		return 2
	}
}

func normalizedFormat(format string, jsonAlias bool) string {
	if jsonAlias || format == "json" {
		return "json"
	}
	if format == "human" {
		return "human"
	}
	return "toon"
}

func validFormat(format string) bool {
	return format == "human" || format == "json" || format == "toon"
}

func (a *App) runHome(ctx context.Context, stdout, stderr io.Writer, cfg runConfig) int {
	provider, code := a.selectProvider(stdout, cfg)
	if code != 0 {
		return code
	}
	projectID, code := a.projectID(ctx, stdout, stderr, cfg, provider.Name())
	if code != 0 {
		return code
	}
	taskCommand := selectedTaskCommand(cfg, projectID)
	status := project.StatusOpen
	tasks, err := provider.ListTasks(ctx, projectID, project.TaskFilter{Status: &status})
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not list open tasks", err, fmt.Sprintf("Retry with `%s`", taskCommand))
	}
	columns := []string{"id", "title", "status"}
	doc := object{
		{name: "bin", value: collapseHome(a.executable())},
		{name: "description", value: "View and update tasks for the current project"},
		{name: "project", value: projectID},
		{name: "count", value: len(tasks)},
		{name: "tasks", value: taskTable(tasks, columns)},
	}
	if len(tasks) == 0 {
		doc = append(doc, field{name: "message", value: fmt.Sprintf("0 open tasks found in project %q", projectID)})
		doc = append(doc, field{name: "help", value: primitiveArray{
			fmt.Sprintf("Run `%s create \"<title>\"` to create a task", taskCommand),
			fmt.Sprintf("Run `%s --status all` to include closed tasks", taskCommand),
		}})
	} else {
		doc = append(doc, field{name: "help", value: primitiveArray{
			fmt.Sprintf("Run `%s show <id>` to view task details", taskCommand),
			fmt.Sprintf("Run `%s create \"<title>\"` to create a task", taskCommand),
		}})
	}
	a.write(stdout, cfg.format, doc)
	return 0
}

func (a *App) runFocus(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	if len(args) == 1 && args[0] == "show" {
		return a.showFocus(ctx, stdout, stderr, cfg)
	}

	usageCommand := "facets focus"
	if len(args) == 1 && isHelp(args[0]) {
		a.write(stdout, cfg.format, focusHelp())
		return 0
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		a.usageError(stdout, cfg.format, "focus text is required as one argument", fmt.Sprintf("Run `%s \"<text>\"`", usageCommand))
		return 2
	}
	if a.ProjectStore == nil {
		return a.providerError(stdout, stderr, cfg.format, "could not save focus", errors.New("local project registry is not configured"), "Configure the local Facets database and retry")
	}
	created, err := a.ProjectStore.CreateDayFocus(ctx, args[0], time.Now())
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not save focus", err, fmt.Sprintf("Retry `%s \"<text>\"`", usageCommand))
	}
	a.write(stdout, cfg.format, object{
		{name: "focus", value: created.Focus},
		{name: "day_start", value: formatTime(created.DayStart)},
		{name: "created_at", value: formatTime(created.CreatedAt)},
		{name: "message", value: "Today's focus saved"},
	})
	return 0
}

func (a *App) showFocus(ctx context.Context, stdout, stderr io.Writer, cfg runConfig) int {
	if a.ProjectStore == nil {
		return a.providerError(stdout, stderr, cfg.format, "could not load today's focus", errors.New("local project registry is not configured"), "Configure the local Facets database and retry")
	}

	focus, err := a.ProjectStore.CurrentDayFocus(ctx, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		a.write(stdout, cfg.format, object{{name: "focus", value: nil}})
		return 0
	}
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not load today's focus", err, "Retry `facets focus show`")
	}
	a.write(stdout, cfg.format, object{
		{name: "focus", value: object{
			{name: "text", value: focus.Focus},
			{name: "day_start", value: formatTime(focus.DayStart)},
		}},
	})
	return 0
}

func (a *App) runTasks(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		a.write(stdout, cfg.format, tasksHelp())
		return 0
	}
	command := "list"
	if len(args) > 0 {
		switch args[0] {
		case "list", "show", "create", "edit", "top", "close", "comment", "reopen", "delete", "daemon":
			command = args[0]
			args = args[1:]
		default:
			if !strings.HasPrefix(args[0], "-") {
				a.usageError(stdout, cfg.format, fmt.Sprintf("unknown tasks command %q", args[0]), fmt.Sprintf("Run `%s --help`", a.taskUsageCommand(cfg)))
				return 2
			}
		}
	}
	if len(args) > 0 && isHelp(args[0]) {
		switch command {
		case "list":
			a.write(stdout, cfg.format, tasksListHelp())
		case "show":
			a.write(stdout, cfg.format, taskShowHelp())
		case "create":
			a.write(stdout, cfg.format, taskCreateHelp())
		case "edit":
			a.write(stdout, cfg.format, taskEditHelp())
		case "top":
			a.write(stdout, cfg.format, taskTopHelp())
		case "close":
			a.write(stdout, cfg.format, taskCloseHelp())
		case "comment":
			a.write(stdout, cfg.format, taskCommentHelp())
		case "reopen":
			a.write(stdout, cfg.format, taskReopenHelp())
		case "delete":
			a.write(stdout, cfg.format, taskDeleteHelp())
		case "daemon":
			a.write(stdout, cfg.format, taskDaemonHelp())
		}
		return 0
	}
	switch command {
	case "list":
		return a.listTasks(ctx, stdout, stderr, cfg, args)
	case "show":
		return a.showTask(ctx, stdout, stderr, cfg, args)
	case "create":
		return a.createTask(ctx, stdout, stderr, cfg, args)
	case "edit":
		return a.editTask(ctx, stdout, stderr, cfg, args)
	case "top":
		return a.setTopTask(ctx, stdout, stderr, cfg, args)
	case "close":
		return a.closeTask(ctx, stdout, stderr, cfg, args)
	case "comment":
		return a.commentTask(ctx, stdout, stderr, cfg, args)
	case "reopen":
		return a.reopenTask(ctx, stdout, stderr, cfg, args)
	case "daemon":
		return a.runTaskDaemon(ctx, stdout, stderr, cfg, args)
	default:
		return a.deleteTask(ctx, stdout, stderr, cfg, args)
	}
}

func (a *App) listTasks(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg)
	fs := commandFlags(usageCommand + " list")
	statusValue := "open"
	fieldsValue := "id,title,status"
	allProjects := fs.Bool("all-projects", false, "list tasks across enabled projects")
	includeDisabled := fs.Bool("all", false, "include disabled projects with --all-projects")
	fs.StringVar(&statusValue, "status", "open", "open, closed, or all")
	fs.StringVar(&fieldsValue, "fields", "id,title,status", "comma-separated output fields")
	if code := a.parseFlags(stdout, cfg.format, fs, args, tasksListHelp()); code >= 0 {
		return code
	}
	var status *project.Status
	switch statusValue {
	case "open":
		value := project.StatusOpen
		status = &value
	case "closed":
		value := project.StatusClosed
		status = &value
	case "all":
	default:
		a.usageError(stdout, cfg.format, "--status must be open, closed, or all", fmt.Sprintf("Use `%s --status open|closed|all`", usageCommand))
		return 2
	}
	columns, err := parseTaskFields(fieldsValue)
	if err != nil {
		a.usageError(stdout, cfg.format, err.Error(), fmt.Sprintf("Use `%s --fields id,title,status` or choose from priority,assignee,updated", usageCommand))
		return 2
	}

	provider, code := a.selectProvider(stdout, cfg)
	if code != 0 {
		return code
	}
	projectID := "all"
	taskCommand := selectedFacetsCommand(cfg) + " tasks"
	var tasks []project.Task
	if *allProjects || *includeDisabled {
		projects, listErr := provider.ListProjects(ctx)
		if listErr != nil {
			return a.providerError(stdout, stderr, cfg.format, "could not list projects", listErr, fmt.Sprintf("Retry with `%s tasks list --all-projects`", selectedFacetsCommand(cfg)))
		}
		if a.ProjectStore != nil {
			if syncErr := a.ProjectStore.SyncProjects(ctx, provider.Name(), projects); syncErr != nil {
				return a.providerError(stdout, stderr, cfg.format, "could not save project registry", syncErr, fmt.Sprintf("Check the local database and retry `%s tasks list --all-projects`", selectedFacetsCommand(cfg)))
			}
		}
		for _, item := range projects {
			if !*includeDisabled && a.ProjectStore != nil {
				registered, lookupErr := a.ProjectStore.RegisteredProject(ctx, provider.Name(), item.ID)
				if lookupErr != nil {
					return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not load project %q metadata", item.ID), lookupErr, "Check the local project registry and retry")
				}
				if registered.DisabledAt != nil {
					continue
				}
			}
			projectTasks, listErr := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: status})
			if listErr != nil {
				return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not list tasks for project %q", item.ID), listErr, fmt.Sprintf("Retry with `%s tasks list --all-projects`", selectedFacetsCommand(cfg)))
			}
			for _, task := range projectTasks {
				if !strings.Contains(task.ID, "#") {
					task.ID = item.ID + "#" + task.ID
				}
				tasks = append(tasks, task)
			}
		}
	} else {
		var taskProjectID string
		var taskCode int
		provider, taskProjectID, taskCommand, taskCode = a.taskDependencies(ctx, stdout, stderr, cfg)
		if taskCode != 0 {
			return taskCode
		}
		projectID = taskProjectID
		tasks, err = provider.ListTasks(ctx, projectID, project.TaskFilter{Status: status})
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, "could not list tasks", err, fmt.Sprintf("Retry with `%s --status %s`", taskCommand, shellQuote(statusValue)))
		}
	}
	doc := object{{name: "project", value: projectID}, {name: "count", value: len(tasks)}, {name: "tasks", value: taskTable(tasks, columns)}}
	if len(tasks) == 0 {
		scope := fmt.Sprintf("in project %q", projectID)
		if *allProjects || *includeDisabled {
			scope = "across all projects"
		}
		doc = append(doc, field{name: "message", value: fmt.Sprintf("0 %s tasks found %s", statusValue, scope)})
	}
	help := primitiveArray{
		fmt.Sprintf("Run `%s show <id>` to view task details", taskCommand),
		fmt.Sprintf("Run `%s create \"<title>\"` to create a task", taskCommand),
	}
	if *allProjects || *includeDisabled {
		help = primitiveArray{"Task IDs use project#task format", fmt.Sprintf("Run `%s tasks list --status all --all-projects` to include closed tasks", selectedFacetsCommand(cfg))}
	}
	doc = append(doc, field{name: "help", value: help})
	a.write(stdout, cfg.format, doc)
	return 0
}

func (a *App) showTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " show"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> [--full]`", usageCommand))
		return 2
	}
	id := args[0]
	fs := commandFlags(usageCommand)
	var full bool
	fs.BoolVar(&full, "full", false, "show the complete body")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskShowHelp()); code >= 0 {
		return code
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	task, err := provider.GetTask(ctx, projectID, id)
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not show task %q", id), err, fmt.Sprintf("Verify the ID with `%s`", taskCommand))
	}
	a.write(stdout, cfg.format, taskDocument(task, full, taskCommand))
	return 0
}

func (a *App) createTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " create"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task title is required", fmt.Sprintf("Run `%s \"<title>\" [flags]`", usageCommand))
		return 2
	}
	title := strings.TrimSpace(args[0])
	fs := commandFlags(usageCommand)
	var body, assignee, idempotency string
	var priority optionalPriority
	fs.StringVar(&body, "body", "", "task body")
	fs.Var(&priority, "priority", "priority from 0 to 4")
	fs.StringVar(&assignee, "assignee", "", "assignee")
	fs.StringVar(&idempotency, "idempotency-key", "", "idempotency key")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskCreateHelp()); code >= 0 {
		return code
	}
	if priority.err != nil {
		a.usageError(stdout, cfg.format, priority.err.Error(), fmt.Sprintf("Use `%s \"<title>\" --priority 0` through `--priority 4`", usageCommand))
		return 2
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	created, err := provider.CreateTask(ctx, projectID, project.TaskInput{Title: title, Description: body, Priority: priority.value, Assignee: assignee, IdempotencyKey: idempotency})
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not create task", err, fmt.Sprintf("Review `%s create --help` and try again", taskCommand))
	}
	a.write(stdout, cfg.format, taskDocument(created, true, taskCommand))
	return 0
}

func (a *App) editTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " edit"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> [flags]`", usageCommand))
		return 2
	}
	id := args[0]
	fs := commandFlags(usageCommand)
	var title, body, assignee optionalString
	priority := optionalPriority{allowClear: true}
	fs.Var(&title, "title", "replacement title")
	fs.Var(&body, "body", "replacement body (empty clears)")
	fs.Var(&priority, "priority", "replacement priority 0..4, or - to clear")
	fs.Var(&assignee, "assignee", "replacement assignee (empty clears)")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskEditHelp()); code >= 0 {
		return code
	}
	if priority.err != nil {
		a.usageError(stdout, cfg.format, priority.err.Error(), fmt.Sprintf("Use `%s <id> --priority 0` through `--priority 4`, or `--priority -` to clear", usageCommand))
		return 2
	}
	if title.set && strings.TrimSpace(title.value) == "" {
		a.usageError(stdout, cfg.format, "--title must not be empty", fmt.Sprintf("Provide a non-empty title with `%s <id> --title \"<title>\"`", usageCommand))
		return 2
	}
	if !title.set && !body.set && !priority.set && !assignee.set {
		a.usageError(stdout, cfg.format, "at least one edit flag is required", fmt.Sprintf("Run `%s <id> --title \"<title>\"`, or set --body, --priority, or --assignee", usageCommand))
		return 2
	}
	patch := project.TaskPatch{Priority: project.PriorityPatch{Set: priority.set, Value: priority.value}}
	if title.set {
		patch.Title = &title.value
	}
	if body.set {
		patch.Description = &body.value
	}
	if assignee.set {
		patch.Assignee = &assignee.value
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	updated, err := provider.UpdateTask(ctx, projectID, id, patch)
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not edit task %q", id), err, fmt.Sprintf("Review values with `%s edit %s --help`", taskCommand, shellQuote(id)))
	}
	a.write(stdout, cfg.format, taskDocument(updated, true, taskCommand))
	return 0
}

func (a *App) setTopTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " top"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> --set true|false`", usageCommand))
		return 2
	}
	id := strings.TrimSpace(args[0])
	fs := commandFlags(usageCommand)
	var value string
	fs.StringVar(&value, "set", "", "set facets.top to true or false")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskTopHelp()); code >= 0 {
		return code
	}
	if value != "true" && value != "false" {
		a.usageError(stdout, cfg.format, "--set must be true or false", fmt.Sprintf("Run `%s %s --set true|false`", usageCommand, shellQuote(id)))
		return 2
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	updated, err := provider.UpdateTask(ctx, projectID, id, project.TaskPatch{
		Metadata: map[string]any{"facets.top": value},
	})
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not update top-task state for %q", id), err, fmt.Sprintf("Retry `%s %s --set %s`", taskCommand, shellQuote(id), value))
	}
	a.write(stdout, cfg.format, taskDocument(updated, true, taskCommand))
	return 0
}

func (a *App) closeTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " close"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> --message \"...\" --evidence \"test:go test ./...\"`", usageCommand))
		return 2
	}
	id := args[0]
	fs := commandFlags(usageCommand)
	var message string
	var comment optionalString
	var evidence stringList
	fs.StringVar(&message, "message", "", "completion message (required)")
	fs.Var(&comment, "comment", "optional comment appended while closing")
	fs.Var(&evidence, "evidence", "typed completion evidence such as test:<command>, commit:<sha>, or pr:<url> (required, repeatable)")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskCloseHelp()); code >= 0 {
		return code
	}
	if strings.TrimSpace(message) == "" {
		a.usageError(stdout, cfg.format, "--message is required", fmt.Sprintf("Provide `%s <id> --message \"what was completed\" --evidence \"test:go test ./...\"`", usageCommand))
		return 2
	}
	if len(evidence) == 0 {
		a.usageError(stdout, cfg.format, "at least one --evidence is required", fmt.Sprintf("Provide `%s <id> --message \"what was completed\" --evidence \"test:go test ./...\"`", usageCommand))
		return 2
	}
	for _, item := range evidence {
		if strings.TrimSpace(item) == "" {
			a.usageError(stdout, cfg.format, "--evidence must not be empty", fmt.Sprintf("Provide typed evidence such as `%s <id> --message \"done\" --evidence \"commit:<sha>\"`", usageCommand))
			return 2
		}
	}
	if comment.set && strings.TrimSpace(comment.value) == "" {
		a.usageError(stdout, cfg.format, "--comment must not be empty", fmt.Sprintf("Provide `%s <id> --message \"done\" --evidence \"test:focused\" --comment \"<text>\"`, or omit --comment", usageCommand))
		return 2
	}
	closed := project.StatusClosed
	patch := project.TaskPatch{Status: &closed, Completion: &project.Completion{Message: strings.TrimSpace(message), Evidence: []string(evidence), Comment: strings.TrimSpace(comment.value)}}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	updated, err := provider.UpdateTask(ctx, projectID, id, patch)
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not close task %q", id), err, fmt.Sprintf("Review completion values with `%s close %s --help`", taskCommand, shellQuote(id)))
	}
	a.write(stdout, cfg.format, taskDocument(updated, true, taskCommand))
	return 0
}

func (a *App) commentTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " comment"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> --body \"<text>\"`", usageCommand))
		return 2
	}
	id := strings.TrimSpace(args[0])
	fs := commandFlags(usageCommand)
	var body optionalString
	fs.Var(&body, "body", "comment body (required)")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskCommentHelp()); code >= 0 {
		return code
	}
	if !body.set || strings.TrimSpace(body.value) == "" {
		a.usageError(stdout, cfg.format, "--body is required and must not be empty", fmt.Sprintf("Provide `%s %s --body \"<text>\"`", usageCommand, shellQuote(id)))
		return 2
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	updated, err := provider.CommentTask(ctx, projectID, id, strings.TrimSpace(body.value))
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not comment on task %q", id), err, fmt.Sprintf("Review the task and comment with `%s comment %s --help`", taskCommand, shellQuote(id)))
	}
	a.write(stdout, cfg.format, taskDocument(updated, true, taskCommand))
	return 0
}

func (a *App) reopenTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " reopen"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id>`", usageCommand))
		return 2
	}
	if len(args) == 2 && isHelp(args[1]) {
		a.write(stdout, cfg.format, taskReopenHelp())
		return 0
	}
	if len(args) != 1 {
		a.usageError(stdout, cfg.format, "tasks reopen accepts only a task ID", fmt.Sprintf("Run `%s <id>`", usageCommand))
		return 2
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	open := project.StatusOpen
	updated, err := provider.UpdateTask(ctx, projectID, args[0], project.TaskPatch{Status: &open})
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not reopen task %q", args[0]), err, fmt.Sprintf("Verify the task ID with `%s --status closed`", taskCommand))
	}
	a.write(stdout, cfg.format, taskDocument(updated, true, taskCommand))
	return 0
}

func (a *App) deleteTask(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := a.taskUsageCommand(cfg) + " delete"
	if missingRequiredArg(args) {
		a.usageError(stdout, cfg.format, "task ID is required", fmt.Sprintf("Run `%s <id> --confirm <id>`", usageCommand))
		return 2
	}
	id := args[0]
	fs := commandFlags(usageCommand)
	var confirm string
	fs.StringVar(&confirm, "confirm", "", "exact task ID (required)")
	if code := a.parseFlags(stdout, cfg.format, fs, args[1:], taskDeleteHelp()); code >= 0 {
		return code
	}
	if confirm != id {
		a.usageError(stdout, cfg.format, "--confirm must exactly match the task ID", fmt.Sprintf("Run `%s %s --confirm %s`", usageCommand, shellQuote(id), shellQuote(id)))
		return 2
	}
	provider, projectID, taskCommand, code := a.taskDependencies(ctx, stdout, stderr, cfg)
	if code != 0 {
		return code
	}
	if err := provider.DeleteTask(ctx, projectID, id); err != nil {
		return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not delete task %q", id), err, fmt.Sprintf("Verify the task ID with `%s --status all`", taskCommand))
	}
	a.write(stdout, cfg.format, object{{name: "deleted", value: object{{name: "id", value: id}, {name: "project", value: projectID}}}})
	return 0
}

func (a *App) runProjects(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		a.write(stdout, cfg.format, projectsHelp())
		return 0
	}
	switch args[0] {
	case "list":
		fs := commandFlags(a.projectUsageCommand(cfg) + " list")
		showAll := fs.Bool("all", false, "include disabled projects at the end")
		if code := a.parseFlags(stdout, cfg.format, fs, args[1:], projectsListHelp()); code >= 0 {
			return code
		}
		provider, code := a.selectProvider(stdout, cfg)
		if code != 0 {
			return code
		}
		items, err := provider.ListProjects(ctx)
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, "could not list projects", err, fmt.Sprintf("Check provider configuration and retry `%s projects list`", selectedFacetsCommand(cfg)))
		}
		if a.ProjectStore != nil {
			if err := a.ProjectStore.SyncProjects(ctx, provider.Name(), items); err != nil {
				return a.providerError(stdout, stderr, cfg.format, "could not save project registry", err, fmt.Sprintf("Check the local database and retry `%s projects list`", selectedFacetsCommand(cfg)))
			}
		}
		activeRows := make([][]any, 0, len(items))
		disabledRows := make([][]any, 0)
		for _, item := range items {
			disabled := false
			if a.ProjectStore != nil {
				registered, lookupErr := a.ProjectStore.RegisteredProject(ctx, provider.Name(), item.ID)
				if lookupErr != nil {
					return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not load project %q metadata", item.ID), lookupErr, "Check the local project registry and retry")
				}
				disabled = registered.DisabledAt != nil
			}
			row := []any{item.ID, item.Name}
			if disabled {
				row[1] = "(" + item.Name + ")"
				disabledRows = append(disabledRows, row)
			} else {
				activeRows = append(activeRows, row)
			}
		}
		rows := activeRows
		if *showAll {
			rows = append(rows, disabledRows...)
		}
		doc := object{{name: "count", value: len(rows)}, {name: "projects", value: table{columns: []string{"id", "name"}, rows: rows}}}
		if len(rows) == 0 {
			doc = append(doc, field{name: "message", value: "0 projects found"})
		}
		projectCommand := selectedFacetsCommand(cfg) + " projects"
		doc = append(doc, field{name: "help", value: primitiveArray{fmt.Sprintf("Run `%s show <id>` to view project details", projectCommand)}})
		a.write(stdout, cfg.format, doc)
		return 0
	case "disable", "enable":
		command := args[0]
		usageCommand := a.projectUsageCommand(cfg) + " " + command
		if (len(args) == 2 && isHelp(args[1])) || (len(args) == 1 && isHelp(args[0])) {
			if command == "disable" {
				a.write(stdout, cfg.format, projectDisableHelp())
			} else {
				a.write(stdout, cfg.format, projectEnableHelp())
			}
			return 0
		}
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" || strings.HasPrefix(args[1], "-") {
			a.usageError(stdout, cfg.format, "project ID is required", fmt.Sprintf("Run `%s <id>`", usageCommand))
			return 2
		}
		if a.ProjectStore == nil {
			return a.providerError(stdout, stderr, cfg.format, "project registry is unavailable", errors.New("local project registry is not configured"), "Configure the local Facets database and retry")
		}
		provider, code := a.selectProvider(stdout, cfg)
		if code != 0 {
			return code
		}
		items, err := provider.ListProjects(ctx)
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, "could not list projects", err, fmt.Sprintf("Check provider configuration and retry `%s projects list`", selectedFacetsCommand(cfg)))
		}
		if err := a.ProjectStore.SyncProjects(ctx, provider.Name(), items); err != nil {
			return a.providerError(stdout, stderr, cfg.format, "could not save project registry", err, fmt.Sprintf("Check the local database and retry `%s projects list`", selectedFacetsCommand(cfg)))
		}
		registered, err := a.ProjectStore.SetProjectDisabled(ctx, provider.Name(), args[1], command == "disable")
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not %s project %q", command, args[1]), err, fmt.Sprintf("Verify the ID with `%s projects list`", selectedFacetsCommand(cfg)))
		}
		a.write(stdout, cfg.format, registeredProjectDocument(registered))
		return 0
	case "set":
		if (len(args) == 2 && isHelp(args[1])) || (len(args) == 3 && isHelp(args[2])) {
			a.write(stdout, cfg.format, projectSetHelp())
			return 0
		}
		if len(args) != 3 || strings.TrimSpace(args[1]) == "" || strings.HasPrefix(args[1], "-") {
			a.usageError(stdout, cfg.format, "project ID and key=value are required", "Run `facets projects set <id> directory=<path>`")
			return 2
		}
		key, value, ok := strings.Cut(args[2], "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			a.usageError(stdout, cfg.format, "project setting must be key=value", "Run `facets projects set <id> directory=<path>`")
			return 2
		}
		if key != "directory" {
			a.usageError(stdout, cfg.format, fmt.Sprintf("unknown project setting %q", key), "The supported setting is `directory=<path>`")
			return 2
		}
		if a.ProjectStore == nil {
			return a.providerError(stdout, stderr, cfg.format, "project registry is unavailable", errors.New("local project registry is not configured"), "Configure the local Facets database and retry")
		}
		provider, code := a.selectProvider(stdout, cfg)
		if code != 0 {
			return code
		}
		directory, err := a.resolveDirectory(value)
		if err != nil {
			a.usageError(stdout, cfg.format, fmt.Sprintf("could not resolve project directory: %v", err), "Use an existing absolute or relative directory path")
			return 2
		}
		registered, err := a.ProjectStore.SetProjectMetadata(ctx, provider.Name(), args[1], key, directory)
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not set project %q metadata", args[1]), err, fmt.Sprintf("Run `%s projects list` first, then retry", selectedFacetsCommand(cfg)))
		}
		a.write(stdout, cfg.format, registeredProjectDocument(registered))
		return 0
	case "show":
		usageCommand := a.projectUsageCommand(cfg) + " show"
		if (len(args) == 2 && isHelp(args[1])) || (len(args) == 3 && isHelp(args[2])) {
			a.write(stdout, cfg.format, projectShowHelp())
			return 0
		}
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" || strings.HasPrefix(args[1], "-") {
			a.usageError(stdout, cfg.format, "project ID is required", fmt.Sprintf("Run `%s <id>`", usageCommand))
			return 2
		}
		provider, code := a.selectProvider(stdout, cfg)
		if code != 0 {
			return code
		}
		item, err := provider.GetProject(ctx, args[1])
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not show project %q", args[1]), err, fmt.Sprintf("Verify the ID with `%s projects list`", selectedFacetsCommand(cfg)))
		}
		builder := a.Summary
		if builder == nil {
			builder = status.NewBuilder()
		}
		root := a.Cwd
		if a.ProjectStore != nil {
			root, err = a.projectDirectory(ctx, provider.Name(), args[1], root)
			if err != nil {
				return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not load project %q metadata", args[1]), err, fmt.Sprintf("Retry `%s projects show %s`", selectedFacetsCommand(cfg), shellQuote(args[1])))
			}
		} else if strings.TrimSpace(root) == "" {
			root = "."
		}
		summary, err := builder.Build(ctx, provider, root, args[1])
		if err != nil {
			return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not summarize project %q", args[1]), err, fmt.Sprintf("Retry `%s projects show %s`", selectedFacetsCommand(cfg), shellQuote(args[1])))
		}
		var registered *store.RegisteredProject
		if a.ProjectStore != nil {
			record, lookupErr := a.ProjectStore.RegisteredProject(ctx, provider.Name(), args[1])
			if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
				return a.providerError(stdout, stderr, cfg.format, fmt.Sprintf("could not load project %q metadata", args[1]), lookupErr, fmt.Sprintf("Retry `%s projects show %s`", selectedFacetsCommand(cfg), shellQuote(args[1])))
			}
			if lookupErr == nil {
				registered = &record
			}
		}
		doc := projectDocument(item, registered)
		doc = append(doc, field{name: "status", value: statusDocument(summary)})
		a.write(stdout, cfg.format, doc)
		return 0
	default:
		a.usageError(stdout, cfg.format, fmt.Sprintf("unknown projects command %q", args[0]), "Run `facets projects --help`")
		return 2
	}
}

func (a *App) resolveDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("directory must not be empty")
	}
	base := strings.TrimSpace(a.Cwd)
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	value, err = filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", value)
	}
	return filepath.Clean(value), nil
}

func (a *App) projectDirectory(ctx context.Context, source, id, fallback string) (string, error) {
	registered, err := a.ProjectStore.RegisteredProject(ctx, source, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	directory, ok := registered.Directory()
	if !ok {
		return "", nil
	}
	if filepath.IsAbs(directory) {
		return filepath.Clean(directory), nil
	}
	base := strings.TrimSpace(fallback)
	if base == "" {
		base, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return filepath.Clean(filepath.Join(base, directory)), nil
}

func registeredProjectDocument(registered store.RegisteredProject) object {
	metadata := object{}
	if directory, ok := registered.Metadata["directory"].(string); ok {
		metadata = append(metadata, field{name: "directory", value: directory})
	}
	project := object{
		{name: "id", value: registered.ID},
		{name: "name", value: registered.Name},
		{name: "source", value: registered.Source},
		{name: "first_seen", value: formatTime(registered.FirstSeen)},
		{name: "last_seen", value: formatTime(registered.LastSeen)},
		{name: "metadata", value: metadata},
		{name: "disabled", value: registered.DisabledAt != nil},
	}
	if registered.DisabledAt != nil {
		project = append(project, field{name: "disabled_at", value: formatTime(*registered.DisabledAt)})
	}
	return object{{name: "project", value: project}}
}

func (a *App) runServe(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	fs := commandFlags("facets serve")
	address := a.getenv("FACETS_ADDR")
	if address == "" {
		address = ":8080"
	}
	fs.StringVar(&address, "addr", address, "listen address")
	if code := a.parseFlags(stdout, cfg.format, fs, args, serveHelp(address)); code >= 0 {
		return code
	}
	if strings.TrimSpace(address) == "" {
		a.usageError(stdout, cfg.format, "--addr must not be empty", "Provide `--addr :8080`")
		return 2
	}
	if a.Serve == nil {
		return a.providerError(stdout, stderr, cfg.format, "server is unavailable", errors.New("serve callback is nil"), "Use a facets build with server support")
	}
	if err := a.Serve(ctx, address); err != nil {
		return a.providerError(stdout, stderr, cfg.format, "server stopped unexpectedly", err, "Check the listen address and try again")
	}
	return 0
}

func commandFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func (a *App) parseFlags(stdout io.Writer, format string, fs *flag.FlagSet, args []string, help object) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.write(stdout, format, help)
			return 0
		}
		a.usageError(stdout, format, err.Error(), fmt.Sprintf("Run `%s --help`", fs.Name()))
		return 2
	}
	if fs.NArg() != 0 {
		a.usageError(stdout, format, fmt.Sprintf("unexpected argument %q", fs.Arg(0)), fmt.Sprintf("Run `%s --help`", fs.Name()))
		return 2
	}
	return -1
}

func (a *App) selectProvider(stdout io.Writer, cfg runConfig) (project.Provider, int) {
	if a.Registry != nil {
		provider, err := a.Registry.Provider(cfg.provider)
		if err == nil {
			return provider, 0
		}
		a.usageError(stdout, cfg.format, fmt.Sprintf("provider %q is not available", cfg.provider), "Choose a provider with `--provider <name>`")
		return nil, 2
	}
	if a.Provider != nil && a.Provider.Name() == cfg.provider {
		return a.Provider, 0
	}
	a.usageError(stdout, cfg.format, fmt.Sprintf("provider %q is not available", cfg.provider), "Choose a provider with `--provider <name>`")
	return nil, 2
}

func (a *App) taskDependencies(ctx context.Context, stdout, stderr io.Writer, cfg runConfig) (project.Provider, string, string, int) {
	provider, code := a.selectProvider(stdout, cfg)
	if code != 0 {
		return nil, "", "", code
	}
	projectID, code := a.projectID(ctx, stdout, stderr, cfg, provider.Name())
	if code != 0 {
		return nil, "", "", code
	}
	return provider, projectID, selectedTaskCommand(cfg, projectID), 0
}

func (a *App) projectID(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, source string) (string, int) {
	var mapped func(string) (string, bool, error)
	if a.ProjectStore != nil {
		mapped = func(cwd string) (string, bool, error) {
			registered, err := a.ProjectStore.RegisteredProjects(ctx, source)
			if err != nil {
				return "", false, err
			}
			return mappedProject(cwd, registered)
		}
	}
	id, err := discoverProject(a.Cwd, cfg.project, a.getenv, mapped)
	if err == nil {
		return id, 0
	}
	if errors.Is(err, errProjectNotDiscovered) {
		a.usageError(stdout, cfg.format, "no project could be discovered from the current workspace", "Pass `--project <id>` or set FACETS_PROJECT")
		return "", 2
	}
	if a.debug() {
		fmt.Fprintf(stderr, "facets debug: project discovery: %v\n", err)
	}
	a.write(stdout, cfg.format, errorDocument("operational", "could not inspect the current workspace", "Pass `--project <id>` to select a project explicitly"))
	return "", 1
}

func (a *App) providerError(stdout, stderr io.Writer, format, message string, err error, help string) int {
	if a.debug() {
		fmt.Fprintf(stderr, "facets debug: %v\n", err)
	}
	if errors.Is(err, project.ErrNotFound) {
		message = strings.Replace(message, "could not show", "not found:", 1)
	}
	a.write(stdout, format, errorDocument("operational", message, help))
	return 1
}

func (a *App) usageError(stdout io.Writer, format, message, help string) {
	a.write(stdout, format, errorDocument("usage", message, help))
}

func errorDocument(kind, message, help string) object {
	return object{{name: "error", value: object{{name: "type", value: kind}, {name: "message", value: message}}}, {name: "help", value: help}}
}

func (a *App) write(w io.Writer, format string, doc object) {
	if err := writeDocument(w, format, doc); err != nil {
		if tracked, ok := w.(*trackingWriter); ok && tracked.err == nil {
			tracked.err = err
		}
	}
}

func (a *App) getenv(key string) string {
	if a.Env != nil {
		return a.Env[key]
	}
	if a.Getenv != nil {
		return a.Getenv(key)
	}
	return os.Getenv(key)
}
func (a *App) debug() bool { value, _ := strconv.ParseBool(a.getenv("FACETS_DEBUG")); return value }
func (a *App) executable() string {
	if a.Executable != "" {
		if absolute, err := filepath.Abs(a.Executable); err == nil {
			return absolute
		}
		return a.Executable
	}
	if executable, err := os.Executable(); err == nil {
		return executable
	}
	return "facets"
}

func collapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_./:@+-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func selectedFacetsCommand(cfg runConfig) string {
	command := "facets"
	if cfg.provider != "kata" {
		command += " --provider " + shellQuote(cfg.provider)
	}
	return command
}

func selectedTaskCommand(cfg runConfig, projectID string) string {
	command := "facets --project " + shellQuote(projectID)
	if cfg.provider != "kata" {
		command += " --provider " + shellQuote(cfg.provider)
	}
	return command + " tasks"
}

func (a *App) configuredProject(cfg runConfig) string {
	if projectID := strings.TrimSpace(cfg.project); projectID != "" {
		return projectID
	}
	return strings.TrimSpace(a.getenv("FACETS_PROJECT"))
}

func (a *App) taskUsageCommand(cfg runConfig) string {
	command := "facets"
	if projectID := a.configuredProject(cfg); projectID != "" {
		command += " --project " + shellQuote(projectID)
	}
	if cfg.provider != "kata" {
		command += " --provider " + shellQuote(cfg.provider)
	}
	return command + " tasks"
}

func (a *App) projectUsageCommand(cfg runConfig) string {
	command := "facets"
	if projectID := a.configuredProject(cfg); projectID != "" {
		command += " --project " + shellQuote(projectID)
	}
	if cfg.provider != "kata" {
		command += " --provider " + shellQuote(cfg.provider)
	}
	return command + " projects"
}
func parseTaskFields(value string) ([]string, error) {
	allowed := map[string]bool{"id": true, "title": true, "status": true, "priority": true, "assignee": true, "updated": true}
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("--fields must not be empty")
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return nil, errors.New("--fields must not be empty")
	}
	seen := make(map[string]bool, len(parts))
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !allowed[part] {
			return nil, fmt.Errorf("unknown task field %q", part)
		}
		if seen[part] {
			return nil, fmt.Errorf("duplicate task field %q", part)
		}
		seen[part] = true
		columns = append(columns, part)
	}
	return columns, nil
}

func taskTable(tasks []project.Task, columns []string) table {
	rows := make([][]any, len(tasks))
	for i, task := range tasks {
		row := make([]any, len(columns))
		for j, column := range columns {
			switch column {
			case "id":
				row[j] = task.ID
			case "title":
				row[j] = task.Title
			case "status":
				row[j] = string(task.Status)
			case "priority":
				if task.Priority != nil {
					row[j] = *task.Priority
				}
			case "assignee":
				row[j] = task.Assignee
			case "updated":
				row[j] = formatTime(task.UpdatedAt)
			}
		}
		rows[i] = row
	}
	return table{columns: columns, rows: rows}
}

func taskDocument(task project.Task, full bool, taskCommand string) object {
	body := task.Description
	characterCount := 0
	truncated := false
	if utf8.ValidString(body) {
		runes := []rune(body)
		characterCount = len(runes)
		truncated = !full && characterCount > 1000
		if truncated {
			body = string(runes[:1000])
		}
	}
	taskObject := object{{name: "id", value: task.ID}, {name: "project", value: task.ProjectID}, {name: "title", value: task.Title}, {name: "status", value: string(task.Status)}, {name: "body", value: body}, {name: "priority", value: priorityValue(task.Priority)}, {name: "assignee", value: task.Assignee}, {name: "created", value: formatTime(task.CreatedAt)}, {name: "updated", value: formatTime(task.UpdatedAt)}}
	doc := object{{name: "task", value: taskObject}}
	if truncated {
		doc = append(doc, field{name: "body_chars", value: characterCount}, field{name: "help", value: fmt.Sprintf("Run `%s show %s --full` to see the complete body", taskCommand, shellQuote(task.ID))})
	}
	return doc
}

func projectDocument(item project.Project, registered *store.RegisteredProject) object {
	projectFields := object{
		{name: "id", value: item.ID},
		{name: "name", value: item.Name},
		{name: "description", value: item.Description},
		{name: "created", value: formatTime(item.CreatedAt)},
		{name: "updated", value: formatTime(item.UpdatedAt)},
	}
	if registered != nil {
		projectFields = append(projectFields,
			field{name: "source", value: registered.Source},
			field{name: "first_seen", value: formatTime(registered.FirstSeen)},
			field{name: "last_seen", value: formatTime(registered.LastSeen)},
			field{name: "metadata", value: metadataDocument(registered.Metadata)},
		)
	}
	return object{{name: "project", value: projectFields}}
}

func metadataDocument(metadata map[string]any) object {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(object, 0, len(keys))
	for _, key := range keys {
		result = append(result, field{name: key, value: metadata[key]})
	}
	return result
}

func statusDocument(summary status.Summary) object {
	return object{
		{name: "period_days", value: summary.PeriodDays},
		{name: "since", value: formatTime(summary.Since)},
		{name: "tasks", value: object{
			{name: "total", value: summary.Tasks.Total},
			{name: "open", value: summary.Tasks.Open},
			{name: "closed", value: summary.Tasks.Closed},
			{name: "open_by_priority", value: priorityCounts(summary.Tasks.OpenByPriority)},
			{name: "closed_by_priority", value: priorityCounts(summary.Tasks.ClosedByPriority)},
		}},
		{name: "activity", value: object{
			{name: "commits", value: summary.Activity.Commits},
			{name: "sessions", value: object{
				{name: "codex", value: summary.Activity.Sessions["codex"]},
				{name: "omp", value: sessionCount(summary.Activity.Sessions["omp"])},
			}},
		}},
	}
}

func priorityCounts(counts map[int]int) object {
	if len(counts) == 0 {
		return object{}
	}
	priorities := make([]int, 0, len(counts))
	for priority := range counts {
		priorities = append(priorities, priority)
	}
	sort.Ints(priorities)
	countsObject := make(object, 0, len(priorities))
	for _, priority := range priorities {
		countsObject = append(countsObject, field{name: strconv.Itoa(priority), value: counts[priority]})
	}
	return countsObject
}
func sessionCount(value int) any {
	if value == status.UnknownSessionCount {
		return "??"
	}
	return value
}
func priorityValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
func missingRequiredArg(args []string) bool {
	return len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-")
}

func isHelp(value string) bool { return value == "--help" || value == "-h" || value == "help" }

type optionalString struct {
	value string
	set   bool
}

func (v *optionalString) String() string         { return v.value }
func (v *optionalString) Set(value string) error { v.value = value; v.set = true; return nil }

type optionalPriority struct {
	value      *int
	set        bool
	allowClear bool
	err        error
}

func (v *optionalPriority) String() string {
	if v.value == nil {
		return ""
	}
	return strconv.Itoa(*v.value)
}
func (v *optionalPriority) Set(value string) error {
	v.set = true
	if value == "-" {
		if !v.allowClear {
			v.err = errors.New("--priority must be an integer from 0 to 4")
			return nil
		}
		v.value = nil
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 4 {
		message := "--priority must be an integer from 0 to 4"
		if v.allowClear {
			message += ", or - to clear"
		}
		v.err = errors.New(message)
		return nil
	}
	v.value = &parsed
	return nil
}

type stringList []string

func (v *stringList) String() string         { return strings.Join(*v, ",") }
func (v *stringList) Set(value string) error { *v = append(*v, value); return nil }
