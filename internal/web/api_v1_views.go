package web

import (
	"net/http"
	"net/url"

	"facets.barnlab.dev/internal/project"
)

type apiV1SavedViewsResponse struct {
	Views []apiV1SavedView `json:"views"`
}

type apiV1TasksResponse struct {
	Tasks []apiV1Task `json:"tasks"`
}

type apiV1CreateSavedViewRequest struct {
	Name  string         `json:"name"`
	Query apiV1TaskQuery `json:"query"`
	Order apiV1TaskOrder `json:"order"`
}

type apiV1UpdateSavedViewRequest struct {
	Name  apiV1Optional[string]         `json:"name"`
	Query apiV1Optional[apiV1TaskQuery] `json:"query"`
	Order apiV1Optional[apiV1TaskOrder] `json:"order"`
}

func (s *server) apiV1Views(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET and POST requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !s.acceptsAPIResponse(w, r) {
			return
		}
		views, err := s.service.ListSavedViews(r.Context())
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		response := apiV1SavedViewsResponse{Views: make([]apiV1SavedView, len(views))}
		for i, view := range views {
			response.Views[i] = apiV1SavedViewFromDomain(view)
		}
		s.writeAPIJSON(w, r, http.StatusOK, response)
	case http.MethodPost:
		if !s.acceptsAPIResponse(w, r) {
			return
		}
		var request apiV1CreateSavedViewRequest
		if err := decodeAPIJSON(r, &request); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		created, err := s.service.CreateSavedView(r.Context(), project.SavedViewInput{
			Name: request.Name, Query: request.Query.domain(), Order: request.Order.domain(),
		})
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		w.Header().Set("Location", apiV1Prefix+"/views/"+url.PathEscape(created.ID))
		s.writeAPIJSON(w, r, http.StatusCreated, apiV1SavedViewFromDomain(created))
	}
}

func (s *server) apiV1View(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET, PATCH, and DELETE requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	id := r.PathValue("view_id")
	switch r.Method {
	case http.MethodGet:
		if !s.acceptsAPIResponse(w, r) {
			return
		}
		view, err := s.service.GetSavedView(r.Context(), id)
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		s.writeAPIJSON(w, r, http.StatusOK, apiV1SavedViewFromDomain(view))
	case http.MethodPatch:
		if !s.acceptsAPIResponse(w, r) {
			return
		}
		var request apiV1UpdateSavedViewRequest
		if err := decodeAPIJSON(r, &request); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		patch, err := request.domain()
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		view, err := s.service.UpdateSavedView(r.Context(), id, patch)
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		s.writeAPIJSON(w, r, http.StatusOK, apiV1SavedViewFromDomain(view))
	case http.MethodDelete:
		if !s.acceptsAPIResponse(w, r) {
			return
		}
		if err := s.service.DeleteSavedView(r.Context(), id); err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) apiV1ExecuteView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET requests.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}
	tasks, err := s.service.ExecuteSavedView(r.Context(), r.PathValue("project_id"), r.PathValue("view_id"))
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	s.writeAPIJSON(w, r, http.StatusOK, apiV1TasksResponse{Tasks: apiV1TasksFromDomain(tasks)})
}

func (s *server) acceptsAPIResponse(w http.ResponseWriter, r *http.Request) bool {
	if acceptsAPIJSON(r.Header.Get("Accept")) {
		return true
	}
	s.respondAPIError(w, r, http.StatusNotAcceptable, "not_acceptable", "The requested response media type is not available; accept application/json.", nil, nil)
	return false
}

func apiV1SavedViewFromDomain(view project.SavedView) apiV1SavedView {
	return apiV1SavedView{
		ID: view.ID, Name: view.Name, Builtin: view.Builtin,
		Query: apiV1TaskQuery{
			Statuses:   append([]project.Status(nil), view.Query.Statuses...),
			Assignees:  append([]string(nil), view.Query.Assignees...),
			Priorities: append([]int(nil), view.Query.Priorities...),
		},
		Order:     apiV1TaskOrder{Field: view.Order.Field, Direction: view.Order.Direction},
		CreatedAt: apiV1Time(view.CreatedAt), UpdatedAt: apiV1Time(view.UpdatedAt),
	}
}

func (query apiV1TaskQuery) domain() project.TaskQuery {
	return project.TaskQuery{
		Statuses:   append([]project.Status(nil), query.Statuses...),
		Assignees:  append([]string(nil), query.Assignees...),
		Priorities: append([]int(nil), query.Priorities...),
	}
}

func (order apiV1TaskOrder) domain() project.TaskOrder {
	return project.TaskOrder{Field: order.Field, Direction: order.Direction}
}

func (request apiV1UpdateSavedViewRequest) domain() (project.SavedViewPatch, error) {
	var patch project.SavedViewPatch
	if request.Name.Set {
		if request.Name.Null {
			return project.SavedViewPatch{}, project.ErrInvalid
		}
		patch.Name = &request.Name.Value
	}
	if request.Query.Set {
		if request.Query.Null {
			return project.SavedViewPatch{}, project.ErrInvalid
		}
		query := request.Query.Value.domain()
		patch.Query = &query
	}
	if request.Order.Set {
		if request.Order.Null {
			return project.SavedViewPatch{}, project.ErrInvalid
		}
		order := request.Order.Value.domain()
		patch.Order = &order
	}
	return patch, nil
}
