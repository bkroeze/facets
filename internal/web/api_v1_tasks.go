package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"facets.barnlab.dev/internal/project"
)

type apiV1IdempotentTask struct {
	Fingerprint string
	Task        project.Task
}

func (s *server) apiV1Tasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.respondAPIError(w, r, 405, "method_not_allowed", "This endpoint accepts only GET and POST requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}
	projectID := r.PathValue("project_id")
	if r.Method == http.MethodGet {
		tasks, err := s.listAPITasks(r, projectID)
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		s.writeAPIJSON(w, r, 200, apiV1TasksResponse{Tasks: apiV1TasksFromDomain(tasks)})
		return
	}
	var req apiV1CreateTaskRequest
	if err := decodeAPIJSON(r, &req); err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	task, err := s.createAPITask(r, projectID, project.TaskInput{Title: req.Title, Description: req.Description, Priority: req.Priority, Assignee: req.Assignee, IdempotencyKey: req.IdempotencyKey})
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", apiV1Prefix+"/projects/"+url.PathEscape(projectID)+"/tasks/"+url.PathEscape(task.ID))
	s.writeAPIJSON(w, r, 201, apiV1TaskFromDomain(task))
}

func (s *server) listAPITasks(r *http.Request, projectID string) ([]project.Task, error) {
	q := r.URL.Query()
	views, vset := q["view"]
	statuses, sset := q["status"]
	if vset && sset {
		return nil, fmt.Errorf("%w: view and status selectors are mutually exclusive", project.ErrInvalid)
	}
	if vset {
		if len(views) != 1 || strings.TrimSpace(views[0]) == "" {
			return nil, fmt.Errorf("%w: view selector must be supplied once", project.ErrInvalid)
		}
		tasks, err := s.service.ExecuteSavedView(r.Context(), projectID, strings.TrimSpace(views[0]))
		if err != nil {
			return nil, err
		}
		return validateAPITaskIdentity(projectID, tasks)
	}
	var f project.TaskFilter
	if sset {
		if len(statuses) != 1 {
			return nil, fmt.Errorf("%w: status selector must be supplied once", project.ErrInvalid)
		}
		switch strings.TrimSpace(statuses[0]) {
		case "all":
		case string(project.StatusOpen), string(project.StatusClosed):
			st := project.Status(strings.TrimSpace(statuses[0]))
			f.Status = &st
		default:
			return nil, fmt.Errorf("%w: unsupported task status", project.ErrInvalid)
		}
	}
	tasks, err := s.service.ListTasks(r.Context(), projectID, f)
	if err != nil {
		return nil, err
	}
	return validateAPITaskIdentity(projectID, tasks)
}

func (s *server) createAPITask(r *http.Request, projectID string, input project.TaskInput) (project.Task, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		task, err := s.service.CreateTask(r.Context(), projectID, input)
		if err != nil {
			return project.Task{}, err
		}
		return validateAPITask(projectID, task.ID, task)
	}
	fpb, err := json.Marshal(struct {
		ProjectID, Title, Description, Assignee string
		Priority                                *int
	}{projectID, strings.TrimSpace(input.Title), input.Description, strings.TrimSpace(input.Assignee), input.Priority})
	if err != nil {
		return project.Task{}, err
	}
	fp := string(fpb)
	s.taskCreateMu.Lock()
	defer s.taskCreateMu.Unlock()
	mapKey := projectID + "\x00" + key
	if old, ok := s.taskCreates[mapKey]; ok {
		if old.Fingerprint != fp {
			return project.Task{}, project.ErrConflict
		}
		return old.Task, nil
	}
	task, err := s.service.CreateTask(r.Context(), projectID, input)
	if err != nil {
		return project.Task{}, err
	}
	task, err = validateAPITask(projectID, task.ID, task)
	if err != nil {
		return project.Task{}, err
	}
	if s.taskCreates == nil {
		s.taskCreates = make(map[string]apiV1IdempotentTask)
	}
	s.taskCreates[mapKey] = apiV1IdempotentTask{fp, task}
	return task, nil
}

func (s *server) apiV1Task(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		s.respondAPIError(w, r, 405, "method_not_allowed", "This endpoint accepts only GET, PATCH, and DELETE requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}
	pid, tid := r.PathValue("project_id"), r.PathValue("task_id")
	switch r.Method {
	case http.MethodGet:
		task, err := s.service.GetTask(r.Context(), pid, tid)
		if err == nil {
			task, err = validateAPITask(pid, tid, task)
		}
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		s.writeAPIJSON(w, r, 200, apiV1TaskFromDomain(task))
	case http.MethodPatch:
		var req apiV1UpdateTaskRequest
		if err := decodeAPIJSON(r, &req); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		patch, err := req.taskPatch()
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		task, err := s.service.UpdateTask(r.Context(), pid, tid, patch)
		if err == nil {
			task, err = validateAPITask(pid, tid, task)
		}
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		s.writeAPIJSON(w, r, 200, apiV1TaskFromDomain(task))
	case http.MethodDelete:
		var req apiV1DeleteTaskRequest
		if err := decodeAPIJSON(r, &req); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		if req.Confirm != tid {
			s.respondAPIServiceError(w, r, fmt.Errorf("%w: delete confirmation must match task ID", project.ErrInvalid))
			return
		}
		if err := s.service.DeleteTask(r.Context(), pid, tid); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) apiV1CommentTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.respondAPIError(w, r, 405, "method_not_allowed", "This endpoint accepts only POST requests.", nil, nil)
		return
	}
	s.apiV1TaskMutation(w, r, func(r *http.Request, pid, tid string) (project.Task, error) {
		var req apiV1CommentTaskRequest
		if err := decodeAPIJSON(r, &req); err != nil {
			return project.Task{}, err
		}
		return s.service.CommentTask(r.Context(), pid, tid, req.Body)
	})
}
func (s *server) apiV1CloseTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.respondAPIError(w, r, 405, "method_not_allowed", "This endpoint accepts only POST requests.", nil, nil)
		return
	}
	s.apiV1TaskMutation(w, r, func(r *http.Request, pid, tid string) (project.Task, error) {
		var req apiV1CloseTaskRequest
		if err := decodeAPIJSON(r, &req); err != nil {
			return project.Task{}, err
		}
		return s.service.CloseTask(r.Context(), pid, tid, project.Completion{Message: req.Message, Evidence: req.Evidence, Comment: req.Comment})
	})
}
func (s *server) apiV1ReopenTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.respondAPIError(w, r, 405, "method_not_allowed", "This endpoint accepts only POST requests.", nil, nil)
		return
	}
	s.apiV1TaskMutation(w, r, func(r *http.Request, pid, tid string) (project.Task, error) {
		return s.service.ReopenTask(r.Context(), pid, tid)
	})
}
func (s *server) apiV1TaskMutation(w http.ResponseWriter, r *http.Request, mutate func(*http.Request, string, string) (project.Task, error)) {
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}
	pid, tid := r.PathValue("project_id"), r.PathValue("task_id")
	task, err := mutate(r, pid, tid)
	if err == nil {
		task, err = validateAPITask(pid, tid, task)
	}
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	s.writeAPIJSON(w, r, 200, apiV1TaskFromDomain(task))
}

func (req apiV1UpdateTaskRequest) taskPatch() (project.TaskPatch, error) {
	var p project.TaskPatch
	if req.Title.Set {
		if req.Title.Null {
			return p, project.ErrInvalid
		}
		p.Title = &req.Title.Value
	}
	if req.Description.Set {
		if req.Description.Null {
			return p, project.ErrInvalid
		}
		p.Description = &req.Description.Value
	}
	if req.Priority.Set {
		p.Priority.Set = true
		if !req.Priority.Null {
			p.Priority.Value = &req.Priority.Value
		}
	}
	if req.Assignee.Set {
		if req.Assignee.Null {
			return p, project.ErrInvalid
		}
		p.Assignee = &req.Assignee.Value
	}
	return p, nil
}
func validateAPITask(pid, tid string, t project.Task) (project.Task, error) {
	if t.ProjectID != pid || (tid != "" && t.ID != tid) {
		return project.Task{}, errors.Join(project.ErrNotFound, fmt.Errorf("task identity does not match request path"))
	}
	return t, nil
}
func validateAPITaskIdentity(pid string, tasks []project.Task) ([]project.Task, error) {
	for _, t := range tasks {
		if t.ProjectID != pid {
			return nil, errors.Join(project.ErrNotFound, fmt.Errorf("task project identity does not match request path"))
		}
	}
	return tasks, nil
}
