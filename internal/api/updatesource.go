package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wispborne/notus-swap/internal/selfupdate"
)

const updateSourceKey = "update_source"

func (a *API) registerUpdateSource(r *router) {
	r.handle("GET /notus/api/update-source", "Where notus-swap's own updates come from: the environment's settings, or a source saved here. The token is never sent.", a.getUpdateSource)
	r.handle("PUT /notus/api/update-source", `Save where notus-swap's own updates come from. Body: {"kind": "" | "gitea" | "github" | "off", ...}. An empty gitea_token keeps the saved one.`, a.putUpdateSource)
}

// UpdateSourceSetting reads the update source saved on the Settings page.
func (a *API) UpdateSourceSetting(ctx context.Context) selfupdate.Override {
	var o selfupdate.Override
	v, err := a.Store.Setting(ctx, updateSourceKey)
	if err != nil {
		a.Log.Error("reading the update source setting", "err", err)
	} else if v != "" {
		if err := json.Unmarshal([]byte(v), &o); err != nil {
			a.Log.Error("reading the update source setting", "err", err)
		}
	}
	return o.Clean()
}

// UpdateSource is what the Settings page shows. It has the saved fields
// without the token.
type UpdateSource struct {
	Kind       string `json:"kind"`
	GiteaURL   string `json:"gitea_url"`
	GiteaRepo  string `json:"gitea_repo"`
	GiteaUser  string `json:"gitea_user"`
	TokenSet   bool   `json:"gitea_token_set"`
	GitHubRepo string `json:"github_repo"`
	// EnvSource is the source the environment gives, such as "GitHub
	// (wispborne/notus-swap)", or "" when it turns updates off.
	EnvSource string `json:"env_source"`
	// Source is the one in use.
	Source string `json:"source"`
}

// getUpdateSource: GET /notus/api/update-source
func (a *API) getUpdateSource(w http.ResponseWriter, r *http.Request) {
	o := a.Updater.Override()
	writeJSON(w, UpdateSource{
		Kind: o.Kind, GiteaURL: o.GiteaURL, GiteaRepo: o.GiteaRepo, GiteaUser: o.GiteaUser, TokenSet: o.GiteaToken != "",
		GitHubRepo: o.GitHubRepo, EnvSource: a.Updater.EnvSource(), Source: a.Updater.Source(),
	})
}

// putUpdateSource: PUT /notus/api/update-source with a selfupdate.Override.
// It takes effect at once; the next check for releases uses it.
func (a *API) putUpdateSource(w http.ResponseWriter, r *http.Request) {
	var o selfupdate.Override
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&o); err != nil {
		http.Error(w, "Expected a JSON request body", http.StatusBadRequest)
		return
	}
	o = o.Clean()
	if old := a.Updater.Override(); o.Kind == "gitea" && o.GiteaToken == "" && old.Kind == "gitea" {
		o.GiteaToken = old.GiteaToken
	}
	if err := o.Check(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b, _ := json.Marshal(o)
	if err := a.Store.SetSetting(r.Context(), updateSourceKey, string(b)); err != nil {
		a.fail(w, err)
		return
	}
	a.Updater.SetOverride(o)
	a.Log.Info("update source saved", "kind", o.Kind, "source", a.Updater.Source())
	a.getUpdateSource(w, r)
}
