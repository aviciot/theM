package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aviciot/them-test-runner/scenario"
	"github.com/go-chi/chi/v5"
)

type RunHandler struct {
	scenarios *scenario.Store
	manager   *scenario.Manager
}

func NewRunHandler(store *scenario.Store, mgr *scenario.Manager) *RunHandler {
	return &RunHandler{scenarios: store, manager: mgr}
}

func (h *RunHandler) Start(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ScenarioID string   `json:"scenario_id"`
		NUsers     *int     `json:"n_users"`
		Messages   []string `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}

	sc, err := h.scenarios.Get(body.ScenarioID)
	if err != nil {
		writeError(w, 404, "scenario not found")
		return
	}
	if body.NUsers != nil {
		sc.NUsers = *body.NUsers
	}
	if len(body.Messages) > 0 {
		sc.Messages = body.Messages
	}
	if sc.NUsers < 1 {
		sc.NUsers = 1
	}

	client, err := newClient()
	if err != nil {
		writeError(w, 502, "cannot connect to the-M: "+err.Error())
		return
	}

	runID, err := h.manager.Start(*sc, client)
	if err != nil {
		writeError(w, 500, "failed to start run")
		return
	}
	writeJSON(w, 202, map[string]string{"run_id": runID})
}

func (h *RunHandler) Stream(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")

	ch, unsub, ok := h.manager.Subscribe(runID)
	if !ok {
		// Run may have already completed — check history.
		summary, err := h.manager.GetHistory(runID)
		if err != nil {
			writeError(w, 404, "run not found")
			return
		}
		// Stream the completed summary immediately.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		b, _ := json.Marshal(scenario.RunEvent{Type: "run_complete", Summary: summary})
		fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if canFlush {
				flusher.Flush()
			}
			if ev.Type == "run_complete" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *RunHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	h.manager.Cancel(runID)
	w.WriteHeader(204)
}

func (h *RunHandler) ListHistory(w http.ResponseWriter, r *http.Request) {
	list, _ := h.manager.ListHistory()
	writeJSON(w, 200, list)
}

func (h *RunHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	s, err := h.manager.GetHistory(runID)
	if err != nil {
		writeError(w, 404, "run not found")
		return
	}
	writeJSON(w, 200, s)
}

func (h *RunHandler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	if err := h.manager.DeleteHistory(runID); err != nil {
		writeError(w, 404, "run not found")
		return
	}
	w.WriteHeader(204)
}
