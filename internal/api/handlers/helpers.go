package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// parseJSON decodes JSON from the request body into v.
func parseJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// writeJSON serializes v to JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// connectorIDFromURL parses the {id} URL parameter as an integer.
// Returns (0, false) on parse failure.
func connectorIDFromURL(r *http.Request) (int, bool) {
	s := chi.URLParam(r, "id")
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Response is the standard envelope for mutation endpoints.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}
