package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/store"
)

type mutableDaemonProvider struct {
	*fakeProvider
	mu          sync.Mutex
	projectList []project.Project
	tasks       map[string][]project.Task
	err         error
	filters     []project.TaskFilter
}

func newMutableDaemonProvider(projects []project.Project, tasks map[string][]project.Task) *mutableDaemonProvider {
	return &mutableDaemonProvider{fakeProvider: &fakeProvider{}, projectList: projects, tasks: tasks}
}

func (p *mutableDaemonProvider) ListProjects(context.Context) ([]project.Project, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return append([]project.Project(nil), p.projectList...), nil
}

func (p *mutableDaemonProvider) ListTasks(_ context.Context, projectID string, filter project.TaskFilter) ([]project.Task, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	p.filters = append(p.filters, filter)
	items := p.tasks[projectID]
	result := make([]project.Task, 0, len(items))
	for _, item := range items {
		if filter.Status == nil || item.Status == *filter.Status {
			result = append(result, item)
		}
	}
	return result, nil
}

func (p *mutableDaemonProvider) setState(tasks map[string][]project.Task, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks = tasks
	p.err = err
}

func openDaemonStore(t *testing.T, provider *mutableDaemonProvider) *store.Store {
	t.Helper()
	registry, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	if err := registry.SyncProjects(context.Background(), provider.Name(), provider.projectList); err != nil {
		t.Fatalf("SyncProjects() error = %v", err)
	}
	return registry
}

func startTaskDaemon(t *testing.T, provider *mutableDaemonProvider, registry *store.Store, interval time.Duration) (*bufio.Scanner, context.CancelFunc, <-chan int, *bytes.Buffer) {
	t.Helper()
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readPipe.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &bytes.Buffer{}
	app := App{Provider: provider, ProjectStore: registry, Stdout: writePipe, Stderr: stderr, TaskDaemonInterval: interval}
	done := make(chan int, 1)
	go func() {
		done <- app.Run(ctx, []string{"tasks", "daemon"})
		_ = writePipe.Close()
	}()
	return bufio.NewScanner(readPipe), cancel, done, stderr
}

func scanDaemonEvent(t *testing.T, scanner *bufio.Scanner) []byte {
	t.Helper()
	type result struct {
		line []byte
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		if !scanner.Scan() {
			resultCh <- result{err: scanner.Err()}
			return
		}
		resultCh <- result{line: append([]byte(nil), scanner.Bytes()...)}
	}()
	select {
	case item := <-resultCh:
		if item.err != nil {
			t.Fatalf("scan daemon event: %v", item.err)
		}
		if len(item.line) == 0 {
			t.Fatal("daemon stream ended before the next event")
		}
		return item.line
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for daemon event")
		return nil
	}
}

func stopTaskDaemon(t *testing.T, cancel context.CancelFunc, done <-chan int) {
	t.Helper()
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("daemon exit code = %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop after cancellation")
	}
}

func TestTaskDaemonInitialSnapshotOrderingFilteringAndCancellation(t *testing.T) {
	priority := 1
	projects := []project.Project{{ID: "zeta", Name: "Zeta"}, {ID: "alpha", Name: "Alpha"}}
	provider := newMutableDaemonProvider(projects, map[string][]project.Task{
		"alpha": {
			{ID: "T-2", ProjectID: "alpha", Title: "Beta", Status: project.StatusOpen, Priority: &priority, Assignee: "bruce", UpdatedAt: time.Date(2026, 8, 10, 12, 0, 0, 123, time.UTC)},
			{ID: "T-1", ProjectID: "alpha", Title: "Alpha", Status: project.StatusOpen},
			{ID: "T-0", ProjectID: "alpha", Title: "Closed", Status: project.StatusClosed},
		},
	})
	registry := openDaemonStore(t, provider)
	directory := filepath.Join(t.TempDir(), "alpha")
	if _, err := registry.SetProjectMetadata(context.Background(), provider.Name(), "alpha", "directory", directory); err != nil {
		t.Fatalf("SetProjectMetadata() error = %v", err)
	}

	scanner, cancel, done, stderr := startTaskDaemon(t, provider, registry, 5*time.Second)
	line := scanDaemonEvent(t, scanner)
	var snapshot taskDaemonSnapshot
	if err := json.Unmarshal(line, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\n%s", err, line)
	}
	if snapshot.Type != "snapshot" || len(snapshot.Projects) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Projects[0].ID != "alpha" || snapshot.Projects[1].ID != "zeta" {
		t.Fatalf("project order = %#v", snapshot.Projects)
	}
	alpha := snapshot.Projects[0]
	if alpha.Directory != directory || snapshot.Projects[1].Directory != "" {
		t.Fatalf("directories = %q, %q", alpha.Directory, snapshot.Projects[1].Directory)
	}
	if len(alpha.Tasks) != 2 || alpha.Tasks[0].ID != "T-1" || alpha.Tasks[1].ID != "T-2" {
		t.Fatalf("open task order = %#v", alpha.Tasks)
	}
	if alpha.Tasks[1].UpdatedAt != "2026-08-10T12:00:00.000000123Z" || alpha.Tasks[1].Priority == nil || *alpha.Tasks[1].Priority != priority {
		t.Fatalf("task fields = %#v", alpha.Tasks[1])
	}
	provider.mu.Lock()
	for _, filter := range provider.filters {
		if filter.Status == nil || *filter.Status != project.StatusOpen {
			provider.mu.Unlock()
			t.Fatalf("task filter = %#v", filter)
		}
	}
	provider.mu.Unlock()

	stopTaskDaemon(t, cancel, done)
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTaskDaemonOmitsDisabledProjects(t *testing.T) {
	projects := []project.Project{{ID: "active", Name: "Active"}, {ID: "disabled", Name: "Disabled"}}
	provider := newMutableDaemonProvider(projects, map[string][]project.Task{
		"active":   {{ID: "open-active", ProjectID: "active", Title: "Active task", Status: project.StatusOpen}},
		"disabled": {{ID: "open-disabled", ProjectID: "disabled", Title: "Disabled task", Status: project.StatusOpen}},
	})
	registry := openDaemonStore(t, provider)
	if _, err := registry.SetProjectDisabled(context.Background(), provider.Name(), "disabled", true); err != nil {
		t.Fatalf("SetProjectDisabled() error = %v", err)
	}

	scanner, cancel, done, _ := startTaskDaemon(t, provider, registry, 5*time.Second)
	line := scanDaemonEvent(t, scanner)
	stopTaskDaemon(t, cancel, done)

	var snapshot taskDaemonSnapshot
	if err := json.Unmarshal(line, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v\n%s", err, line)
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].ID != "active" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestTaskDaemonRecoversAfterRefreshError(t *testing.T) {
	projects := []project.Project{{ID: "demo", Name: "Demo"}}
	provider := newMutableDaemonProvider(projects, map[string][]project.Task{"demo": {{ID: "T-1", Title: "Before", Status: project.StatusOpen}}})
	registry := openDaemonStore(t, provider)
	scanner, cancel, done, stderr := startTaskDaemon(t, provider, registry, minimumTaskDaemonInterval)

	var initial taskDaemonSnapshot
	if err := json.Unmarshal(scanDaemonEvent(t, scanner), &initial); err != nil {
		t.Fatal(err)
	}
	provider.setState(nil, errors.New("kata unavailable"))
	var failure taskDaemonError
	if err := json.Unmarshal(scanDaemonEvent(t, scanner), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Type != "error" || !failure.Retrying || !strings.Contains(failure.Message, "kata unavailable") {
		t.Fatalf("failure event = %#v", failure)
	}

	provider.setState(map[string][]project.Task{"demo": {{ID: "T-2", Title: "After", Status: project.StatusOpen}}}, nil)
	var recovered taskDaemonSnapshot
	if err := json.Unmarshal(scanDaemonEvent(t, scanner), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Type != "snapshot" || len(recovered.Projects) != 1 || len(recovered.Projects[0].Tasks) != 1 || recovered.Projects[0].Tasks[0].ID != "T-2" {
		t.Fatalf("recovered snapshot = %#v", recovered)
	}
	stopTaskDaemon(t, cancel, done)
	if !strings.Contains(stderr.String(), "refresh failed: list projects: kata unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTaskDaemonHelpIntervalAndWriteFailure(t *testing.T) {
	provider := newMutableDaemonProvider(nil, nil)
	registry := openDaemonStore(t, provider)
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr}
	if code := app.Run(context.Background(), []string{"tasks", "daemon", "--help"}); code != 0 {
		t.Fatalf("help code = %d", code)
	}
	for _, want := range []string{"newline-delimited JSON", "snapshot event", "error event", "--interval <duration>"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if code := app.Run(context.Background(), []string{"tasks", "daemon", "--interval", "10ms"}); code != 2 {
		t.Fatalf("invalid interval code = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "--interval must be between 250ms and 5m") {
		t.Fatalf("invalid interval output = %s", stdout.String())
	}

	stderr.Reset()
	app.Stdout = failingWriter{}
	if code := app.Run(context.Background(), []string{"tasks", "daemon", "--interval", "5s"}); code != 1 {
		t.Fatalf("write failure code = %d", code)
	}
	if !strings.Contains(stderr.String(), "write event: write failed") {
		t.Fatalf("write failure stderr = %q", stderr.String())
	}
}
func TestTaskDaemonExplicitFormats(t *testing.T) {
	priority := 2
	event := taskDaemonSnapshot{
		Type: "snapshot",
		Projects: []taskDaemonProject{{
			ID: "demo", Name: "Demo", Directory: "/tmp/demo",
			Tasks: []taskDaemonTask{{ID: "T-1", Title: "Fix login", Status: "open", Priority: &priority}},
		}},
	}
	human, err := encodeTaskDaemonEvent(event, "human")
	if err != nil || !strings.Contains(string(human), "\x1b[") || !strings.Contains(string(human), "Fix login") {
		t.Fatalf("human event err=%v output=%q", err, human)
	}
	toon, err := encodeTaskDaemonEvent(event, "toon")
	if err != nil || !strings.Contains(string(toon), "projects[1]:") || !strings.Contains(string(toon), "Fix login") {
		t.Fatalf("TOON event err=%v output=%q", err, toon)
	}
	jsonEvent, err := encodeTaskDaemonEvent(event, "json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded taskDaemonSnapshot
	if err := json.Unmarshal(jsonEvent, &decoded); err != nil || decoded.Projects[0].Tasks[0].ID != "T-1" {
		t.Fatalf("JSON event err=%v output=%q decoded=%#v", err, jsonEvent, decoded)
	}
}
