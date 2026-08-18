package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/status"
	"facets.barnlab.dev/internal/store"
)

type fakeProvider struct {
	name               string
	projects           []project.Project
	tasks              []project.Task
	tasksByProject     map[string][]project.Task
	err                error
	listProjectID      string
	listFilter         project.TaskFilter
	createdProjectID   string
	created            project.TaskInput
	updatedProjectID   string
	updatedID          string
	patch              project.TaskPatch
	commentedProjectID string
	commentedID        string
	commentBody        string
	deletedProjectID   string
	deletedID          string
}

func (f *fakeProvider) Name() string {
	if f.name == "" {
		return "kata"
	}
	return f.name
}
func (f *fakeProvider) ListProjects(context.Context) ([]project.Project, error) {
	return f.projects, f.err
}
func (f *fakeProvider) GetProject(_ context.Context, id string) (project.Project, error) {
	if f.err != nil {
		return project.Project{}, f.err
	}
	for _, item := range f.projects {
		if item.ID == id {
			return item, nil
		}
	}
	return project.Project{}, project.ErrNotFound
}
func (f *fakeProvider) CreateProject(context.Context, project.ProjectInput) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (f *fakeProvider) UpdateProject(context.Context, string, project.ProjectPatch) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (f *fakeProvider) DeleteProject(context.Context, string) error { return project.ErrUnsupported }
func (f *fakeProvider) ListTasks(_ context.Context, projectID string, filter project.TaskFilter) ([]project.Task, error) {
	f.listProjectID, f.listFilter = projectID, filter
	if f.tasksByProject != nil {
		return f.tasksByProject[projectID], f.err
	}
	return f.tasks, f.err
}
func (f *fakeProvider) GetTask(_ context.Context, projectID, id string) (project.Task, error) {
	if f.err != nil {
		return project.Task{}, f.err
	}
	for _, item := range f.tasks {
		if item.ID == id {
			return item, nil
		}
	}
	return project.Task{}, project.ErrNotFound
}
func (f *fakeProvider) CreateTask(_ context.Context, projectID string, input project.TaskInput) (project.Task, error) {
	f.createdProjectID, f.created = projectID, input
	if f.err != nil {
		return project.Task{}, f.err
	}
	return project.Task{ID: "T-new", ProjectID: projectID, Title: input.Title, Description: input.Description, Status: project.StatusOpen, Priority: input.Priority, Assignee: input.Assignee}, nil
}
func (f *fakeProvider) UpdateTask(_ context.Context, projectID, id string, patch project.TaskPatch) (project.Task, error) {
	f.updatedProjectID, f.updatedID, f.patch = projectID, id, patch
	if f.err != nil {
		return project.Task{}, f.err
	}
	status := project.StatusOpen
	if patch.Status != nil {
		status = *patch.Status
	}
	return project.Task{ID: id, ProjectID: projectID, Title: "updated", Status: status}, nil
}
func (f *fakeProvider) CommentTask(_ context.Context, projectID, id, body string) (project.Task, error) {
	f.commentedProjectID, f.commentedID, f.commentBody = projectID, id, body
	if f.err != nil {
		return project.Task{}, f.err
	}
	return project.Task{ID: id, ProjectID: projectID, Title: "updated", Status: project.StatusOpen}, nil
}
func (f *fakeProvider) DeleteTask(_ context.Context, projectID, id string) error {
	f.deletedProjectID, f.deletedID = projectID, id
	return f.err
}

func runCLI(provider *fakeProvider, cwd string, env map[string]string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, Summary: &status.Builder{Activity: fakeActivitySource{}, Now: time.Now, PeriodDays: 30}, Stdout: &stdout, Stderr: &stderr, Cwd: cwd, Env: env, Executable: "/opt/facets/bin/facets"}
	code := app.Run(context.Background(), args)
	return code, stdout.String(), stderr.String()
}

type fakeActivitySource struct {
	activity status.Activity
	err      error
}

func (f fakeActivitySource) Summarize(_ context.Context, _ string, _ time.Time) (status.Activity, error) {
	return f.activity, f.err
}

func runCLIWithSummary(provider *fakeProvider, cwd string, builder *status.Builder, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, Summary: builder, Stdout: &stdout, Stderr: &stderr, Cwd: cwd, Env: map[string]string{}, Executable: "/opt/facets/bin/facets"}
	code := app.Run(context.Background(), args)
	return code, stdout.String(), stderr.String()
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func sampleTask() project.Task {
	priority := 2
	return project.Task{ID: "T-1", ProjectID: "demo", Title: "Fix, login", Description: "body", Status: project.StatusOpen, Priority: &priority, Assignee: "alice", UpdatedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}
}

func TestNoArgsShowsHomeAndOpenTasks(t *testing.T) {
	provider := &fakeProvider{tasks: []project.Task{sampleTask()}}
	code, stdout, stderr := runCLI(provider, t.TempDir(), map[string]string{"FACETS_PROJECT": "demo"})
	if code != 0 {
		t.Fatalf("code = %d, stdout = %s", code, stdout)
	}
	for _, want := range []string{"bin: \"/opt/facets/bin/facets\"", "description: \"View and update tasks for the current project\"", "tasks[1]{id,title,status}:", "\"T-1\",\"Fix, login\",\"open\""} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if provider.listFilter.Status == nil || *provider.listFilter.Status != project.StatusOpen {
		t.Fatalf("home filter = %#v", provider.listFilter)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestFocusCommandPersistsAndValidates(t *testing.T) {
	ctx := context.Background()
	registry, err := store.Open(ctx, filepath.Join(t.TempDir(), "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer registry.Close()

	var stdout, stderr bytes.Buffer
	app := App{ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, Env: map[string]string{}}
	if code := app.Run(ctx, []string{"focus", "--help"}); code != 0 || !strings.Contains(stdout.String(), "facets focus <text>") {
		t.Fatalf("focus help code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := app.Run(ctx, []string{"focus"}); code != 2 || !strings.Contains(stdout.String(), "focus text is required") {
		t.Fatalf("missing focus code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := app.Run(ctx, []string{"focus", " "}); code != 2 || !strings.Contains(stdout.String(), "focus text is required") {
		t.Fatalf("blank focus code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := app.Run(ctx, []string{"focus", "Plan the day"}); code != 0 || !strings.Contains(stdout.String(), "Today's focus saved") {
		t.Fatalf("save focus code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	current, err := registry.CurrentDayFocus(ctx, time.Now())
	if err != nil || current.Focus != "Plan the day" {
		t.Fatalf("CurrentDayFocus() = %#v, %v", current, err)
	}
}

func TestListTopTasksAcrossProjectsFiltersMetadataAndStatus(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	provider := &fakeProvider{
		projects: []project.Project{{ID: "beta", Name: "Beta"}, {ID: "alpha", Name: "Alpha"}},
		tasksByProject: map[string][]project.Task{
			"alpha": {
				{ID: "top", ProjectID: "alpha", Title: "Top alpha", Status: project.StatusOpen, Metadata: map[string]any{"facets.top": true}, UpdatedAt: now},
				{ID: "closed", ProjectID: "alpha", Title: "Closed alpha", Status: project.StatusClosed, Metadata: map[string]any{"facets.top": true}, UpdatedAt: now},
				{ID: "string", ProjectID: "alpha", Title: "String alpha", Status: project.StatusOpen, Metadata: map[string]any{"facets.top": "true"}, UpdatedAt: now},
			},
			"beta": {
				{ID: "missing", ProjectID: "beta", Title: "Missing beta", Status: project.StatusOpen, UpdatedAt: now},
				{ID: "false", ProjectID: "beta", Title: "False beta", Status: project.StatusOpen, Metadata: map[string]any{"facets.top": false}, UpdatedAt: now},
			},
		},
	}
	tasks, err := listTopTasks(context.Background(), provider)
	if err != nil {
		t.Fatalf("listTopTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Project.ID != "alpha" || tasks[0].Task.ID != "top" || tasks[0].Task.Title != "Top alpha" {
		t.Fatalf("listTopTasks() = %#v", tasks)
	}
	if provider.listFilter.Status == nil || *provider.listFilter.Status != project.StatusOpen {
		t.Fatalf("top-task filter = %#v", provider.listFilter)
	}
}

func TestCompletedTodayUsesLocalBoundariesAndTopMetadata(t *testing.T) {
	location := time.FixedZone("user", -4*60*60)
	dayStart := time.Date(2026, 8, 16, 0, 0, 0, 0, location)
	provider := &fakeProvider{
		projects: []project.Project{{ID: "alpha", Name: "Alpha"}},
		tasksByProject: map[string][]project.Task{
			"alpha": {
				{ID: "before", Status: project.StatusClosed, UpdatedAt: dayStart.Add(-time.Nanosecond)},
				{ID: "start", Status: project.StatusClosed, Metadata: map[string]any{"facets.top": true}, UpdatedAt: dayStart},
				{ID: "end", Status: project.StatusClosed, UpdatedAt: dayStart.AddDate(0, 0, 1)},
				{ID: "open", Status: project.StatusOpen, Metadata: map[string]any{"facets.top": true}, UpdatedAt: dayStart.Add(time.Hour)},
				{ID: "regular", Status: project.StatusClosed, UpdatedAt: dayStart.Add(2 * time.Hour)},
			},
		},
	}
	metrics, err := completedToday(context.Background(), provider, dayStart.Add(12*time.Hour))
	if err != nil {
		t.Fatalf("completedToday() error = %v", err)
	}
	if metrics.All != 2 || metrics.Top != 1 || !metrics.DayStart.Equal(dayStart) || !metrics.DayEnd.Equal(dayStart.AddDate(0, 0, 1)) {
		t.Fatalf("completedToday() = %#v", metrics)
	}
	if provider.listFilter.Status == nil || *provider.listFilter.Status != project.StatusClosed {
		t.Fatalf("completion filter = %#v", provider.listFilter)
	}
}
func TestTaskListTOONJSONFieldsAndEmptyState(t *testing.T) {
	provider := &fakeProvider{tasks: []project.Task{sampleTask()}}
	code, stdout, _ := runCLI(provider, "", map[string]string{}, "--project", "demo", "tasks", "--status", "all", "--fields", "id,priority,assignee,updated")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, "tasks[1]{id,priority,assignee,updated}:") || provider.listFilter.Status != nil {
		t.Fatalf("unexpected list: %s", stdout)
	}
	if !strings.Contains(stdout, "facets --project demo tasks show <id>") {
		t.Fatalf("list hint lost selected project: %s", stdout)
	}

	code, stdout, _ = runCLI(provider, "", map[string]string{}, "--project", "demo", "--json", "tasks")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stdout)
	}
	var decoded struct {
		Project string           `json:"project"`
		Count   int              `json:"count"`
		Tasks   []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if decoded.Project != "demo" || decoded.Count != 1 || decoded.Tasks[0]["id"] != "T-1" {
		t.Fatalf("decoded = %#v", decoded)
	}

	provider.tasks = nil
	code, stdout, _ = runCLI(provider, "", map[string]string{}, "--project", "demo", "tasks", "--status", "closed")
	if code != 0 || !strings.Contains(stdout, "tasks: []") || !strings.Contains(stdout, "0 closed tasks found") {
		t.Fatalf("empty result code=%d:\n%s", code, stdout)
	}
}

func TestTaskListAllUsesCrossProjectIDsAndRetainsFilters(t *testing.T) {
	provider := &fakeProvider{
		projects: []project.Project{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta"}},
		tasks:    []project.Task{sampleTask()},
	}
	code, stdout, stderr := runCLI(provider, "", map[string]string{}, "tasks", "list", "--all", "--status", "closed", "--fields", "id,title")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `project: "all"`) || !strings.Contains(stdout, "count: 2") || !strings.Contains(stdout, "alpha#T-1") || !strings.Contains(stdout, "beta#T-1") {
		t.Fatalf("all-task output = %s", stdout)
	}
	if provider.listFilter.Status == nil || *provider.listFilter.Status != project.StatusClosed {
		t.Fatalf("all-task filter = %#v", provider.listFilter)
	}
}

func TestTaskListProjectFlagsRespectDisabledProjects(t *testing.T) {
	provider := &fakeProvider{
		projects: []project.Project{{ID: "alpha", Name: "Alpha"}, {ID: "beta", Name: "Beta"}},
		tasksByProject: map[string][]project.Task{
			"alpha": {{ID: "T-1", ProjectID: "alpha", Title: "Alpha task", Status: project.StatusOpen}},
			"beta":  {{ID: "T-2", ProjectID: "beta", Title: "Beta task", Status: project.StatusOpen}},
		},
	}
	registry, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer registry.Close()
	if err := registry.SyncProjects(context.Background(), provider.Name(), provider.projects); err != nil {
		t.Fatalf("SyncProjects() error = %v", err)
	}
	if _, err := registry.SetProjectDisabled(context.Background(), provider.Name(), "beta", true); err != nil {
		t.Fatalf("SetProjectDisabled() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, Env: map[string]string{}}
	run := func(args ...string) string {
		stdout.Reset()
		stderr.Reset()
		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	output := run("tasks", "list", "--all-projects")
	if !strings.Contains(output, "count: 1") || !strings.Contains(output, "alpha#T-1") || strings.Contains(output, "beta#T-2") {
		t.Fatalf("enabled-project output = %s", output)
	}
	output = run("tasks", "list", "--all")
	if !strings.Contains(output, "count: 2") || !strings.Contains(output, "alpha#T-1") || !strings.Contains(output, "beta#T-2") {
		t.Fatalf("all-project output = %s", output)
	}
	output = run("tasks", "list", "--all-projects", "--all")
	if !strings.Contains(output, "count: 2") {
		t.Fatalf("combined-flag output = %s", output)
	}
}

func TestTaskHintsPreserveNonDefaultProviderAndQuoteProject(t *testing.T) {
	provider := &fakeProvider{name: "other", tasks: []project.Task{sampleTask()}}
	code, stdout, stderr := runCLI(provider, "", map[string]string{}, "--project", "demo project", "--provider", "other")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := "facets --project 'demo project' --provider other tasks show <id>"
	if !strings.Contains(stdout, want) {
		t.Fatalf("home hint lost selected context; want %q in %s", want, stdout)
	}
	code, stdout, stderr = runCLI(provider, "", map[string]string{}, "--provider", "other", "projects", "list")
	if code != 0 || !strings.Contains(stdout, "facets --provider other projects show <id>") {
		t.Fatalf("project hint lost provider code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestProjectDiscoveryKataThenJJ(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kata.toml"), []byte("[project]\nname = \"logical-name\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	code, stdout, _ := runCLI(provider, nested, map[string]string{}, "tasks")
	if code != 0 || provider.listProjectID != "logical-name" {
		t.Fatalf("kata discovery code=%d project=%q: %s", code, provider.listProjectID, stdout)
	}
	if err := os.Remove(filepath.Join(root, ".kata.toml")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runCLI(provider, nested, map[string]string{}, "tasks")
	if code != 0 || provider.listProjectID != filepath.Base(root) {
		t.Fatalf("jj discovery code=%d project=%q: %s", code, provider.listProjectID, stdout)
	}
}

func TestKataTOMLFormsAndMalformedFile(t *testing.T) {
	for name, contents := range map[string]string{
		"inline-comment": "[project]\nname = \"inline\" # project ID\n",
		"single-quote":   "[project]\nname = 'single'\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".kata.toml"), []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			provider := &fakeProvider{}
			code, stdout, _ := runCLI(provider, root, map[string]string{}, "tasks")
			if code != 0 || provider.listProjectID == "" {
				t.Fatalf("code=%d project=%q stdout=%s", code, provider.listProjectID, stdout)
			}
		})
	}

	for name, contents := range map[string]string{
		"malformed":    "[project\nname = ???\n",
		"missing-name": "[project]\ndescription = \"no name\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".kata.toml"), []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			provider := &fakeProvider{}
			code, stdout, stderr := runCLI(provider, root, map[string]string{}, "tasks")
			if code != 1 || !strings.Contains(stdout, "could not inspect the current workspace") || provider.listProjectID != "" || stderr != "" {
				t.Fatalf("%s code=%d project=%q stdout=%s stderr=%s", name, code, provider.listProjectID, stdout, stderr)
			}
		})
	}
}

func TestJJDiscoveryRejectsInvalidEntries(t *testing.T) {
	for name, setup := range map[string]func(string) error{
		"not-directory": func(root string) error {
			return os.WriteFile(filepath.Join(root, ".jj"), []byte("not a directory"), 0o644)
		},
		"io-error": func(root string) error {
			return os.Symlink(".jj", filepath.Join(root, ".jj"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := setup(root); err != nil {
				t.Fatal(err)
			}
			provider := &fakeProvider{}
			code, stdout, stderr := runCLI(provider, root, map[string]string{}, "tasks")
			if code != 1 || !strings.Contains(stdout, "could not inspect the current workspace") || provider.listProjectID != "" || stderr != "" {
				t.Fatalf("%s code=%d project=%q stdout=%s stderr=%s", name, code, provider.listProjectID, stdout, stderr)
			}
		})
	}
}
func TestTaskMutationsBuildProviderNeutralInputs(t *testing.T) {
	provider := &fakeProvider{}
	base := []string{"--project", "demo"}
	code, stdout, _ := runCLI(provider, "", nil, append(base, "tasks", "create", "New task", "--body", "details", "--priority", "3", "--assignee", "bob", "--idempotency-key", "key-1")...)
	if code != 0 {
		t.Fatalf("create code=%d: %s", code, stdout)
	}

	if provider.createdProjectID != "demo" || provider.created.Title != "New task" || provider.created.Description != "details" || provider.created.Priority == nil || *provider.created.Priority != 3 || provider.created.Assignee != "bob" || provider.created.IdempotencyKey != "key-1" {
		t.Fatalf("create input = %#v", provider.created)
	}

	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "edit", "T-1", "--title", "Renamed", "--body", "", "--priority", "4", "--assignee", "")...)
	if code != 0 {
		t.Fatalf("edit code=%d: %s", code, stdout)
	}
	if provider.patch.Title == nil || *provider.patch.Title != "Renamed" || provider.patch.Description == nil || *provider.patch.Description != "" || !provider.patch.Priority.Set || provider.patch.Priority.Value == nil || *provider.patch.Priority.Value != 4 || provider.patch.Assignee == nil || *provider.patch.Assignee != "" {
		t.Fatalf("edit patch = %#v", provider.patch)
	}

	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "edit", "T-1", "--priority", "-")...)
	if code != 0 || !provider.patch.Priority.Set || provider.patch.Priority.Value != nil {
		t.Fatalf("clear priority code=%d patch=%#v: %s", code, provider.patch, stdout)
	}

	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "close", "T-1", "--message", "Done", "--evidence", "test:go test ./...", "--evidence", "pr:https://example.test/pull/42")...)
	if code != 0 {
		t.Fatalf("close code=%d: %s", code, stdout)
	}
	if provider.patch.Status == nil || *provider.patch.Status != project.StatusClosed || provider.patch.Completion == nil || provider.patch.Completion.Message != "Done" || len(provider.patch.Completion.Evidence) != 2 || provider.patch.Completion.Evidence[0] != "test:go test ./..." || provider.patch.Completion.Evidence[1] != "pr:https://example.test/pull/42" || provider.patch.Completion.Comment != "" {
		t.Fatalf("close patch = %#v", provider.patch)
	}

	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "reopen", "T-1")...)
	if code != 0 || provider.patch.Status == nil || *provider.patch.Status != project.StatusOpen || provider.patch.Completion != nil {
		t.Fatalf("reopen code=%d patch=%#v: %s", code, provider.patch, stdout)
	}

	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "delete", "T-1", "--confirm", "wrong")...)
	if code != 2 || provider.deletedID != "" {
		t.Fatalf("bad confirmation code=%d deleted=%q: %s", code, provider.deletedID, stdout)
	}
	code, stdout, _ = runCLI(provider, "", nil, append(base, "tasks", "delete", "T-1", "--confirm", "T-1")...)
	if code != 0 || provider.deletedProjectID != "demo" || provider.deletedID != "T-1" {
		t.Fatalf("delete code=%d project=%q id=%q: %s", code, provider.deletedProjectID, provider.deletedID, stdout)
	}
}

func TestTaskCloseCarriesOptionalComment(t *testing.T) {
	provider := &fakeProvider{}
	code, stdout, stderr := runCLI(provider, "", nil, "--project", "demo", "tasks", "close", "T-1", "--message", "Done", "--evidence", "test:focused", "--comment", "Released to users")
	if code != 0 || stderr != "" || provider.patch.Completion == nil || provider.patch.Completion.Comment != "Released to users" {
		t.Fatalf("close comment code=%d patch=%#v stdout=%s stderr=%s", code, provider.patch, stdout, stderr)
	}
}

func TestTaskCommentTOONJSONAndProviderErrors(t *testing.T) {
	provider := &fakeProvider{}
	code, stdout, stderr := runCLI(provider, "", nil, "--project", "demo", "tasks", "comment", "T-1", "--body", "Follow-up context")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "task:") || provider.commentedProjectID != "demo" || provider.commentedID != "T-1" || provider.commentBody != "Follow-up context" {
		t.Fatalf("TOON comment code=%d project=%q id=%q body=%q stdout=%s stderr=%s", code, provider.commentedProjectID, provider.commentedID, provider.commentBody, stdout, stderr)
	}

	code, stdout, stderr = runCLI(provider, "", nil, "--project", "demo", "--json", "tasks", "comment", "T-2", "--body", "JSON context")
	var decoded struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("invalid comment JSON: %v\n%s", err, stdout)
	}
	if code != 0 || stderr != "" || decoded.Task.ID != "T-2" {
		t.Fatalf("JSON comment code=%d decoded=%#v stdout=%s stderr=%s", code, decoded, stdout, stderr)
	}

	provider.err = errors.New("kata: raw subprocess secret")
	code, stdout, stderr = runCLI(provider, "", nil, "--project", "demo", "tasks", "comment", "T-1", "--body", "context")
	if code != 1 || stderr != "" || !strings.Contains(stdout, "type: \"operational\"") || !strings.Contains(stdout, "could not comment on task") || strings.Contains(stdout, "kata") || strings.Contains(stdout, "subprocess") {
		t.Fatalf("comment error code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI(provider, "", nil, "--project", "demo", "--json", "tasks", "comment", "T-1", "--body", "context")
	var failure struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &failure); err != nil {
		t.Fatalf("invalid comment error JSON: %v\n%s", err, stdout)
	}
	if code != 1 || stderr != "" || failure.Error.Type != "operational" || !strings.Contains(failure.Error.Message, "could not comment on task") || strings.Contains(stdout, "kata") || strings.Contains(stdout, "subprocess") {
		t.Fatalf("JSON comment error code=%d failure=%#v stdout=%s stderr=%s", code, failure, stdout, stderr)
	}
}

func TestMutationValidationDoesNotCallProvider(t *testing.T) {
	provider := &fakeProvider{}
	cases := [][]string{
		{"--project", "demo", "tasks", "create", ""},
		{"--project", "demo", "tasks", "edit", "T-1"},
		{"--project", "demo", "tasks", "close", "T-1", "--evidence", "test:go test ./..."},
		{"--project", "demo", "tasks", "close", "T-1", "--message", "done"},
		{"--project", "demo", "tasks", "close", "T-1", "--message", "done", "--evidence", "test:focused", "--comment", " "},
		{"--project", "demo", "tasks", "comment", "", "--body", "context"},
		{"--project", "demo", "tasks", "comment", "T-1"},
		{"--project", "demo", "tasks", "comment", "T-1", "--body", " "},
		{"--project", "demo", "tasks", "delete", "T-1"},
	}
	for _, args := range cases {
		provider.updatedID, provider.createdProjectID, provider.deletedID, provider.commentedID = "", "", "", ""
		code, stdout, _ := runCLI(provider, "", nil, args...)
		if code != 2 || !strings.Contains(stdout, "type: \"usage\"") {
			t.Errorf("args=%v code=%d: %s", args, code, stdout)
		}
		if provider.updatedID != "" || provider.createdProjectID != "" || provider.deletedID != "" || provider.commentedID != "" {
			t.Errorf("provider called for args %v", args)
		}
	}
}

func TestInvalidTaskInvocationsDoNotRequireProject(t *testing.T) {
	provider := &fakeProvider{}
	cases := [][]string{
		{"tasks", "--status", "invalid"},
		{"tasks", "show"},
		{"tasks", "create", ""},
		{"tasks", "edit", "T-1"},
		{"tasks", "close", "T-1", "--message", "done"},
		{"tasks", "comment", "T-1"},
		{"tasks", "reopen", "T-1", "extra"},
		{"tasks", "delete", "T-1"},
	}
	for _, args := range cases {
		args = append([]string{"--provider", "absent"}, args...)
		code, stdout, stderr := runCLI(provider, t.TempDir(), map[string]string{}, args...)
		if code != 2 || strings.Contains(stdout, "no project could be discovered") || strings.Contains(stdout, "provider \"absent\" is not available") || stderr != "" {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}

func TestUsageHintsPreserveConfiguredSelectorsBeforeDiscovery(t *testing.T) {
	provider := &fakeProvider{name: "other"}
	prefix := []string{"--project", "demo project", "--provider", "other"}
	for _, args := range [][]string{
		append(append([]string{}, prefix...), "tasks", "close", "T-1", "--message", "done"),
		append(append([]string{}, prefix...), "tasks", "show", "T-1", "--bogus"),
		append(append([]string{}, prefix...), "projects", "show", "--bogus"),
	} {
		code, stdout, stderr := runCLI(provider, t.TempDir(), map[string]string{}, args...)
		if code != 2 || !strings.Contains(stdout, "facets --project 'demo project' --provider other") || stderr != "" {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
	code, stdout, _ := runCLI(&fakeProvider{}, t.TempDir(), map[string]string{"FACETS_PROJECT": "env project"}, "tasks", "show")
	if code != 2 || !strings.Contains(stdout, "facets --project 'env project' tasks show") {
		t.Fatalf("environment selector lost code=%d stdout=%s", code, stdout)
	}
}

func TestTrailingHelpAfterIDsDoesNotResolveDependencies(t *testing.T) {
	provider := &fakeProvider{}
	for _, args := range [][]string{{"--provider", "absent", "tasks", "reopen", "T-1", "--help"}, {"--provider", "absent", "tasks", "reopen", "T-1", "-h"}, {"--provider", "absent", "projects", "show", "demo", "--help"}, {"--provider", "absent", "projects", "show", "demo", "-h"}} {
		code, stdout, stderr := runCLI(provider, t.TempDir(), map[string]string{}, args...)
		if code != 0 || !strings.Contains(stdout, "help:") || stderr != "" {
			t.Errorf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
	}
}

func TestProjectsCommands(t *testing.T) {
	provider := &fakeProvider{projects: []project.Project{{ID: "demo", Name: "Demo", Description: "A project"}}}
	code, stdout, _ := runCLI(provider, "", nil, "projects", "list")
	if code != 0 || !strings.Contains(stdout, "projects[1]{id,name}:") {
		t.Fatalf("list code=%d: %s", code, stdout)
	}
	code, stdout, _ = runCLI(provider, "", nil, "--json", "projects", "show", "demo")
	if code != 0 {
		t.Fatalf("show code=%d: %s", code, stdout)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil || decoded["project"] == nil {
		t.Fatalf("show JSON err=%v: %s", err, stdout)
	}
}

func TestProjectDisableEnableAndListAll(t *testing.T) {
	provider := &fakeProvider{projects: []project.Project{
		{ID: "alpha", Name: "Alpha"},
		{ID: "beta", Name: "Beta"},
		{ID: "gamma", Name: "Gamma"},
	}}
	registry, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer registry.Close()

	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, Env: map[string]string{}}
	run := func(args ...string) string {
		stdout.Reset()
		stderr.Reset()
		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	if output := run("projects", "list"); !strings.Contains(output, "count: 3") {
		t.Fatalf("initial list = %s", output)
	}
	if output := run("projects", "disable", "beta"); !strings.Contains(output, "disabled: true") || !strings.Contains(output, "disabled_at:") {
		t.Fatalf("disable output = %s", output)
	}
	if output := run("projects", "list"); !strings.Contains(output, "count: 2") || strings.Contains(output, "Beta") {
		t.Fatalf("default list after disable = %s", output)
	}
	if output := run("projects", "list", "--all"); !strings.Contains(output, "count: 3") {
		t.Fatalf("all-project list = %s", output)
	} else if strings.Index(output, "Gamma") > strings.Index(output, "(Beta)") {
		t.Fatalf("disabled project was not listed last = %s", output)
	}
	if output := run("projects", "enable", "beta"); !strings.Contains(output, "disabled: false") {
		t.Fatalf("enable output = %s", output)
	}
	if output := run("projects", "list"); !strings.Contains(output, "count: 3") || !strings.Contains(output, "Beta") {
		t.Fatalf("default list after enable = %s", output)
	}
}
func TestProjectRegistrySetAndUnknownActivity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{projects: []project.Project{{ID: "demo", Name: "Demo"}}}
	registry, err := store.Open(context.Background(), filepath.Join(root, "state", "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer registry.Close()

	var stdout, stderr bytes.Buffer
	app := App{
		Provider:     provider,
		ProjectStore: registry,
		Stdout:       &stdout,
		Stderr:       &stderr,
		Cwd:          root,
		Env:          map[string]string{},
		Summary:      &status.Builder{Activity: fakeActivitySource{activity: status.Activity{Sessions: map[string]int{"omp": 2}}}, PeriodDays: 1},
	}
	if code := app.Run(context.Background(), []string{"projects", "list"}); code != 0 {
		t.Fatalf("projects list code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if code := app.Run(context.Background(), []string{"projects", "set", "demo", "directory=" + root}); code != 0 {
		t.Fatalf("projects set code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	registered, err := registry.RegisteredProject(context.Background(), "kata", "demo")
	if err != nil {
		t.Fatalf("RegisteredProject() error = %v", err)
	}
	if registered.Metadata["directory"] != root || registered.Source != "kata" || registered.FirstSeen.IsZero() {
		t.Fatalf("registered project = %#v", registered)
	}
	stdout.Reset()
	if code := app.Run(context.Background(), []string{"--json", "projects", "show", "demo"}); code != 0 {
		t.Fatalf("projects show code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"metadata":{"directory":"`+root+`"}`) || !strings.Contains(stdout.String(), `"source":"kata"`) {
		t.Fatalf("project metadata missing from show output: %s", stdout.String())
	}

	output, err := marshalJSON(statusDocument(status.Summary{Activity: status.Activity{Sessions: map[string]int{"omp": status.UnknownSessionCount}}}))
	if err != nil {
		t.Fatalf("marshal unknown activity: %v", err)
	}
	if !strings.Contains(string(output), `"omp":"??"`) {
		t.Fatalf("unknown OMP count missing: %s", output)
	}
}

func TestServeDispatchUsesFlagAndEnvironment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var addresses []string
	app := App{Provider: &fakeProvider{}, Stdout: &stdout, Stderr: &stderr, Env: map[string]string{"FACETS_ADDR": ":9090"}, Serve: func(_ context.Context, address string) error { addresses = append(addresses, address); return nil }}
	if code := app.Run(context.Background(), []string{"serve"}); code != 0 {
		t.Fatalf("env serve code=%d: %s", code, stdout.String())
	}
	if code := app.Run(context.Background(), []string{"serve", "--addr", ":7070"}); code != 0 {
		t.Fatalf("flag serve code=%d: %s", code, stdout.String())
	}
	if len(addresses) != 2 || addresses[0] != ":9090" || addresses[1] != ":7070" {
		t.Fatalf("addresses = %#v", addresses)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("channels stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestProjectShowIncludesDeterministicStatusSummary(t *testing.T) {
	root := t.TempDir()
	openPriority := 1
	closedPriority := 3
	provider := &fakeProvider{
		projects: []project.Project{{ID: "demo", Name: "Demo"}},
		tasks: []project.Task{
			{ID: "T-open-priority", ProjectID: "demo", Status: project.StatusOpen, Priority: &openPriority},
			{ID: "T-open", ProjectID: "demo", Status: project.StatusOpen},
			{ID: "T-closed", ProjectID: "demo", Status: project.StatusClosed, Priority: &closedPriority},
		},
	}
	builder := &status.Builder{
		Activity:   fakeActivitySource{activity: status.Activity{Commits: 7, Sessions: map[string]int{"codex": 2, "omp": 4, "other": 99}}},
		Now:        func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) },
		PeriodDays: 30,
	}
	code, stdout, stderr := runCLIWithSummary(provider, root, builder, "projects", "show", "demo")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "status:\n  period_days: 30\n") || !strings.Contains(stdout, "commits: 7") || !strings.Contains(stdout, "codex: 2") {
		t.Fatalf("TOON show code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	code, stdout, stderr = runCLIWithSummary(provider, root, builder, "--json", "projects", "show", "demo")
	if code != 0 || stderr != "" {
		t.Fatalf("JSON show code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	var decoded struct {
		Status struct {
			PeriodDays int    `json:"period_days"`
			Since      string `json:"since"`
			Tasks      struct {
				Total            int            `json:"total"`
				Open             int            `json:"open"`
				Closed           int            `json:"closed"`
				OpenByPriority   map[string]any `json:"open_by_priority"`
				ClosedByPriority map[string]any `json:"closed_by_priority"`
			} `json:"tasks"`
			Activity struct {
				Commits  int            `json:"commits"`
				Sessions map[string]any `json:"sessions"`
			} `json:"activity"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if decoded.Status.PeriodDays != 30 || decoded.Status.Since != "2026-08-01T12:00:00Z" {
		t.Fatalf("period = %#v", decoded.Status)
	}
	if decoded.Status.Tasks.Total != 3 || decoded.Status.Tasks.Open != 2 || decoded.Status.Tasks.Closed != 1 ||
		decoded.Status.Tasks.OpenByPriority["1"] != float64(1) || decoded.Status.Tasks.ClosedByPriority["3"] != float64(1) {
		t.Fatalf("tasks = %#v", decoded.Status.Tasks)
	}
	if decoded.Status.Activity.Commits != 7 || decoded.Status.Activity.Sessions["codex"] != float64(2) || decoded.Status.Activity.Sessions["omp"] != float64(4) {
		t.Fatalf("activity = %#v", decoded.Status.Activity)
	}
}

func TestProjectShowStatusFailureIsOperational(t *testing.T) {
	root := t.TempDir()
	provider := &fakeProvider{projects: []project.Project{{ID: "demo", Name: "Demo"}}}
	builder := &status.Builder{
		Activity:   fakeActivitySource{err: errors.New("activity unavailable")},
		Now:        func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) },
		PeriodDays: 30,
	}
	code, stdout, stderr := runCLIWithSummary(provider, root, builder, "--json", "projects", "show", "demo")
	if code != 1 || stderr != "" || !strings.Contains(stdout, `"type":"operational"`) || strings.Contains(stdout, `"project"`) {
		t.Fatalf("failure code=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
}

func TestHelpUsageOperationalErrorsAndChannels(t *testing.T) {
	provider := &fakeProvider{}
	code, stdout, stderr := runCLI(provider, "", nil, "help")
	if code != 0 || !strings.Contains(stdout, "usage: \"facets [global flags] <command>\"") || stderr != "" {
		t.Fatalf("help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, t.TempDir(), map[string]string{}, "tasks", "show", "--help")
	if code != 0 || !strings.Contains(stdout, "facets tasks show <id> [--full]") || stderr != "" {
		t.Fatalf("command help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, t.TempDir(), map[string]string{}, "tasks", "--help")
	if code != 0 || !strings.Contains(stdout, "close") || !strings.Contains(stdout, "comment") || stderr != "" {
		t.Fatalf("tasks help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, t.TempDir(), map[string]string{}, "tasks", "comment", "--help")
	if code != 0 || !strings.Contains(stdout, "facets tasks comment <id> --body <text>") || stderr != "" {
		t.Fatalf("comment help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, t.TempDir(), map[string]string{}, "tasks", "close", "--help")
	if code != 0 || !strings.Contains(stdout, "--comment <text>") || stderr != "" {
		t.Fatalf("close help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, "", nil, "--project", "demo", "tasks", "show")
	if code != 2 || !strings.Contains(stdout, "type: \"usage\"") || stderr != "" {
		t.Fatalf("usage code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, t.TempDir(), map[string]string{}, "tasks")
	if code != 2 || !strings.Contains(stdout, "--project <id>") || stderr != "" {
		t.Fatalf("discovery usage code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, "", nil, "--project", "demo", "tasks", "bogus")
	if code != 2 || !strings.Contains(stdout, "unknown tasks command") || !strings.Contains(stdout, "facets --project demo tasks --help") || stderr != "" {
		t.Fatalf("unknown task command code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	provider.err = errors.New("kata: raw subprocess secret")
	code, stdout, stderr = runCLI(provider, "", map[string]string{}, "--project", "demo", "tasks")
	if code != 1 || !strings.Contains(stdout, "type: \"operational\"") || strings.Contains(stdout, "kata") || strings.Contains(stdout, "subprocess") || stderr != "" {
		t.Fatalf("operational code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(provider, "", map[string]string{"FACETS_DEBUG": "true"}, "--project", "demo", "tasks")
	if code != 1 || strings.Contains(stdout, "subprocess") || !strings.Contains(stderr, "raw subprocess secret") {
		t.Fatalf("debug code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestTaskBodyTruncationAndFull(t *testing.T) {
	longBody := strings.Repeat("x", 1001)
	provider := &fakeProvider{tasks: []project.Task{{ID: "T-1", ProjectID: "demo", Title: "Long", Description: longBody, Status: project.StatusOpen}}}
	code, stdout, _ := runCLI(provider, "", nil, "--project", "demo", "--json", "tasks", "show", "T-1")
	if code != 0 {
		t.Fatalf("show code=%d: %s", code, stdout)
	}
	var truncated struct {
		Task struct {
			Body string `json:"body"`
		} `json:"task"`
		BodyChars int    `json:"body_chars"`
		Help      string `json:"help"`
	}
	if err := json.Unmarshal([]byte(stdout), &truncated); err != nil {
		t.Fatal(err)
	}
	if len(truncated.Task.Body) != 1000 || truncated.BodyChars != 1001 || !strings.Contains(truncated.Help, "facets --project demo tasks show T-1 --full") {
		t.Fatalf("truncated = %#v", truncated)
	}
	code, stdout, _ = runCLI(provider, "", nil, "--project", "demo", "--json", "tasks", "show", "T-1", "--full")
	if code != 0 || strings.Contains(stdout, "\"help\"") || !strings.Contains(stdout, longBody) {
		t.Fatalf("full code=%d: %s", code, stdout)
	}
}

func TestTOONEscapesAllControlCharacters(t *testing.T) {
	got, err := quoteTOON("a\b\f")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"a\u0008\u000c"` {
		t.Fatalf("quoteTOON = %q", got)
	}
}

func TestInvalidUTF8MakesRunOperational(t *testing.T) {
	invalid := string([]byte{0xff})
	body := strings.Repeat("x", 500) + invalid + strings.Repeat("y", 600)
	provider := &fakeProvider{tasks: []project.Task{{ID: "T-1", ProjectID: "demo", Title: "Invalid body", Description: body, Status: project.StatusOpen}}}
	for _, args := range [][]string{
		{"--project", "demo", "tasks", "show", "T-1"},
		{"--project", "demo", "--json", "tasks", "show", "T-1"},
	} {
		code, stdout, stderr := runCLI(provider, "", map[string]string{}, args...)
		if code != 1 || stdout != "" || stderr != "" {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	if _, err := quoteTOON(invalid); err == nil {
		t.Fatal("quoteTOON accepted invalid UTF-8")
	}
}

func TestStdoutWriteFailureOverridesExitCode(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"unknown"}} {
		var stderr bytes.Buffer
		app := App{Provider: &fakeProvider{}, Stdout: failingWriter{}, Stderr: &stderr, Env: map[string]string{}}
		if code := app.Run(context.Background(), args); code != 1 {
			t.Errorf("args=%v code=%d", args, code)
		}
		if stderr.Len() != 0 {
			t.Errorf("args=%v stderr=%q", args, stderr.String())
		}
	}
}
