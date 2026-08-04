package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

//go:embed templates/*.html assets/*
var content embed.FS

// New returns the Facets HTTP handler.
func New(logger *slog.Logger) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}

	templates, err := template.ParseFS(content, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	assets, err := fs.Sub(content, "assets")
	if err != nil {
		return nil, fmt.Errorf("web: load assets: %w", err)
	}

	s := &server{logger: logger, templates: templates}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /partials/status", s.status)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("/", s.notFound)
	return s.observe(mux), nil
}

type server struct {
	logger    *slog.Logger
	templates *template.Template
	requests  atomic.Uint64
}

type pageData struct {
	Year int
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
	if err := s.render(w, http.StatusOK, "index.html", pageData{Year: time.Now().UTC().Year()}); err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "Dashboard unavailable", "Facets could not render this view. Try again shortly.", err)
	}
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
	s.logger.ErrorContext(r.Context(), "http request failed", attributes...)

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
					s.respondError(recorder, r, http.StatusInternalServerError, "Unexpected server error", "Facets hit an unexpected error. The failure was logged.", fmt.Errorf("panic: %v", recovered))
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
