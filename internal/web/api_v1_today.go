package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/store"
)

func (s *server) apiV1Today(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if s.registry == nil {
		s.respondAPIServiceError(w, r, errAPIV1RegistryUnavailable)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}

	now := s.currentTime()
	focus, err := s.registry.CurrentDayFocus(r.Context(), now)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.respondAPIServiceError(w, r, apiV1RegistryError(err))
		return
	}
	topTasks, err := s.listAPITodayTopTasks(r)
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	completion, err := s.completedAPIToday(r, now)
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	response := apiV1TodayResponse{
		TopTasks:       topTasks,
		CompletedToday: completion,
	}
	if focus.Focus != "" {
		response.Focus = &apiV1TodayFocus{Text: focus.Focus, DayStart: focus.DayStart.UTC().Format(time.RFC3339Nano)}
	}
	s.writeAPIJSON(w, r, http.StatusOK, response)
}

func (s *server) apiV1TodayFocus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only POST requests.", nil, nil)
		return
	}
	if s.registry == nil {
		s.respondAPIServiceError(w, r, errAPIV1RegistryUnavailable)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}
	var req apiV1TodayFocusRequest
	if err := decodeAPIJSON(r, &req); err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		s.respondAPIServiceError(w, r, fmt.Errorf("%w: focus text is required", project.ErrInvalid))
		return
	}
	created, err := s.registry.CreateDayFocus(r.Context(), text, s.currentTime())
	if err != nil {
		s.respondAPIServiceError(w, r, apiV1RegistryError(err))
		return
	}
	s.writeAPIJSON(w, r, http.StatusOK, apiV1TodayFocusResponse{Focus: apiV1TodayFocus{Text: created.Focus, DayStart: created.DayStart.UTC().Format(time.RFC3339Nano)}})
}

type apiV1TodayFocusRequest struct {
	Text string `json:"text"`
}

type apiV1TodayFocusResponse struct {
	Focus apiV1TodayFocus `json:"focus"`
}

type apiV1TodayTaskEntry struct {
	updatedAt time.Time
	item      apiV1TodayTask
}

func (s *server) listAPITodayTopTasks(r *http.Request) ([]apiV1TodayTask, error) {
	projects, err := s.service.ListProjects(r.Context())
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	open := project.StatusOpen
	entries := make([]apiV1TodayTaskEntry, 0)
	for _, item := range projects {
		items, err := s.service.ListTasks(r.Context(), item.ID, project.TaskFilter{Status: &open})
		if err != nil {
			return nil, fmt.Errorf("list open tasks for project %q: %w", item.ID, err)
		}
		for _, task := range items {
			task, err = validateAPITodayTask(item.ID, task)
			if err != nil {
				return nil, err
			}
			if task.Status != project.StatusOpen || !apiV1IsTopTask(task) {
				continue
			}
			entries = append(entries, apiV1TodayTaskEntry{
				item:      apiV1TodayTask{Project: item.ID, ProjectName: item.Name, Task: task.ID, Title: task.Title},
				updatedAt: task.UpdatedAt,
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].item.Project != entries[j].item.Project {
			return entries[i].item.Project < entries[j].item.Project
		}
		if entries[i].updatedAt.Equal(entries[j].updatedAt) {
			return entries[i].item.Task < entries[j].item.Task
		}
		if entries[i].updatedAt.IsZero() {
			return false
		}
		if entries[j].updatedAt.IsZero() {
			return true
		}
		return entries[i].updatedAt.After(entries[j].updatedAt)
	})
	tasks := make([]apiV1TodayTask, len(entries))
	for i := range entries {
		tasks[i] = entries[i].item
	}
	return tasks, nil
}

func (s *server) completedAPIToday(r *http.Request, now time.Time) (apiV1TodayCompletion, error) {
	if now.IsZero() {
		return apiV1TodayCompletion{}, fmt.Errorf("%w: current time is required", project.ErrInvalid)
	}
	local := now.In(now.Location())
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)
	projects, err := s.service.ListProjects(r.Context())
	if err != nil {
		return apiV1TodayCompletion{}, fmt.Errorf("list projects: %w", err)
	}
	closed := project.StatusClosed
	result := apiV1TodayCompletion{DayStart: dayStart.UTC().Format(time.RFC3339Nano), DayEnd: dayEnd.UTC().Format(time.RFC3339Nano)}
	for _, item := range projects {
		tasks, err := s.service.ListTasks(r.Context(), item.ID, project.TaskFilter{Status: &closed})
		if err != nil {
			return apiV1TodayCompletion{}, fmt.Errorf("list closed tasks for project %q: %w", item.ID, err)
		}
		for _, task := range tasks {
			task, err = validateAPITodayTask(item.ID, task)
			if err != nil {
				return apiV1TodayCompletion{}, err
			}
			if task.Status != project.StatusClosed || task.UpdatedAt.Before(dayStart) || !task.UpdatedAt.Before(dayEnd) {
				continue
			}
			result.All++
			if apiV1IsTopTask(task) {
				result.Top++
			}
		}
	}
	return result, nil
}

func validateAPITodayTask(projectID string, task project.Task) (project.Task, error) {
	if task.ProjectID == "" {
		task.ProjectID = projectID
	}
	if task.ProjectID != projectID {
		return project.Task{}, errors.Join(project.ErrNotFound, fmt.Errorf("task project identity does not match request project"))
	}
	return task, nil
}

func apiV1IsTopTask(task project.Task) bool {
	value, ok := task.Metadata["facets.top"]
	if !ok {
		return false
	}
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func apiV1RegistryError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.Join(project.ErrStorage, err)
}

func (s *server) currentTime() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}
