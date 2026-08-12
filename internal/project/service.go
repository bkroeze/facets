package project

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Service is the provider-neutral application boundary used by interactive and
// remote clients. It owns input validation and task lifecycle orchestration;
// providers remain responsible for translating normalized operations to their
// external system.
type Service struct {
	provider  Provider
	viewStore SavedViewStore
}

// NewService constructs an application service for provider. A single optional
// saved-view store enables persisted view operations.
func NewService(provider Provider, stores ...SavedViewStore) (*Service, error) {
	if provider == nil || isNilProvider(provider) {
		return nil, fmt.Errorf("project service: provider is required")
	}
	if len(stores) > 1 {
		return nil, fmt.Errorf("project service: only one saved-view store is supported")
	}
	service := &Service{provider: provider}
	if len(stores) == 1 {
		if stores[0] == nil || isNilSavedViewStore(stores[0]) {
			return nil, fmt.Errorf("project service: saved-view store is required")
		}
		service.viewStore = stores[0]
	}
	return service, nil
}

func isNilSavedViewStore(store SavedViewStore) bool {
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// ListProjects returns projects in a stable display order.
func (s *Service) ListProjects(ctx context.Context) ([]Project, error) {
	projects, err := s.provider.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	projects = append([]Project(nil), projects...)
	sort.SliceStable(projects, func(i, j int) bool {
		left, right := strings.ToLower(projects[i].Name), strings.ToLower(projects[j].Name)
		if left == right {
			return projects[i].ID < projects[j].ID
		}
		return left < right
	})
	return projects, nil
}

// GetProject returns one normalized project.
func (s *Service) GetProject(ctx context.Context, id string) (Project, error) {
	id, err := requiredID("project ID", id)
	if err != nil {
		return Project{}, err
	}
	return s.provider.GetProject(ctx, id)
}

// CreateProject creates one project.
func (s *Service) CreateProject(ctx context.Context, input ProjectInput) (Project, error) {
	name, err := requiredValue("project name", input.Name)
	if err != nil {
		return Project{}, err
	}
	input.Name = name
	return s.provider.CreateProject(ctx, input)
}

// UpdateProject replaces the supplied project fields.
func (s *Service) UpdateProject(ctx context.Context, id string, patch ProjectPatch) (Project, error) {
	id, err := requiredID("project ID", id)
	if err != nil {
		return Project{}, err
	}
	if patch.Name == nil && patch.Description == nil && patch.Metadata == nil {
		return Project{}, invalid("project patch must contain a change")
	}
	if patch.Name != nil {
		name, err := requiredValue("project name", *patch.Name)
		if err != nil {
			return Project{}, err
		}
		patch.Name = &name
	}
	return s.provider.UpdateProject(ctx, id, patch)
}

// DeleteProject deletes or archives one project according to provider semantics.
func (s *Service) DeleteProject(ctx context.Context, id string) error {
	id, err := requiredID("project ID", id)
	if err != nil {
		return err
	}
	return s.provider.DeleteProject(ctx, id)
}

// ListTasks returns tasks in deterministic newest-first order. IDs break ties.
func (s *Service) ListTasks(ctx context.Context, projectID string, filter TaskFilter) ([]Task, error) {
	projectID, err := requiredID("project ID", projectID)
	if err != nil {
		return nil, err
	}
	if filter.Status != nil {
		if err := validStatus(*filter.Status); err != nil {
			return nil, err
		}
	}
	tasks, err := s.provider.ListTasks(ctx, projectID, filter)
	if err != nil {
		return nil, err
	}
	tasks = append([]Task(nil), tasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
	})
	return tasks, nil
}

// GetTask returns one task within projectID.
func (s *Service) GetTask(ctx context.Context, projectID, id string) (Task, error) {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return Task{}, err
	}
	return s.provider.GetTask(ctx, projectID, id)
}

// CreateTask creates one task. IdempotencyKey is passed through to providers
// that support idempotent creation.
func (s *Service) CreateTask(ctx context.Context, projectID string, input TaskInput) (Task, error) {
	projectID, err := requiredID("project ID", projectID)
	if err != nil {
		return Task{}, err
	}
	title, err := requiredValue("task title", input.Title)
	if err != nil {
		return Task{}, err
	}
	if err := validPriority(input.Priority); err != nil {
		return Task{}, err
	}
	input.Title = title
	input.Assignee = strings.TrimSpace(input.Assignee)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return s.provider.CreateTask(ctx, projectID, input)
}

// UpdateTask replaces editable task fields. Lifecycle changes use CloseTask or
// ReopenTask so completion evidence cannot be bypassed.
func (s *Service) UpdateTask(ctx context.Context, projectID, id string, patch TaskPatch) (Task, error) {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return Task{}, err
	}
	if patch.Status != nil || patch.Completion != nil {
		return Task{}, invalid("task lifecycle changes must use close or reopen")
	}
	if patch.Title == nil && patch.Description == nil && !patch.Priority.Set && patch.Assignee == nil && patch.Metadata == nil {
		return Task{}, invalid("task patch must contain a change")
	}
	if patch.Title != nil {
		title, err := requiredValue("task title", *patch.Title)
		if err != nil {
			return Task{}, err
		}
		patch.Title = &title
	}
	if err := validPriority(patch.Priority.Value); err != nil {
		return Task{}, err
	}
	if patch.Assignee != nil {
		assignee := strings.TrimSpace(*patch.Assignee)
		patch.Assignee = &assignee
	}
	return s.provider.UpdateTask(ctx, projectID, id, patch)
}

// CommentTask appends provider-native commentary to one task.
func (s *Service) CommentTask(ctx context.Context, projectID, id, body string) (Task, error) {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return Task{}, err
	}
	body, err = requiredValue("comment body", body)
	if err != nil {
		return Task{}, err
	}
	return s.provider.CommentTask(ctx, projectID, id, body)
}

// CloseTask closes one task with the audit context required by Facets.
func (s *Service) CloseTask(ctx context.Context, projectID, id string, completion Completion) (Task, error) {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return Task{}, err
	}
	completion.Message, err = requiredValue("completion message", completion.Message)
	if err != nil {
		return Task{}, err
	}
	if len(completion.Evidence) == 0 {
		return Task{}, invalid("completion evidence is required")
	}
	completion.Evidence = append([]string(nil), completion.Evidence...)
	for i, evidence := range completion.Evidence {
		completion.Evidence[i], err = validEvidence(fmt.Sprintf("completion evidence %d", i+1), evidence)
		if err != nil {
			return Task{}, err
		}
	}
	if completion.Comment != "" {
		completion.Comment, err = requiredValue("completion comment", completion.Comment)
		if err != nil {
			return Task{}, err
		}
	}
	closed := StatusClosed
	return s.provider.UpdateTask(ctx, projectID, id, TaskPatch{Status: &closed, Completion: &completion})
}

// ReopenTask returns one closed task to the open state.
func (s *Service) ReopenTask(ctx context.Context, projectID, id string) (Task, error) {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return Task{}, err
	}
	open := StatusOpen
	return s.provider.UpdateTask(ctx, projectID, id, TaskPatch{Status: &open})
}

// DeleteTask deletes or archives one task according to provider semantics.
func (s *Service) DeleteTask(ctx context.Context, projectID, id string) error {
	projectID, id, err := taskIdentity(projectID, id)
	if err != nil {
		return err
	}
	return s.provider.DeleteTask(ctx, projectID, id)
}

func taskIdentity(projectID, id string) (string, string, error) {
	projectID, err := requiredID("project ID", projectID)
	if err != nil {
		return "", "", err
	}
	id, err = requiredID("task ID", id)
	if err != nil {
		return "", "", err
	}
	return projectID, id, nil
}

func requiredValue(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalid(field + " is required")
	}
	return value, nil
}

func requiredID(field, value string) (string, error) {
	if value == "" {
		return "", invalid(field + " is required")
	}
	return value, nil
}

func validEvidence(field, evidence string) (string, error) {
	evidence = strings.TrimSpace(evidence)
	kind, value, found := strings.Cut(evidence, ":")
	if !found || strings.TrimSpace(kind) == "" || strings.ContainsAny(kind, " \t\r\n") || strings.TrimSpace(value) == "" {
		return "", invalid(field + " must use type:value format")
	}
	return evidence, nil
}

func validStatus(status Status) error {
	if status != StatusOpen && status != StatusClosed {
		return invalid(fmt.Sprintf("unsupported task status %q", status))
	}
	return nil
}

func validPriority(priority *int) error {
	if priority != nil && (*priority < 0 || *priority > 4) {
		return invalid(fmt.Sprintf("task priority %d is outside 0..4", *priority))
	}
	return nil
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
