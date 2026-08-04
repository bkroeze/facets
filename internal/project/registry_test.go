package project

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRegistryLifecycle(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	alpha := stubProvider{name: "alpha"}
	zeta := stubProvider{name: "zeta"}
	if err := registry.Register(zeta); err != nil {
		t.Fatalf("Register(zeta) error = %v", err)
	}
	if err := registry.Register(alpha); err != nil {
		t.Fatalf("Register(alpha) error = %v", err)
	}
	if err := registry.Register(alpha); !errors.Is(err, ErrProviderExists) {
		t.Fatalf("Register(duplicate) error = %v, want ErrProviderExists", err)
	}

	provider, err := registry.Provider("alpha")
	if err != nil {
		t.Fatalf("Provider(alpha) error = %v", err)
	}
	if provider.Name() != "alpha" {
		t.Fatalf("Provider(alpha).Name() = %q", provider.Name())
	}
	if _, err := registry.Provider("missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("Provider(missing) error = %v, want ErrProviderNotFound", err)
	}
	if names := registry.Names(); !reflect.DeepEqual(names, []string{"alpha", "zeta"}) {
		t.Fatalf("Names() = %#v", names)
	}
}

func TestRegistryRejectsInvalidProviders(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register(nil); err == nil {
		t.Fatal("Register(nil) error = nil")
	}
	if err := registry.Register(stubProvider{}); err == nil {
		t.Fatal("Register(empty name) error = nil")
	}
	var typedNil *stubProvider
	if err := registry.Register(typedNil); err == nil {
		t.Fatal("Register(typed nil) error = nil")
	}
	if err := registry.Register(stubProvider{name: " alpha"}); err == nil {
		t.Fatal("Register(leading whitespace name) error = nil")
	}
	if err := registry.Register(stubProvider{name: "alpha "}); err == nil {
		t.Fatal("Register(trailing whitespace name) error = nil")
	}
}

type stubProvider struct {
	name string
}

func (p stubProvider) Name() string { return p.name }

func (stubProvider) ListProjects(context.Context) ([]Project, error) {
	return nil, ErrUnsupported
}

func (stubProvider) GetProject(context.Context, string) (Project, error) {
	return Project{}, ErrUnsupported
}

func (stubProvider) CreateProject(context.Context, ProjectInput) (Project, error) {
	return Project{}, ErrUnsupported
}

func (stubProvider) UpdateProject(context.Context, string, ProjectPatch) (Project, error) {
	return Project{}, ErrUnsupported
}

func (stubProvider) DeleteProject(context.Context, string) error { return ErrUnsupported }

func (stubProvider) ListTasks(context.Context, string, TaskFilter) ([]Task, error) {
	return nil, ErrUnsupported
}

func (stubProvider) GetTask(context.Context, string, string) (Task, error) {
	return Task{}, ErrUnsupported
}

func (stubProvider) CreateTask(context.Context, string, TaskInput) (Task, error) {
	return Task{}, ErrUnsupported
}

func (stubProvider) UpdateTask(context.Context, string, string, TaskPatch) (Task, error) {
	return Task{}, ErrUnsupported
}

func (stubProvider) DeleteTask(context.Context, string, string) error { return ErrUnsupported }
