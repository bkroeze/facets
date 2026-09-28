package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/store"
)

const apiV1Prefix = "/api/v1"

var (
	errAPIV1InvalidRequest       = errors.New("invalid API request")
	errAPIV1UnsupportedMediaType = errors.New("unsupported API media type")
	errAPIV1RegistryUnavailable  = errors.New("API registry unavailable")
)

type apiV1RootResponse struct {
	Version string `json:"version"`
}

type apiV1Project struct {
	CreatedAt       *string `json:"created_at"`
	UpdatedAt       *string `json:"updated_at"`
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	ActiveTaskCount int     `json:"active_task_count"`
}

type apiV1Task struct {
	Priority    *int           `json:"priority"`
	CreatedAt   *string        `json:"created_at"`
	UpdatedAt   *string        `json:"updated_at"`
	ID          string         `json:"id"`
	ProjectID   string         `json:"project_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      project.Status `json:"status"`
	Assignee    string         `json:"assignee"`
	Top         bool           `json:"top"`
}

type apiV1TodayFocus struct {
	Text     string `json:"text"`
	DayStart string `json:"day_start"`
}

type apiV1TodayTask struct {
	Project     string `json:"project"`
	ProjectName string `json:"project_name"`
	Task        string `json:"task"`
	Title       string `json:"title"`
}

type apiV1TodayCompletion struct {
	DayStart string `json:"day_start"`
	DayEnd   string `json:"day_end"`
	All      int    `json:"all"`
	Top      int    `json:"top"`
}

type apiV1TodayResponse struct {
	Focus          *apiV1TodayFocus     `json:"focus"`
	TopTasks       []apiV1TodayTask     `json:"top_tasks"`
	CompletedToday apiV1TodayCompletion `json:"completed_today"`
}

type apiV1TaskQuery struct {
	Statuses   []project.Status `json:"statuses"`
	Assignees  []string         `json:"assignees"`
	Priorities []int            `json:"priorities"`
}

func (q apiV1TaskQuery) MarshalJSON() ([]byte, error) {
	type query apiV1TaskQuery
	if q.Statuses == nil {
		q.Statuses = []project.Status{}
	}
	if q.Assignees == nil {
		q.Assignees = []string{}
	}
	if q.Priorities == nil {
		q.Priorities = []int{}
	}
	return json.Marshal(query(q))
}

type apiV1TaskOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type apiV1SavedView struct {
	CreatedAt *string        `json:"created_at"`
	UpdatedAt *string        `json:"updated_at"`
	Order     apiV1TaskOrder `json:"order"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Query     apiV1TaskQuery `json:"query"`
	Builtin   bool           `json:"builtin"`
}

type apiV1CreateTaskRequest struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	Priority       *int   `json:"priority"`
	Assignee       string `json:"assignee"`
	IdempotencyKey string `json:"idempotency_key"`
}

type apiV1UpdateTaskRequest struct {
	Title       apiV1Optional[string] `json:"title"`
	Description apiV1Optional[string] `json:"description"`
	Assignee    apiV1Optional[string] `json:"assignee"`
	Priority    apiV1Optional[int]    `json:"priority"`
	Top         apiV1Optional[bool]   `json:"top"`
}

type apiV1CloseTaskRequest struct {
	Message  string   `json:"message"`
	Comment  string   `json:"comment"`
	Evidence []string `json:"evidence"`
}
type apiV1CommentTaskRequest struct {
	Body string `json:"body"`
}

type apiV1DeleteTaskRequest struct {
	Confirm string `json:"confirm"`
}

// apiV1Optional distinguishes an omitted PATCH field from a JSON null. Null is
// meaningful for nullable fields such as task priority.
type apiV1Optional[T any] struct {
	Value T
	Set   bool
	Null  bool
}

func (o *apiV1Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Null = true
		var zero T
		o.Value = zero
		return nil
	}
	o.Null = false
	return json.Unmarshal(data, &o.Value)
}

type apiV1ErrorResponse struct {
	Error apiV1Error `json:"error"`
}

type apiV1Error struct {
	Details   map[string]any `json:"details,omitempty"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
}

func apiV1JSONField(name string, value any) ([]byte, error) {
	key, err := json.Marshal(name)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	field := make([]byte, 0, len(key)+1+len(encoded))
	field = append(field, key...)
	field = append(field, ':')
	field = append(field, encoded...)
	return field, nil
}

func marshalAPIJSONObject(fields ...[]byte) []byte {
	body := bytes.Join(fields, []byte{','})
	result := make([]byte, 0, len(body)+2)
	result = append(result, '{')
	result = append(result, body...)
	result = append(result, '}')
	return result
}

func marshalAPIJSONFields(names []string, values []any) ([]byte, error) {
	fields := make([][]byte, 0, len(names))
	for i, name := range names {
		encoded, err := apiV1JSONField(name, values[i])
		if err != nil {
			return nil, err
		}
		fields = append(fields, encoded)
	}
	return marshalAPIJSONObject(fields...), nil
}

func (item apiV1Project) MarshalJSON() ([]byte, error) {
	return marshalAPIJSONFields(
		[]string{"id", "name", "description", "active_task_count", "created_at", "updated_at"},
		[]any{item.ID, item.Name, item.Description, item.ActiveTaskCount, item.CreatedAt, item.UpdatedAt},
	)
}

func (item apiV1Task) MarshalJSON() ([]byte, error) {
	return marshalAPIJSONFields(
		[]string{"id", "project_id", "title", "description", "status", "priority", "assignee", "top", "created_at", "updated_at"},
		[]any{item.ID, item.ProjectID, item.Title, item.Description, item.Status, item.Priority, item.Assignee, item.Top, item.CreatedAt, item.UpdatedAt},
	)
}

func (item apiV1TodayCompletion) MarshalJSON() ([]byte, error) {
	return marshalAPIJSONFields(
		[]string{"all", "top", "day_start", "day_end"},
		[]any{item.All, item.Top, item.DayStart, item.DayEnd},
	)
}

func (item apiV1TodayResponse) MarshalJSON() ([]byte, error) {
	return marshalAPIJSONFields(
		[]string{"focus", "top_tasks", "completed_today"},
		[]any{item.Focus, item.TopTasks, item.CompletedToday},
	)
}

func (item apiV1SavedView) MarshalJSON() ([]byte, error) {
	return marshalAPIJSONFields(
		[]string{"id", "name", "builtin", "query", "order", "created_at", "updated_at"},
		[]any{item.ID, item.Name, item.Builtin, item.Query, item.Order, item.CreatedAt, item.UpdatedAt},
	)
}

func (item apiV1Error) MarshalJSON() ([]byte, error) {
	names := []string{"code", "message", "request_id"}
	values := []any{item.Code, item.Message, item.RequestID}
	if len(item.Details) > 0 {
		names = append(names, "details")
		values = append(values, item.Details)
	}
	return marshalAPIJSONFields(names, values)
}

func apiV1ProjectFromDomain(item project.Project) apiV1Project {
	return apiV1Project{
		ID: item.ID, Name: item.Name, Description: item.Description,
		CreatedAt: apiV1Time(item.CreatedAt), UpdatedAt: apiV1Time(item.UpdatedAt),
	}
}

func apiV1TaskFromDomain(item project.Task) apiV1Task {
	return apiV1Task{
		ID: item.ID, ProjectID: item.ProjectID, Title: item.Title,
		Description: item.Description, Status: item.Status, Priority: item.Priority,
		Assignee: item.Assignee, Top: apiV1IsTopTask(item), CreatedAt: apiV1Time(item.CreatedAt), UpdatedAt: apiV1Time(item.UpdatedAt),
	}
}

func apiV1TasksFromDomain(items []project.Task) []apiV1Task {
	tasks := make([]apiV1Task, len(items))
	for i, item := range items {
		tasks[i] = apiV1TaskFromDomain(item)
	}
	return tasks
}

func apiV1Time(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func (s *server) apiV1Root(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET and HEAD requests.", nil, nil)
		return
	}
	if !acceptsAPIJSON(r.Header.Get("Accept")) {
		s.respondAPIError(w, r, http.StatusNotAcceptable, "not_acceptable", "The requested response media type is not available; accept application/json.", nil, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.writeAPIJSON(w, r, http.StatusOK, apiV1RootResponse{Version: "v1"})
}

func (s *server) apiV1Fallback(w http.ResponseWriter, r *http.Request) {
	if !acceptsAPIJSON(r.Header.Get("Accept")) {
		s.respondAPIError(w, r, http.StatusNotAcceptable, "not_acceptable", "The requested response media type is not available; accept application/json.", nil, nil)
		return
	}
	s.respondAPIError(w, r, http.StatusNotFound, "not_found", "The requested API resource does not exist.", nil, nil)
}

func (s *server) writeAPIJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.ErrorContext(r.Context(), "encode API response", "request_id", w.Header().Get("X-Request-ID"), "status", status, "error", err)
	}
}

func (s *server) respondAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any, cause error) {
	attributes := []any{
		"request_id", w.Header().Get("X-Request-ID"),
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"code", code,
	}
	if cause != nil {
		attributes = append(attributes, "error", cause)
	}
	if status >= http.StatusInternalServerError {
		s.logger.ErrorContext(r.Context(), "API request failed", attributes...)
	} else {
		s.logger.WarnContext(r.Context(), "API request rejected", attributes...)
	}
	s.writeAPIJSON(w, r, status, apiV1ErrorResponse{Error: apiV1Error{
		Code: code, Message: message, RequestID: w.Header().Get("X-Request-ID"), Details: details,
	}})
}

func (s *server) respondAPIServiceError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := apiV1ServiceError(err)
	s.respondAPIError(w, r, status, code, message, nil, err)
}

func apiV1ServiceError(err error) (int, string, string) {
	switch {
	case errors.Is(err, errAPIV1InvalidRequest):
		return http.StatusBadRequest, "invalid_request", "The request document is malformed."
	case errors.Is(err, errAPIV1UnsupportedMediaType):
		return http.StatusUnsupportedMediaType, "unsupported_media_type", "Request bodies must use application/json."
	case errors.Is(err, project.ErrInvalid):
		return http.StatusUnprocessableEntity, "validation_failed", "The request contains invalid values."
	case errors.Is(err, project.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not_found", "The requested resource does not exist."
	case errors.Is(err, project.ErrConflict), errors.Is(err, project.ErrProviderExists):
		return http.StatusConflict, "conflict", "The requested change conflicts with existing state."
	case errors.Is(err, project.ErrStorage):
		return http.StatusInternalServerError, "internal_error", "Facets could not complete the request."
	case errors.Is(err, project.ErrUnsupported):
		return http.StatusNotImplemented, "unsupported_operation", "The configured provider does not support this operation."
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "provider_timeout", "The task provider did not respond before the deadline."
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request_canceled", "The request was canceled."
	case errors.Is(err, project.ErrProviderNotFound):
		return http.StatusServiceUnavailable, "provider_unavailable", "The configured task provider is unavailable."
	case errors.Is(err, errAPIV1RegistryUnavailable):
		return http.StatusServiceUnavailable, "registry_unavailable", "The local registry is unavailable."
	default:
		return http.StatusBadGateway, "provider_failure", "The task provider could not complete the request."
	}
}

func acceptsAPIJSON(header string) bool {
	if strings.TrimSpace(header) == "" {
		return true
	}
	bestSpecificity := -1
	bestQuality := 0.0
	for _, item := range strings.Split(header, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		quality := 1.0
		if encoded, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(encoded, 64)
			if err != nil || math.IsNaN(quality) || math.IsInf(quality, 0) || quality < 0 || quality > 1 {
				continue
			}
		}
		specificity := -1
		switch mediaType {
		case "*/*":
			specificity = 0
		case "application/*":
			specificity = 1
		case "application/json":
			specificity = 2
		}
		if specificity > bestSpecificity || specificity == bestSpecificity && quality > bestQuality {
			bestSpecificity = specificity
			bestQuality = quality
		}
	}
	return bestSpecificity >= 0 && bestQuality > 0
}

func requireAPIJSON(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", errAPIV1UnsupportedMediaType)
	}
	return nil
}

func decodeAPIJSON(r *http.Request, destination any) error {
	if err := requireAPIJSON(r); err != nil {
		return err
	}
	decoder := json.NewDecoder(r.Body)
	var document json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("%w: %v", errAPIV1InvalidRequest, err)
	}
	trimmed := bytes.TrimSpace(document)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: request body must contain a JSON object", errAPIV1InvalidRequest)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: request body must contain one JSON document", errAPIV1InvalidRequest)
		}
		return fmt.Errorf("%w: %v", errAPIV1InvalidRequest, err)
	}
	strict := json.NewDecoder(bytes.NewReader(document))
	strict.DisallowUnknownFields()
	if err := strict.Decode(destination); err != nil {
		return fmt.Errorf("%w: %v", errAPIV1InvalidRequest, err)
	}
	return nil
}

func (s *server) rejectMalformedAPIPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath := r.URL.EscapedPath()
		if isAPIPath(r.URL.Path) && path.Clean(escapedPath) != escapedPath {
			s.respondAPIError(w, r, http.StatusBadRequest, "invalid_request", "The request path is malformed.", nil, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAPIPath(path string) bool {
	return path == apiV1Prefix || strings.HasPrefix(path, apiV1Prefix+"/")
}
