// Package selfupdate installs notus-swap releases and rolls back a release
// that won't start.
//
// Releases come from a Gitea server when all four GITEA_ settings are set,
// and otherwise from GitHub (NOTUS_GITHUB_REPO, wispborne/notus-swap by
// default). Setting NOTUS_GITHUB_REPO to nothing turns updates off.
//
// Installing: the release binary and its .sha256 are downloaded next to the
// running binary and checked. The running binary is renamed to
// "<exe>.prev", the new one takes its name, and a marker file records the
// update. notus-swap then exits, and systemd (Restart=always) starts the new
// binary.
//
// Confirming: on start-up, CheckOnStart counts attempts in the marker. The
// new version calls Confirm once it has served for 30 seconds, which removes
// the marker. If it crashes before that three times in a row, the third
// start swaps .prev back in and exits, so systemd starts the old version.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// MaxAttempts is how many starts a new version gets before it is rolled back.
const MaxAttempts = 3

// DefaultGitHubRepo is where public releases are published.
const DefaultGitHubRepo = "wispborne/notus-swap"

type Updater struct {
	GiteaURL, Repo, User, Token string

	// Used when the Gitea settings aren't all set. GitHubToken is optional.
	GitHubRepo, GitHubToken string
	GitHubAPI, GitHubURL    string // https://api.github.com and https://github.com; tests change them

	Exe     string // path of the running binary
	Version string // running version
	Client  *http.Client

	mu       sync.Mutex
	cache    []Release
	cachedAt time.Time
}

// FromEnv reads GITEA_URL, GITEA_REPO, GITEA_USER and GITEA_TOKEN, then
// NOTUS_GITHUB_REPO and GITHUB_TOKEN.
func FromEnv(version string) *Updater {
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	githubRepo, set := os.LookupEnv("NOTUS_GITHUB_REPO")
	if !set {
		githubRepo = DefaultGitHubRepo
	}
	return &Updater{
		GiteaURL: strings.TrimRight(os.Getenv("GITEA_URL"), "/"), Repo: os.Getenv("GITEA_REPO"),
		User: os.Getenv("GITEA_USER"), Token: os.Getenv("GITEA_TOKEN"),
		GitHubRepo: githubRepo, GitHubToken: os.Getenv("GITHUB_TOKEN"),
		GitHubAPI: "https://api.github.com", GitHubURL: "https://github.com",
		Exe: exe, Version: version, Client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// useGitea reports whether the Gitea settings are all present.
func (u *Updater) useGitea() bool {
	return u.GiteaURL != "" && u.Repo != "" && u.User != "" && u.Token != ""
}

// Configured reports whether there is anywhere to get releases from.
func (u *Updater) Configured() bool {
	return u.useGitea() || u.GitHubRepo != ""
}

// Source names where releases come from, such as "GitHub
// (wispborne/notus-swap)". It is empty when updates are off.
func (u *Updater) Source() string {
	switch {
	case u.useGitea():
		return "Gitea (" + u.Repo + ")"
	case u.GitHubRepo != "":
		return "GitHub (" + u.GitHubRepo + ")"
	}
	return ""
}

// releasesURL is the API address that lists recent releases.
func (u *Updater) releasesURL() string {
	if u.useGitea() {
		return u.GiteaURL + "/api/v1/repos/" + u.Repo + "/releases?limit=30"
	}
	return u.GitHubAPI + "/repos/" + u.GitHubRepo + "/releases?per_page=30"
}

// downloadURL is the address of one file in a release. Gitea and GitHub use
// the same layout.
func (u *Updater) downloadURL(tag, file string) string {
	base, repo := u.GitHubURL, u.GitHubRepo
	if u.useGitea() {
		base, repo = u.GiteaURL, u.Repo
	}
	return base + "/" + repo + "/releases/download/" + url.PathEscape(tag) + "/" + file
}

// Asset is the release file for this machine.
func Asset() string { return "notus-swap-" + runtime.GOOS + "-" + runtime.GOARCH }

type Release struct {
	Tag       string    `json:"tag"`
	Notes     string    `json:"notes"`
	Published time.Time `json:"published"`
}

// Releases lists recent releases, newest first. Answers are cached for a
// minute.
func (u *Updater) Releases(ctx context.Context) ([]Release, error) {
	u.mu.Lock()
	if time.Since(u.cachedAt) < time.Minute && u.cache != nil {
		defer u.mu.Unlock()
		return u.cache, nil
	}
	u.mu.Unlock()

	var raw []struct {
		TagName     string    `json:"tag_name"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	if err := u.getJSON(ctx, u.releasesURL(), &raw); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range raw {
		if !r.Draft && !r.Prerelease {
			out = append(out, Release{Tag: r.TagName, Notes: strings.TrimSpace(r.Body), Published: r.PublishedAt})
		}
	}
	u.mu.Lock()
	u.cache, u.cachedAt = out, time.Now()
	u.mu.Unlock()
	return out, nil
}

// Watch checks for releases now and then every interval, so
// UpdateAvailable has something to answer from. It does nothing when updates
// aren't set up.
func (u *Updater) Watch(ctx context.Context, every time.Duration) {
	if !u.Configured() {
		return
	}
	for {
		u.Releases(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// UpdateAvailable reports whether the newest release from the last check is
// not the running version. It asks no server, so it is cheap to call.
func (u *Updater) UpdateAvailable() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.cache) > 0 && u.cache[0].Tag != u.Version
}

// get fetches address, signed in to Gitea, or to GitHub when GITHUB_TOKEN is
// set. GitHub's downloads redirect to another host, and Go drops the
// Authorization header on that hop.
func (u *Updater) get(ctx context.Context, address string) (*http.Response, error) {
	if !u.Configured() {
		return nil, errors.New("updates are off: NOTUS_GITHUB_REPO is empty in the env file")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	server := "GitHub"
	if u.useGitea() {
		server = "Gitea"
		req.SetBasicAuth(u.User, u.Token)
	} else if u.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.GitHubToken)
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s for %s", server, resp.Status, address)
	}
	return resp, nil
}

func (u *Updater) getJSON(ctx context.Context, path string, v any) error {
	resp, err := u.get(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// ---- Files next to the binary ----

func (u *Updater) prevPath() string   { return u.Exe + ".prev" }
func (u *Updater) markerPath() string { return u.Exe + ".update.json" }
func (u *Updater) resultPath() string { return u.Exe + ".update-result.json" }

// Marker records an update in progress.
type Marker struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	At       time.Time `json:"at"`
	Attempts int       `json:"attempts"`
}

// Result records how the last update ended.
type Result struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Outcome string    `json:"outcome"` // "installed", "rolled back", or "rolled back by hand"
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

// HasPrevious reports whether there is a previous binary to roll back to.
func (u *Updater) HasPrevious() bool {
	_, err := os.Stat(u.prevPath())
	return err == nil
}

// LastResult returns how the last update ended, if one is recorded.
func (u *Updater) LastResult() *Result {
	var r Result
	if readJSON(u.resultPath(), &r) {
		return &r
	}
	return nil
}

// Pending returns the update being confirmed, if any.
func (u *Updater) Pending() *Marker {
	var m Marker
	if readJSON(u.markerPath(), &m) {
		return &m
	}
	return nil
}

// ---- Install, confirm, roll back ----

// Install downloads tag, checks it, and swaps it in. The caller then exits
// so systemd starts the new binary.
func (u *Updater) Install(ctx context.Context, tag string) error {
	dir := filepath.Dir(u.Exe)
	sumResp, err := u.get(ctx, u.downloadURL(tag, Asset()+".sha256"))
	if err != nil {
		return err
	}
	sumText, err := io.ReadAll(io.LimitReader(sumResp.Body, 4096))
	sumResp.Body.Close()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(sumText))
	if len(fields) == 0 {
		return errors.New("release checksum file is empty")
	}
	want := strings.ToLower(fields[0])

	binResp, err := u.get(ctx, u.downloadURL(tag, Asset()))
	if err != nil {
		return err
	}
	defer binResp.Body.Close()
	tmp, err := os.CreateTemp(dir, ".notus-swap-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), binResp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, expected %s", got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}

	// Swap: running binary becomes .prev, the download takes its name. The
	// running process keeps its open file, so renaming under it is safe.
	if err := os.Rename(u.Exe, u.prevPath()); err != nil {
		return fmt.Errorf("could not save the current binary: %w", err)
	}
	if err := os.Rename(tmp.Name(), u.Exe); err != nil {
		os.Rename(u.prevPath(), u.Exe) // put it back
		return fmt.Errorf("installing the new binary: %w", err)
	}
	os.Remove(u.resultPath())
	return writeJSON(u.markerPath(), Marker{From: u.Version, To: tag, At: time.Now()})
}

// swap exchanges the binary and .prev.
func (u *Updater) swap() error {
	tmp := u.Exe + ".swap"
	if err := os.Rename(u.prevPath(), tmp); err != nil {
		return err
	}
	if err := os.Rename(u.Exe, u.prevPath()); err != nil {
		os.Rename(tmp, u.prevPath())
		return err
	}
	return os.Rename(tmp, u.Exe)
}

// Rollback swaps the previous binary back in, by hand. The caller then
// exits so systemd starts it. Rolling back twice returns to where it began.
func (u *Updater) Rollback() error {
	if !u.HasPrevious() {
		return errors.New("no previous version to restore")
	}
	if err := u.swap(); err != nil {
		return err
	}
	os.Remove(u.markerPath())
	return writeJSON(u.resultPath(), Result{From: u.Version, Outcome: "rolled back by hand", At: time.Now()})
}

// UndoInstall restores the running binary before restart and removes the
// cancelled update and its marker.
func (u *Updater) UndoInstall() error {
	if u.Pending() == nil {
		return errors.New("no installed update to undo")
	}
	if err := u.swap(); err != nil {
		return fmt.Errorf("putting the running binary back: %w", err)
	}
	// Remove .prev so a later rollback can't install the cancelled update.
	os.Remove(u.prevPath())
	os.Remove(u.markerPath())
	return nil
}

// UndoRollback reverses Rollback before restart.
func (u *Updater) UndoRollback() error {
	if err := u.swap(); err != nil {
		return fmt.Errorf("putting the running binary back: %w", err)
	}
	os.Remove(u.resultPath())
	return nil
}

// CheckOnStart runs first thing at start-up. If an update is being
// confirmed, it counts this start. It returns true when the new version has
// used up its attempts and the old binary is back in place: the caller must
// exit at once so systemd starts it.
func (u *Updater) CheckOnStart() (rolledBack bool, err error) {
	var m Marker
	if !readJSON(u.markerPath(), &m) {
		return false, nil
	}
	m.Attempts++
	if m.Attempts < MaxAttempts {
		return false, writeJSON(u.markerPath(), m)
	}
	if err := u.swap(); err != nil {
		return false, fmt.Errorf("rolling back a failed update: %w", err)
	}
	os.Remove(u.markerPath())
	reason := fmt.Sprintf("new version stopped %d times before running for 30 seconds", MaxAttempts-1)
	writeJSON(u.resultPath(), Result{From: m.From, To: m.To, Outcome: "rolled back", Reason: reason, At: time.Now()})
	return true, nil
}

// Confirm marks the running version as good, ending an update.
func (u *Updater) Confirm() {
	var m Marker
	if !readJSON(u.markerPath(), &m) {
		return
	}
	os.Remove(u.markerPath())
	writeJSON(u.resultPath(), Result{From: m.From, To: m.To, Outcome: "installed", At: time.Now()})
}
