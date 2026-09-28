package kata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
)

type scriptStep struct {
	err    error
	binary string
	stdout string
	stderr string
	args   []string
}

type scriptedRunner struct {
	t     *testing.T
	steps []scriptStep
	next  int
}

func (r *scriptedRunner) Run(_ context.Context, binary string, args ...string) ([]byte, []byte, error) {
	r.t.Helper()
	if r.next >= len(r.steps) {
		r.t.Fatalf("unexpected command %q %q", binary, args)
	}
	step := r.steps[r.next]
	r.next++
	if binary != step.binary {
		r.t.Errorf("command %d binary = %q, want %q", r.next, binary, step.binary)
	}
	if !reflect.DeepEqual(args, step.args) {
		r.t.Errorf("command %d args = %#v, want %#v", r.next, args, step.args)
	}
	return []byte(step.stdout), []byte(step.stderr), step.err
}

func (r *scriptedRunner) done() {
	r.t.Helper()
	if r.next != len(r.steps) {
		r.t.Fatalf("executed %d commands, want %d", r.next, len(r.steps))
	}
}

func step(args []string, stdout string) scriptStep {
	return scriptStep{binary: "/test/kata", args: withOutput(args...), stdout: stdout}
}

func withOutput(args ...string) []string {
	return append(args, "--json", "--as", "robot")
}

const projectsJSON = `{
  "kata_api_version": 1,
  "projects": [
    {"id": 8, "uid": "01PROJECTA", "name": "alpha", "metadata": {"team": "core"}, "revision": 3, "created_at": "2026-08-01T10:11:12Z"},
    {"id": 9, "uid": "01PROJECTB", "name": "beta", "metadata": {}, "revision": 1, "created_at": "2026-08-02T10:11:12.123Z"}
  ]
}`

const projectJSON = `{
  "kata_api_version": 1,
  "project": {"id": 8, "uid": "01PROJECTA", "name": "alpha", "metadata": {"team": "core"}, "revision": 3, "created_at": "2026-08-01T10:11:12Z", "updated_at": "2026-08-03T10:11:12Z"},
  "aliases": [
    {"id": 1, "project_id": 8, "alias_identity": "local:///work/alpha", "alias_kind": "local", "created_at": "2026-08-01T10:11:13Z"},
    {"id": 2, "project_id": 8, "alias_identity": "alpha-alias", "alias_kind": "name", "created_at": "2026-08-01T10:11:14Z"}
  ]
}`

const createdProjectJSON = `{"kata_api_version":1,"project":{"id":10,"uid":"01PROJECTC","name":"gamma","metadata":{},"revision":1,"created_at":"2026-08-04T10:11:12Z"},"aliases":[]}`
const renamedProjectJSON = `{"kata_api_version":1,"project":{"id":8,"uid":"01PROJECTA","name":"delta","metadata":{"team":"core"},"revision":4,"created_at":"2026-08-01T10:11:12Z","updated_at":"2026-08-04T11:12:13Z"},"aliases":[]}`
const successJSON = `{"kata_api_version":1}`

const openIssue = `{
  "id": 21,
  "uid": "01TASKOPEN",
  "project_id": 8,
  "project_uid": "01PROJECTA",
  "short_id": "op21",
  "qualified_id": "alpha#op21",
  "title": "Open task",
  "body": "task body",
  "status": "open",
  "owner": "sam",
  "priority": 1,
  "author": "alex",
  "metadata": {"native.key": "native-value"},
  "revision": 4,
  "created_at": "2026-08-01T12:00:00Z",
  "updated_at": "2026-08-02T13:30:00.125Z"
}`

const closedIssue = `{
  "id": 22,
  "uid": "01TASKCLOSED",
  "project_id": 8,
  "project_uid": "01PROJECTA",
  "short_id": "cl22",
  "qualified_id": "alpha#cl22",
  "title": "Closed task",
  "body": "finished",
  "status": "closed",
  "owner": "lee",
  "priority": 3,
  "author": "alex",
  "metadata": {},
  "revision": 6,
  "created_at": "2026-07-30T12:00:00Z",
  "updated_at": "2026-08-03T13:30:00Z"
}`

func issuesEnvelope(issues ...string) string {
	return `{"kata_api_version":1,"issues":[` + strings.Join(issues, ",") + `]}`
}

func issueEnvelope(issue string) string {
	return `{"kata_api_version":1,"issue":` + issue + `}`
}

func TestProjectCRUDCommandsAndMapping(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"projects", "list"}, projectsJSON),
		step([]string{"projects", "show", "alpha"}, projectJSON),
		step([]string{"projects", "create", "gamma"}, createdProjectJSON),
		step([]string{"projects", "rename", "alpha", "delta"}, renamedProjectJSON),
		step([]string{"projects", "remove", "delta", "--force"}, successJSON),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	ctx := context.Background()

	projects, err := provider.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if got := []string{projects[0].ID, projects[1].ID}; !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("project order/IDs = %v", got)
	}
	if projects[0].Name != "alpha" || projects[0].Metadata["native_id"] != int64(8) || projects[0].Metadata["uid"] != "01PROJECTA" || projects[0].Metadata["revision"] != 3 || projects[0].Metadata["team"] != "core" {
		t.Fatalf("mapped list project = %#v", projects[0])
	}
	if want := time.Date(2026, 8, 1, 10, 11, 12, 0, time.UTC); !projects[0].CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", projects[0].CreatedAt, want)
	}

	gotProject, err := provider.GetProject(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if gotProject.ID != "alpha" || gotProject.Name != "alpha" {
		t.Fatalf("project identity = %#v", gotProject)
	}
	if aliases, ok := gotProject.Metadata["aliases"].([]string); !ok || !reflect.DeepEqual(aliases, []string{"local:///work/alpha", "alpha-alias"}) {
		t.Fatalf("aliases = %#v", gotProject.Metadata["aliases"])
	}
	if want := time.Date(2026, 8, 3, 10, 11, 12, 0, time.UTC); !gotProject.UpdatedAt.Equal(want) {
		t.Fatalf("UpdatedAt = %v, want %v", gotProject.UpdatedAt, want)
	}

	created, err := provider.CreateProject(ctx, project.ProjectInput{Name: "gamma"})
	if err != nil || created.ID != "gamma" {
		t.Fatalf("CreateProject = %#v, %v", created, err)
	}
	renamed, err := provider.UpdateProject(ctx, "alpha", project.ProjectPatch{Name: new("delta")})
	if err != nil || renamed.ID != "delta" || renamed.Metadata["revision"] != 4 {
		t.Fatalf("UpdateProject = %#v, %v", renamed, err)
	}
	if err := provider.DeleteProject(ctx, "delta"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	runner.done()
}

func TestTaskCRUDCommandsFiltersAndMapping(t *testing.T) {
	updatedIssue := strings.Replace(openIssue, `"title": "Open task"`, `"title": "Revised"`, 1)
	updatedIssue = strings.Replace(updatedIssue, `"body": "task body"`, `"body": ""`, 1)
	updatedIssue = strings.Replace(updatedIssue, `"owner": "sam"`, `"owner": ""`, 1)
	updatedIssue = strings.Replace(updatedIssue, `"priority": 1`, `"priority": 4`, 1)
	clearedIssue := strings.Replace(updatedIssue, "  \"priority\": 4,\n", "", 1)
	closedAfterClear := strings.Replace(clearedIssue, `"status": "open"`, `"status": "closed"`, 1)
	issueWithoutPriority := strings.Replace(openIssue, "  \"priority\": 1,\n", "", 1)

	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"list", "--project", "alpha", "--status", "all"}, issuesEnvelope(openIssue, closedIssue)),
		step([]string{"list", "--project", "alpha", "--status", "open"}, issuesEnvelope(openIssue)),
		step([]string{"list", "--project", "alpha", "--status", "closed"}, issuesEnvelope(closedIssue)),
		step([]string{"show", "op21", "--project", "alpha"}, issueEnvelope(openIssue)),
		step([]string{"create", "New task", "--project", "alpha", "--body", "details", "--priority", "2", "--owner", "sam", "--idempotency-key", "request-17", "--meta", "a=first", "--meta", "rank=2", "--meta", "z=last"}, issueEnvelope(openIssue)),
		step([]string{"create", "Default priority", "--project", "alpha"}, issueEnvelope(issueWithoutPriority)),
		step([]string{"edit", "op21", "--project", "alpha", "--title", "Revised", "--body", "", "--priority", "4", "--owner", ""}, issueEnvelope(updatedIssue)),
		step([]string{"edit", "op21", "--project", "alpha", "--priority", "-"}, issueEnvelope(clearedIssue)),
		step([]string{"close", "op21", "--project", "alpha", "--reason", "done", "--message", "verified end to end", "--evidence", "test:go test ./internal/project/kata", "--evidence", "commit:abc123"}, issueEnvelope(closedAfterClear)),
		step([]string{"reopen", "op21", "--project", "alpha"}, issueEnvelope(clearedIssue)),
		step([]string{"delete", "op21", "--project", "alpha", "--confirm", "DELETE op21", "--force"}, successJSON),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	ctx := context.Background()

	tasks, err := provider.ListTasks(ctx, "alpha", project.TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	if got := []string{tasks[0].ID, tasks[1].ID}; !reflect.DeepEqual(got, []string{"op21", "cl22"}) {
		t.Fatalf("task order = %v", got)
	}
	assertMappedTask(t, tasks[0])

	open := project.StatusOpen
	if _, listErr := provider.ListTasks(ctx, "alpha", project.TaskFilter{Status: &open}); listErr != nil {
		t.Fatalf("ListTasks open: %v", listErr)
	}
	closed := project.StatusClosed
	if _, listErr := provider.ListTasks(ctx, "alpha", project.TaskFilter{Status: &closed}); listErr != nil {
		t.Fatalf("ListTasks closed: %v", listErr)
	}

	gotTask, err := provider.GetTask(ctx, "alpha", "op21")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	assertMappedTask(t, gotTask)

	created, err := provider.CreateTask(ctx, "alpha", project.TaskInput{
		Title:          "New task",
		Description:    "details",
		Priority:       new(2),
		Assignee:       "sam",
		IdempotencyKey: "request-17",
		Metadata:       map[string]any{"z": "last", "rank": 2, "a": "first"},
	})
	if err != nil || created.ID != "op21" {
		t.Fatalf("CreateTask = %#v, %v", created, err)
	}
	defaultPriority, err := provider.CreateTask(ctx, "alpha", project.TaskInput{Title: "Default priority"})
	if err != nil || defaultPriority.Priority != nil {
		t.Fatalf("CreateTask without priority = %#v, %v", defaultPriority, err)
	}

	updated, err := provider.UpdateTask(ctx, "alpha", "op21", project.TaskPatch{
		Title:       new("Revised"),
		Description: new(""),
		Priority:    project.PriorityPatch{Set: true, Value: new(4)},
		Assignee:    new(""),
	})
	if err != nil || updated.Title != "Revised" || updated.Description != "" || updated.Priority == nil || *updated.Priority != 4 || updated.Assignee != "" {
		t.Fatalf("UpdateTask fields = %#v, %v", updated, err)
	}
	cleared, err := provider.UpdateTask(ctx, "alpha", "op21", project.TaskPatch{
		Priority: project.PriorityPatch{Set: true},
	})
	if err != nil || cleared.Priority != nil {
		t.Fatalf("UpdateTask clear priority = %#v, %v", cleared, err)
	}

	closedTask, err := provider.UpdateTask(ctx, "alpha", "op21", project.TaskPatch{
		Status: &closed,
		Completion: &project.Completion{
			Message:  "verified end to end",
			Evidence: []string{"test:go test ./internal/project/kata", "commit:abc123"},
		},
	})
	if err != nil || closedTask.Status != project.StatusClosed {
		t.Fatalf("UpdateTask close = %#v, %v", closedTask, err)
	}
	reopened, err := provider.UpdateTask(ctx, "alpha", "op21", project.TaskPatch{Status: &open})
	if err != nil || reopened.Status != project.StatusOpen {
		t.Fatalf("UpdateTask reopen = %#v, %v", reopened, err)
	}
	if err := provider.DeleteTask(ctx, "alpha", "op21"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	runner.done()
}

func TestListTasksPreservesTopMetadataType(t *testing.T) {
	issue := strings.Replace(openIssue, `"native.key": "native-value"`, `"facets.top": true, "native.key": "native-value"`, 1)
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"list", "--project", "alpha", "--status", "open"}, issuesEnvelope(issue)),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	status := project.StatusOpen
	tasks, err := provider.ListTasks(context.Background(), "alpha", project.TaskFilter{Status: &status})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Metadata["facets.top"] != true {
		t.Fatalf("top metadata = %#v", tasks)
	}
	runner.done()
}

func assertMappedTask(t *testing.T, task project.Task) {
	t.Helper()
	if task.ID != "op21" || task.ProjectID != "alpha" || task.Title != "Open task" || task.Description != "task body" || task.Status != project.StatusOpen || task.Priority == nil || *task.Priority != 1 || task.Assignee != "sam" {
		t.Fatalf("mapped task = %#v", task)
	}
	wantMetadata := map[string]any{
		"native.key":   "native-value",
		"native_id":    int64(21),
		"uid":          "01TASKOPEN",
		"qualified_id": "alpha#op21",
		"author":       "alex",
		"revision":     4,
		"project_uid":  "01PROJECTA",
	}
	if !reflect.DeepEqual(task.Metadata, wantMetadata) {
		t.Fatalf("task metadata = %#v, want %#v", task.Metadata, wantMetadata)
	}
	if want := time.Date(2026, 8, 2, 13, 30, 0, 125000000, time.UTC); !task.UpdatedAt.Equal(want) {
		t.Fatalf("task UpdatedAt = %v, want %v", task.UpdatedAt, want)
	}
}

func TestUpdateTaskEditsBeforeStatusTransition(t *testing.T) {
	closed := project.StatusClosed
	edited := strings.Replace(openIssue, `"title": "Open task"`, `"title": "Revised"`, 1)
	closedIssue := strings.Replace(edited, `"status": "open"`, `"status": "closed"`, 1)
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"edit", "op21", "--project", "alpha", "--title", "Revised"}, issueEnvelope(edited)),
		step([]string{"close", "op21", "--project", "alpha", "--reason", "done", "--message", "verified", "--evidence", "test:focused"}, issueEnvelope(closedIssue)),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	result, err := provider.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{
		Title: new("Revised"), Status: &closed,
		Completion: &project.Completion{Message: "verified", Evidence: []string{"test:focused"}},
	})
	if err != nil || result.Status != project.StatusClosed || result.Title != "Revised" {
		t.Fatalf("UpdateTask combined = %#v, %v", result, err)
	}
	runner.done()
}

func TestUpdateTaskClosePassesOptionalComment(t *testing.T) {
	closed := project.StatusClosed
	closedIssue := strings.Replace(openIssue, `"status": "open"`, `"status": "closed"`, 1)
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"close", "op21", "--project", "alpha", "--reason", "done", "--message", "verified", "--evidence", "test:focused", "--comment", "Released to users"}, issueEnvelope(closedIssue)),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	result, err := provider.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{
		Status: &closed,
		Completion: &project.Completion{
			Message: "verified", Evidence: []string{"test:focused"}, Comment: "Released to users",
		},
	})
	if err != nil || result.Status != project.StatusClosed {
		t.Fatalf("UpdateTask close with comment = %#v, %v", result, err)
	}
	runner.done()
}

func TestUpdateTaskMetadataSetAndUnset(t *testing.T) {
	setIssue := strings.Replace(openIssue, `"native.key": "native-value"`, `"facets.top": "true", "native.key": "native-value"`, 1)
	falseIssue := strings.Replace(setIssue, `"facets.top": "true"`, `"facets.top": "false"`, 1)
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"meta", "set", "op21", "facets.top", "true", "--project", "alpha"}, issueEnvelope(setIssue)),
		step([]string{"meta", "set", "op21", "facets.top", "false", "--project", "alpha"}, issueEnvelope(falseIssue)),
		step([]string{"meta", "unset", "op21", "facets.top", "--project", "alpha"}, issueEnvelope(openIssue)),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})

	for _, value := range []string{"true", "false"} {
		updated, err := provider.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{
			Metadata: map[string]any{"facets.top": value},
		})
		if err != nil {
			t.Fatalf("set facets.top=%s: %v", value, err)
		}
		if updated.Metadata["facets.top"] != value {
			t.Fatalf("set facets.top=%s metadata = %#v", value, updated.Metadata)
		}
	}
	updated, err := provider.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{
		Metadata: map[string]any{"facets.top": nil},
	})
	if err != nil {
		t.Fatalf("unset facets.top: %v", err)
	}
	if _, ok := updated.Metadata["facets.top"]; ok {
		t.Fatalf("unset facets.top metadata = %#v", updated.Metadata)
	}
	runner.done()
}

func TestCommentTaskCommandAndMapping(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"comment", "op21", "--project", "alpha", "--body", "Follow-up context"}, issueEnvelope(openIssue)),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	commented, err := provider.CommentTask(context.Background(), "alpha", "op21", "Follow-up context")
	if err != nil {
		t.Fatalf("CommentTask: %v", err)
	}
	assertMappedTask(t, commented)
	runner.done()
}

func TestDefaultBinaryAndOptionalActor(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptStep{{
		binary: "kata",
		args:   []string{"projects", "list", "--json"},
		stdout: `{"kata_api_version":1,"projects":[]}`,
	}}}
	provider := New(Config{Runner: runner})
	if provider.Name() != "kata" {
		t.Fatalf("Name = %q", provider.Name())
	}
	projects, err := provider.ListProjects(context.Background())
	if err != nil || projects == nil || len(projects) != 0 {
		t.Fatalf("ListProjects = %#v, %v", projects, err)
	}
	runner.done()
}

func TestRejectsUnsupportedAPIVersion(t *testing.T) {
	runner := &scriptedRunner{t: t, steps: []scriptStep{
		step([]string{"projects", "list"}, `{"kata_api_version":2,"projects":[]}`),
	}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	_, err := provider.ListProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported kata_api_version 2") {
		t.Fatalf("error = %v", err)
	}
	runner.done()
}

func TestCommandFailureRetainsContextStderrAndCause(t *testing.T) {
	cause := errors.New("exit status 17")
	runner := &scriptedRunner{t: t, steps: []scriptStep{{
		binary: "/test/kata",
		args:   withOutput("show", "op21", "--project", "alpha"),
		stderr: `daemon unavailable: connection refused`,
		err:    cause,
	}}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	_, err := provider.GetTask(context.Background(), "alpha", "op21")
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("error does not retain cause: %v", err)
	}
	if !strings.Contains(err.Error(), `"/test/kata" "show" "op21" "--project" "alpha" "--json" "--as" "robot"`) || !strings.Contains(err.Error(), "daemon unavailable: connection refused") {
		t.Fatalf("error lacks command/stderr: %v", err)
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Stderr != "daemon unavailable: connection refused" {
		t.Fatalf("CommandError = %#v", commandErr)
	}
	runner.done()
}

func TestNotFoundFailureWrapsSentinelAndCommandCause(t *testing.T) {
	cause := errors.New("exit status 1")
	runner := &scriptedRunner{t: t, steps: []scriptStep{{
		binary: "/test/kata",
		args:   withOutput("projects", "show", "missing"),
		stdout: `{"kata_api_version":1,"error":{"kind":"not_found","code":"issue_not_found","message":"project missing"}}`,
		stderr: `request failed`,
		err:    cause,
	}}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	_, err := provider.GetProject(context.Background(), "missing")
	if !errors.Is(err, project.ErrNotFound) || !errors.Is(err, cause) {
		t.Fatalf("error = %v; ErrNotFound=%v cause=%v", err, errors.Is(err, project.ErrNotFound), errors.Is(err, cause))
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("error lacks diagnostics: %v", err)
	}
	runner.done()
}

func TestCommandCancellationRetainsContextAndCommandErrors(t *testing.T) {
	cause := errors.New("process terminated")
	runner := &scriptedRunner{t: t, steps: []scriptStep{{
		binary: "/test/kata",
		args:   withOutput("show", "op21", "--project", "alpha"),
		stderr: "terminated",
		err:    cause,
	}}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.GetTask(ctx, "alpha", "op21")
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("error = %v; canceled=%v cause=%v", err, errors.Is(err, context.Canceled), errors.Is(err, cause))
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error does not retain CommandError: %v", err)
	}
	runner.done()
}

func TestExecRunnerCancellationKillsDescendants(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is unavailable")
	}
	tempDir := t.TempDir()
	marker := filepath.Join(tempDir, "survived")
	ready := filepath.Join(tempDir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, _, err := (execRunner{}).Run(
			ctx,
			"/bin/sh",
			"-c",
			`(sleep 0.5; printf survived > "$1") & printf ready > "$2"; wait`,
			"facets-exec-runner-test",
			marker,
			ready,
		)
		done <- result{err: err}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant process did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case result := <-done:
		if result.err == nil {
			t.Fatal("execRunner.Run() succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("execRunner.Run() remained blocked after cancellation")
	}

	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived process-group cancellation: marker error = %v", err)
	}
}

func TestNotFoundRequiresStructuredExactKind(t *testing.T) {
	cause := errors.New("exit status 1")
	runner := &scriptedRunner{t: t, steps: []scriptStep{{
		binary: "/test/kata",
		args:   withOutput("projects", "show", "missing"),
		stdout: `{"error":{"kind":"conflict","code":"issue_not_found","message":"contains not_found"}}`,
		stderr: "not_found",
		err:    cause,
	}}}
	provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
	_, err := provider.GetProject(context.Background(), "missing")
	if errors.Is(err, project.ErrNotFound) {
		t.Fatalf("substring/code false positive wrapped ErrNotFound: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error does not retain cause: %v", err)
	}
	runner.done()
}

func TestProjectResponsesRequireProjectAndName(t *testing.T) {
	tests := []struct {
		call   func(*Provider) error
		name   string
		stdout string
		args   []string
	}{
		{
			name:   "list missing name",
			args:   []string{"projects", "list"},
			stdout: `{"kata_api_version":1,"projects":[{}]}`,
			call: func(p *Provider) error {
				_, err := p.ListProjects(context.Background())
				return err
			},
		},
		{
			name:   "show missing project",
			args:   []string{"projects", "show", "alpha"},
			stdout: `{"kata_api_version":1}`,
			call: func(p *Provider) error {
				_, err := p.GetProject(context.Background(), "alpha")
				return err
			},
		},
		{
			name:   "create null project",
			args:   []string{"projects", "create", "alpha"},
			stdout: `{"kata_api_version":1,"project":null}`,
			call: func(p *Provider) error {
				_, err := p.CreateProject(context.Background(), project.ProjectInput{Name: "alpha"})
				return err
			},
		},
		{
			name:   "rename missing name",
			args:   []string{"projects", "rename", "alpha", "next"},
			stdout: `{"kata_api_version":1,"project":{"id":8,"uid":"uid","name":" "}}`,
			call: func(p *Provider) error {
				_, err := p.UpdateProject(context.Background(), "alpha", project.ProjectPatch{Name: new("next")})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{t: t, steps: []scriptStep{step(test.args, test.stdout)}}
			provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
			if err := test.call(provider); err == nil {
				t.Fatal("expected invalid response error")
			}
			runner.done()
		})
	}
}

func TestTaskPriorityMappingCopiesOptionalValue(t *testing.T) {
	priority := 2
	task, err := mapIssue(rawIssue{ShortID: "op21", Status: "open", Priority: &priority}, "alpha")
	if err != nil {
		t.Fatalf("mapIssue: %v", err)
	}
	priority = 4
	if task.Priority == nil || *task.Priority != 2 {
		t.Fatalf("mapped priority aliases source or is missing: %#v", task.Priority)
	}

	task, err = mapIssue(rawIssue{ShortID: "op21", Status: "open"}, "alpha")
	if err != nil || task.Priority != nil {
		t.Fatalf("mapIssue absent priority = %#v, %v", task.Priority, err)
	}
}

func TestEmptyMetadataPatchesAreExplicitlyUnsupported(t *testing.T) {
	runner := &countingRunner{}
	provider := New(Config{Runner: runner})
	_, projectErr := provider.UpdateProject(context.Background(), "alpha", project.ProjectPatch{
		Name:     new("next"),
		Metadata: map[string]any{},
	})
	_, taskErr := provider.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{
		Title:    new("next"),
		Metadata: map[string]any{},
	})
	if !errors.Is(projectErr, project.ErrUnsupported) || !errors.Is(taskErr, project.ErrUnsupported) {
		t.Fatalf("project error = %v; task error = %v", projectErr, taskErr)
	}
	if runner.calls != 0 {
		t.Fatalf("runner called %d times", runner.calls)
	}
}

type countingRunner struct{ calls int }

func (r *countingRunner) Run(context.Context, string, ...string) ([]byte, []byte, error) {
	r.calls++
	return nil, nil, errors.New("runner must not be called")
}

func TestValidationRejectsInvalidInputBeforeCommands(t *testing.T) {
	closed := project.StatusClosed
	open := project.StatusOpen
	invalid := project.Status("pending")
	tests := []struct {
		call func(*Provider) error
		name string
	}{
		{name: "get project ID", call: func(p *Provider) error { _, err := p.GetProject(context.Background(), " "); return err }},
		{name: "create project name", call: func(p *Provider) error {
			_, err := p.CreateProject(context.Background(), project.ProjectInput{})
			return err
		}},
		{name: "create project unsupported fields", call: func(p *Provider) error {
			_, err := p.CreateProject(context.Background(), project.ProjectInput{Name: "alpha", Description: "description"})
			return err
		}},
		{name: "update project ID", call: func(p *Provider) error {
			_, err := p.UpdateProject(context.Background(), "", project.ProjectPatch{Name: new("next")})
			return err
		}},
		{name: "update project empty patch", call: func(p *Provider) error {
			_, err := p.UpdateProject(context.Background(), "alpha", project.ProjectPatch{})
			return err
		}},
		{name: "update project empty name", call: func(p *Provider) error {
			_, err := p.UpdateProject(context.Background(), "alpha", project.ProjectPatch{Name: new(" ")})
			return err
		}},
		{name: "update project empty metadata replacement", call: func(p *Provider) error {
			_, err := p.UpdateProject(context.Background(), "alpha", project.ProjectPatch{Name: new("next"), Metadata: map[string]any{}})
			if !errors.Is(err, project.ErrUnsupported) {
				return errors.New("expected ErrUnsupported")
			}
			return err
		}},
		{name: "delete project ID", call: func(p *Provider) error { return p.DeleteProject(context.Background(), "") }},
		{name: "list task project ID", call: func(p *Provider) error {
			_, err := p.ListTasks(context.Background(), "", project.TaskFilter{})
			return err
		}},
		{name: "list task status", call: func(p *Provider) error {
			_, err := p.ListTasks(context.Background(), "alpha", project.TaskFilter{Status: &invalid})
			return err
		}},
		{name: "get task project ID", call: func(p *Provider) error { _, err := p.GetTask(context.Background(), "", "op21"); return err }},
		{name: "get task ID", call: func(p *Provider) error { _, err := p.GetTask(context.Background(), "alpha", ""); return err }},
		{name: "create task title", call: func(p *Provider) error {
			_, err := p.CreateTask(context.Background(), "alpha", project.TaskInput{Title: " "})
			return err
		}},
		{name: "create task priority", call: func(p *Provider) error {
			_, err := p.CreateTask(context.Background(), "alpha", project.TaskInput{Title: "title", Priority: new(5)})
			return err
		}},
		{name: "update task empty patch", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{})
			return err
		}},
		{name: "update task invalid title", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Title: new(" ")})
			return err
		}},
		{name: "update task invalid priority", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Priority: project.PriorityPatch{Set: true, Value: new(-1)}})
			return err
		}},
		{name: "update task invalid status", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &invalid})
			return err
		}},
		{name: "completion without close", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &open, Completion: &project.Completion{Message: "m", Evidence: []string{"test:x"}}})
			return err
		}},
		{name: "close without completion", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &closed})
			return err
		}},
		{name: "close without message", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &closed, Completion: &project.Completion{Evidence: []string{"test:x"}}})
			return err
		}},
		{name: "close without evidence", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &closed, Completion: &project.Completion{Message: "done"}})
			return err
		}},
		{name: "close with empty evidence", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &closed, Completion: &project.Completion{Message: "done", Evidence: []string{" "}}})
			return err
		}},
		{name: "close with empty comment", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Status: &closed, Completion: &project.Completion{Message: "done", Evidence: []string{"test:x"}, Comment: " "}})
			return err
		}},
		{name: "update task empty metadata replacement", call: func(p *Provider) error {
			_, err := p.UpdateTask(context.Background(), "alpha", "op21", project.TaskPatch{Title: new("next"), Metadata: map[string]any{}})
			if !errors.Is(err, project.ErrUnsupported) {
				return errors.New("expected ErrUnsupported")
			}
			return err
		}},
		{name: "comment task project ID", call: func(p *Provider) error {
			_, err := p.CommentTask(context.Background(), "", "op21", "context")
			return err
		}},
		{name: "comment task ID", call: func(p *Provider) error {
			_, err := p.CommentTask(context.Background(), "alpha", " ", "context")
			return err
		}},
		{name: "comment task body", call: func(p *Provider) error {
			_, err := p.CommentTask(context.Background(), "alpha", "op21", " ")
			return err
		}},
		{name: "delete task project ID", call: func(p *Provider) error { return p.DeleteTask(context.Background(), "", "op21") }},
		{name: "delete task ID", call: func(p *Provider) error { return p.DeleteTask(context.Background(), "alpha", "") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &countingRunner{}
			provider := New(Config{Runner: runner})
			if err := test.call(provider); err == nil {
				t.Fatal("expected validation error")
			}
			if runner.calls != 0 {
				t.Fatalf("runner called %d times", runner.calls)
			}
		})
	}
}

func TestInvalidResponseStatusAndTimestampAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		issue string
		want  string
	}{
		{"status", strings.Replace(openIssue, `"status": "open"`, `"status": "pending"`, 1), `unsupported task status "pending"`},
		{"timestamp", strings.Replace(openIssue, `"created_at": "2026-08-01T12:00:00Z"`, `"created_at": "yesterday"`, 1), "parse task created_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{t: t, steps: []scriptStep{
				step([]string{"show", "op21", "--project", "alpha"}, issueEnvelope(test.issue)),
			}}
			provider := New(Config{Binary: "/test/kata", Actor: "robot", Runner: runner})
			_, err := provider.GetTask(context.Background(), "alpha", "op21")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			runner.done()
		})
	}
}
