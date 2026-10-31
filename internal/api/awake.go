package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wispborne/notus-swap/internal/awake"
)

const keepAwakeKey = "keep_awake"

func (a *API) registerKeepAwake(r *router) {
	r.handle("GET /notus/api/keep-awake", "Whether this computer is kept from sleeping while requests are in flight (Windows only), and whether it is now.", a.getKeepAwake)
	r.handle("PUT /notus/api/keep-awake", `Turn keeping awake on or off. Body: {"enabled": true | false}.`, a.putKeepAwake)
}

// KeepAwakeSetting reads the keep-awake setting. It is off until turned on.
func (a *API) KeepAwakeSetting(ctx context.Context) bool {
	v, err := a.Store.Setting(ctx, keepAwakeKey)
	if err != nil {
		a.Log.Error("reading the keep-awake setting", "err", err)
	}
	return v == "true"
}

// getKeepAwake: GET /notus/api/keep-awake
func (a *API) getKeepAwake(w http.ResponseWriter, r *http.Request) {
	if a.Awake == nil {
		writeJSON(w, awake.State{})
		return
	}
	writeJSON(w, a.Awake.State())
}

// putKeepAwake: PUT /notus/api/keep-awake with {"enabled": bool}.
func (a *API) putKeepAwake(w http.ResponseWriter, r *http.Request) {
	var s struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&s); err != nil || s.Enabled == nil {
		http.Error(w, `Expected {"enabled": true} or {"enabled": false}`, http.StatusBadRequest)
		return
	}
	v := "true"
	if !*s.Enabled {
		v = "false"
	}
	if err := a.Store.SetSetting(r.Context(), keepAwakeKey, v); err != nil {
		a.fail(w, err)
		return
	}
	if a.Awake != nil {
		a.Awake.SetEnabled(*s.Enabled)
	}
	a.Log.Info("keep-awake setting saved", "enabled", *s.Enabled)
	a.getKeepAwake(w, r)
}
