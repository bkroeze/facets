package status

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
	_ "modernc.org/sqlite"
)

type fakeProvider struct {
	tasks []project.Task
	err   error
}

func (f fakeProvider) Name() string                                            { return "fake" }
func (f fakeProvider) ListProjects(context.Context) ([]project.Project, error) { return nil, nil }
func (f fakeProvider) GetProject(context.Context, string) (project.Project, error) {
	return project.Project{}, errors.New("unused")
}
func (f fakeProvider) CreateProject(context.Context, project.ProjectInput) (project.Project, error) {
	return project.Project{}, errors.New("unused")
}
func (f fakeProvider) UpdateProject(context.Context, string, project.ProjectPatch) (project.Project, error) {
	return project.Project{}, errors.New("unused")
}
func (f fakeProvider) DeleteProject(context.Context, string) error { return errors.New("unused") }
func (f fakeProvider) ListTasks(context.Context, string, project.TaskFilter) ([]project.Task, error) {
	return f.tasks, f.err
}
func (f fakeProvider) GetTask(context.Context, string, string) (project.Task, error) {
	return project.Task{}, errors.New("unused")
}
func (f fakeProvider) CreateTask(context.Context, string, project.TaskInput) (project.Task, error) {
	return project.Task{}, errors.New("unused")
}
func (f fakeProvider) UpdateTask(context.Context, string, string, project.TaskPatch) (project.Task, error) {
	return project.Task{}, errors.New("unused")
}

func (f fakeProvider) CommentTask(context.Context, string, string, string) (project.Task, error) {
	return project.Task{}, errors.New("unused")
}

func (f fakeProvider) DeleteTask(context.Context, string, string) error { return errors.New("unused") }

type fakeActivity struct {
	activity Activity
	root     string
	since    time.Time
	err      error
}

func (f *fakeActivity) Summarize(_ context.Context, root string, since time.Time) (Activity, error) {
	f.root, f.since = root, since
	return f.activity, f.err
}

func TestBuilderBuildAggregatesTasksAndActivity(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	openPriority, closedPriority := 1, 3
	activity := &fakeActivity{activity: Activity{Commits: 4, Sessions: map[string]int{"codex": 2}}}
	builder := &Builder{Activity: activity, Now: func() time.Time { return now }, PeriodDays: 7}
	got, err := builder.Build(context.Background(), fakeProvider{tasks: []project.Task{
		{Status: project.StatusOpen, Priority: &openPriority},
		{Status: project.StatusOpen},
		{Status: project.StatusClosed, Priority: &closedPriority},
		{Status: "blocked", Priority: &openPriority},
	}}, root, "project-1")
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got.PeriodDays != 7 || !got.Since.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("period = %#v", got)
	}
	if got.Tasks.Total != 4 || got.Tasks.Open != 2 || got.Tasks.Closed != 1 {
		t.Fatalf("tasks = %#v", got.Tasks)
	}
	if got.Tasks.OpenByPriority[1] != 1 || got.Tasks.ClosedByPriority[3] != 1 {
		t.Fatalf("priority buckets = %#v", got.Tasks)
	}
	if got.Activity.Commits != 4 || got.Activity.Sessions["codex"] != 2 || got.Activity.Sessions["omp"] != 0 {
		t.Fatalf("activity = %#v", got.Activity)
	}
	if activity.root != root || !activity.since.Equal(got.Since) {
		t.Fatalf("activity arguments root=%q since=%s", activity.root, activity.since)
	}
}

func TestBuilderBuildValidatesInputs(t *testing.T) {
	root := t.TempDir()
	provider := fakeProvider{}
	validActivity := &fakeActivity{}
	cases := []struct {
		name  string
		build func() (Summary, error)
	}{
		{"provider", func() (Summary, error) {
			return (&Builder{Activity: validActivity, PeriodDays: 1}).Build(context.Background(), nil, root, "p")
		}},
		{"activity", func() (Summary, error) {
			return (&Builder{PeriodDays: 1}).Build(context.Background(), provider, root, "p")
		}},
		{"root", func() (Summary, error) {
			return (&Builder{Activity: validActivity, PeriodDays: 1}).Build(context.Background(), provider, filepath.Join(root, "missing"), "p")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.build(); err == nil {
				t.Fatal("Build() error = nil")
			}
		})
	}
}

func TestWorkspaceRootFindsRepositoryAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "internal", "status")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := workspaceRoot(child)
	if err != nil {
		t.Fatalf("workspaceRoot() error = %v", err)
	}
	if got != root {
		t.Fatalf("workspaceRoot() = %q, want %q", got, root)
	}
}
func TestLocalActivitySourceMarksUnknownOMPWithoutRoot(t *testing.T) {
	activity, err := (localActivitySource{}).Summarize(context.Background(), "", time.Now())
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if activity.Sessions["omp"] != UnknownSessionCount {
		t.Fatalf("OMP sessions = %d, want %d", activity.Sessions["omp"], UnknownSessionCount)
	}
}

func TestLocalActivitySourceCountsContainedRecentRows(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	codexHome := t.TempDir()
	ompHome := t.TempDir()
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	createActivityDB(t, filepath.Join(codexHome, "state_5.sqlite"), "threads", []activityRow{
		{filepath.Join(root, "child"), "2026-08-02T00:00:00Z"},
		{filepath.Join(root, "other"), "2026-07-31T00:00:00Z"},
		{filepath.Join(filepath.Dir(root), "sibling"), "2026-08-03T00:00:00Z"},
	})
	if err := os.Mkdir(filepath.Join(ompHome, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	createActivityDB(t, filepath.Join(ompHome, "agent", "history.db"), "history", []activityRow{
		{root, "2026-08-01T00:00:00Z"},
		{filepath.Join(root, "nested"), "2026-08-04T00:00:00Z"},
	})
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("OMP_HOME", ompHome)
	jjDir := t.TempDir()
	jjPath := filepath.Join(jjDir, "jj")
	if err := os.WriteFile(jjPath, []byte("#!/bin/sh\ncase \" $* \" in\n  *\" --ignore-working-copy \"*) printf '%s\\n%s\\n' '2026-08-02T00:00:00Z' '2026-07-31T00:00:00Z' ;;\n  *) printf '%s\\n' 'The working copy is stale' >&2; exit 1 ;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", jjDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (localActivitySource{}).Summarize(context.Background(), root, since)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if got.Commits != 1 || got.Sessions["codex"] != 1 || got.Sessions["omp"] != 2 {
		t.Fatalf("activity = %#v", got)
	}
}

func TestLocalActivitySourceCountsGitCommits(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OMP_HOME", t.TempDir())
	gitDir := t.TempDir()
	gitPath := filepath.Join(gitDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nprintf '%s\\n%s\\n' '2026-08-02T00:00:00Z' '2026-07-31T00:00:00Z'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", gitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got, err := (localActivitySource{}).Summarize(context.Background(), root, since)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if got.Commits != 1 {
		t.Fatalf("commits = %d, want 1", got.Commits)
	}
}

type activityRow struct{ cwd, createdAt string }

func createActivityDB(t *testing.T, path, table string, rows []activityRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	timestampColumn := "created_at"
	createSQL := "CREATE TABLE " + table + " (cwd TEXT, " + timestampColumn + " TEXT"
	if table == "threads" {
		timestampColumn = "created_at_ms"
		createSQL = "CREATE TABLE " + table + " (cwd TEXT, " + timestampColumn + " TEXT"
	}
	if table == "history" {
		createSQL += ", session_id TEXT"
	}
	createSQL += ")"
	if _, err := db.Exec(createSQL); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if table == "history" {
			if _, err := db.Exec("INSERT INTO "+table+" (cwd, "+timestampColumn+", session_id) VALUES (?, ?, ?)", row.cwd, row.createdAt, fmt.Sprintf("session-%d", i)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := db.Exec("INSERT INTO "+table+" (cwd, "+timestampColumn+") VALUES (?, ?)", row.cwd, row.createdAt); err != nil {
			t.Fatal(err)
		}
	}
}
