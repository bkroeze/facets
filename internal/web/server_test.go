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
	"strings"
	"testing"

	"facets.barnlab.dev/internal/project"
)

func TestDashboardShell(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("GET / Content-Type = %q", contentType)
	}
	body := response.Body.String()
	for _, marker := range []string{
		`href="/assets/app.css"`,
		`hx-get="/partials/status"`,
		"htmx",
		"alpinejs",
		"x-data",
		`id="system-message"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("GET / body missing %q", marker)
		}
	}
}

func TestNewRejectsNilProjectSource(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := New(logger, nil); err == nil {
		t.Fatal("New() accepted a nil project source")
	}
	var source *projectSourceStub
	if _, err := New(logger, source); err == nil {
		t.Fatal("New() accepted a typed nil project source")
	}
}

func TestDashboardListsProviderProjects(t *testing.T) {
	t.Parallel()

	projects := []project.Project{
		{ID: "facets", Name: "Facets", Description: "Personal dashboard"},
		{ID: "thornwear", Name: "Thornwear", Description: "<script>alert('x')</script>"},
	}
	handler := newTestHandlerWithProjects(t, slog.New(slog.NewTextHandler(io.Discard, nil)), projectSourceStub{projects: projects})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, marker := range []string{"Projects", "2", "Facets", "facets", "Personal dashboard", "Thornwear", "thornwear", "&lt;script&gt;"} {
		if !strings.Contains(body, marker) {
			t.Errorf("GET / body missing project marker %q", marker)
		}
	}
	if strings.Contains(body, "<script>alert('x')</script>") || strings.Contains(body, "No projects yet") {
		t.Fatalf("GET / body rendered unsafe or stale project state: %s", body)
	}
}

func TestDashboardSurfacesProjectProviderFailure(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	handler := newTestHandlerWithProjects(t, slog.New(slog.NewJSONHandler(&logs, nil)), projectSourceStub{err: errors.New("provider secret")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusBadGateway {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Projects unavailable") || strings.Contains(body, "provider secret") {
		t.Fatalf("GET / body = %q", body)
	}
	if !strings.Contains(logs.String(), "provider secret") {
		t.Fatalf("provider failure was not logged: %s", logs.String())
	}
}

func TestStatusPartial(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/partials/status", nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /partials/status status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET /partials/status Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if strings.TrimSpace(response.Body.String()) == "" {
		t.Fatal("GET /partials/status body is empty")
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("GET /healthz body = %#v", body)
	}
}

func TestMissingViewReturnsVisibleError(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if response.Header().Get("HX-Retarget") != "#system-message" {
		t.Fatalf("GET /missing HX-Retarget = %q", response.Header().Get("HX-Retarget"))
	}
	body := response.Body.String()
	if !strings.Contains(body, "View not found") {
		t.Fatalf("GET /missing body = %q", body)
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Fatalf("GET /missing HTMX response contains a full document: %q", body)
	}
}

func TestMissingViewReturnsStandaloneError(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want %d", response.Code, http.StatusNotFound)
	}
	body := response.Body.String()
	if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, "View not found") {
		t.Fatalf("GET /missing body is not a standalone error page: %q", body)
	}
}

func TestKnownRouteRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if allow := response.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("POST / Allow = %q, want %q", allow, "GET, HEAD")
	}
	if !strings.Contains(response.Body.String(), "Method not allowed") {
		t.Fatalf("POST / body = %q", response.Body.String())
	}
}

func TestRequestsAreLogged(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := newTestHandler(t, logger)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	entry := logs.String()
	for _, field := range []string{`"msg":"http request"`, `"method":"GET"`, `"path":"/healthz"`, `"status":200`} {
		if !strings.Contains(entry, field) {
			t.Errorf("request log missing %s: %s", field, entry)
		}
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("GET /healthz X-Request-ID is empty")
	}
}

func TestStylesheetIsEmbedded(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/app.css", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.css status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/css") {
		t.Fatalf("GET /assets/app.css Content-Type = %q", contentType)
	}
	if response.Body.Len() == 0 {
		t.Fatal("GET /assets/app.css body is empty")
	}
}

type projectSourceStub struct {
	err      error
	projects []project.Project
}

func (s projectSourceStub) ListProjects(context.Context) ([]project.Project, error) {
	return s.projects, s.err
}

func newTestHandler(t *testing.T, logger *slog.Logger) http.Handler {
	t.Helper()
	return newTestHandlerWithProjects(t, logger, projectSourceStub{})
}

func newTestHandlerWithProjects(t *testing.T, logger *slog.Logger, projects ProjectSource) http.Handler {
	t.Helper()
	handler, err := New(logger, projects)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}
