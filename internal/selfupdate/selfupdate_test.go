package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitea serves a release list and one release's files, and checks the
// basic-auth token.
func fakeGitea(t *testing.T, binary string, badSum bool) *httptest.Server {
	sum := sha256.Sum256([]byte(binary))
	hash := hex.EncodeToString(sum[:])
	if badSum {
		hash = strings.Repeat("0", 64)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "owner" || p != "secret" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/repos/owner/notus-swap/releases":
			fmt.Fprint(w, `[{"tag_name":"2026.09.25.1000-bbbbbbb","body":"New thing\n","published_at":"2026-09-25T10:00:00Z"},
				{"tag_name":"draft","draft":true},
				{"tag_name":"2026.09.24.1000-aaaaaaa","body":"Old thing","published_at":"2026-09-24T10:00:00Z"}]`)
		case "/owner/notus-swap/releases/download/2026.09.25.1000-bbbbbbb/" + Asset():
			fmt.Fprint(w, binary)
		case "/owner/notus-swap/releases/download/2026.09.25.1000-bbbbbbb/" + Asset() + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", hash, Asset())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func updater(t *testing.T, srv *httptest.Server) *Updater {
	exe := filepath.Join(t.TempDir(), "notus-swap")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{GiteaURL: srv.URL, Repo: "owner/notus-swap", User: "owner", Token: "secret",
		Exe: exe, Version: "2026.09.24.1000-aaaaaaa", Client: srv.Client()}
}

func read(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReleases(t *testing.T) {
	u := updater(t, fakeGitea(t, "x", false))
	rs, err := u.Releases(context.Background())
	if err != nil || len(rs) != 2 || rs[0].Notes != "New thing" || rs[1].Tag != "2026.09.24.1000-aaaaaaa" {
		t.Errorf("releases %+v err %v", rs, err)
	}
}

func TestUpdateAvailable(t *testing.T) {
	u := updater(t, fakeGitea(t, "x", false))
	if u.UpdateAvailable() {
		t.Error("available before any check")
	}
	u.Releases(context.Background())
	if !u.UpdateAvailable() {
		t.Error("newer release not seen")
	}
	u.Version = "2026.09.25.1000-bbbbbbb"
	if u.UpdateAvailable() {
		t.Error("available while running the newest release")
	}
}

func TestInstallConfirm(t *testing.T) {
	u := updater(t, fakeGitea(t, "new binary", false))
	if err := u.Install(context.Background(), "2026.09.25.1000-bbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if read(t, u.Exe) != "new binary" || read(t, u.prevPath()) != "old binary" {
		t.Error("binaries not swapped")
	}
	if m := u.Pending(); m == nil || m.To != "2026.09.25.1000-bbbbbbb" {
		t.Errorf("marker %+v", m)
	}

	// The new version starts, then runs long enough to confirm.
	if rolled, err := u.CheckOnStart(); rolled || err != nil {
		t.Fatalf("first start: %v %v", rolled, err)
	}
	u.Confirm()
	if u.Pending() != nil || u.LastResult().Outcome != "installed" {
		t.Errorf("after confirm: pending %+v result %+v", u.Pending(), u.LastResult())
	}
}

func TestFailedStartsRollBack(t *testing.T) {
	u := updater(t, fakeGitea(t, "broken binary", false))
	if err := u.Install(context.Background(), "2026.09.25.1000-bbbbbbb"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxAttempts; i++ {
		if rolled, _ := u.CheckOnStart(); rolled {
			t.Fatalf("rolled back after %d starts", i)
		}
	}
	rolled, err := u.CheckOnStart()
	if !rolled || err != nil {
		t.Fatalf("no rollback after %d starts: %v", MaxAttempts, err)
	}
	if read(t, u.Exe) != "old binary" || u.Pending() != nil || u.LastResult().Outcome != "rolled back" {
		t.Errorf("exe %q pending %+v result %+v", read(t, u.Exe), u.Pending(), u.LastResult())
	}
}

func TestDamagedDownloadChangesNothing(t *testing.T) {
	u := updater(t, fakeGitea(t, "new binary", true))
	if err := u.Install(context.Background(), "2026.09.25.1000-bbbbbbb"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err %v", err)
	}
	if read(t, u.Exe) != "old binary" || u.HasPrevious() || u.Pending() != nil {
		t.Error("a failed download changed files")
	}
	entries, _ := os.ReadDir(filepath.Dir(u.Exe))
	if len(entries) != 1 {
		t.Errorf("left files behind: %v", entries)
	}
}

func TestManualRollbackSwapsBack(t *testing.T) {
	u := updater(t, fakeGitea(t, "new binary", false))
	if err := u.Rollback(); err == nil {
		t.Error("rolled back with no previous version")
	}
	u.Install(context.Background(), "2026.09.25.1000-bbbbbbb")
	u.Confirm()
	if err := u.Rollback(); err != nil || read(t, u.Exe) != "old binary" || read(t, u.prevPath()) != "new binary" {
		t.Fatalf("rollback: %v", err)
	}
	if err := u.Rollback(); err != nil || read(t, u.Exe) != "new binary" {
		t.Fatalf("rolling back twice should return: %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	u := &Updater{Exe: filepath.Join(t.TempDir(), "x")}
	if _, err := u.Releases(context.Background()); err == nil || !strings.Contains(err.Error(), "updates are off") {
		t.Errorf("err %v", err)
	}
}

func TestUndoInstallRestoresRunningBinary(t *testing.T) {
	u := updater(t, fakeGitea(t, "new binary", false))
	if err := u.UndoInstall(); err == nil {
		t.Error("undid an install that never happened")
	}
	if err := u.Install(context.Background(), "2026.09.25.1000-bbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if err := u.UndoInstall(); err != nil {
		t.Fatal(err)
	}
	if read(t, u.Exe) != "old binary" || u.HasPrevious() || u.Pending() != nil {
		t.Errorf("exe %q, previous %v, pending %+v", read(t, u.Exe), u.HasPrevious(), u.Pending())
	}
	if rolled, err := u.CheckOnStart(); rolled || err != nil {
		t.Fatalf("start after undo: %v %v", rolled, err)
	}
}

func TestUndoRollbackRestoresRunningBinary(t *testing.T) {
	u := updater(t, fakeGitea(t, "new binary", false))
	if err := u.Install(context.Background(), "2026.09.25.1000-bbbbbbb"); err != nil {
		t.Fatal(err)
	}
	u.Confirm()
	if err := u.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := u.UndoRollback(); err != nil {
		t.Fatal(err)
	}
	if read(t, u.Exe) != "new binary" || read(t, u.prevPath()) != "old binary" || u.LastResult() != nil {
		t.Errorf("exe %q, previous %q, result %+v", read(t, u.Exe), read(t, u.prevPath()), u.LastResult())
	}
}

// fakeGitHub serves the same release as fakeGitea, at GitHub's paths, and
// checks the optional token.
func fakeGitHub(t *testing.T, binary, token string) *httptest.Server {
	sum := sha256.Sum256([]byte(binary))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("GitHub request sent basic auth")
		}
		if got := r.Header.Get("Authorization"); token != "" && got != "Bearer "+token || token == "" && got != "" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/repos/owner/notus-swap/releases":
			fmt.Fprint(w, `[{"tag_name":"2026.09.25.1000-bbbbbbb","body":"New thing","published_at":"2026-09-25T10:00:00Z"},
				{"tag_name":"2026.09.24.1000-aaaaaaa","body":"Old thing","published_at":"2026-09-24T10:00:00Z"}]`)
		case "/web/owner/notus-swap/releases/download/2026.09.25.1000-bbbbbbb/" + Asset():
			fmt.Fprint(w, binary)
		case "/web/owner/notus-swap/releases/download/2026.09.25.1000-bbbbbbb/" + Asset() + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), Asset())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func githubUpdater(t *testing.T, srv *httptest.Server, token string) *Updater {
	u := updater(t, srv)
	u.GiteaURL = "" // the Gitea settings are incomplete, so GitHub is used
	u.GitHubRepo, u.GitHubToken = "owner/notus-swap", token
	u.GitHubAPI, u.GitHubURL = srv.URL+"/api", srv.URL+"/web"
	return u
}

func TestGitHubInstall(t *testing.T) {
	for _, token := range []string{"", "ghtoken"} {
		u := githubUpdater(t, fakeGitHub(t, "new binary", token), token)
		if got := u.Source(); got != "GitHub (owner/notus-swap)" {
			t.Errorf("source %q", got)
		}
		rs, err := u.Releases(context.Background())
		if err != nil || len(rs) != 2 || rs[0].Tag != "2026.09.25.1000-bbbbbbb" {
			t.Fatalf("token %q: releases %+v err %v", token, rs, err)
		}
		if err := u.Install(context.Background(), rs[0].Tag); err != nil {
			t.Fatalf("token %q: install: %v", token, err)
		}
		if read(t, u.Exe) != "new binary" || read(t, u.Exe+".prev") != "old binary" {
			t.Errorf("token %q: binaries not swapped", token)
		}
	}
}

func TestSource(t *testing.T) {
	gitea := &Updater{GiteaURL: "https://git.example.com", Repo: "o/r", User: "o", Token: "t", GitHubRepo: "x/y"}
	if got := gitea.Source(); got != "Gitea (o/r)" {
		t.Errorf("all Gitea settings: %q", got)
	}
	off := &Updater{GiteaURL: "https://git.example.com"}
	if off.Configured() || off.Source() != "" {
		t.Errorf("no GitHub repo and incomplete Gitea settings should turn updates off")
	}
}

func TestFromEnvGitHubRepo(t *testing.T) {
	for _, k := range []string{"GITEA_URL", "GITEA_REPO", "GITEA_USER", "GITEA_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("NOTUS_GITHUB_REPO", "") // restored after the test
	os.Unsetenv("NOTUS_GITHUB_REPO")
	if got := FromEnv("v").GitHubRepo; got != DefaultGitHubRepo {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("NOTUS_GITHUB_REPO", "")
	if FromEnv("v").Configured() {
		t.Error("an empty NOTUS_GITHUB_REPO should turn updates off")
	}
}
