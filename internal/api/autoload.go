package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wispborne/notus-swap/internal/autoload"
	"github.com/wispborne/notus-swap/internal/llamaconfig"
)

const defaultModelKey = "default_model"

func (a *API) registerAutoLoad(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/default-model", a.getDefaultModel)
	mux.HandleFunc("PUT /notus/api/default-model", a.putDefaultModel)
}

// DefaultModelSetting reads the default model setting.
func (a *API) DefaultModelSetting(ctx context.Context) autoload.Setting {
	v, err := a.Store.Setting(ctx, defaultModelKey)
	if err != nil {
		a.Log.Error("reading the default model setting", "err", err)
	}
	return autoload.Parse(v)
}

// DefaultModel is what the System page shows about the default model.
type DefaultModel struct {
	autoload.State
	// TTLs is each model's idle unload time in seconds from llama-swap's
	// config, 0 for never. Nil when the config can't be read.
	TTLs map[string]int `json:"ttls"`
}

// getDefaultModel: GET /notus/api/default-model
func (a *API) getDefaultModel(w http.ResponseWriter, r *http.Request) {
	if a.AutoLoad == nil {
		http.Error(w, "the default model isn't available", http.StatusServiceUnavailable)
		return
	}
	out := DefaultModel{State: a.AutoLoad.State()}
	if a.Config != nil {
		if f, err := a.Config.Read(); err == nil {
			if out.TTLs, err = llamaconfig.TTLs(f.Content); err != nil {
				a.Log.Debug("reading model TTLs from llama-swap's config", "err", err)
			}
		} else if !errors.Is(err, llamaconfig.ErrOff) {
			a.Log.Warn("reading llama-swap's config for model TTLs", "err", err)
		}
	}
	writeJSON(w, out)
}

// putDefaultModel: PUT /notus/api/default-model with an autoload.Setting.
// Saving clears a pause and starts the countdown again.
func (a *API) putDefaultModel(w http.ResponseWriter, r *http.Request) {
	if a.AutoLoad == nil {
		http.Error(w, "the default model isn't available", http.StatusServiceUnavailable)
		return
	}
	var s autoload.Setting
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&s); err != nil {
		http.Error(w, "Expected a JSON request body", http.StatusBadRequest)
		return
	}
	s = s.Clean()
	if s.Enabled && s.Model == "" {
		http.Error(w, "Choose a model first", http.StatusBadRequest)
		return
	}
	b, _ := json.Marshal(s)
	if err := a.Store.SetSetting(r.Context(), defaultModelKey, string(b)); err != nil {
		a.fail(w, err)
		return
	}
	a.AutoLoad.Restart()
	a.Log.Info("default model setting saved", "enabled", s.Enabled, "model", s.Model, "minutes", s.Minutes)
	a.getDefaultModel(w, r)
}

// unloadedByHand pauses the default model after an unload from the web UI,
// so a model unloaded on purpose isn't replaced later.
func (a *API) unloadedByHand() {
	if a.AutoLoad != nil {
		a.AutoLoad.Pause()
	}
}
