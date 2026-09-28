package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
	facetsstore "facets.barnlab.dev/internal/store"
)

func TestAPIV1SavedViewCRUDAndExecution(t *testing.T) {
	t.Parallel()

	priorityOne, priorityTwo := 1, 2
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	provider := &savedViewProviderStub{tasks: []project.Task{
		{ID: "z", ProjectID: "demo", Title: "Zeta", Status: project.StatusOpen, Assignee: "sam", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "a", ProjectID: "demo", Title: "Alpha", Status: project.StatusOpen, Assignee: "sam", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "low", ProjectID: "demo", Title: "Low", Status: project.StatusOpen, Assignee: "sam", Priority: &priorityOne, UpdatedAt: now.Add(time.Hour)},
		{ID: "other", ProjectID: "demo", Title: "Other", Status: project.StatusOpen, Assignee: "bruce", Priority: &priorityTwo, UpdatedAt: now},
	}}
	handler := newSavedViewTestHandler(t, provider)

	createBody := `{"name":"Assigned","query":{"statuses":["open"],"assignees":["sam"],"priorities":[2]},"order":{"field":"updated_at","direction":"desc"}}`
	createdResponse := serveAPIRequest(handler, http.MethodPost, apiV1Prefix+"/views", createBody)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("POST views status = %d, body = %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created apiV1SavedView
	if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created view: %v", err)
	}
	if created.ID == "" || created.Name != "Assigned" || created.Builtin || createdResponse.Header().Get("Location") == "" {
		t.Fatalf("created view = %#v, Location = %q", created, createdResponse.Header().Get("Location"))
	}

	listResponse := serveAPIRequest(handler, http.MethodGet, apiV1Prefix+"/views", "")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("GET views status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listed apiV1SavedViewsResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatalf("decode views: %v", err)
	}
	if len(listed.Views) != 2 || listed.Views[0].ID != project.ActiveViewID || listed.Views[1].ID != created.ID {
		t.Fatalf("views = %#v", listed.Views)
	}

	patchBody := `{"name":"Mine","order":{"field":"title","direction":"asc"}}`
	patchResponse := serveAPIRequest(handler, http.MethodPatch, apiV1Prefix+"/views/"+created.ID, patchBody)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("PATCH view status = %d, body = %s", patchResponse.Code, patchResponse.Body.String())
	}
	var patched apiV1SavedView
	if err := json.NewDecoder(patchResponse.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patched view: %v", err)
	}
	if patched.Name != "Mine" || patched.Order.Field != project.TaskOrderTitle {
		t.Fatalf("patched view = %#v", patched)
	}

	executeResponse := serveAPIRequest(handler, http.MethodGet, apiV1Prefix+"/projects/demo/views/"+created.ID+"/tasks", "")
	if executeResponse.Code != http.StatusOK {
		t.Fatalf("GET view tasks status = %d, body = %s", executeResponse.Code, executeResponse.Body.String())
	}
	var executed apiV1TasksResponse
	if err := json.NewDecoder(executeResponse.Body).Decode(&executed); err != nil {
		t.Fatalf("decode executed tasks: %v", err)
	}
	if len(executed.Tasks) != 2 || executed.Tasks[0].ID != "a" || executed.Tasks[1].ID != "z" {
		t.Fatalf("executed tasks = %#v", executed.Tasks)
	}

	deleteResponse := serveAPIRequest(handler, http.MethodDelete, apiV1Prefix+"/views/"+created.ID, "")
	if deleteResponse.Code != http.StatusNoContent || deleteResponse.Body.Len() != 0 {
		t.Fatalf("DELETE view = %d, %q", deleteResponse.Code, deleteResponse.Body.String())
	}
	missingResponse := serveAPIRequest(handler, http.MethodGet, apiV1Prefix+"/views/"+created.ID, "")
	assertAPIError(t, missingResponse, http.StatusNotFound, "not_found")
}

func TestAPIV1SavedViewValidationAndBuiltInProtection(t *testing.T) {
	t.Parallel()

	handler := newSavedViewTestHandler(t, &savedViewProviderStub{})
	tests := []struct {
		method string
		path   string
		body   string
		code   string
		status int
	}{
		{method: http.MethodPost, path: apiV1Prefix + "/views", body: `{"name":"active","query":{},"order":{}}`, status: http.StatusConflict, code: "conflict"},
		{method: http.MethodPost, path: apiV1Prefix + "/views", body: `{"name":"Bad","query":{"priorities":[9]},"order":{}}`, status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{method: http.MethodPost, path: apiV1Prefix + "/views", body: `{"name":"Bad","unknown":true}`, status: http.StatusBadRequest, code: "invalid_request"},
		{method: http.MethodPatch, path: apiV1Prefix + "/views/active", body: `{"name":"Renamed"}`, status: http.StatusConflict, code: "conflict"},
		{method: http.MethodDelete, path: apiV1Prefix + "/views/active", status: http.StatusConflict, code: "conflict"},
	}
	for _, test := range tests {
		response := serveAPIRequest(handler, test.method, test.path, test.body)
		assertAPIError(t, response, test.status, test.code)
	}
}

func TestAPIV1SavedViewMethodsRejectBeforeDependencyLookup(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		method string
		path   string
		allow  string
	}{
		{method: http.MethodPut, path: apiV1Prefix + "/views", allow: "GET, POST"},
		{method: http.MethodPost, path: apiV1Prefix + "/views/example", allow: "GET, PATCH, DELETE"},
		{method: http.MethodPost, path: apiV1Prefix + "/projects/demo/views/active/tasks", allow: "GET"},
	}
	for _, test := range tests {
		response := serveAPIRequest(handler, test.method, test.path, "")
		assertAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if got := response.Header().Get("Allow"); got != test.allow {
			t.Errorf("%s %s Allow = %q, want %q", test.method, test.path, got, test.allow)
		}
	}
}

func newSavedViewTestHandler(t *testing.T, provider *savedViewProviderStub) http.Handler {
	t.Helper()
	registry, err := facetsstore.Open(context.Background(), filepath.Join(t.TempDir(), "facets.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		closeErr := registry.Close()
		if closeErr != nil {
			t.Errorf("registry.Close() error = %v", closeErr)
		}
	})
	handler, err := NewWithRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), provider, registry, provider)
	if err != nil {
		t.Fatalf("NewWithRegistry() error = %v", err)
	}
	return handler
}

func serveAPIRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Accept", "application/json")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type savedViewProviderStub struct {
	tasks []project.Task
}

func (*savedViewProviderStub) Name() string { return "stub" }
func (*savedViewProviderStub) ListProjects(context.Context) ([]project.Project, error) {
	return []project.Project{{ID: "demo", Name: "Demo"}}, nil
}
func (*savedViewProviderStub) GetProject(context.Context, string) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*savedViewProviderStub) CreateProject(context.Context, project.ProjectInput) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*savedViewProviderStub) UpdateProject(context.Context, string, project.ProjectPatch) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*savedViewProviderStub) DeleteProject(context.Context, string) error {
	return project.ErrUnsupported
}
func (p *savedViewProviderStub) ListTasks(_ context.Context, _ string, filter project.TaskFilter) ([]project.Task, error) {
	tasks := make([]project.Task, 0, len(p.tasks))
	for _, task := range p.tasks {
		if filter.Status == nil || task.Status == *filter.Status {
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}
func (*savedViewProviderStub) GetTask(context.Context, string, string) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (*savedViewProviderStub) CreateTask(context.Context, string, project.TaskInput) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (*savedViewProviderStub) UpdateTask(context.Context, string, string, project.TaskPatch) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (*savedViewProviderStub) CommentTask(context.Context, string, string, string) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (*savedViewProviderStub) DeleteTask(context.Context, string, string) error {
	return project.ErrUnsupported
}
