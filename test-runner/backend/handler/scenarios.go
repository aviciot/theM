package handler

import (
	"encoding/json"
	"net/http"

	"github.com/aviciot/them-test-runner/scenario"
	"github.com/go-chi/chi/v5"
)

type ScenarioHandler struct {
	store *scenario.Store
}

func NewScenarioHandler(store *scenario.Store) *ScenarioHandler {
	return &ScenarioHandler{store: store}
}

func (h *ScenarioHandler) List(w http.ResponseWriter, r *http.Request) {
	list, _ := h.store.List()
	writeJSON(w, 200, list)
}

func (h *ScenarioHandler) Create(w http.ResponseWriter, r *http.Request) {
	var sc scenario.Scenario
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	created, err := h.store.Create(sc)
	if err != nil {
		writeError(w, 500, "failed to save scenario")
		return
	}
	writeJSON(w, 201, created)
}

func (h *ScenarioHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var sc scenario.Scenario
	if err := json.NewDecoder(r.Body).Decode(&sc); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	updated, err := h.store.Update(id, sc)
	if err != nil {
		writeError(w, 500, "failed to update scenario")
		return
	}
	writeJSON(w, 200, updated)
}

func (h *ScenarioHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.store.Delete(id); err != nil {
		writeError(w, 404, "scenario not found")
		return
	}
	w.WriteHeader(204)
}
