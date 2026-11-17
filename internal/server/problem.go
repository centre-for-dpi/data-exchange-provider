package server

import (
	"encoding/json"
	"net/http"
)

// Problem writes an RFC 9457 problem details answer. title is for people, in the node's language (DEN_LANG); code is
// stable for programs.
func Problem(w http.ResponseWriter, status int, code, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": title, "status": status, "code": code})
}
