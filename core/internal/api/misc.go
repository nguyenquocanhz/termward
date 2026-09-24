package api

import (
	"net/http"

	"github.com/nguyenquocanhz/termward/core/internal/store"
)

func (s *Server) listSnippets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snippets())
}

func (s *Server) saveSnippet(w http.ResponseWriter, r *http.Request) {
	var in store.Snippet
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	sn, err := s.store.SaveSnippet(in)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) deleteSnippet(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSnippet(r.PathValue("id")); err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Settings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if !decode(w, r, &in) {
		return
	}
	v, err := s.store.SaveSettings(in)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	s.monitor.Wake()
	writeJSON(w, http.StatusOK, v)
}
