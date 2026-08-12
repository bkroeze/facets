package web

import (
	"net/http"

	"facets.barnlab.dev/internal/project"
)

type apiV1ProjectsResponse struct {
	Projects []apiV1Project `json:"projects"`
}

func (s *server) apiV1Projects(w http.ResponseWriter, r *http.Request) {
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

	projects, err := s.service.ListProjects(r.Context())
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	response := apiV1ProjectsResponse{Projects: make([]apiV1Project, len(projects))}
	for i, item := range projects {
		active, err := s.service.ExecuteSavedView(r.Context(), item.ID, project.ActiveViewID)
		if err != nil {
			s.respondAPIServiceError(w, r, err)
			return
		}
		response.Projects[i] = apiV1ProjectFromDomainWithActiveCount(item, len(active))
	}
	s.writeAPIJSON(w, r, http.StatusOK, response)
}

func (s *server) apiV1Project(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		s.respondAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint accepts only GET requests.", nil, nil)
		return
	}
	if projectID := r.PathValue("project_id"); projectID == "." || projectID == ".." || projectID == "" {
		s.respondAPIError(w, r, http.StatusNotFound, "not_found", "The requested API resource does not exist.", nil, nil)
		return
	}
	if s.service == nil {
		s.respondAPIServiceError(w, r, project.ErrProviderNotFound)
		return
	}
	if !s.acceptsAPIResponse(w, r) {
		return
	}

	item, err := s.service.GetProject(r.Context(), r.PathValue("project_id"))
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	active, err := s.service.ExecuteSavedView(r.Context(), item.ID, project.ActiveViewID)
	if err != nil {
		s.respondAPIServiceError(w, r, err)
		return
	}
	s.writeAPIJSON(w, r, http.StatusOK, apiV1ProjectFromDomainWithActiveCount(item, len(active)))
}

func apiV1ProjectFromDomainWithActiveCount(item project.Project, activeTaskCount int) apiV1Project {
	result := apiV1ProjectFromDomain(item)
	result.ActiveTaskCount = activeTaskCount
	return result
}
