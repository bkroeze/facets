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

type controlledDaemonProvider struct {
	*mutableDaemonProvider
	controlMu  sync.Mutex
	blockFirst bool
	blockCall  int
	firstDelay time.Duration
	starts     chan time.Time
	finishes   chan time.Time
	calls      int
}

func (p *controlledDaemonProvider) ListProjects(ctx context.Context) ([]project.Project, error) {
	started := time.Now()
	if p.starts != nil {
		p.starts <- started
	}
	p.controlMu.Lock()
	p.calls++
	call := p.calls
	block := p.blockFirst
	blockCall := p.blockCall
	delay := p.firstDelay
	p.controlMu.Unlock()
	defer func() {
		if p.finishes != nil {
			p.finishes <- time.Now()
		}
	}()

	if (call == 1 && block) || call == blockCall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if call == 1 && delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return p.mutableDaemonProvider.ListProjects(ctx)
}

type failAfterWriter struct {
	writes int
}

func (w *failAfterWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("write failed")
	}
	return len(data), nil
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
	return startTaskDaemonAtRuntime(t, provider, registry, interval, t.TempDir(), nil)
}

func startTaskDaemonAtRuntime(t *testing.T, provider project.Provider, registry *store.Store, interval time.Duration, runtimeDir string, configure func(*App)) (*bufio.Scanner, context.CancelFunc, <-chan int, *bytes.Buffer) {
	t.Helper()
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readPipe.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &bytes.Buffer{}
	app := App{
		Provider:           provider,
		ProjectStore:       registry,
		Stdout:             writePipe,
		Stderr:             stderr,
		Env:                map[string]string{"XDG_RUNTIME_DIR": runtimeDir},
		TaskDaemonInterval: interval,
		TaskDaemonLockPath: filepath.Join(runtimeDir, "daemon.lock"),
	}
	if configure != nil {
		configure(&app)
	}
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
			{ID: "T-2", ProjectID: "alpha", Title: "Beta", Status: project.StatusOpen, Priority: &priority, Assignee: "bruce", Metadata: map[string]any{"facets.top": "true"}, UpdatedAt: time.Date(2026, 8, 10, 12, 0, 0, 123, time.UTC)},
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
	if alpha.Tasks[1].UpdatedAt != "2026-08-10T12:00:00.000000123Z" || alpha.Tasks[1].Priority == nil || *alpha.Tasks[1].Priority != priority || !alpha.Tasks[1].Top {
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

func TestTaskDaemonRejectsConcurrentInstance(t *testing.T) {
	withRuntimeEnv := App{Env: map[string]string{"XDG_RUNTIME_DIR": t.TempDir()}}
	withoutRuntimeEnv := App{}
	if withRuntimeEnv.taskDaemonLockFile() != withoutRuntimeEnv.taskDaemonLockFile() {
		t.Fatalf("default lock path depends on XDG_RUNTIME_DIR: %q != %q", withRuntimeEnv.taskDaemonLockFile(), withoutRuntimeEnv.taskDaemonLockFile())
	}

	provider := newMutableDaemonProvider(nil, nil)
	registry := openDaemonStore(t, provider)
	runtimeDir := t.TempDir()
	scanner, cancel, done, _ := startTaskDaemonAtRuntime(t, provider, registry, 5*time.Second, runtimeDir, nil)
	_ = scanDaemonEvent(t, scanner)
	var stdout, stderr bytes.Buffer

	second := App{
		Provider:           provider,
		ProjectStore:       registry,
		Stdout:             &stdout,
		Stderr:             &stderr,
		TaskDaemonInterval: 5 * time.Second,
		TaskDaemonLockPath: filepath.Join(runtimeDir, "daemon.lock"),
	}
	if code := second.Run(context.Background(), []string{"tasks", "daemon"}); code != 1 {
		t.Fatalf("concurrent daemon exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "another tasks daemon is already running") {
		t.Fatalf("concurrent daemon stderr = %q", stderr.String())
	}
	stopTaskDaemon(t, cancel, done)
}

func TestTaskDaemonRefreshTimeoutRecovers(t *testing.T) {
	base := newMutableDaemonProvider([]project.Project{{ID: "demo", Name: "Demo"}}, nil)
	provider := &controlledDaemonProvider{mutableDaemonProvider: base, blockFirst: true}
	registry := openDaemonStore(t, base)
	scanner, cancel, done, stderr := startTaskDaemonAtRuntime(t, provider, registry, minimumTaskDaemonInterval, t.TempDir(), func(app *App) {
		app.TaskDaemonRefreshTimeout = minimumTaskDaemonRefreshTimeout
	})

	var failure taskDaemonError
	if err := json.Unmarshal(scanDaemonEvent(t, scanner), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Type != "error" || !strings.Contains(failure.Message, "context deadline exceeded") {
		t.Fatalf("timeout event = %#v", failure)
	}

	var recovered taskDaemonSnapshot
	if err := json.Unmarshal(scanDaemonEvent(t, scanner), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Type != "snapshot" || len(recovered.Projects) != 1 || recovered.Projects[0].ID != "demo" {
		t.Fatalf("recovered snapshot = %#v", recovered)
	}
	stopTaskDaemon(t, cancel, done)
	if !strings.Contains(stderr.String(), "refresh failed: list projects: context deadline exceeded") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTaskDaemonWaitsIntervalAfterRefreshCompletion(t *testing.T) {
	base := newMutableDaemonProvider(nil, nil)
	starts := make(chan time.Time, 4)
	finishes := make(chan time.Time, 4)
	provider := &controlledDaemonProvider{
		mutableDaemonProvider: base,
		firstDelay:            350 * time.Millisecond,
		starts:                starts,
		finishes:              finishes,
	}
	registry := openDaemonStore(t, base)
	scanner, cancel, done, _ := startTaskDaemonAtRuntime(t, provider, registry, minimumTaskDaemonInterval, t.TempDir(), func(app *App) {
		app.TaskDaemonRefreshTimeout = 2 * time.Second
	})
	_ = scanDaemonEvent(t, scanner)

	var firstFinished, secondStarted time.Time
	select {
	case firstFinished = <-finishes:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first refresh completion")
	}
	<-starts
	select {
	case secondStarted = <-starts:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second refresh")
	}
	if gap := secondStarted.Sub(firstFinished); gap < minimumTaskDaemonInterval-25*time.Millisecond {
		t.Fatalf("next refresh started after %s, want at least %s", gap, minimumTaskDaemonInterval)
	}
	stopTaskDaemon(t, cancel, done)
}

func TestTaskDaemonHeartbeatDetectsWriteFailure(t *testing.T) {
	provider := newMutableDaemonProvider(nil, nil)
	registry := openDaemonStore(t, provider)
	writer := &failAfterWriter{}
	var stderr bytes.Buffer
	app := App{
		Provider:                    provider,
		ProjectStore:                registry,
		Stdout:                      writer,
		Stderr:                      &stderr,
		TaskDaemonInterval:          maximumTaskDaemonInterval,
		TaskDaemonHeartbeatInterval: 25 * time.Millisecond,
		TaskDaemonLockPath:          filepath.Join(t.TempDir(), "daemon.lock"),
	}
	if code := app.Run(context.Background(), []string{"tasks", "daemon"}); code != 1 {
		t.Fatalf("heartbeat write failure exit code = %d", code)
	}
	if writer.writes != 2 || !strings.Contains(stderr.String(), "write heartbeat: write failed") {
		t.Fatalf("writes = %d, stderr = %q", writer.writes, stderr.String())
	}
}

func TestTaskDaemonHeartbeatContinuesDuringRefresh(t *testing.T) {
	base := newMutableDaemonProvider(nil, nil)
	provider := &controlledDaemonProvider{mutableDaemonProvider: base, blockCall: 2, starts: make(chan time.Time, 2)}
	registry := openDaemonStore(t, base)
	writer := &failAfterWriter{}
	var stderr bytes.Buffer
	app := App{
		Provider:                    provider,
		ProjectStore:                registry,
		Stdout:                      writer,
		Stderr:                      &stderr,
		TaskDaemonInterval:          minimumTaskDaemonInterval,
		TaskDaemonRefreshTimeout:    maximumTaskDaemonRefreshTimeout,
		TaskDaemonHeartbeatInterval: 400 * time.Millisecond,
		TaskDaemonLockPath:          filepath.Join(t.TempDir(), "daemon.lock"),
	}

	done := make(chan int, 1)
	go func() {
		done <- app.Run(context.Background(), []string{"tasks", "daemon"})
	}()
	select {
	case <-provider.starts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial refresh")
	}
	select {
	case <-provider.starts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked refresh")
	}

	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("heartbeat write failure exit code = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not detect a detached consumer during refresh")
	}
	if writer.writes != 2 || !strings.Contains(stderr.String(), "write heartbeat: write failed") {
		t.Fatalf("writes = %d, stderr = %q", writer.writes, stderr.String())
	}
}

func TestTaskDaemonHelpIntervalAndWriteFailure(t *testing.T) {
	provider := newMutableDaemonProvider(nil, nil)
	registry := openDaemonStore(t, provider)
	var stdout, stderr bytes.Buffer
	app := App{Provider: provider, ProjectStore: registry, Stdout: &stdout, Stderr: &stderr, TaskDaemonLockPath: filepath.Join(t.TempDir(), "daemon.lock")}
	if code := app.Run(context.Background(), []string{"tasks", "daemon", "--help"}); code != 0 {
		t.Fatalf("help code = %d", code)
	}
	for _, want := range []string{"newline-delimited JSON", "snapshot event", "error event", "--interval <duration>", "--refresh-timeout <duration>"} {
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

	stdout.Reset()
	if code := app.Run(context.Background(), []string{"tasks", "daemon", "--refresh-timeout", "10ms"}); code != 2 {
		t.Fatalf("invalid refresh timeout code = %d, stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "--refresh-timeout must be between 250ms and 5m") {
		t.Fatalf("invalid refresh timeout output = %s", stdout.String())
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
