package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
	facetsstore "facets.barnlab.dev/internal/store"
)

func TestAPIV1TodayAndFocusRoutes(t *testing.T) {
	now := time.Now()
	provider := &todayProviderStub{tasks: []project.Task{
		{ID: "top-open", ProjectID: "demo", Title: "Top open", Status: project.StatusOpen, Metadata: map[string]any{"facets.top": "true"}, UpdatedAt: now},
		{ID: "regular-open", ProjectID: "demo", Title: "Regular open", Status: project.StatusOpen, UpdatedAt: now},
		{ID: "top-closed", ProjectID: "demo", Title: "Top closed", Status: project.StatusClosed, Metadata: map[string]any{"facets.top": true}, UpdatedAt: now},
		{ID: "old-closed", ProjectID: "demo", Title: "Old closed", Status: project.StatusClosed, UpdatedAt: now.Add(-48 * time.Hour)},
	}}
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
	emptyTodayResponse := serveAPIRequest(handler, http.MethodGet, apiV1Prefix+"/today", "")
	if emptyTodayResponse.Code != http.StatusOK {
		t.Fatalf("GET today without focus status = %d, body = %s", emptyTodayResponse.Code, emptyTodayResponse.Body.String())
	}
	var emptyToday apiV1TodayResponse
	if err := json.NewDecoder(emptyTodayResponse.Body).Decode(&emptyToday); err != nil {
		t.Fatalf("decode empty today: %v", err)
	}
	if emptyToday.Focus != nil {
		t.Fatalf("empty today focus = %#v, want nil", emptyToday.Focus)
	}

	focusResponse := serveAPIRequest(handler, http.MethodPost, apiV1Prefix+"/today/focus", `{"text":"  Ship Today  "}`)
	if focusResponse.Code != http.StatusOK {
		t.Fatalf("POST focus status = %d, body = %s", focusResponse.Code, focusResponse.Body.String())
	}
	var focus apiV1TodayFocusResponse
	if err := json.NewDecoder(focusResponse.Body).Decode(&focus); err != nil {
		t.Fatalf("decode focus: %v", err)
	}
	if focus.Focus.Text != "Ship Today" || focus.Focus.DayStart == "" {
		t.Fatalf("focus = %#v", focus)
	}

	todayResponse := serveAPIRequest(handler, http.MethodGet, apiV1Prefix+"/today", "")
	if todayResponse.Code != http.StatusOK {
		t.Fatalf("GET today status = %d, body = %s", todayResponse.Code, todayResponse.Body.String())
	}
	var today apiV1TodayResponse
	if err := json.NewDecoder(todayResponse.Body).Decode(&today); err != nil {
		t.Fatalf("decode today: %v", err)
	}
	if today.Focus == nil || today.Focus.Text != "Ship Today" {
		t.Fatalf("today focus = %#v", today.Focus)
	}
	if len(today.TopTasks) != 1 || today.TopTasks[0].Project != "demo" || today.TopTasks[0].Task != "top-open" {
		t.Fatalf("today top tasks = %#v", today.TopTasks)
	}
	if today.CompletedToday.All != 1 || today.CompletedToday.Top != 1 || today.CompletedToday.DayStart == "" || today.CompletedToday.DayEnd == "" {
		t.Fatalf("today completion = %#v", today.CompletedToday)
	}

	malformed := serveAPIRequest(handler, http.MethodPost, apiV1Prefix+"/today/focus", `{"text":"focus","unknown":true}`)
	assertAPIError(t, malformed, http.StatusBadRequest, "invalid_request")
}
func TestAPIV1TodayReportsUnavailablePrerequisites(t *testing.T) {
	noProvider := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	assertAPIError(t, serveAPIRequest(noProvider, http.MethodGet, apiV1Prefix+"/today", ""), http.StatusServiceUnavailable, "provider_unavailable")

	provider := &todayProviderStub{}
	handler, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)), provider, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	assertAPIError(t, serveAPIRequest(handler, http.MethodPost, apiV1Prefix+"/today/focus", `{"text":"focus"}`), http.StatusServiceUnavailable, "registry_unavailable")
}

func TestAPIV1TopTaskPatchMapsStringMetadata(t *testing.T) {
	provider := &todayProviderStub{tasks: []project.Task{{ID: "top", ProjectID: "demo", Title: "Top", Status: project.StatusOpen}}}
	handler := newTodayTestHandler(t, provider)
	response := serveAPIRequest(handler, http.MethodPatch, apiV1Prefix+"/projects/demo/tasks/top", `{"top":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH task status = %d, body = %s", response.Code, response.Body.String())
	}
	if provider.patch.Metadata["facets.top"] != "true" {
		t.Fatalf("metadata patch = %#v", provider.patch.Metadata)
	}
	var task apiV1Task
	if err := json.NewDecoder(response.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if !task.Top {
		t.Fatalf("patched task top = false: %#v", task)
	}

	null := serveAPIRequest(handler, http.MethodPatch, apiV1Prefix+"/projects/demo/tasks/top", `{"top":null}`)
	assertAPIError(t, null, http.StatusUnprocessableEntity, "validation_failed")
}

func newTodayTestHandler(t *testing.T, provider *todayProviderStub) http.Handler {
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

type todayProviderStub struct {
	patch project.TaskPatch
	tasks []project.Task
}

func (p *todayProviderStub) Name() string { return "today-stub" }
func (p *todayProviderStub) ListProjects(context.Context) ([]project.Project, error) {
	return []project.Project{{ID: "demo", Name: "Demo"}}, nil
}
func (*todayProviderStub) GetProject(context.Context, string) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*todayProviderStub) CreateProject(context.Context, project.ProjectInput) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*todayProviderStub) UpdateProject(context.Context, string, project.ProjectPatch) (project.Project, error) {
	return project.Project{}, project.ErrUnsupported
}
func (*todayProviderStub) DeleteProject(context.Context, string) error { return project.ErrUnsupported }
func (p *todayProviderStub) ListTasks(_ context.Context, _ string, filter project.TaskFilter) ([]project.Task, error) {
	result := make([]project.Task, 0, len(p.tasks))
	for _, task := range p.tasks {
		if filter.Status == nil || task.Status == *filter.Status {
			result = append(result, task)
		}
	}
	return result, nil
}
func (p *todayProviderStub) GetTask(context.Context, string, string) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (p *todayProviderStub) CreateTask(context.Context, string, project.TaskInput) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (p *todayProviderStub) UpdateTask(_ context.Context, _, id string, patch project.TaskPatch) (project.Task, error) {
	p.patch = patch
	for _, task := range p.tasks {
		if task.ID != id {
			continue
		}
		if patch.Metadata != nil {
			task.Metadata = map[string]any{}
			for key, value := range patch.Metadata {
				task.Metadata[key] = value
			}
		}
		return task, nil
	}
	return project.Task{}, project.ErrNotFound
}
func (*todayProviderStub) CommentTask(context.Context, string, string, string) (project.Task, error) {
	return project.Task{}, project.ErrUnsupported
}
func (*todayProviderStub) DeleteTask(context.Context, string, string) error {
	return project.ErrUnsupported
}
