package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/store"
)

type topTask struct {
	Project project.Project
	Task    project.Task
}

type completionMetrics struct {
	DayStart time.Time
	DayEnd   time.Time
	All      int
	Top      int
}

// listTopTasks returns open tasks marked with facets.top=true. Providers may
// decode metadata booleans as either bool or the string "true"; missing or
// malformed metadata is ignored.
func listTopTasks(ctx context.Context, provider project.Provider) ([]topTask, error) {
	projects, err := provider.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	status := project.StatusOpen
	tasks := make([]topTask, 0)
	for _, item := range projects {
		items, err := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: &status})
		if err != nil {
			return nil, fmt.Errorf("list open tasks for project %q: %w", item.ID, err)
		}
		for _, task := range items {
			if task.Status != project.StatusOpen || !isTopTask(task) {
				continue
			}
			if task.ProjectID == "" {
				task.ProjectID = item.ID
			}
			tasks = append(tasks, topTask{Project: item, Task: task})
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Project.ID != tasks[j].Project.ID {
			return tasks[i].Project.ID < tasks[j].Project.ID
		}
		if tasks[i].Task.UpdatedAt.Equal(tasks[j].Task.UpdatedAt) {
			return tasks[i].Task.ID < tasks[j].Task.ID
		}
		return tasks[i].Task.UpdatedAt.After(tasks[j].Task.UpdatedAt)
	})
	return tasks, nil
}

func isTopTask(task project.Task) bool {
	value, ok := task.Metadata["facets.top"]
	if !ok {
		return false
	}
	switch top := value.(type) {
	case bool:
		return top
	case string:
		return strings.EqualFold(strings.TrimSpace(top), "true")
	default:
		return false
	}
}

// completedToday counts closed tasks whose UpdatedAt falls within the local
// calendar day represented by now. UpdatedAt is the provider's completion
// timestamp; open tasks and malformed or missing facets.top metadata are not
// counted as top tasks.
func completedToday(ctx context.Context, provider project.Provider, now time.Time) (completionMetrics, error) {
	if now.IsZero() {
		return completionMetrics{}, fmt.Errorf("current time is required")
	}
	local := now.In(now.Location())
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)
	projects, err := provider.ListProjects(ctx)
	if err != nil {
		return completionMetrics{}, fmt.Errorf("list projects: %w", err)
	}
	status := project.StatusClosed
	metrics := completionMetrics{DayStart: dayStart, DayEnd: dayEnd}
	for _, item := range projects {
		tasks, err := provider.ListTasks(ctx, item.ID, project.TaskFilter{Status: &status})
		if err != nil {
			return completionMetrics{}, fmt.Errorf("list closed tasks for project %q: %w", item.ID, err)
		}
		for _, task := range tasks {
			if task.Status != project.StatusClosed || task.UpdatedAt.Before(dayStart) || !task.UpdatedAt.Before(dayEnd) {
				continue
			}
			metrics.All++
			if isTopTask(task) {
				metrics.Top++
			}
		}
	}
	return metrics, nil
}
func (a *App) runToday(ctx context.Context, stdout, stderr io.Writer, cfg runConfig, args []string) int {
	usageCommand := "facets today"
	fs := commandFlags(usageCommand)
	var jsonAlias bool
	fs.StringVar(&cfg.format, "format", cfg.format, "output format: human, json, or toon")
	fs.BoolVar(&jsonAlias, "json", false, "alias for --format json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.write(stdout, normalizedFormat(cfg.format, jsonAlias), todayHelp())
			return 0
		}
		a.usageError(stdout, normalizedFormat(cfg.format, jsonAlias), err.Error(), fmt.Sprintf("Run `%s --help`", usageCommand))
		return 2
	}
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
	if fs.NArg() != 0 {
		a.usageError(stdout, cfg.format, "today does not accept arguments", fmt.Sprintf("Run `%s --help`", usageCommand))
		return 2
	}
	if a.ProjectStore == nil {
		return a.providerError(stdout, stderr, cfg.format, "could not load today's focus", errors.New("local project registry is not configured"), "Configure the local Facets database and retry")
	}

	now := time.Now()
	focus, err := a.ProjectStore.CurrentDayFocus(ctx, now)
	if errors.Is(err, store.ErrNotFound) {
		if !a.isInteractive() {
			a.usageError(stdout, cfg.format, "today's focus is not set", "Run `facets focus \"<text>\"` or use `facets today` interactively")
			return 2
		}
		focusText, readErr := a.promptTodayFocus(stderr)
		if readErr != nil {
			a.usageError(stdout, cfg.format, readErr.Error(), "Run `facets focus \"<text>\"` to set today's focus")
			return 2
		}
		focus, err = a.ProjectStore.CreateDayFocus(ctx, focusText, now)
	}
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not load today's focus", err, "Retry `facets today`")
	}

	provider, code := a.selectProvider(stdout, cfg)
	if code != 0 {
		return code
	}
	topTasks, err := listTopTasks(ctx, provider)
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not list top tasks", err, "Retry `facets today`")
	}
	metrics, err := completedToday(ctx, provider, now)
	if err != nil {
		return a.providerError(stdout, stderr, cfg.format, "could not calculate today's completion", err, "Retry `facets today`")
	}

	rows := make([][]any, len(topTasks))
	for i, item := range topTasks {
		rows[i] = []any{item.Project.ID, item.Project.Name, item.Task.ID, item.Task.Title}
	}
	a.write(stdout, cfg.format, object{
		{name: "focus", value: object{
			{name: "text", value: focus.Focus},
			{name: "day_start", value: formatTime(focus.DayStart)},
		}},
		{name: "top_tasks", value: table{
			columns: []string{"project", "project_name", "task", "title"},
			rows:    rows,
		}},
		{name: "completed_today", value: object{
			{name: "all", value: metrics.All},
			{name: "top", value: metrics.Top},
			{name: "day_start", value: formatTime(metrics.DayStart)},
			{name: "day_end", value: formatTime(metrics.DayEnd)},
		}},
	})
	return 0
}

func (a *App) isInteractive() bool {
	if a.Interactive != nil {
		return a.Interactive()
	}
	stdin := a.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (a *App) promptTodayFocus(stderr io.Writer) (string, error) {
	stdin := a.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	if _, err := fmt.Fprint(stderr, "Today's focus: "); err != nil {
		return "", fmt.Errorf("could not prompt for today's focus: %w", err)
	}
	value, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("could not read today's focus: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("today's focus must not be empty")
	}
	return value, nil
}
