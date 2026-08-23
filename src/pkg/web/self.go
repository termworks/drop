package web

import "net/http"

// Identity is who this page is acting as.
//
// Worth showing plainly: two pages open side by side are two different devices, and without their
// names on screen there is nothing to tell them apart.
type Identity struct {
	Name  string   `json:"name"`
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

func (s *Server) self(w http.ResponseWriter, r *http.Request) {
	who, err := s.send.Self(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if who.Addrs == nil {
		who.Addrs = []string{}
	}
	reply(w, who)
}
