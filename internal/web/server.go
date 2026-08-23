package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"facets.barnlab.dev/internal/nilcheck"
	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/status"
	"facets.barnlab.dev/internal/store"
)

//go:embed templates/*.html assets/*
var content embed.FS

// ProjectSource supplies the projects rendered by the dashboard.
type ProjectSource interface {
	ListProjects(context.Context) ([]project.Project, error)
}

// New returns the Facets HTTP handler.
func New(logger *slog.Logger, projects ProjectSource, providers ...project.Provider) (http.Handler, error) {
	return newWithRegistry(logger, projects, nil, providers...)
}

// NewWithRegistry returns a Facets HTTP handler backed by a local project registry.
func NewWithRegistry(logger *slog.Logger, projects ProjectSource, registry *store.Store, providers ...project.Provider) (http.Handler, error) {
	return newWithRegistry(logger, projects, registry, providers...)
}

func newWithRegistry(logger *slog.Logger, projects ProjectSource, registry *store.Store, providers ...project.Provider) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if nilcheck.IsNil(projects) {
		return nil, errors.New("web: project source is required")
	}
	if len(providers) > 1 {
		return nil, errors.New("web: only one project provider is supported")
	}
	var provider project.Provider
	if len(providers) == 1 {
		if nilcheck.IsNil(providers[0]) {
			return nil, errors.New("web: project provider is required")
		}
		provider = providers[0]
	}
	var service *project.Service
	if provider != nil {
		var (
			created *project.Service
			err     error
		)
		if registry != nil {
			created, err = project.NewService(provider, registry)
		} else {
			created, err = project.NewService(provider)
		}
		if err != nil {
			return nil, fmt.Errorf("web: create project service: %w", err)
		}
		service = created
	}
	templates, err := template.New("facets").Funcs(template.FuncMap{
		"sessionCount": func(value int) string {
			if value == status.UnknownSessionCount {
				return "??"
			}
			return strconv.Itoa(value)
		},
	}).ParseFS(content, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	assets, err := fs.Sub(content, "assets")
	if err != nil {
		return nil, fmt.Errorf("web: load assets: %w", err)
	}

	s := &server{
		logger: logger, projects: projects, provider: provider, service: service, registry: registry,
		summarizer: status.NewBuilder(), templates: templates,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /partials/status", s.status)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc(apiV1Prefix+"/projects", s.apiV1Projects)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}", s.apiV1Project)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/tasks", s.apiV1Tasks)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/tasks/{task_id}", s.apiV1Task)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/tasks/{task_id}/comments", s.apiV1CommentTask)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/tasks/{task_id}/close", s.apiV1CloseTask)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/tasks/{task_id}/reopen", s.apiV1ReopenTask)
	mux.HandleFunc(apiV1Prefix+"/views", s.apiV1Views)
	mux.HandleFunc(apiV1Prefix+"/views/{view_id}", s.apiV1View)
	mux.HandleFunc(apiV1Prefix+"/projects/{project_id}/views/{view_id}/tasks", s.apiV1ExecuteView)
	mux.HandleFunc(apiV1Prefix, s.apiV1Root)
	mux.HandleFunc(apiV1Prefix+"/", s.apiV1Fallback)
	mux.HandleFunc("/", s.fallback)
	return s.observe(s.rejectMalformedAPIPaths(mux)), nil
}

type server struct {
	logger       *slog.Logger
	projects     ProjectSource
	provider     project.Provider
	service      *project.Service
	registry     *store.Store
	summarizer   *status.Builder
	templates    *template.Template
	requests     atomic.Uint64
	taskCreateMu sync.Mutex
	taskCreates  map[string]apiV1IdempotentTask
}
type projectView struct {
	project.Project
	Summary *status.Summary
}

type pageData struct {
	Year     int
	Projects []projectView
}

type statusData struct {
	CheckedAt string
}

type errorData struct {
	Status  int
	Title   string
	Message string
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	projects, err := s.listProjects(r.Context())
	if err != nil {
		s.respondError(w, r, http.StatusBadGateway, "Projects unavailable", "Facets could not load projects from the configured provider. Try again shortly.", err)
		return
	}
	if s.registry != nil && s.provider != nil {
		if err := s.registry.SyncProjects(r.Context(), s.provider.Name(), projects); err != nil {
			s.respondError(w, r, http.StatusBadGateway, "Project registry unavailable", "Facets could not update the local project registry. Try again shortly.", err)
			return
		}
	}
	views := make([]projectView, len(projects))
	for i, item := range projects {
		views[i] = projectView{Project: item}
		if s.provider != nil {
			root := "."
			if s.registry != nil {
				root, err = s.projectDirectory(r.Context(), s.provider.Name(), item.ID)
				if err != nil {
					s.respondError(w, r, http.StatusBadGateway, "Project metadata unavailable", "Facets could not load project metadata. Try again shortly.", err)
					return
				}
			}
			summary, err := s.summarizer.Build(r.Context(), s.provider, root, item.ID)
			if err != nil {
				s.respondError(w, r, http.StatusBadGateway, "Project status unavailable", "Facets could not summarize project activity. Try again shortly.", err)
				return
			}
			views[i].Summary = &summary
		}
	}

	data := pageData{Year: time.Now().UTC().Year(), Projects: views}
	if err := s.render(w, http.StatusOK, "index.html", data); err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "Dashboard unavailable", "Facets could not render this view. Try again shortly.", err)
	}
}

func (s *server) listProjects(ctx context.Context) ([]project.Project, error) {
	if s.service != nil {
		return s.service.ListProjects(ctx)
	}
	return s.projects.ListProjects(ctx)
}

func (s *server) projectDirectory(ctx context.Context, source, id string) (string, error) {
	registered, err := s.registry.RegisteredProject(ctx, source, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	directory, ok := registered.Metadata["directory"].(string)
	if !ok || strings.TrimSpace(directory) == "" {
		return "", nil
	}
	return strings.TrimSpace(directory), nil
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	data := statusData{CheckedAt: time.Now().Format("15:04:05 MST")}
	if err := s.render(w, http.StatusOK, "status.html", data); err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "Status unavailable", "The service status could not be loaded.", err)
	}
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		s.logger.ErrorContext(r.Context(), "encode health response", "error", err)
	}
}

func (s *server) fallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && knownGetPath(r.URL.Path) {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, r, http.StatusMethodNotAllowed, "Method not allowed", "This Facets view only accepts read requests.", nil)
		return
	}
	s.notFound(w, r)
}

func knownGetPath(path string) bool {
	switch path {
	case "/", "/partials/status", "/healthz":
		return true
	default:
		return strings.HasPrefix(path, "/assets/")
	}
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	s.respondError(w, r, http.StatusNotFound, "View not found", "The requested Facets view does not exist.", nil)
}

func (s *server) render(w http.ResponseWriter, status int, name string, data any) error {
	var output bytes.Buffer
	if err := s.templates.ExecuteTemplate(&output, name, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := output.WriteTo(w)
	return err
}

func (s *server) respondError(w http.ResponseWriter, r *http.Request, status int, title, message string, cause error) {
	attributes := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
	}
	if cause != nil {
		attributes = append(attributes, "error", cause)
	}
	if status >= http.StatusInternalServerError {
		s.logger.ErrorContext(r.Context(), "http request failed", attributes...)
	} else {
		s.logger.WarnContext(r.Context(), "http request rejected", attributes...)
	}

	templateName := "error.html"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Retarget", "#system-message")
		w.Header().Set("HX-Reswap", "innerHTML")
		templateName = "error-partial"
	}
	if err := s.render(w, status, templateName, errorData{Status: status, Title: title, Message: message}); err != nil {
		s.logger.ErrorContext(r.Context(), "render error response", "error", err, "status", status)
		http.Error(w, title, status)
	}
}

func (s *server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strconv.FormatUint(s.requests.Add(1), 10)
		w.Header().Set("X-Request-ID", requestID)
		recorder := &responseRecorder{ResponseWriter: w}

		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.ErrorContext(r.Context(), "panic while serving request", "request_id", requestID, "panic", recovered)
				if recorder.status == 0 {
					cause := fmt.Errorf("panic: %v", recovered)
					if isAPIPath(r.URL.Path) {
						s.respondAPIError(recorder, r, http.StatusInternalServerError, "internal_error", "Facets could not complete the request.", nil, cause)
					} else {
						s.respondError(recorder, r, http.StatusInternalServerError, "Unexpected server error", "Facets hit an unexpected error. The failure was logged.", cause)
					}
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			s.logger.InfoContext(r.Context(), "http request",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration", time.Since(started),
			)
		}()

		next.ServeHTTP(recorder, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
