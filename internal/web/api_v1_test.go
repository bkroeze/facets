package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"facets.barnlab.dev/internal/project"
)

func TestAPIV1RootNegotiatesJSON(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, apiV1Prefix, nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d", apiV1Prefix, response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Body.String(); got != "{\"version\":\"v1\"}\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestAPIV1RootRejectsUnsupportedRepresentation(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, apiV1Prefix, nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assertAPIError(t, response, http.StatusNotAcceptable, "not_acceptable")
}

func TestAPIV1RootRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, apiV1Prefix, nil))

	assertAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q", got)
	}
}

func TestAPIV1TaskMutationMethodsRejectBeforeDependencyLookup(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{
		apiV1Prefix + "/projects/demo/tasks/1/comments",
		apiV1Prefix + "/projects/demo/tasks/1/close",
		apiV1Prefix + "/projects/demo/tasks/1/reopen",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

		assertAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if got := response.Header().Get("Allow"); got != http.MethodPost {
			t.Errorf("GET %s Allow = %q, want %q", path, got, http.MethodPost)
		}
	}
}

func TestAPIV1UnknownResourceReturnsJSONError(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiV1Prefix+"/missing", nil))

	assertAPIError(t, response, http.StatusNotFound, "not_found")
}

func TestAPIV1MalformedPathReturnsJSONErrorWithoutRedirect(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiV1Prefix+"//missing", nil))

	assertAPIError(t, response, http.StatusBadRequest, "invalid_request")
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("Location = %q", location)
	}
}

func TestAPIV1EncodedOpaqueIDIsNotPathCleaned(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiV1Prefix+"/projects/%2E%2E", nil))

	assertAPIError(t, response, http.StatusNotFound, "not_found")
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("Location = %q", location)
	}
}

func TestAPIV1PanicReturnsInternalJSONError(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	s := &server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	handler := s.observe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("provider secret")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, apiV1Prefix+"/panic", nil))

	assertAPIError(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), "provider secret") {
		t.Fatalf("panic leaked in response: %s", response.Body.String())
	}
	if !strings.Contains(logs.String(), "provider secret") {
		t.Fatalf("panic missing from logs: %s", logs.String())
	}
}

func TestAPIV1ServiceErrorResponseDoesNotLeakCause(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	s := &server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	handler := s.observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.respondAPIServiceError(w, r, errors.Join(project.ErrInvalid, errors.New("private task body")))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, apiV1Prefix+"/projects/facets/tasks", nil))

	assertAPIError(t, response, http.StatusUnprocessableEntity, "validation_failed")
	if strings.Contains(response.Body.String(), "private task body") {
		t.Fatalf("service cause leaked in response: %s", response.Body.String())
	}
	if !strings.Contains(logs.String(), "private task body") {
		t.Fatalf("service cause missing from logs: %s", logs.String())
	}
}

func TestAPIV1ServiceErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		name   string
		code   string
		status int
	}{
		{name: "invalid document", err: errAPIV1InvalidRequest, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "media type", err: errAPIV1UnsupportedMediaType, status: http.StatusUnsupportedMediaType, code: "unsupported_media_type"},
		{name: "validation", err: project.ErrInvalid, status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "not found", err: project.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "conflict", err: project.ErrProviderExists, status: http.StatusConflict, code: "conflict"},
		{name: "storage", err: project.ErrStorage, status: http.StatusInternalServerError, code: "internal_error"},
		{name: "unsupported", err: project.ErrUnsupported, status: http.StatusNotImplemented, code: "unsupported_operation"},
		{name: "deadline", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: "provider_timeout"},
		{name: "canceled", err: context.Canceled, status: http.StatusRequestTimeout, code: "request_canceled"},
		{name: "missing provider", err: project.ErrProviderNotFound, status: http.StatusServiceUnavailable, code: "provider_unavailable"},
		{name: "provider failure", err: errors.New("secret failure"), status: http.StatusBadGateway, code: "provider_failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, code, _ := apiV1ServiceError(fmtWrap(test.err))
			if status != test.status || code != test.code {
				t.Fatalf("apiV1ServiceError() = %d, %q; want %d, %q", status, code, test.status, test.code)
			}
		})
	}
}

func TestAPIV1DTOsExcludeProviderMetadataAndNormalizeTime(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 12, 14, 30, 0, 123, time.FixedZone("offset", 2*60*60))
	projectDTO := apiV1ProjectFromDomain(project.Project{
		ID: "facets", Name: "Facets", Description: "Dashboard", Metadata: map[string]any{"directory": "/secret"}, CreatedAt: created,
	})
	projectJSON, err := json.Marshal(projectDTO)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	wantProject := `{"id":"facets","name":"Facets","description":"Dashboard","active_task_count":0,"created_at":"2026-08-12T12:30:00.000000123Z","updated_at":null}`
	if string(projectJSON) != wantProject {
		t.Fatalf("project JSON = %s", projectJSON)
	}

	priority := 2
	taskDTO := apiV1TaskFromDomain(project.Task{
		ID: "t1", ProjectID: "facets", Title: "Ship", Description: "Body", Status: project.StatusOpen,
		Priority: &priority, Assignee: "bruce", Metadata: map[string]any{"facets.top": "true", "provider_secret": true}, UpdatedAt: created,
	})
	taskJSON, err := json.Marshal(taskDTO)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	wantTask := `{"id":"t1","project_id":"facets","title":"Ship","description":"Body","status":"open","priority":2,"assignee":"bruce","top":true,"created_at":null,"updated_at":"2026-08-12T12:30:00.000000123Z"}`
	if string(taskJSON) != wantTask {
		t.Fatalf("task JSON = %s", taskJSON)
	}

	viewDTO := apiV1SavedView{
		ID: "active", Name: "Active", Builtin: true,
		Query:     apiV1TaskQuery{Statuses: []project.Status{project.StatusOpen}, Assignees: []string{}, Priorities: []int{}},
		Order:     apiV1TaskOrder{Field: "updated_at", Direction: "desc"},
		CreatedAt: nil, UpdatedAt: apiV1Time(created),
	}
	viewJSON, err := json.Marshal(viewDTO)
	if err != nil {
		t.Fatalf("marshal saved view: %v", err)
	}
	wantView := `{"id":"active","name":"Active","builtin":true,"query":{"statuses":["open"],"assignees":[],"priorities":[]},"order":{"field":"updated_at","direction":"desc"},"created_at":null,"updated_at":"2026-08-12T12:30:00.000000123Z"}`
	if string(viewJSON) != wantView {
		t.Fatalf("saved view JSON = %s", viewJSON)
	}

	emptyQueryJSON, err := json.Marshal(apiV1TaskQuery{})
	if err != nil {
		t.Fatalf("marshal empty query: %v", err)
	}
	if got, want := string(emptyQueryJSON), `{"statuses":[],"assignees":[],"priorities":[]}`; got != want {
		t.Fatalf("empty query JSON = %s", emptyQueryJSON)
	}
}

func TestAPIV1PatchDistinguishesMissingNullAndValue(t *testing.T) {
	t.Parallel()

	var request apiV1UpdateTaskRequest
	if err := json.Unmarshal([]byte(`{"description":"body","priority":null}`), &request); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if request.Title.Set {
		t.Fatal("omitted title marked set")
	}
	if !request.Description.Set || request.Description.Null || request.Description.Value != "body" {
		t.Fatalf("description = %#v", request.Description)
	}
	if !request.Priority.Set || !request.Priority.Null {
		t.Fatalf("priority = %#v", request.Priority)
	}
	var topRequest apiV1UpdateTaskRequest
	if err := json.Unmarshal([]byte(`{"top":true}`), &topRequest); err != nil {
		t.Fatalf("unmarshal top patch: %v", err)
	}
	topPatch, err := topRequest.taskPatch()
	if err != nil || topPatch.Metadata["facets.top"] != "true" {
		t.Fatalf("top patch = %#v, %v", topPatch, err)
	}
	if err := json.Unmarshal([]byte(`{"top":null}`), &topRequest); err != nil {
		t.Fatalf("unmarshal null top patch: %v", err)
	}
	if _, err := topRequest.taskPatch(); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("null top patch error = %v, want project.ErrInvalid", err)
	}
}

func TestAcceptsAPIV1JSON(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"":                                       true,
		"*/*":                                    true,
		"application/json":                       true,
		"application/json; q=0.5, text/html":     true,
		"application/json;q=0, text/html":        false,
		"application/json;q=0, */*;q=1":          false,
		"application/json;q=invalid, text/plain": false,
		"application/json;q=NaN, application/json;q=0.5": true,
		"application/json;q=1.5, text/plain":             false,
		"text/html":                                      false,
	}
	for header, want := range tests {
		if got := acceptsAPIJSON(header); got != want {
			t.Errorf("acceptsAPIJSON(%q) = %t, want %t", header, got, want)
		}
	}
}

func TestRequireAPIJSON(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, apiV1Prefix+"/tasks", nil)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	if err := requireAPIJSON(request); err != nil {
		t.Fatalf("requireAPIJSON() error = %v", err)
	}
	request.Header.Set("Content-Type", "text/plain")
	if err := requireAPIJSON(request); !errors.Is(err, errAPIV1UnsupportedMediaType) {
		t.Fatalf("requireAPIJSON() error = %v, want unsupported media type", err)
	}
}

func TestDecodeAPIJSONRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"title":"task","unknown":true}`,
		`{"title":"task"} {"title":"second"}`,
		`null`,
		`[]`,
	} {
		request := httptest.NewRequest(http.MethodPost, apiV1Prefix+"/tasks", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		var destination apiV1CreateTaskRequest
		if err := decodeAPIJSON(request, &destination); !errors.Is(err, errAPIV1InvalidRequest) {
			t.Errorf("decodeAPIJSON(%q) error = %v, want invalid request", body, err)
		}
	}
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body apiV1ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %#v", body.Error)
	}
	if body.Error.RequestID == "" || body.Error.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("request ID body/header = %q/%q", body.Error.RequestID, response.Header().Get("X-Request-ID"))
	}
	if !reflect.DeepEqual(body.Error.Details, map[string]any(nil)) {
		t.Fatalf("details = %#v", body.Error.Details)
	}
}

func fmtWrap(err error) error {
	return errors.Join(errors.New("wrapped"), err)
}
