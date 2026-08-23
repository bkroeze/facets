// Package project defines provider-neutral projects and tasks.
package project

import (
	"context"
	"errors"
	"reflect"
	"time"
)

var (
	ErrInvalid          = errors.New("project: invalid input")
	ErrConflict         = errors.New("project: conflict")
	ErrNotFound         = errors.New("project: not found")
	ErrStorage          = errors.New("project: storage failure")
	ErrUnsupported      = errors.New("project: operation unsupported")
	ErrProviderExists   = errors.New("project: provider already registered")
	ErrProviderNotFound = errors.New("project: provider not registered")
)

func isNilInterface(value any) bool {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

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

// TaskQuery describes a provider-neutral saved task selection. Empty fields do
// not restrict the result.
type TaskQuery struct {
	Statuses   []Status `json:"statuses"`
	Assignees  []string `json:"assignees"`
	Priorities []int    `json:"priorities"`
}

// TaskOrder defines deterministic task ordering.
type TaskOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

const (
	TaskOrderTitle     = "title"
	TaskOrderStatus    = "status"
	TaskOrderPriority  = "priority"
	TaskOrderAssignee  = "assignee"
	TaskOrderCreatedAt = "created_at"
	TaskOrderUpdatedAt = "updated_at"

	TaskOrderAscending  = "asc"
	TaskOrderDescending = "desc"
)

// SavedView is a named provider-neutral task query.
type SavedView struct {
	ID        string
	Name      string
	Builtin   bool
	Query     TaskQuery
	Order     TaskOrder
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SavedViewInput contains fields used to create a saved view.
type SavedViewInput struct {
	Name  string
	Query TaskQuery
	Order TaskOrder
}

// SavedViewPatch contains saved-view fields to replace when non-nil.
type SavedViewPatch struct {
	Name  *string
	Query *TaskQuery
	Order *TaskOrder
}

// SavedViewStore persists user-defined views. Built-in views are synthesized
// by Service and never passed to this interface.
type SavedViewStore interface {
	ListSavedViews(context.Context) ([]SavedView, error)
	GetSavedView(context.Context, string) (SavedView, error)
	CreateSavedView(context.Context, SavedView) (SavedView, error)
	UpdateSavedView(context.Context, string, SavedViewPatch) (SavedView, error)
	DeleteSavedView(context.Context, string) error
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
