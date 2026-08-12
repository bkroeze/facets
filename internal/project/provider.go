// Package project defines provider-neutral projects and tasks.
package project

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid          = errors.New("project: invalid input")
	ErrNotFound         = errors.New("project: not found")
	ErrUnsupported      = errors.New("project: operation unsupported")
	ErrProviderExists   = errors.New("project: provider already registered")
	ErrProviderNotFound = errors.New("project: provider not registered")
)

// Status is a provider-neutral task lifecycle state.
type Status string

const (
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
)

// Project identifies a project managed by a provider.
type Project struct {
	ID          string
	Name        string
	Description string
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Task is work tracked inside a project.
type Task struct {
	ID          string
	ProjectID   string
	Title       string
	Description string
	Status      Status
	Priority    *int
	Assignee    string
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectInput contains fields used to create a project.
type ProjectInput struct {
	Name        string
	Description string
	Metadata    map[string]any
}

// ProjectPatch contains project fields to replace when non-nil.
type ProjectPatch struct {
	Name        *string
	Description *string
	Metadata    map[string]any
}

// TaskFilter narrows a task listing. A nil status includes every status.
type TaskFilter struct {
	Status *Status
}

// TaskInput contains fields used to create a task.
type TaskInput struct {
	Title          string
	Description    string
	Priority       *int
	Assignee       string
	IdempotencyKey string
	Metadata       map[string]any
}

// Completion supplies the audit context needed to close a task. Comment, when
// non-empty, appends provider-native commentary as part of the close mutation.
type Completion struct {
	Message  string
	Evidence []string
	Comment  string
}

// PriorityPatch distinguishes an unchanged priority from setting or clearing it.
// Set false leaves the priority unchanged. Set true with a nil Value clears it.
type PriorityPatch struct {
	Set   bool
	Value *int
}

// TaskPatch contains optional task field changes.
type TaskPatch struct {
	Title       *string
	Description *string
	Priority    PriorityPatch
	Assignee    *string
	Status      *Status
	Completion  *Completion
	Metadata    map[string]any
}

// Provider supplies project and task CRUD plus task comments for one external system.
// Delete operations may archive when the external system supports recovery.
type Provider interface {
	Name() string

	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, id string) (Project, error)
	CreateProject(ctx context.Context, input ProjectInput) (Project, error)
	UpdateProject(ctx context.Context, id string, patch ProjectPatch) (Project, error)
	DeleteProject(ctx context.Context, id string) error

	ListTasks(ctx context.Context, projectID string, filter TaskFilter) ([]Task, error)
	GetTask(ctx context.Context, projectID, id string) (Task, error)
	CreateTask(ctx context.Context, projectID string, input TaskInput) (Task, error)
	UpdateTask(ctx context.Context, projectID, id string, patch TaskPatch) (Task, error)
	CommentTask(ctx context.Context, projectID, id, body string) (Task, error)
	DeleteTask(ctx context.Context, projectID, id string) error
}
