package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func newTestHandler(t *testing.T, logger *slog.Logger) http.Handler {
	t.Helper()
	handler, err := New(logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}
