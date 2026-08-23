package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	ActiveViewID   = "active"
	ActiveViewName = "Active"
)

// ActiveSavedView returns the built-in, non-persisted open-task view.
func ActiveSavedView() SavedView {
	return SavedView{
		ID: ActiveViewID, Name: ActiveViewName, Builtin: true,
		Query: TaskQuery{Statuses: []Status{StatusOpen}, Assignees: []string{}, Priorities: []int{}},
		Order: TaskOrder{Field: TaskOrderUpdatedAt, Direction: TaskOrderDescending},
	}
}

// ListSavedViews returns the active view followed by persisted views in stable
// name and ID order.
func (s *Service) ListSavedViews(ctx context.Context) ([]SavedView, error) {
	views := []SavedView{ActiveSavedView()}
	if s.viewStore == nil {
		return views, nil
	}
	persisted, err := s.viewStore.ListSavedViews(ctx)
	if err != nil {
		return nil, savedViewStoreError(err)
	}
	for i := range persisted {
		persisted[i].Builtin = false
	}
	sort.SliceStable(persisted, func(i, j int) bool {
		left, right := strings.ToLower(persisted[i].Name), strings.ToLower(persisted[j].Name)
		if left == right {
			return persisted[i].ID < persisted[j].ID
		}
		return left < right
	})
	return append(views, persisted...), nil
}

// GetSavedView returns one built-in or persisted view.
func (s *Service) GetSavedView(ctx context.Context, id string) (SavedView, error) {
	id, err := requiredID("saved view ID", id)
	if err != nil {
		return SavedView{}, err
	}
	if id == ActiveViewID {
		return ActiveSavedView(), nil
	}
	if s.viewStore == nil {
		return SavedView{}, ErrNotFound
	}
	view, err := s.viewStore.GetSavedView(ctx, id)
	if err != nil {
		return SavedView{}, savedViewStoreError(err)
	}
	view.Builtin = false
	return view, nil
}

// CreateSavedView validates and persists a user-defined view.
func (s *Service) CreateSavedView(ctx context.Context, input SavedViewInput) (SavedView, error) {
	if s.viewStore == nil {
		return SavedView{}, ErrUnsupported
	}
	clean, err := cleanSavedViewInput(input)
	if err != nil {
		return SavedView{}, err
	}
	id, err := newSavedViewID()
	if err != nil {
		return SavedView{}, err
	}
	now := time.Now().UTC()
	view, err := s.viewStore.CreateSavedView(ctx, SavedView{
		ID: id, Name: clean.Name, Query: clean.Query, Order: clean.Order,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return SavedView{}, savedViewStoreError(err)
	}
	return view, nil
}

// UpdateSavedView validates and updates a user-defined view.
func (s *Service) UpdateSavedView(ctx context.Context, id string, patch SavedViewPatch) (SavedView, error) {
	id, err := requiredID("saved view ID", id)
	if err != nil {
		return SavedView{}, err
	}
	if id == ActiveViewID {
		return SavedView{}, ErrConflict
	}
	if s.viewStore == nil {
		return SavedView{}, ErrUnsupported
	}
	if patch.Name == nil && patch.Query == nil && patch.Order == nil {
		return SavedView{}, invalid("saved view patch must contain a change")
	}
	if patch.Name != nil {
		name, err := cleanSavedViewName(*patch.Name)
		if err != nil {
			return SavedView{}, err
		}
		patch.Name = &name
	}
	if patch.Query != nil {
		query, err := cleanTaskQuery(*patch.Query)
		if err != nil {
			return SavedView{}, err
		}
		patch.Query = &query
	}
	if patch.Order != nil {
		order, err := cleanTaskOrder(*patch.Order)
		if err != nil {
			return SavedView{}, err
		}
		patch.Order = &order
	}
	view, err := s.viewStore.UpdateSavedView(ctx, id, patch)
	if err != nil {
		return SavedView{}, savedViewStoreError(err)
	}
	view.Builtin = false
	return view, nil
}

// DeleteSavedView removes one user-defined view.
func (s *Service) DeleteSavedView(ctx context.Context, id string) error {
	id, err := requiredID("saved view ID", id)
	if err != nil {
		return err
	}
	if id == ActiveViewID {
		return ErrConflict
	}
	if s.viewStore == nil {
		return ErrUnsupported
	}
	if err := s.viewStore.DeleteSavedView(ctx, id); err != nil {
		return savedViewStoreError(err)
	}
	return nil
}

// ExecuteSavedView lists tasks selected and ordered by a saved view.
func (s *Service) ExecuteSavedView(ctx context.Context, projectID, viewID string) ([]Task, error) {
	projectID, err := requiredID("project ID", projectID)
	if err != nil {
		return nil, err
	}

	view, err := s.GetSavedView(ctx, viewID)
	if err != nil {
		return nil, err
	}

	filter := TaskFilter{}
	if len(view.Query.Statuses) == 1 {
		status := view.Query.Statuses[0]
		filter.Status = &status
	}
	tasks, err := s.provider.ListTasks(ctx, projectID, filter)
	if err != nil {
		return nil, err
	}
	selected := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if matchesTaskQuery(task, view.Query) {
			selected = append(selected, task)
		}
	}
	sortTasks(selected, view.Order)
	return selected, nil
}
func savedViewStoreError(err error) error {
	switch {
	case errors.Is(err, ErrInvalid),
		errors.Is(err, ErrConflict),
		errors.Is(err, ErrNotFound),
		errors.Is(err, ErrUnsupported),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrStorage, err)
	}
}

func cleanSavedViewInput(input SavedViewInput) (SavedViewInput, error) {
	name, err := cleanSavedViewName(input.Name)
	if err != nil {
		return SavedViewInput{}, err
	}
	query, err := cleanTaskQuery(input.Query)
	if err != nil {
		return SavedViewInput{}, err
	}
	order, err := cleanTaskOrder(input.Order)
	if err != nil {
		return SavedViewInput{}, err
	}
	return SavedViewInput{Name: name, Query: query, Order: order}, nil
}

func cleanSavedViewName(name string) (string, error) {
	name, err := requiredValue("saved view name", name)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(name, ActiveViewName) {
		return "", ErrConflict
	}
	return name, nil
}

func cleanTaskQuery(query TaskQuery) (TaskQuery, error) {
	statuses := make([]Status, 0, len(query.Statuses))
	seenStatus := make(map[Status]bool, len(query.Statuses))
	for _, status := range query.Statuses {
		if err := validStatus(status); err != nil {
			return TaskQuery{}, err
		}
		if !seenStatus[status] {
			seenStatus[status] = true
			statuses = append(statuses, status)
		}
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i] < statuses[j] })

	assignees := make([]string, 0, len(query.Assignees))
	seenAssignee := make(map[string]bool, len(query.Assignees))
	for _, assignee := range query.Assignees {
		assignee = strings.TrimSpace(assignee)
		if assignee == "" {
			return TaskQuery{}, invalid("saved view assignees must not contain empty values")
		}
		if !seenAssignee[assignee] {
			seenAssignee[assignee] = true
			assignees = append(assignees, assignee)
		}
	}
	sort.Strings(assignees)

	priorities := make([]int, 0, len(query.Priorities))
	seenPriority := make(map[int]bool, len(query.Priorities))
	for _, priority := range query.Priorities {
		if err := validPriority(&priority); err != nil {
			return TaskQuery{}, err
		}
		if !seenPriority[priority] {
			seenPriority[priority] = true
			priorities = append(priorities, priority)
		}
	}
	sort.Ints(priorities)
	return TaskQuery{Statuses: statuses, Assignees: assignees, Priorities: priorities}, nil
}

func cleanTaskOrder(order TaskOrder) (TaskOrder, error) {
	if order.Field == "" && order.Direction == "" {
		return TaskOrder{Field: TaskOrderUpdatedAt, Direction: TaskOrderDescending}, nil
	}
	switch order.Field {
	case TaskOrderTitle, TaskOrderStatus, TaskOrderPriority, TaskOrderAssignee, TaskOrderCreatedAt, TaskOrderUpdatedAt:
	default:
		return TaskOrder{}, invalid(fmt.Sprintf("unsupported task order field %q", order.Field))
	}
	if order.Direction != TaskOrderAscending && order.Direction != TaskOrderDescending {
		return TaskOrder{}, invalid(fmt.Sprintf("unsupported task order direction %q", order.Direction))
	}
	return order, nil
}

func matchesTaskQuery(task Task, query TaskQuery) bool {
	if len(query.Statuses) > 0 && !slices.Contains(query.Statuses, task.Status) {
		return false
	}
	if len(query.Assignees) > 0 && !slices.Contains(query.Assignees, task.Assignee) {
		return false
	}
	if len(query.Priorities) > 0 && (task.Priority == nil || !slices.Contains(query.Priorities, *task.Priority)) {
		return false
	}
	return true
}

func sortTasks(tasks []Task, order TaskOrder) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if order.Field == TaskOrderCreatedAt || order.Field == TaskOrderUpdatedAt {
			left, right := taskOrderTime(tasks[i], order.Field), taskOrderTime(tasks[j], order.Field)
			if left.IsZero() != right.IsZero() {
				return !left.IsZero()
			}
		}
		comparison := compareTasks(tasks[i], tasks[j], order.Field)
		if comparison == 0 {
			return tasks[i].ID < tasks[j].ID
		}
		if order.Direction == TaskOrderDescending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func compareTasks(left, right Task, field string) int {
	switch field {
	case TaskOrderTitle:
		return strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
	case TaskOrderStatus:
		return strings.Compare(string(left.Status), string(right.Status))
	case TaskOrderPriority:
		return comparePriorities(left.Priority, right.Priority)
	case TaskOrderAssignee:
		return strings.Compare(strings.ToLower(left.Assignee), strings.ToLower(right.Assignee))
	case TaskOrderCreatedAt:
		return compareTimes(left.CreatedAt, right.CreatedAt)
	default:
		return compareTimes(left.UpdatedAt, right.UpdatedAt)
	}
}

func taskOrderTime(task Task, field string) time.Time {
	if field == TaskOrderCreatedAt {
		return task.CreatedAt
	}
	return task.UpdatedAt
}

func comparePriorities(left, right *int) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	return *left - *right
}

func compareTimes(left, right time.Time) int {
	if left.Equal(right) {
		return 0
	}
	if left.After(right) {
		return 1
	}
	return -1
}

func newSavedViewID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("project service: generate saved view ID: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}
