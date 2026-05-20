package server

import (
	"net/http"
	"strconv"
)

// projectIDFromQuery reads ?project=N from the request, returning 0 if
// missing or invalid (which most handlers treat as "all projects").
func projectIDFromQuery(r *http.Request) int64 {
	v := r.URL.Query().Get("project")
	if v == "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// ---- read-only listings for the v0.2 derived data tables ----

func (s *Server) handleListScrapedPages(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListScrapedPages(r.Context(), 200)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListActions(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	if id == 0 {
		id, _ = s.st.LatestProjectID(r.Context())
	}
	rows, err := s.st.ListActions(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListBriefs(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	if id == 0 {
		id, _ = s.st.LatestProjectID(r.Context())
	}
	rows, err := s.st.ListContentBriefs(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	if id == 0 {
		id, _ = s.st.LatestProjectID(r.Context())
	}
	rows, err := s.st.ListPromptSuggestions(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	rows, err := s.st.ListSchedules(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListWatchers(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	rows, err := s.st.ListWatchers(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handleListSimulations(w http.ResponseWriter, r *http.Request) {
	id := projectIDFromQuery(r)
	if id == 0 {
		id, _ = s.st.LatestProjectID(r.Context())
	}
	rows, err := s.st.ListSimulations(r.Context(), id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, rows)
}
