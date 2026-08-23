package web

import (
	"net/http"

	"github.com/bresilla/drop/src/pkg/book"
)

// Space is one namespace a peer serves, as the page needs it.
type Space struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Writable bool   `json:"writable"`
}

// spaces answers what a device offers, so the page can show it rather than making someone guess a
// path. A peer that says nothing is one that has not been asked, or an older node.
func (s *Server) spaces(w http.ResponseWriter, r *http.Request) {
	entry, err := book.Resolve(r.PathValue("peer"))
	if err != nil {
		fail(w, err)
		return
	}

	found, err := s.send.Spaces(r.Context(), entry)
	if err != nil {
		fail(w, err)
		return
	}
	if found == nil {
		found = []Space{}
	}
	reply(w, found)
}
