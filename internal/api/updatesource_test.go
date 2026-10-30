package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wispborne/notus-swap/internal/selfupdate"
	"github.com/wispborne/notus-swap/internal/store"
)

func TestUpdateSource(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &API{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Updater: &selfupdate.Updater{GitHubRepo: "wispborne/notus-swap"}}
	mux := http.NewServeMux()
	a.Register(mux)
	put := func(body string) (int, string) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("PUT", "/notus/api/update-source", strings.NewReader(body)))
		return w.Code, w.Body.String()
	}

	code, body := put(`{"kind":"gitea","gitea_url":"https://git.example.com/","gitea_repo":"o/r","gitea_user":"o","gitea_token":"secret"}`)
	if code != 200 || strings.Contains(body, "secret") {
		t.Fatalf("save: %d %s", code, body)
	}
	var got UpdateSource
	json.Unmarshal([]byte(body), &got)
	if !got.TokenSet || got.Source != "Gitea (o/r)" || got.EnvSource != "GitHub (wispborne/notus-swap)" || got.GiteaURL != "https://git.example.com" {
		t.Errorf("after save: %+v", got)
	}

	// A blank token keeps the saved one, so the page never needs to know it.
	if code, body := put(`{"kind":"gitea","gitea_url":"https://git.example.com","gitea_repo":"o/r2","gitea_user":"o"}`); code != 200 {
		t.Fatalf("save without token: %d %s", code, body)
	}
	if saved := a.UpdateSourceSetting(t.Context()); saved.GiteaToken != "secret" || saved.GiteaRepo != "o/r2" {
		t.Errorf("stored: %+v", saved)
	}

	// Switching away drops the token, so switching back needs it again.
	put(`{"kind":"github","github_repo":"me/fork"}`)
	if code, _ := put(`{"kind":"gitea","gitea_url":"https://git.example.com","gitea_repo":"o/r","gitea_user":"o"}`); code != 400 {
		t.Errorf("gitea without a token after switching away: %d", code)
	}
	if code, _ := put(`{"kind":""}`); code != 200 || a.Updater.Source() != "GitHub (wispborne/notus-swap)" {
		t.Errorf("back to the environment: %d %q", code, a.Updater.Source())
	}
}
