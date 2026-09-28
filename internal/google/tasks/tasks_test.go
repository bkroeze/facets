package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
)

func TestReferencesRoundTripAndReplacement(t *testing.T) {
	reference := ProjectReference("demo/project", "task/one")
	encoded, err := EncodeReference(reference)
	if err != nil {
		t.Fatal(err)
	}
	parsed, found, err := ParseReference("notes\n" + encoded)
	if err != nil || !found || parsed != reference {
		t.Fatalf("ParseReference() = %#v, %v, %v", parsed, found, err)
	}
	updated, err := WithReference("notes\n"+encoded, PeriodicalReference("daily/1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(updated, referenceLine) != 1 {
		t.Fatalf("reference count = %d, notes=%q", strings.Count(updated, referenceLine), updated)
	}
	periodical, found, err := ParseReference(updated)
	if err != nil || !found || periodical != PeriodicalReference("daily/1") {
		t.Fatalf("periodical reference = %#v, %v, %v", periodical, found, err)
	}
}

func TestClientTaskCRUDAndPagination(t *testing.T) {
	var listRequests []string
	var created googleTask
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/v1/users/@me/lists":
			listRequests = append(listRequests, r.URL.Query().Get("pageToken"))
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("pageToken") == "next" {
				if _, err := w.Write([]byte(`{"items":[{"id":"list-2","title":"Later"}]}`)); err != nil {
					t.Errorf("write response: %v", err)
				}
				return
			}
			if _, err := w.Write([]byte(`{"items":[{"id":"list-1","title":"Today"}],"nextPageToken":"next"}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/tasks/v1/lists/list-1/tasks":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode create: %v", err)
			}
			created.ID, created.Status = "task-1", "needsAction"
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(created); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/v1/lists/list-1/tasks/task-1":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(googleTask{ID: "task-1", Title: "Read", Notes: "old", Status: "needsAction"}); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case r.Method == http.MethodPatch && r.URL.Path == "/tasks/v1/lists/list-1/tasks/task-1":
			var patch googleTask
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			patch.ID, patch.Status = "task-1", "completed"
			if err := json.NewEncoder(w).Encode(patch); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/tasks/v1/lists/list-1/tasks/task-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, ListID: "list-1", TokenSource: StaticTokenSource("token")})
	if err != nil {
		t.Fatal(err)
	}
	lists, err := client.ListLists(context.Background())
	if err != nil || len(lists) != 2 || len(listRequests) != 2 || listRequests[1] != "next" {
		t.Fatalf("ListLists() = %#v, requests=%#v, err=%v", lists, listRequests, err)
	}
	due := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	createdTask, err := client.CreateTask(context.Background(), TaskInput{Title: "Read", Notes: "old", Due: &due, Reference: refPtr(ProjectReference("demo", "T-1"))})
	if err != nil || createdTask.ID != "task-1" || !strings.Contains(created.Notes, "facets://project/demo/task/T-1") {
		t.Fatalf("CreateTask() = %#v, body=%#v, err=%v", createdTask, created, err)
	}
	got, err := client.GetTask(context.Background(), "task-1")
	if err != nil || got.ID != "task-1" {
		t.Fatalf("GetTask() = %#v, err=%v", got, err)
	}
	newTitle := "Read now"
	updated, err := client.UpdateTask(context.Background(), "task-1", TaskPatch{Title: &newTitle, Reference: refPtr(ProjectReference("demo", "T-2"))})
	if err != nil || updated.ID != "task-1" {
		t.Fatalf("UpdateTask() = %#v, err=%v", updated, err)
	}
	completed, err := client.CompleteTask(context.Background(), "task-1")
	if err != nil || completed.Status != StatusCompleted {
		t.Fatalf("CompleteTask() = %#v, err=%v", completed, err)
	}
	if err := client.DeleteTask(context.Background(), "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateTaskPreservesReferenceAndClearsDue(t *testing.T) {
	var patch map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(googleTask{ID: "task-1", Notes: "old\nfacets-ref: facets://project/demo/task/T-1", Due: "2026-08-04T00:00:00Z"}); err != nil {
				t.Errorf("encode response: %v", err)
			}
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			notes, ok := patch["notes"].(string)
			if !ok {
				t.Errorf("notes payload has type %T, want string", patch["notes"])
				return
			}
			if err := json.NewEncoder(w).Encode(googleTask{ID: "task-1", Notes: notes}); err != nil {
				t.Errorf("encode response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, ListID: "list-1", TokenSource: StaticTokenSource("token")})
	if err != nil {
		t.Fatal(err)
	}
	notes := ""
	if _, err := client.UpdateTask(context.Background(), "task-1", TaskPatch{Notes: &notes, ClearDue: true}); err != nil {
		t.Fatal(err)
	}
	notes, ok := patch["notes"].(string)
	if !ok {
		t.Fatalf("notes payload has type %T, want string", patch["notes"])
	}
	if !strings.Contains(notes, "facets://project/demo/task/T-1") {
		t.Fatalf("reference was dropped: %#v", patch)
	}
	if value, ok := patch["due"]; !ok || value != nil {
		t.Fatalf("due clear payload = %#v", patch)
	}
}

func TestClientEscapesResourceIDsOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawPath, "%252F") || !strings.Contains(r.URL.RawPath, "%2F") {
			t.Errorf("RawPath = %q", r.URL.RawPath)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(googleTask{ID: "task/1", Status: string(StatusOpen)}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, ListID: "list/1", TokenSource: StaticTokenSource("token")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetTask(context.Background(), "task/1"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncDuePeriodicalsReopensExistingTasks(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"items":[{"id":"remote-1","title":"Old","notes":"facets-ref: facets://periodical/meditate","status":"completed"}]}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.Method == http.MethodPatch {
			var patch googleTask
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if patch.Status != string(StatusOpen) || !strings.Contains(patch.Notes, "facets://periodical/meditate") {
				t.Errorf("periodical patch = %#v", patch)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(googleTask{ID: "remote-1", Title: patch.Title, Notes: patch.Notes, Status: patch.Status}); err != nil {
				t.Errorf("encode response: %v", err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, ListID: "list-1", TokenSource: StaticTokenSource("token")})
	if err != nil {
		t.Fatal(err)
	}
	source := fakeDueSource{items: []DuePeriodical{{ID: "meditate", Title: "Meditate", Description: "6 of 7", Due: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)}}}
	got, err := SyncDuePeriodicals(context.Background(), client, source, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	if err != nil || len(got) != 1 || requests != 3 {
		t.Fatalf("SyncDuePeriodicals() = %#v, requests=%d, err=%v", got, requests, err)
	}
}

func TestReconcileCompletedProjectTask(t *testing.T) {
	provider := &fakeProjectProvider{task: project.Task{ID: "T-1", ProjectID: "demo", Status: project.StatusOpen}}
	remote := []Task{{ID: "remote-1", Status: StatusCompleted, Notes: "facets-ref: facets://project/demo/task/T-1"}}
	applied, err := ReconcileCompleted(context.Background(), provider, remote)
	if err != nil || len(applied) != 1 || provider.patch.Status == nil || *provider.patch.Status != project.StatusClosed {
		t.Fatalf("ReconcileCompleted() = %#v, patch=%#v, err=%v", applied, provider.patch, err)
	}
	if provider.patch.Completion == nil || len(provider.patch.Completion.Evidence) == 0 || provider.patch.Completion.Evidence[0] != "remote-1" {
		t.Fatalf("completion = %#v", provider.patch.Completion)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte(`{"error":{"message":"invalid token"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, ListID: "list-1", TokenSource: StaticTokenSource("token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetTask(context.Background(), "task-1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "invalid token" {
		t.Fatalf("error = %v", err)
	}
}

type fakeDueSource struct{ items []DuePeriodical }

func (f fakeDueSource) DuePeriodicals(context.Context, time.Time) ([]DuePeriodical, error) {
	return f.items, nil
}

type fakeProjectProvider struct {
	task  project.Task
	patch project.TaskPatch
}

func (f *fakeProjectProvider) Name() string { return "fake" }
func (f *fakeProjectProvider) ListProjects(context.Context) ([]project.Project, error) {
	return nil, nil
}
func (f *fakeProjectProvider) GetProject(context.Context, string) (project.Project, error) {
	return project.Project{}, project.ErrNotFound
}
func (f *fakeProjectProvider) CreateProject(context.Context, project.ProjectInput) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (f *fakeProjectProvider) UpdateProject(context.Context, string, project.ProjectPatch) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (f *fakeProjectProvider) DeleteProject(context.Context, string) error {
	return project.ErrUnsupported
}
func (f *fakeProjectProvider) ListTasks(context.Context, string, project.TaskFilter) ([]project.Task, error) {
	return []project.Task{f.task}, nil
}
func (f *fakeProjectProvider) GetTask(context.Context, string, string) (project.Task, error) {
	return f.task, nil
}
func (f *fakeProjectProvider) CreateTask(context.Context, string, project.TaskInput) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (f *fakeProjectProvider) UpdateTask(_ context.Context, _ string, _ string, patch project.TaskPatch) (project.Task, error) {
	f.patch = patch
	return f.task, nil
}

func (f *fakeProjectProvider) CommentTask(context.Context, string, string, string) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}

func (f *fakeProjectProvider) DeleteTask(context.Context, string, string) error {
	return project.ErrUnsupported
}

func refPtr(value Reference) *Reference { return &value }
