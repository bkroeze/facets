package project

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestActiveSavedViewWorksWithoutStore(t *testing.T) {
	t.Parallel()

	provider := &serviceProviderStub{tasks: []Task{{ID: "open", Status: StatusOpen}, {ID: "closed", Status: StatusClosed}}}
	service, err := NewService(provider)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	views, err := service.ListSavedViews(context.Background())
	if err != nil {
		t.Fatalf("ListSavedViews() error = %v", err)
	}
	if len(views) != 1 || views[0].ID != ActiveViewID || !views[0].Builtin {
		t.Fatalf("ListSavedViews() = %#v", views)
	}
	tasks, err := service.ExecuteSavedView(context.Background(), "demo", ActiveViewID)
	if err != nil {
		t.Fatalf("ExecuteSavedView() error = %v", err)
	}
	if provider.listProjectID != "demo" || len(tasks) != 1 || tasks[0].ID != "open" {
		t.Fatalf("ExecuteSavedView() tasks = %#v, project = %q", tasks, provider.listProjectID)
	}
	if provider.listFilter.Status == nil || *provider.listFilter.Status != StatusOpen {
		t.Fatalf("provider filter = %#v", provider.listFilter)
	}
}

func TestNewServiceRejectsTypedNilSavedViewStore(t *testing.T) {
	t.Parallel()

	var store *savedViewStoreStub
	if _, err := NewService(&serviceProviderStub{}, store); err == nil {
		t.Fatal("NewService(typed nil store) succeeded")
	}
}

func TestSavedViewStoreFailuresAreClassifiedAsStorageErrors(t *testing.T) {
	t.Parallel()

	store := newSavedViewStoreStub()
	store.err = errors.New("disk failure")
	service, err := NewService(&serviceProviderStub{}, store)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.ListSavedViews(context.Background()); !errors.Is(err, ErrStorage) {
		t.Fatalf("ListSavedViews() error = %v, want ErrStorage", err)
	}
}

func TestSavedViewCRUDValidationAndBuiltInProtection(t *testing.T) {
	t.Parallel()

	store := newSavedViewStoreStub()
	service, err := NewService(&serviceProviderStub{}, store)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	created, err := service.CreateSavedView(context.Background(), SavedViewInput{
		Name:  " Mine ",
		Query: TaskQuery{Statuses: []Status{StatusOpen, StatusOpen}, Assignees: []string{" sam ", "sam"}, Priorities: []int{3, 1, 3}},
	})
	if err != nil {
		t.Fatalf("CreateSavedView() error = %v", err)
	}
	if created.ID == "" || created.Name != "Mine" || created.Builtin {
		t.Fatalf("CreateSavedView() = %#v", created)
	}
	if !reflect.DeepEqual(created.Query.Statuses, []Status{StatusOpen}) || !reflect.DeepEqual(created.Query.Assignees, []string{"sam"}) || !reflect.DeepEqual(created.Query.Priorities, []int{1, 3}) {
		t.Fatalf("normalized query = %#v", created.Query)
	}
	if created.Order != (TaskOrder{Field: TaskOrderUpdatedAt, Direction: TaskOrderDescending}) {
		t.Fatalf("default order = %#v", created.Order)
	}

	name := "Renamed"
	order := TaskOrder{Field: TaskOrderTitle, Direction: TaskOrderAscending}
	updated, err := service.UpdateSavedView(context.Background(), created.ID, SavedViewPatch{Name: &name, Order: &order})
	if err != nil {
		t.Fatalf("UpdateSavedView() error = %v", err)
	}
	if updated.Name != name || updated.Order != order {
		t.Fatalf("UpdateSavedView() = %#v", updated)
	}
	if err := service.DeleteSavedView(context.Background(), created.ID); err != nil {
		t.Fatalf("DeleteSavedView() error = %v", err)
	}
	if _, err := service.GetSavedView(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSavedView(deleted) error = %v, want ErrNotFound", err)
	}

	if _, err := service.CreateSavedView(context.Background(), SavedViewInput{Name: "active"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateSavedView(active) error = %v, want ErrConflict", err)
	}
	if _, err := service.UpdateSavedView(context.Background(), ActiveViewID, SavedViewPatch{Name: &name}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateSavedView(active) error = %v, want ErrConflict", err)
	}
	if err := service.DeleteSavedView(context.Background(), ActiveViewID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteSavedView(active) error = %v, want ErrConflict", err)
	}
	if _, err := service.CreateSavedView(context.Background(), SavedViewInput{Name: "bad", Query: TaskQuery{Priorities: []int{9}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateSavedView(invalid priority) error = %v, want ErrInvalid", err)
	}
}

func TestExecuteSavedViewFiltersAndOrdersTasksDeterministically(t *testing.T) {
	t.Parallel()

	priorityOne, priorityTwo := 1, 2
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	provider := &serviceProviderStub{tasks: []Task{
		{ID: "z", Status: StatusOpen, Assignee: "sam", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "a", Status: StatusOpen, Assignee: "sam", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "low", Status: StatusOpen, Assignee: "sam", Priority: &priorityOne, UpdatedAt: now.Add(time.Hour)},
		{ID: "other", Status: StatusOpen, Assignee: "bruce", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "closed", Status: StatusClosed, Assignee: "sam", Priority: &priorityTwo, UpdatedAt: now},
		{ID: "unknown-time", Status: StatusOpen, Assignee: "sam", Priority: &priorityTwo},
	}}
	store := newSavedViewStoreStub()
	store.views["mine"] = SavedView{
		ID: "mine", Name: "Mine",
		Query: TaskQuery{Statuses: []Status{StatusOpen}, Assignees: []string{"sam"}, Priorities: []int{2}},
		Order: TaskOrder{Field: TaskOrderUpdatedAt, Direction: TaskOrderDescending},
	}
	service, err := NewService(provider, store)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	tasks, err := service.ExecuteSavedView(context.Background(), "demo", "mine")
	if err != nil {
		t.Fatalf("ExecuteSavedView() error = %v", err)
	}
	if got := []string{tasks[0].ID, tasks[1].ID, tasks[2].ID}; !reflect.DeepEqual(got, []string{"a", "z", "unknown-time"}) {
		t.Fatalf("task IDs = %v", got)
	}
}

type savedViewStoreStub struct {
	views map[string]SavedView
	err   error
}

func newSavedViewStoreStub() *savedViewStoreStub {
	return &savedViewStoreStub{views: make(map[string]SavedView)}
}

func (s *savedViewStoreStub) ListSavedViews(context.Context) ([]SavedView, error) {
	if s.err != nil {
		return nil, s.err
	}
	views := make([]SavedView, 0, len(s.views))
	for _, view := range s.views {
		views = append(views, view)
	}
	return views, nil
}

func (s *savedViewStoreStub) GetSavedView(_ context.Context, id string) (SavedView, error) {
	view, ok := s.views[id]
	if !ok {
		return SavedView{}, ErrNotFound
	}
	return view, nil
}

func (s *savedViewStoreStub) CreateSavedView(_ context.Context, view SavedView) (SavedView, error) {
	for _, existing := range s.views {
		if existing.Name == view.Name {
			return SavedView{}, ErrConflict
		}
	}
	s.views[view.ID] = view
	return view, nil
}

func (s *savedViewStoreStub) UpdateSavedView(_ context.Context, id string, patch SavedViewPatch) (SavedView, error) {
	view, ok := s.views[id]
	if !ok {
		return SavedView{}, ErrNotFound
	}
	if patch.Name != nil {
		view.Name = *patch.Name
	}
	if patch.Query != nil {
		view.Query = *patch.Query
	}
	if patch.Order != nil {
		view.Order = *patch.Order
	}
	s.views[id] = view
	return view, nil
}

func (s *savedViewStoreStub) DeleteSavedView(_ context.Context, id string) error {
	if _, ok := s.views[id]; !ok {
		return ErrNotFound
	}
	delete(s.views, id)
	return nil
}
