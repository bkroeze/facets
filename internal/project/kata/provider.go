// Package kata adapts the Kata CLI to the project provider contract.
package kata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
)

const apiVersion = 1

// Runner executes one Kata command. Implementations must not invoke a shell.
type Runner interface {
	Run(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)

// Run executes f.
func (f RunnerFunc) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	return f(ctx, binary, args...)
}

// Config configures a Kata provider.
type Config struct {
	Binary string
	Actor  string
	Runner Runner
}

// Provider manages projects and tasks through Kata.
type Provider struct {
	binary string
	actor  string
	runner Runner
}

var _ project.Provider = (*Provider)(nil)

// New returns a Kata provider. Binary defaults to "kata" and Runner defaults to
// an os/exec runner that invokes the binary directly without a shell.
func New(config Config) *Provider {
	binary := strings.TrimSpace(config.Binary)
	if binary == "" {
		binary = "kata"
	}
	runner := config.Runner
	if runner == nil {
		runner = execRunner{}
	}
	return &Provider{binary: binary, actor: strings.TrimSpace(config.Actor), runner: runner}
}

func (p *Provider) Name() string { return "kata" }

func (p *Provider) ListProjects(ctx context.Context) ([]project.Project, error) {
	var response projectsResponse
	if err := p.runJSON(ctx, []string{"projects", "list"}, &response); err != nil {
		return nil, err
	}
	projects := make([]project.Project, len(response.Projects))
	for i := range response.Projects {
		mapped, err := mapProject(response.Projects[i], nil)
		if err != nil {
			return nil, fmt.Errorf("kata: map project at index %d: %w", i, err)
		}
		projects[i] = mapped
	}
	return projects, nil
}

func (p *Provider) GetProject(ctx context.Context, id string) (project.Project, error) {
	id, err := required("project ID", id)
	if err != nil {
		return project.Project{}, err
	}
	var response projectResponse
	if err := p.runJSON(ctx, []string{"projects", "show", id}, &response); err != nil {
		return project.Project{}, err
	}
	return mapProjectResponse(response)
}

func (p *Provider) CreateProject(ctx context.Context, input project.ProjectInput) (project.Project, error) {
	name, err := required("project name", input.Name)
	if err != nil {
		return project.Project{}, err
	}
	if input.Description != "" || len(input.Metadata) != 0 {
		return project.Project{}, fmt.Errorf("%w: Kata project creation supports only a name", project.ErrUnsupported)
	}
	var response projectResponse
	if err := p.runJSON(ctx, []string{"projects", "create", name}, &response); err != nil {
		return project.Project{}, err
	}
	return mapProjectResponse(response)
}

func (p *Provider) UpdateProject(ctx context.Context, id string, patch project.ProjectPatch) (project.Project, error) {
	id, err := required("project ID", id)
	if err != nil {
		return project.Project{}, err
	}
	if patch.Description != nil || patch.Metadata != nil {
		return project.Project{}, fmt.Errorf("%w: Kata project updates support only renaming", project.ErrUnsupported)
	}
	if patch.Name == nil {
		return project.Project{}, errors.New("kata: project patch must contain a name")
	}
	name, err := required("project name", *patch.Name)
	if err != nil {
		return project.Project{}, err
	}
	var response projectResponse
	if err := p.runJSON(ctx, []string{"projects", "rename", id, name}, &response); err != nil {
		return project.Project{}, err
	}
	return mapProjectResponse(response)
}

func (p *Provider) DeleteProject(ctx context.Context, id string) error {
	id, err := required("project ID", id)
	if err != nil {
		return err
	}
	return p.runJSON(ctx, []string{"projects", "remove", id, "--force"}, nil)
}

func (p *Provider) ListTasks(ctx context.Context, projectID string, filter project.TaskFilter) ([]project.Task, error) {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return nil, err
	}
	status := "all"
	if filter.Status != nil {
		if err := validateStatus(*filter.Status); err != nil {
			return nil, err
		}
		status = string(*filter.Status)
	}
	var response issuesResponse
	args := []string{"list", "--project", projectID, "--status", status}
	if err := p.runJSON(ctx, args, &response); err != nil {
		return nil, err
	}
	tasks := make([]project.Task, len(response.Issues))
	for i := range response.Issues {
		mapped, err := mapIssue(response.Issues[i], projectID)
		if err != nil {
			return nil, fmt.Errorf("kata: map task at index %d: %w", i, err)
		}
		tasks[i] = mapped
	}
	return tasks, nil
}

func (p *Provider) GetTask(ctx context.Context, projectID, id string) (project.Task, error) {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return project.Task{}, err
	}
	id, err = required("task ID", id)
	if err != nil {
		return project.Task{}, err
	}
	var response issueResponse
	if err := p.runJSON(ctx, []string{"show", id, "--project", projectID}, &response); err != nil {
		return project.Task{}, err
	}
	return mapIssue(response.Issue, projectID)
}

func (p *Provider) CreateTask(ctx context.Context, projectID string, input project.TaskInput) (project.Task, error) {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return project.Task{}, err
	}
	title, err := required("task title", input.Title)
	if err != nil {
		return project.Task{}, err
	}
	if input.Priority != nil {
		if err := validatePriority(*input.Priority); err != nil {
			return project.Task{}, err
		}
	}
	args := []string{"create", title, "--project", projectID}
	if input.Description != "" {
		args = append(args, "--body", input.Description)
	}
	if input.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*input.Priority))
	}
	if input.Assignee != "" {
		args = append(args, "--owner", input.Assignee)
	}
	if input.IdempotencyKey != "" {
		args = append(args, "--idempotency-key", input.IdempotencyKey)
	}
	metadataArgs, err := metadataFlags(input.Metadata)
	if err != nil {
		return project.Task{}, err
	}
	args = append(args, metadataArgs...)
	var response issueResponse
	if err := p.runJSON(ctx, args, &response); err != nil {
		return project.Task{}, err
	}
	return mapIssue(response.Issue, projectID)
}

func (p *Provider) UpdateTask(ctx context.Context, projectID, id string, patch project.TaskPatch) (project.Task, error) {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return project.Task{}, err
	}
	id, err = required("task ID", id)
	if err != nil {
		return project.Task{}, err
	}
	if patch.Metadata != nil && len(patch.Metadata) == 0 {
		return project.Task{}, fmt.Errorf("%w: empty task metadata replacement", project.ErrUnsupported)
	}
	if patch.Completion != nil && (patch.Status == nil || *patch.Status != project.StatusClosed) {
		return project.Task{}, errors.New("kata: completion is valid only when closing a task")
	}

	var statusArgs []string
	if patch.Status != nil {
		if err := validateStatus(*patch.Status); err != nil {
			return project.Task{}, err
		}
		switch *patch.Status {
		case project.StatusOpen:
			statusArgs = []string{"reopen", id, "--project", projectID}
		case project.StatusClosed:
			if patch.Completion == nil {
				return project.Task{}, errors.New("kata: closing a task requires completion")
			}
			message, err := required("completion message", patch.Completion.Message)
			if err != nil {
				return project.Task{}, err
			}
			if len(patch.Completion.Evidence) == 0 {
				return project.Task{}, errors.New("kata: closing a task requires evidence")
			}
			statusArgs = []string{"close", id, "--project", projectID, "--reason", "done", "--message", message}
			for i, evidence := range patch.Completion.Evidence {
				evidence, err = required(fmt.Sprintf("completion evidence %d", i+1), evidence)
				if err != nil {
					return project.Task{}, err
				}
				statusArgs = append(statusArgs, "--evidence", evidence)
			}
			if patch.Completion.Comment != "" {
				comment, err := required("completion comment", patch.Completion.Comment)
				if err != nil {
					return project.Task{}, err
				}
				statusArgs = append(statusArgs, "--comment", comment)
			}
		}
	}

	editArgs := []string{"edit", id, "--project", projectID}
	hasEdit := false
	if patch.Title != nil {
		title, err := required("task title", *patch.Title)
		if err != nil {
			return project.Task{}, err
		}
		editArgs = append(editArgs, "--title", title)
		hasEdit = true
	}
	if patch.Description != nil {
		editArgs = append(editArgs, "--body", *patch.Description)
		hasEdit = true
	}
	if patch.Priority.Set {
		priority := "-"
		if patch.Priority.Value != nil {
			if err := validatePriority(*patch.Priority.Value); err != nil {
				return project.Task{}, err
			}
			priority = strconv.Itoa(*patch.Priority.Value)
		}
		editArgs = append(editArgs, "--priority", priority)
		hasEdit = true
	}
	if patch.Assignee != nil {
		editArgs = append(editArgs, "--owner", *patch.Assignee)
		hasEdit = true
	}
	hasMetadata := len(patch.Metadata) > 0
	if !hasEdit && patch.Status == nil && !hasMetadata {
		return project.Task{}, errors.New("kata: task patch must contain a change")
	}

	var response issueResponse
	hasResponse := false
	if hasEdit {
		if err := p.runJSON(ctx, editArgs, &response); err != nil {
			return project.Task{}, err
		}
		hasResponse = true
	}
	if patch.Status != nil {
		if err := p.runJSON(ctx, statusArgs, &response); err != nil {
			return project.Task{}, err
		}
		hasResponse = true
	}
	if hasMetadata {
		response, err = p.updateTaskMetadata(ctx, projectID, id, patch.Metadata)
		if err != nil {
			return project.Task{}, err
		}
		hasResponse = true
	}
	if !hasResponse {
		return project.Task{}, errors.New("kata: task patch must contain a change")
	}
	return mapIssue(response.Issue, projectID)
}

func (p *Provider) updateTaskMetadata(ctx context.Context, projectID, id string, metadata map[string]any) (issueResponse, error) {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		if strings.TrimSpace(key) == "" {
			return issueResponse{}, errors.New("kata: task metadata key is required")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var response issueResponse
	for _, key := range keys {
		value := metadata[key]
		args := []string{"meta", "set", id, key}
		if value == nil {
			args = []string{"meta", "unset", id, key}
		} else {
			encoded, jsonValue, err := encodeMetadataValue(value)
			if err != nil {
				return issueResponse{}, fmt.Errorf("kata: encode task metadata %q: %w", key, err)
			}
			args = append(args, encoded)
			if jsonValue {
				args = append(args, "--json-value")
			}
		}
		args = append(args, "--project", projectID)
		if err := p.runJSON(ctx, args, &response); err != nil {
			return issueResponse{}, err
		}
	}
	return response, nil
}

func encodeMetadataValue(value any) (string, bool, error) {
	if text, ok := value.(string); ok {
		return text, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}
func (p *Provider) CommentTask(ctx context.Context, projectID, id, body string) (project.Task, error) {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return project.Task{}, err
	}
	id, err = required("task ID", id)
	if err != nil {
		return project.Task{}, err
	}
	body, err = required("comment body", body)
	if err != nil {
		return project.Task{}, err
	}
	var response issueResponse
	if err := p.runJSON(ctx, []string{"comment", id, "--project", projectID, "--body", body}, &response); err != nil {
		return project.Task{}, err
	}
	return mapIssue(response.Issue, projectID)
}

// DeleteTask explicitly authorizes Kata's destructive, recoverable soft-delete.
// It never invokes Kata's irreversible purge command.
func (p *Provider) DeleteTask(ctx context.Context, projectID, id string) error {
	projectID, err := required("project ID", projectID)
	if err != nil {
		return err
	}
	id, err = required("task ID", id)
	if err != nil {
		return err
	}
	args := []string{"delete", id, "--project", projectID, "--confirm", "DELETE " + id, "--force"}
	return p.runJSON(ctx, args, nil)
}

func (p *Provider) runJSON(ctx context.Context, args []string, response any) error {
	args = append(args, "--json")
	if p.actor != "" {
		args = append(args, "--as", p.actor)
	}
	stdout, stderr, err := p.runner.Run(ctx, p.binary, args...)
	if err != nil {
		commandErr := &CommandError{
			Binary: p.binary,
			Args:   append([]string(nil), args...),
			Stdout: strings.TrimSpace(string(stdout)),
			Stderr: strings.TrimSpace(string(stderr)),
			Err:    err,
		}
		var result error = commandErr
		if commandErrorKind(stdout) == "not_found" || commandErrorKind(stderr) == "not_found" {
			result = fmt.Errorf("%w: %w", project.ErrNotFound, commandErr)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			result = errors.Join(result, ctxErr)
		}
		return result
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		return fmt.Errorf("kata: decode %s response: %w", commandText(p.binary, args), err)
	}
	if envelope.Version != apiVersion {
		return fmt.Errorf("kata: %s returned unsupported kata_api_version %d", commandText(p.binary, args), envelope.Version)
	}
	if response != nil {
		if err := json.Unmarshal(stdout, response); err != nil {
			return fmt.Errorf("kata: decode %s response: %w", commandText(p.binary, args), err)
		}
	}
	return nil
}

// CommandError reports a failed Kata subprocess while retaining its original
// error and captured output for diagnostics.
type CommandError struct {
	Binary string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	detail := e.Stderr
	if detail == "" {
		detail = e.Stdout
	}
	if detail == "" {
		return fmt.Sprintf("kata: command %s failed: %v", commandText(e.Binary, e.Args), e.Err)
	}
	return fmt.Sprintf("kata: command %s failed: %v: %s", commandText(e.Binary, e.Args), e.Err, detail)
}

func (e *CommandError) Unwrap() error { return e.Err }

type execRunner struct{}

func (execRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type apiEnvelope struct {
	Version int `json:"kata_api_version"`
}

type commandErrorEnvelope struct {
	Error *struct {
		Kind string `json:"kind"`
	} `json:"error"`
}

type projectsResponse struct {
	Version  int          `json:"kata_api_version"`
	Projects []rawProject `json:"projects"`
}

type projectResponse struct {
	Version int         `json:"kata_api_version"`
	Project *rawProject `json:"project"`
	Aliases []rawAlias  `json:"aliases"`
}

type issuesResponse struct {
	Version int        `json:"kata_api_version"`
	Issues  []rawIssue `json:"issues"`
}

type issueResponse struct {
	Version int      `json:"kata_api_version"`
	Issue   rawIssue `json:"issue"`
}

type rawProject struct {
	ID        int64          `json:"id"`
	UID       string         `json:"uid"`
	Name      string         `json:"name"`
	Metadata  map[string]any `json:"metadata"`
	Revision  int            `json:"revision"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type rawAlias struct {
	Identity string `json:"alias_identity"`
}

type rawIssue struct {
	ID          int64          `json:"id"`
	UID         string         `json:"uid"`
	ProjectID   int64          `json:"project_id"`
	ProjectUID  string         `json:"project_uid"`
	ShortID     string         `json:"short_id"`
	QualifiedID string         `json:"qualified_id"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Status      string         `json:"status"`
	Owner       string         `json:"owner"`
	Priority    *int           `json:"priority"`
	Author      string         `json:"author"`
	Metadata    map[string]any `json:"metadata"`
	Revision    int            `json:"revision"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

func mapProjectResponse(response projectResponse) (project.Project, error) {
	if response.Project == nil {
		return project.Project{}, errors.New("kata: response is missing project")
	}
	return mapProject(*response.Project, response.Aliases)
}

func mapProject(source rawProject, aliases []rawAlias) (project.Project, error) {
	name, err := required("response project name", source.Name)
	if err != nil {
		return project.Project{}, err
	}
	createdAt, err := parseTime("project created_at", source.CreatedAt)
	if err != nil {
		return project.Project{}, err
	}
	updatedAt, err := parseTime("project updated_at", source.UpdatedAt)
	if err != nil {
		return project.Project{}, err
	}
	metadata := cloneMetadata(source.Metadata)
	metadata["native_id"] = source.ID
	metadata["uid"] = source.UID
	metadata["revision"] = source.Revision
	if aliases != nil {
		identities := make([]string, len(aliases))
		for i := range aliases {
			identities[i] = aliases[i].Identity
		}
		metadata["aliases"] = identities
	}
	return project.Project{
		ID:        name,
		Name:      name,
		Metadata:  metadata,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func mapIssue(source rawIssue, projectID string) (project.Task, error) {
	status := project.Status(source.Status)
	if err := validateStatus(status); err != nil {
		return project.Task{}, fmt.Errorf("task %q: %w", source.ShortID, err)
	}
	createdAt, err := parseTime("task created_at", source.CreatedAt)
	if err != nil {
		return project.Task{}, err
	}
	updatedAt, err := parseTime("task updated_at", source.UpdatedAt)
	if err != nil {
		return project.Task{}, err
	}
	metadata := cloneMetadata(source.Metadata)
	metadata["uid"] = source.UID
	metadata["qualified_id"] = source.QualifiedID
	metadata["author"] = source.Author
	metadata["revision"] = source.Revision
	metadata["project_uid"] = source.ProjectUID
	metadata["native_id"] = source.ID
	var priority *int
	if source.Priority != nil {
		priority = new(*source.Priority)
	}
	return project.Task{
		ID:          source.ShortID,
		ProjectID:   projectID,
		Title:       source.Title,
		Description: source.Body,
		Status:      status,
		Priority:    priority,
		Assignee:    source.Owner,
		Metadata:    metadata,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}, nil
}

func commandErrorKind(output []byte) string {
	var envelope commandErrorEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(output), &envelope); err != nil || envelope.Error == nil {
		return ""
	}
	return envelope.Error.Kind
}

func cloneMetadata(source map[string]any) map[string]any {
	metadata := make(map[string]any, len(source)+6)
	for key, value := range source {
		metadata[key] = value
	}
	return metadata
}

func parseTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("kata: parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

func required(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("kata: %s is required", field)
	}
	return value, nil
}

func validateStatus(status project.Status) error {
	if status != project.StatusOpen && status != project.StatusClosed {
		return fmt.Errorf("kata: unsupported task status %q", status)
	}
	return nil
}

func validatePriority(priority int) error {
	if priority < 0 || priority > 4 {
		return fmt.Errorf("kata: task priority %d is outside 0..4", priority)
	}
	return nil
}

func metadataFlags(metadata map[string]any) ([]string, error) {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		if strings.TrimSpace(key) == "" {
			return nil, errors.New("kata: task metadata key is required")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		value, ok := metadata[key].(string)
		if !ok {
			encoded, err := json.Marshal(metadata[key])
			if err != nil {
				return nil, fmt.Errorf("kata: encode task metadata %q: %w", key, err)
			}
			value = string(encoded)
		}
		args = append(args, "--meta", key+"="+value)
	}
	return args, nil
}

func commandText(binary string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, strconv.Quote(binary))
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}
