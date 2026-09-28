// Package tasks integrates Facets with the Google Tasks API.
package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
)

const (
	defaultBaseURL = "https://tasks.googleapis.com"
	referenceLine  = "facets-ref:"
)

// TokenSource supplies an OAuth access token for one request.
type TokenSource interface {
	Token(context.Context) (string, error)
}

// StaticTokenSource is useful for a configured token or isolated tests.
type StaticTokenSource string

// Token returns the configured token.
func (s StaticTokenSource) Token(context.Context) (string, error) {
	token := strings.TrimSpace(string(s))
	if token == "" {
		return "", errors.New("tasks: access token is required")
	}
	return token, nil
}

// Config configures a Google Tasks client for one dedicated list.
type Config struct {
	HTTPClient  *http.Client
	TokenSource TokenSource
	BaseURL     string
	ListID      string
}

// Client manages one Google Tasks list.
type Client struct {
	httpClient  *http.Client
	tokenSource TokenSource
	baseURL     *url.URL
	listID      string
}

// New validates configuration and returns a Google Tasks client.
func New(config Config) (*Client, error) {
	if config.TokenSource == nil {
		return nil, errors.New("tasks: token source is required")
	}
	listID := strings.TrimSpace(config.ListID)
	if listID == "" {
		return nil, errors.New("tasks: list ID is required")
	}
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("tasks: invalid base URL %q", baseURL)
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{httpClient: httpClient, tokenSource: config.TokenSource, baseURL: parsed, listID: listID}, nil
}

// APIError describes a non-successful Google Tasks response.
type APIError struct {
	Method     string
	URL        string
	Message    string
	StatusCode int
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("tasks: %s %s returned HTTP %d", e.Method, e.URL, e.StatusCode)
	}
	return fmt.Sprintf("tasks: %s %s returned HTTP %d: %s", e.Method, e.URL, e.StatusCode, e.Message)
}

// List is a Google Tasks task list.
type List struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	ETag    string `json:"etag"`
}

// Task is the provider-neutral subset of a Google Tasks task.
type Task struct {
	Due        *time.Time
	Completed  *time.Time
	Updated    *time.Time
	ID         string
	ETag       string
	Title      string
	Notes      string
	Status     Status
	WebViewURL string
	Deleted    bool
	Hidden     bool
}

// Status is a Google Tasks lifecycle state.
type Status string

const (
	StatusOpen      Status = "needsAction"
	StatusCompleted Status = "completed"
)

// TaskInput creates a task in the configured list.
type TaskInput struct {
	Due       *time.Time
	Reference *Reference
	Title     string
	Notes     string
}

// TaskPatch updates fields that are non-nil. ClearDue removes the due date.
type TaskPatch struct {
	Title     *string
	Notes     *string
	Due       *time.Time
	Status    *Status
	Reference *Reference
	ClearDue  bool
}

// ListFilter controls task listing and pagination.
type ListFilter struct {
	DueBefore     *time.Time
	MaxResults    int
	ShowCompleted bool
	ShowHidden    bool
}

// Reference identifies the Facets object represented by a remote task.
type Reference struct {
	Kind      string
	ID        string
	ProjectID string
	TaskID    string
}

// ProjectReference returns a reference to a project task.
func ProjectReference(projectID, taskID string) Reference {
	return Reference{Kind: "project", ProjectID: strings.TrimSpace(projectID), TaskID: strings.TrimSpace(taskID)}
}

// PeriodicalReference returns a reference to a due periodical task.
func PeriodicalReference(id string) Reference {
	return Reference{Kind: "periodical", ID: strings.TrimSpace(id)}
}

// EncodeReference returns the stable reference stored in Google Task notes.
func EncodeReference(reference Reference) (string, error) {
	reference.Kind = strings.TrimSpace(reference.Kind)
	reference.ID = strings.TrimSpace(reference.ID)
	reference.ProjectID = strings.TrimSpace(reference.ProjectID)
	reference.TaskID = strings.TrimSpace(reference.TaskID)
	switch reference.Kind {
	case "project":
		if reference.ProjectID == "" || reference.TaskID == "" {
			return "", errors.New("tasks: project reference requires project and task IDs")
		}
		return fmt.Sprintf("%s facets://project/%s/task/%s", referenceLine, url.PathEscape(reference.ProjectID), url.PathEscape(reference.TaskID)), nil
	case "periodical":
		if reference.ID == "" {
			return "", errors.New("tasks: periodical reference requires an ID")
		}
		return fmt.Sprintf("%s facets://periodical/%s", referenceLine, url.PathEscape(reference.ID)), nil
	default:
		return "", fmt.Errorf("tasks: unsupported reference kind %q", reference.Kind)
	}
}

// ParseReference extracts a Facets reference from task notes.
func ParseReference(notes string) (Reference, bool, error) {
	for _, line := range strings.Split(notes, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, referenceLine) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, referenceLine))
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "facets" {
			return Reference{}, true, fmt.Errorf("tasks: invalid Facets reference %q", value)
		}
		parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
		for i := range parts {
			parts[i], err = url.PathUnescape(parts[i])
			if err != nil {
				return Reference{}, true, fmt.Errorf("tasks: invalid Facets reference path: %w", err)
			}
		}
		switch parsed.Host {
		case "project":
			if len(parts) != 3 || parts[1] != "task" || parts[0] == "" || parts[2] == "" {
				return Reference{}, true, fmt.Errorf("tasks: invalid project reference %q", value)
			}
			return ProjectReference(parts[0], parts[2]), true, nil
		case "periodical":
			if len(parts) != 1 || parts[0] == "" {
				return Reference{}, true, fmt.Errorf("tasks: invalid periodical reference %q", value)
			}
			return PeriodicalReference(parts[0]), true, nil
		default:
			return Reference{}, true, fmt.Errorf("tasks: unsupported reference host %q", parsed.Host)
		}
	}
	return Reference{}, false, nil
}

// WithReference replaces an existing reference line or appends one to notes.
func WithReference(notes string, reference Reference) (string, error) {
	encoded, err := EncodeReference(reference)
	if err != nil {
		return "", err
	}
	lines := strings.Split(notes, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), referenceLine) {
			lines[i] = encoded
			return strings.Join(lines, "\n"), nil
		}
	}
	if strings.TrimSpace(notes) == "" {
		return encoded, nil
	}
	return strings.TrimRight(notes, "\n") + "\n" + encoded, nil
}

// ListLists returns all task lists visible to the authenticated user.
func (c *Client) ListLists(ctx context.Context) ([]List, error) {
	lists := make([]List, 0)
	pageToken := ""
	for {
		response := struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []List `json:"items"`
		}{}
		query := url.Values{}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		if err := c.doJSON(ctx, http.MethodGet, "/tasks/v1/users/@me/lists", query, nil, &response); err != nil {
			return nil, err
		}
		lists = append(lists, response.Items...)
		pageToken = response.NextPageToken
		if pageToken == "" {
			return lists, nil
		}
	}
}

// GetList returns a task list by ID.
func (c *Client) GetList(ctx context.Context, id string) (List, error) {
	var response List
	if err := c.doJSON(ctx, http.MethodGet, "/tasks/v1/users/@me/lists/"+url.PathEscape(strings.TrimSpace(id)), nil, nil, &response); err != nil {
		return List{}, err
	}
	return response, nil
}

// CreateList creates a task list.
func (c *Client) CreateList(ctx context.Context, title string) (List, error) {
	if strings.TrimSpace(title) == "" {
		return List{}, errors.New("tasks: list title is required")
	}
	var response List
	if err := c.doJSON(ctx, http.MethodPost, "/tasks/v1/users/@me/lists", nil, map[string]string{"title": title}, &response); err != nil {
		return List{}, err
	}
	return response, nil
}

// UpdateList renames a task list.
func (c *Client) UpdateList(ctx context.Context, id, title string) (List, error) {
	if strings.TrimSpace(title) == "" {
		return List{}, errors.New("tasks: list title is required")
	}
	var response List
	if err := c.doJSON(ctx, http.MethodPut, "/tasks/v1/users/@me/lists/"+url.PathEscape(strings.TrimSpace(id)), nil, map[string]string{"title": title}, &response); err != nil {
		return List{}, err
	}
	return response, nil
}

// DeleteList deletes a task list.
func (c *Client) DeleteList(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/tasks/v1/users/@me/lists/"+url.PathEscape(strings.TrimSpace(id)), nil, nil, nil)
}

// ListTasks returns every task in the configured list, following API pages.
func (c *Client) ListTasks(ctx context.Context, filter ListFilter) ([]Task, error) {
	items := make([]Task, 0)
	pageToken := ""
	for {
		query := url.Values{}
		query.Set("showCompleted", fmt.Sprint(filter.ShowCompleted))
		query.Set("showHidden", fmt.Sprint(filter.ShowHidden))
		if filter.MaxResults > 0 {
			query.Set("maxResults", fmt.Sprint(filter.MaxResults))
		}
		if filter.DueBefore != nil {
			query.Set("dueMax", filter.DueBefore.UTC().Format(time.RFC3339))
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			NextPageToken string       `json:"nextPageToken"`
			Items         []googleTask `json:"items"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/tasks/v1/lists/"+url.PathEscape(c.listID)+"/tasks", query, nil, &response); err != nil {
			return nil, err
		}
		for _, item := range response.Items {
			mapped, err := item.toTask()
			if err != nil {
				return nil, err
			}
			if !item.Deleted {
				items = append(items, mapped)
			}
		}
		pageToken = response.NextPageToken
		if pageToken == "" {
			return items, nil
		}
	}
}

// GetTask returns a task in the configured list.
func (c *Client) GetTask(ctx context.Context, id string) (Task, error) {
	var response googleTask
	if err := c.doJSON(ctx, http.MethodGet, c.taskPath(id), nil, nil, &response); err != nil {
		return Task{}, err
	}
	return response.toTask()
}

// CreateTask creates a task and injects its Facets reference into notes.
func (c *Client) CreateTask(ctx context.Context, input TaskInput) (Task, error) {
	if strings.TrimSpace(input.Title) == "" {
		return Task{}, errors.New("tasks: task title is required")
	}
	notes, err := notesWithReference(input.Notes, input.Reference)
	if err != nil {
		return Task{}, err
	}
	body := googleTask{Title: input.Title, Notes: notes, Due: formatTime(input.Due)}
	var response googleTask
	if err = c.doJSON(ctx, http.MethodPost, "/tasks/v1/lists/"+url.PathEscape(c.listID)+"/tasks", nil, body, &response); err != nil {
		return Task{}, err
	}
	return response.toTask()
}

// UpdateTask updates a task and preserves or replaces its Facets reference.
func (c *Client) UpdateTask(ctx context.Context, id string, patch TaskPatch) (Task, error) {
	body := make(map[string]any)
	if patch.Title != nil {
		body["title"] = *patch.Title
	}
	if patch.Status != nil {
		body["status"] = string(*patch.Status)
	}
	if patch.Due != nil {
		body["due"] = formatTime(patch.Due)
	}
	if patch.ClearDue {
		body["due"] = nil
	}

	var existing Task
	if patch.Notes != nil || patch.Reference != nil {
		var err error
		existing, err = c.GetTask(ctx, id)
		if err != nil {
			return Task{}, err
		}
	}
	if patch.Notes != nil {
		notes := *patch.Notes
		if reference, found, err := ParseReference(existing.Notes); err != nil {
			return Task{}, err
		} else if found {
			notes, err = WithReference(notes, reference)
			if err != nil {
				return Task{}, err
			}
		}
		body["notes"] = notes
	}
	if patch.Reference != nil {
		notes := existing.Notes
		if patch.Notes != nil {
			updatedNotes, ok := body["notes"].(string)
			if !ok {
				return Task{}, errors.New("tasks: notes payload is not a string")
			}
			notes = updatedNotes
		}
		updatedNotes, err := WithReference(notes, *patch.Reference)
		if err != nil {
			return Task{}, err
		}
		body["notes"] = updatedNotes
	}
	var response googleTask
	if err := c.doJSON(ctx, http.MethodPatch, c.taskPath(id), nil, body, &response); err != nil {
		return Task{}, err
	}
	return response.toTask()
}

// CompleteTask marks a task complete.
func (c *Client) CompleteTask(ctx context.Context, id string) (Task, error) {
	status := StatusCompleted
	return c.UpdateTask(ctx, id, TaskPatch{Status: &status})
}

// DeleteTask deletes a task from the configured list.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, c.taskPath(id), nil, nil, nil)
}

// DuePeriodical is a periodical Facets task that is due in a daily list.
type DuePeriodical struct {
	Due         time.Time
	ID          string
	Title       string
	Description string
}

// DuePeriodicalSource supplies periodical tasks due on a date.
type DuePeriodicalSource interface {
	DuePeriodicals(context.Context, time.Time) ([]DuePeriodical, error)
}

// SyncDuePeriodicals creates or updates the dedicated-list entries for due periodicals.
func SyncDuePeriodicals(ctx context.Context, client *Client, source DuePeriodicalSource, now time.Time) ([]Task, error) {
	if client == nil || source == nil {
		return nil, errors.New("tasks: client and periodical source are required")
	}
	due, err := source.DuePeriodicals(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("tasks: list due periodicals: %w", err)
	}
	remote, err := client.ListTasks(ctx, ListFilter{ShowCompleted: true, ShowHidden: true})
	if err != nil {
		return nil, fmt.Errorf("tasks: list periodical tasks: %w", err)
	}
	byID := make(map[string]Task)
	for _, item := range remote {
		ref, found, err := ParseReference(item.Notes)
		if err != nil {
			return nil, err
		}
		if found && ref.Kind == "periodical" {
			byID[ref.ID] = item
		}
	}
	result := make([]Task, 0, len(due))
	for _, item := range due {
		itemID := strings.TrimSpace(item.ID)
		title := strings.TrimSpace(item.Title)
		if itemID == "" || title == "" {
			return nil, errors.New("tasks: due periodical requires an ID and title")
		}
		item.ID, item.Title = itemID, title
		ref := PeriodicalReference(itemID)
		existing, found := byID[itemID]
		if !found {
			created, err := client.CreateTask(ctx, TaskInput{Title: item.Title, Notes: item.Description, Due: &item.Due, Reference: &ref})
			if err != nil {
				return nil, fmt.Errorf("tasks: create periodical %q: %w", itemID, err)
			}
			result = append(result, created)
			continue
		}
		status := StatusOpen
		updated, err := client.UpdateTask(ctx, existing.ID, TaskPatch{Title: &item.Title, Notes: &item.Description, Due: &item.Due, Status: &status, Reference: &ref})
		if err != nil {
			return nil, fmt.Errorf("tasks: update periodical %q: %w", itemID, err)
		}
		result = append(result, updated)
	}
	return result, nil
}

// ReconcileCompleted applies completed project-linked Google Tasks to a project provider.
func ReconcileCompleted(ctx context.Context, provider project.Provider, remote []Task) ([]Task, error) {
	if provider == nil {
		return nil, errors.New("tasks: project provider is required")
	}
	applied := make([]Task, 0)
	for _, item := range remote {
		if item.Status != StatusCompleted {
			continue
		}
		ref, found, err := ParseReference(item.Notes)
		if err != nil {
			return nil, err
		}
		if !found || ref.Kind != "project" {
			continue
		}
		local, err := provider.GetTask(ctx, ref.ProjectID, ref.TaskID)
		if err != nil {
			return nil, fmt.Errorf("tasks: get linked task %s/%s: %w", ref.ProjectID, ref.TaskID, err)
		}
		if local.Status == project.StatusClosed {
			continue
		}
		status := project.StatusClosed
		message := "Completed in Google Tasks"
		_, err = provider.UpdateTask(ctx, ref.ProjectID, ref.TaskID, project.TaskPatch{
			Status:     &status,
			Completion: &project.Completion{Message: message, Evidence: []string{item.ID}},
		})
		if err != nil {
			return nil, fmt.Errorf("tasks: complete linked task %s/%s: %w", ref.ProjectID, ref.TaskID, err)
		}
		applied = append(applied, item)
	}
	return applied, nil
}

type googleTask struct {
	ID         string `json:"id,omitempty"`
	ETag       string `json:"etag,omitempty"`
	Title      string `json:"title,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Status     string `json:"status,omitempty"`
	Due        string `json:"due,omitempty"`
	Completed  string `json:"completed,omitempty"`
	Updated    string `json:"updated,omitempty"`
	WebViewURL string `json:"webViewLink,omitempty"`
	Deleted    bool   `json:"deleted,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
}

func (g googleTask) toTask() (Task, error) {
	due, err := parseTime(g.Due)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: parse due time: %w", err)
	}
	completed, err := parseTime(g.Completed)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: parse completed time: %w", err)
	}
	updated, err := parseTime(g.Updated)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: parse updated time: %w", err)
	}
	status := Status(g.Status)
	if status == "" {
		status = StatusOpen
	}
	return Task{ID: g.ID, ETag: g.ETag, Title: g.Title, Notes: g.Notes, Status: status, Due: due, Completed: completed, Updated: updated, Deleted: g.Deleted, Hidden: g.Hidden, WebViewURL: g.WebViewURL}, nil
}
func notesWithReference(notes string, reference *Reference) (string, error) {
	if reference == nil {
		return notes, nil
	}
	return WithReference(notes, *reference)
}

func (c *Client) taskPath(id string) string {
	return "/tasks/v1/lists/" + url.PathEscape(c.listID) + "/tasks/" + url.PathEscape(strings.TrimSpace(id))
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, result any) (returnErr error) {
	endpoint := *c.baseURL
	escapedPath := strings.TrimRight(endpoint.EscapedPath(), "/") + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return fmt.Errorf("tasks: invalid request path: %w", err)
	}
	endpoint.Path = decodedPath
	endpoint.RawPath = escapedPath
	endpoint.RawQuery = query.Encode()
	var payload io.Reader
	if body != nil {
		var marshalErr error
		var encoded []byte
		encoded, marshalErr = json.Marshal(body)
		if marshalErr != nil {
			return fmt.Errorf("tasks: encode request: %w", marshalErr)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), payload)
	if err != nil {
		return fmt.Errorf("tasks: create request: %w", err)
	}
	token, err := c.tokenSource.Token(ctx)
	if err != nil {
		return fmt.Errorf("tasks: get access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tasks: request %s %s: %w", method, endpoint.String(), err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("tasks: close response body: %w", closeErr))
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		if readErr != nil {
			return fmt.Errorf("tasks: read error response: %w", readErr)
		}
		message := strings.TrimSpace(string(data))
		var apiMessage struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &apiMessage) == nil && apiMessage.Error.Message != "" {
			message = apiMessage.Error.Message
		}
		return &APIError{StatusCode: response.StatusCode, Method: method, URL: endpoint.String(), Message: message}
	}
	if result == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err = json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("tasks: decode response: %w", err)
	}
	return nil
}

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func parseTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
