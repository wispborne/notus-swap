package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wispborne/notus-swap/internal/llamaconfig"
)

// registerConfig adds the config editor's endpoints.
func (a *API) registerConfig(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/config", a.getConfig)
	mux.HandleFunc("POST /notus/api/config/check", a.checkConfig)
	mux.HandleFunc("PUT /notus/api/config", a.saveConfig)
	mux.HandleFunc("GET /notus/api/config/held", a.heldConfig)
	mux.HandleFunc("POST /notus/api/config/held/now", a.heldConfigNow)
	mux.HandleFunc("DELETE /notus/api/config/held", a.heldConfigCancel)
	mux.HandleFunc("GET /notus/api/config/backups", a.configBackups)
	mux.HandleFunc("GET /notus/api/config/backups/{name}", a.configBackup)
}

func (a *API) configOff(w http.ResponseWriter, err error) bool {
	if errors.Is(err, llamaconfig.ErrOff) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return true
	}
	return false
}

// getConfig: GET /notus/api/config
func (a *API) getConfig(w http.ResponseWriter, r *http.Request) {
	f, err := a.Config.Read()
	if a.configOff(w, err) {
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, f)
}

func readContent(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(v)
}

// checkConfig: POST /notus/api/config/check {"content": "..."}
func (a *API) checkConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := readContent(r, &body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	writeJSON(w, a.Config.Check(r.Context(), body.Content))
}

// saveConfig: PUT /notus/api/config
//
//	{"content": "...", "base_hash": "...", "skip_llama_swap": false, "overwrite": false}
//
// 200 {check, file} when saved. 202 {check, held} when validated and waiting
// for requests to finish. 409 {error, file} when the file changed since
// base_hash; file is the current version. 422 {error, check} on validation failure.
func (a *API) saveConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content       string `json:"content"`
		BaseHash      string `json:"base_hash"`
		SkipLlamaSwap bool   `json:"skip_llama_swap"`
		Overwrite     bool   `json:"overwrite"`
	}
	if err := readContent(r, &body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	// Config reloads can interrupt streams; validate now and write when idle.
	wait := a.ConfigHeld != nil && a.Hub.Count() > 0
	save := a.Config.Save
	if wait {
		save = a.Config.CheckSave
	}
	check, f, err := save(r.Context(), body.Content, body.BaseHash, body.SkipLlamaSwap, body.Overwrite)
	if a.configOff(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case errors.Is(err, llamaconfig.ErrChanged):
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "file": f})
	case err != nil && f == nil && (!check.YAMLOK || !check.LlamaSwapOK || len(check.RoutingProblems) > 0):
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "check": check})
	case err != nil:
		a.fail(w, err)
	case wait:
		id := a.ConfigHeld.Hold(llamaconfig.SaveArgs{Content: body.Content, BaseHash: body.BaseHash, SkipLlamaSwap: body.SkipLlamaSwap, Overwrite: body.Overwrite})
		a.Log.Info("llama-swap config save waiting for requests to finish", "in_flight", a.Hub.Count())
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"check": check, "held": id})
	default:
		a.Log.Info("llama-swap config saved", "path", f.Path, "skipped_llama_swap_check", body.SkipLlamaSwap && !check.LlamaSwapOK)
		json.NewEncoder(w).Encode(map[string]any{"check": check, "file": f})
	}
}

// heldConfig: GET /notus/api/config/held
//
//	{"waiting": {id, since, writing} | null, "last": {id, at, check, file, error} | null, "in_flight": 2}
func (a *API) heldConfig(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"waiting": nil, "last": nil, "in_flight": a.Hub.Count()}
	if a.ConfigHeld != nil {
		out["waiting"], out["last"] = a.ConfigHeld.Status()
	}
	writeJSON(w, out)
}

// heldConfigNow: POST /notus/api/config/held/now starts the save immediately.
func (a *API) heldConfigNow(w http.ResponseWriter, r *http.Request) {
	if a.ConfigHeld == nil || !a.ConfigHeld.Now() {
		http.Error(w, "No save is waiting", http.StatusNotFound)
		return
	}
	a.Log.Info("held llama-swap config save written without waiting, from the web UI", "in_flight", a.Hub.Count())
	w.WriteHeader(http.StatusNoContent)
}

// heldConfigCancel: DELETE /notus/api/config/held drops the held save.
func (a *API) heldConfigCancel(w http.ResponseWriter, r *http.Request) {
	if a.ConfigHeld == nil || !a.ConfigHeld.Cancel() {
		http.Error(w, "No save is waiting, or it is being written", http.StatusConflict)
		return
	}
	a.Log.Info("held llama-swap config save cancelled")
	w.WriteHeader(http.StatusNoContent)
}

// configBackups: GET /notus/api/config/backups
func (a *API) configBackups(w http.ResponseWriter, r *http.Request) {
	list, err := a.Config.Backups()
	if a.configOff(w, err) {
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, list)
}

// configBackup: GET /notus/api/config/backups/{name}
func (a *API) configBackup(w http.ResponseWriter, r *http.Request) {
	content, err := a.Config.ReadBackup(r.PathValue("name"))
	if a.configOff(w, err) {
		return
	}
	if err != nil {
		http.Error(w, "Backup not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"content": content})
}
