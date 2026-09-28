package project

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNewServiceRejectsNilProvider(t *testing.T) {
	t.Parallel()

	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService(nil) succeeded")
	}
	var typedNil *serviceProviderStub
	if _, err := NewService(typedNil); err == nil {
		t.Fatal("NewService(typed nil) succeeded")
	}
}

func TestServiceReturnsStableCollectionOrderWithoutMutatingProviderSlices(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	providerProjects := []Project{{ID: "z", Name: "Zeta"}, {ID: "b", Name: "alpha"}, {ID: "a", Name: "Alpha"}}
	providerTasks := []Task{
		{ID: "old", UpdatedAt: now.Add(-time.Hour)},
		{ID: "b", UpdatedAt: now},
		{ID: "a", UpdatedAt: now},
	}
	provider := &serviceProviderStub{projects: providerProjects, tasks: providerTasks}
	service, err := NewService(provider)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	projects, err := service.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if got := []string{projects[0].ID, projects[1].ID, projects[2].ID}; !reflect.DeepEqual(got, []string{"a", "b", "z"}) {
		t.Fatalf("ListProjects() IDs = %v", got)
	}
	tasks, err := service.ListTasks(context.Background(), " demo ", TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if got := []string{tasks[0].ID, tasks[1].ID, tasks[2].ID}; !reflect.DeepEqual(got, []string{"a", "b", "old"}) {
		t.Fatalf("ListTasks() IDs = %v", got)
	}
	if provider.listProjectID != " demo " {
		t.Fatalf("provider project ID = %q", provider.listProjectID)
	}
	if providerProjects[0].ID != "z" || providerTasks[0].ID != "old" {
		t.Fatal("service reordered provider-owned slices")
	}
}

func TestServiceValidatesTaskInputBeforeCallingProvider(t *testing.T) {
	t.Parallel()

	provider := &serviceProviderStub{}
	service, err := NewService(provider)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	priority := 5
	_, err = service.CreateTask(context.Background(), "demo", TaskInput{Title: "Task", Priority: &priority})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateTask() error = %v, want ErrInvalid", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("provider CreateTask calls = %d", provider.createCalls)
	}

	closed := StatusClosed
	_, err = service.UpdateTask(context.Background(), "demo", "task", TaskPatch{Status: &closed})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateTask(lifecycle) error = %v, want ErrInvalid", err)
	}
	if provider.updateCalls != 0 {
		t.Fatalf("provider UpdateTask calls = %d", provider.updateCalls)
	}
}

func TestServiceOrchestratesTaskLifecycle(t *testing.T) {
	t.Parallel()

	provider := &serviceProviderStub{task: Task{ID: "task", ProjectID: "demo"}}
	service, err := NewService(provider)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	input := Completion{Message: " done ", Evidence: []string{" test:go test ./... "}, Comment: " shipped "}
	if _, err := service.CloseTask(context.Background(), " demo ", " task ", input); err != nil {
		t.Fatalf("CloseTask() error = %v", err)
	}
	if provider.updateProjectID != " demo " || provider.updateTaskID != " task " {
		t.Fatalf("provider identity = %q/%q", provider.updateProjectID, provider.updateTaskID)
	}
	patch := provider.patch
	if patch.Status == nil || *patch.Status != StatusClosed || patch.Completion == nil {
		t.Fatalf("close patch = %#v", patch)
	}
	if patch.Completion.Message != "done" || !reflect.DeepEqual(patch.Completion.Evidence, []string{"test:go test ./..."}) || patch.Completion.Comment != "shipped" {
		t.Fatalf("completion = %#v", patch.Completion)
	}
	if input.Evidence[0] != " test:go test ./... " {
		t.Fatal("CloseTask mutated caller evidence")
	}

	if _, err := service.ReopenTask(context.Background(), "demo", "task"); err != nil {
		t.Fatalf("ReopenTask() error = %v", err)
	}
	if provider.patch.Status == nil || *provider.patch.Status != StatusOpen || provider.patch.Completion != nil {
		t.Fatalf("reopen patch = %#v", provider.patch)
	}
}

func TestServiceRejectsInvalidCloseEvidence(t *testing.T) {
	t.Parallel()

	for _, completion := range []Completion{
		{Message: "done"},
		{Message: "done", Evidence: []string{"verified manually"}},
		{Message: "done", Evidence: []string{"test:"}},
	} {
		provider := &serviceProviderStub{}
		service, err := NewService(provider)
		if err != nil {
			t.Fatalf("NewService() error = %v", err)
		}
		_, err = service.CloseTask(context.Background(), "demo", "task", completion)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("CloseTask(%#v) error = %v, want ErrInvalid", completion, err)
		}
		if provider.updateCalls != 0 {
			t.Errorf("provider UpdateTask calls = %d", provider.updateCalls)
		}
	}
}

type serviceProviderStub struct {
	listFilter      TaskFilter
	task            Task
	patch           TaskPatch
	listProjectID   string
	updateProjectID string
	updateTaskID    string
	projects        []Project
	tasks           []Task
	createCalls     int
	updateCalls     int
}

func (*serviceProviderStub) Name() string { return "stub" }
func (p *serviceProviderStub) ListProjects(context.Context) ([]Project, error) {
	return p.projects, nil
}
func (*serviceProviderStub) GetProject(context.Context, string) (Project, error) {
	return Project{}, nil
}
func (*serviceProviderStub) CreateProject(context.Context, ProjectInput) (Project, error) {
	return Project{}, nil
}
func (*serviceProviderStub) UpdateProject(context.Context, string, ProjectPatch) (Project, error) {
	return Project{}, nil
}
func (*serviceProviderStub) DeleteProject(context.Context, string) error { return nil }
func (p *serviceProviderStub) ListTasks(_ context.Context, projectID string, filter TaskFilter) ([]Task, error) {
	p.listProjectID = projectID
	p.listFilter = filter
	return p.tasks, nil
}
func (p *serviceProviderStub) GetTask(context.Context, string, string) (Task, error) {
	return p.task, nil
}
func (p *serviceProviderStub) CreateTask(context.Context, string, TaskInput) (Task, error) {
	p.createCalls++
	return p.task, nil
}
func (p *serviceProviderStub) UpdateTask(_ context.Context, projectID, id string, patch TaskPatch) (Task, error) {
	p.updateCalls++
	p.updateProjectID = projectID
	p.updateTaskID = id
	p.patch = patch
	return p.task, nil
}
func (p *serviceProviderStub) CommentTask(context.Context, string, string, string) (Task, error) {
	return p.task, nil
}
func (*serviceProviderStub) DeleteTask(context.Context, string, string) error { return nil }
