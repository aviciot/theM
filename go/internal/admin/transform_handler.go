package admin

import (
	"encoding/json"
	"net/http"

	"github.com/aviciot/them/internal/agentgen/transform"
)

// TransformHandler serves the three transform API endpoints:
//
//	GET  /admin/transform-functions  — full self-describing function catalog
//	POST /admin/transform-test       — run a function chain, return step-by-step trace
//	POST /admin/transform-assist     — AI chain suggestion (stub, 501 for now)
type TransformHandler struct{}

// catalogResponse is the shape returned by GET /admin/transform-functions.
type catalogResponse struct {
	Functions []transform.FunctionDef            `json:"functions"`
	ByCategory map[transform.Category][]transform.FunctionDef `json:"by_category"`
}

func (h TransformHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	resp := catalogResponse{
		Functions:  transform.Catalog(),
		ByCategory: transform.CatalogByCategory(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// transformTestRequest is the POST /admin/transform-test body.
type transformTestRequest struct {
	Functions []transform.FunctionStep `json:"functions"`
	Vars      map[string]any           `json:"vars"`
}

func (h TransformHandler) Test(w http.ResponseWriter, r *http.Request) {
	var req transformTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if len(req.Functions) == 0 {
		http.Error(w, "functions list is required", http.StatusBadRequest)
		return
	}

	// Validate all function names exist before executing.
	if err := transform.Validate(req.Functions); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	vars := transform.Vars(req.Vars)
	if vars == nil {
		vars = transform.Vars{}
	}

	trace, err := transform.Execute(req.Functions, vars)
	// Always return the trace — even on error the partial trace is useful.
	// The error is surfaced in the last step's Error field.
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	json.NewEncoder(w).Encode(trace) //nolint:errcheck
}

// Assist is a placeholder for the AI chain suggestion endpoint.
// Returns 501 until the AI assistant is implemented.
func (h TransformHandler) Assist(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "transform-assist: AI assistant not yet implemented", http.StatusNotImplemented)
}
