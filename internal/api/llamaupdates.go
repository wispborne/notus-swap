package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaupdate"
)

func (a *API) registerLlamaUpdates(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/llama-updates", a.llamaUpdates)
	mux.HandleFunc("POST /notus/api/llama-updates/check", a.checkLlamaUpdates)
	mux.HandleFunc("GET /notus/api/llama-updates/{component}/changelog", a.llamaChangelog)
	mux.HandleFunc("POST /notus/api/llama-updates/{component}/{action}", a.startLlamaUpdate)
}

// llamaUpdates returns installed versions, the last check, and install status
// without contacting GitHub.
func (a *API) llamaUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.LlamaUpdates.Status())
}

// checkLlamaUpdates: POST /notus/api/llama-updates/check. Asks GitHub now.
func (a *API) checkLlamaUpdates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	a.LlamaUpdates.Check(ctx)
	writeJSON(w, a.LlamaUpdates.Status())
}

// llamaChangelog: GET /notus/api/llama-updates/{llama-swap|llama.cpp}/changelog.
// Recent releases with their notes, newest first.
func (a *API) llamaChangelog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	rs, err := a.LlamaUpdates.Changelog(ctx, r.PathValue("component"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, rs)
}

// startLlamaUpdate: POST /notus/api/llama-updates/{llama-swap|llama.cpp}/{install|rollback}
// with {"tag": "..."}. An empty tag installs the newest release. The work
// runs in the background; GET /notus/api/llama-updates follows it.
func (a *API) startLlamaUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag string `json:"tag"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	err := a.LlamaUpdates.Start(r.PathValue("component"), r.PathValue("action"), body.Tag)
	switch {
	case errors.Is(err, llamaupdate.ErrBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}
