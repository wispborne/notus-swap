package llamaupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const swapRepo = "mostlygeek/llama-swap"

var (
	swapTag     = regexp.MustCompile(`^v\d+$`)
	swapVersion = regexp.MustCompile(`\bv\d+\b`)
)

// swapWait is how long a restarted llama-swap gets to answer. Tests shorten it.
var swapWait = time.Minute

// LlamaSwap installs llama-swap releases from GitHub over the binary at Bin,
// then restarts llama-swap's systemd unit. The binary it replaces is kept as
// "<Bin>.bak", the same name llama-swap's own update script uses.
type LlamaSwap struct {
	Bin      string
	Config   string // llama-swap's config, checked with the new binary before it goes in; "" skips the check
	Unit     string // systemd unit to restart
	Upstream string // llama-swap's address, asked for its version after a restart
	Client   *http.Client
	// Restart restarts Unit. Nil means RestartUnit. Tests replace it.
	Restart func(ctx context.Context, unit string) error

	mu       sync.Mutex
	versions map[string]cachedVersion
}

type cachedVersion struct {
	file    os.FileInfo
	version string
}

func (s *LlamaSwap) backup() string { return s.Bin + ".bak" }

// Installed is the version of the binary at Bin, or "" if it can't be told.
func (s *LlamaSwap) Installed() string { return s.version(s.Bin) }

// Backup is the version kept as <Bin>.bak, or "" if it can't be told.
func (s *LlamaSwap) Backup() string { return s.version(s.backup()) }

// HasBackup reports whether there is a <Bin>.bak to roll back to.
func (s *LlamaSwap) HasBackup() bool { return exists(s.backup()) }

// version runs "<path> -version", which prints "version: v258 (<commit>),
// built at <date>". The answer is kept until the file changes.
func (s *LlamaSwap) version(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	c, ok := s.versions[path]
	s.mu.Unlock()
	if ok && os.SameFile(c.file, fi) && c.file.ModTime().Equal(fi.ModTime()) && c.file.Size() == fi.Size() {
		return c.version
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "-version").CombinedOutput()
	v := swapVersion.FindString(string(out))
	s.mu.Lock()
	if s.versions == nil {
		s.versions = map[string]cachedVersion{}
	}
	s.versions[path] = cachedVersion{fi, v}
	s.mu.Unlock()
	return v
}

// asset is the release file for this machine, such as
// llama-swap_258_linux_amd64.tar.gz.
func swapAsset(tag string) string {
	return fmt.Sprintf("llama-swap_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH)
}

// Install downloads release tag, checks the config with it, swaps it in,
// and restarts llama-swap. If llama-swap doesn't come back as the new
// version within a minute, the previous binary is put back and restarted.
func (s *LlamaSwap) Install(ctx context.Context, gh *GitHub, tag string, r report) (string, error) {
	if !swapTag.MatchString(tag) {
		return "", fmt.Errorf("%q is not a llama-swap release tag, such as v258", tag)
	}
	old := s.Installed()
	if old == tag {
		return "", fmt.Errorf("%s is already installed", tag)
	}
	r.Step("Looking up " + tag + " on GitHub")
	rel, err := gh.Tag(ctx, swapRepo, tag)
	if err != nil {
		return "", err
	}
	a, ok := rel.asset(swapAsset(tag))
	if !ok {
		return "", fmt.Errorf("release %s has no %s", tag, swapAsset(tag))
	}

	dir := filepath.Dir(s.Bin)
	tgz := filepath.Join(dir, ".llama-swap-download.tar.gz")
	defer os.Remove(tgz)
	r.Step("Downloading " + a.Name)
	if err := gh.Download(ctx, a, tgz, func(n int64) { r.Bytes(n, a.Size) }); err != nil {
		return "", err
	}
	next := filepath.Join(dir, ".llama-swap-next")
	defer os.Remove(next) // no-op once renamed
	if err := extractFile(tgz, "llama-swap", next, 0o755); err != nil {
		return "", err
	}
	if s.Config != "" {
		r.Step("Checking " + filepath.Base(s.Config) + " with " + tag)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := exec.CommandContext(cctx, next, "-validate", "-config", s.Config).CombinedOutput()
		cancel()
		if err != nil {
			return "", fmt.Errorf("%s rejects the current config, so nothing was changed: %s", tag, strings.TrimSpace(string(out)))
		}
	}

	r.Step("Replacing the binary")
	if exists(s.Bin) {
		os.Remove(s.backup())
		// The running llama-swap keeps its open file, so renaming under it is safe.
		if err := os.Rename(s.Bin, s.backup()); err != nil {
			return "", fmt.Errorf("could not keep the current binary: %w", err)
		}
	}
	if err := os.Rename(next, s.Bin); err != nil {
		os.Rename(s.backup(), s.Bin)
		return "", fmt.Errorf("installing the new binary: %w", err)
	}

	r.Step("Restarting llama-swap")
	if err := s.restart(ctx); err != nil {
		return "", fmt.Errorf("%s is installed, but llama-swap could not be restarted: %w. The old version keeps running until llama-swap restarts", tag, err)
	}
	r.Step("Waiting for llama-swap to answer")
	if err := s.waitFor(ctx, tag, swapWait); err != nil {
		r.Step("Putting the previous version back")
		if serr := s.swap(); serr != nil {
			return "", fmt.Errorf("%w. Putting the previous version back also failed: %v", err, serr)
		}
		if rerr := s.restart(ctx); rerr != nil {
			return "", fmt.Errorf("%w. The previous version is back in place, but restarting it failed: %v", err, rerr)
		}
		return "", fmt.Errorf("%w, so %s was put back", err, orUnknown(old))
	}
	return fmt.Sprintf("Installed llama-swap %s. %s is kept as %s.", tag, keptName(old), filepath.Base(s.backup())), nil
}

// Rollback swaps the binary and <Bin>.bak, then restarts llama-swap.
// Rolling back twice returns to where it began.
func (s *LlamaSwap) Rollback(ctx context.Context, r report) (string, error) {
	if !s.HasBackup() {
		return "", errors.New("there is no previous version to go back to")
	}
	want, from := s.Backup(), s.Installed()
	r.Step("Swapping in " + orUnknown(want))
	if err := s.swap(); err != nil {
		return "", err
	}
	r.Step("Restarting llama-swap")
	if err := s.restart(ctx); err != nil {
		return "", fmt.Errorf("%s is back in place, but llama-swap could not be restarted: %w", orUnknown(want), err)
	}
	r.Step("Waiting for llama-swap to answer")
	if err := s.waitFor(ctx, want, swapWait); err != nil {
		return "", err
	}
	return fmt.Sprintf("Went back to llama-swap %s. %s is kept as %s.", orUnknown(want), keptName(from), filepath.Base(s.backup())), nil
}

// swap exchanges the binary and <Bin>.bak.
func (s *LlamaSwap) swap() error {
	tmp := s.Bin + ".swap"
	if err := os.Rename(s.backup(), tmp); err != nil {
		return err
	}
	if err := os.Rename(s.Bin, s.backup()); err != nil {
		os.Rename(tmp, s.backup())
		return err
	}
	return os.Rename(tmp, s.Bin)
}

func (s *LlamaSwap) restart(ctx context.Context) error {
	if s.Restart != nil {
		return s.Restart(ctx, s.Unit)
	}
	return RestartUnit(ctx, s.Unit)
}

// waitFor asks llama-swap for its version every second until it answers
// as want, or until limit has passed.
func (s *LlamaSwap) waitFor(ctx context.Context, want string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	last := "it was not reachable"
	for {
		got, err := s.running(ctx)
		switch {
		case err == nil && (got == "" || want == "" || got == want):
			return nil
		case err == nil:
			last = "it reported " + got
		default:
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("llama-swap did not come back as %s within %s: %s", orUnknown(want), limit, last)
		case <-time.After(time.Second):
		}
	}
}

// running asks llama-swap which version it is. It answers "" when
// llama-swap is up but doesn't say: older versions have no /api/version,
// and an API key may be required.
func (s *LlamaSwap) running(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.Upstream, "/")+"/api/version", nil)
	if err != nil {
		return "", err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("it was not reachable")
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&v) != nil {
		return "", nil
	}
	return v.Version, nil
}

// RestartUnit runs "systemctl restart unit". The polkit rule in the install
// guide lets notus-swap's user do this without a password.
func RestartUnit(ctx context.Context, unit string) error {
	return controlUnit(ctx, "restart", unit)
}

func StartUnit(ctx context.Context, unit string) error {
	return controlUnit(ctx, "start", unit)
}

func StopUnit(ctx context.Context, unit string) error {
	return controlUnit(ctx, "stop", unit)
}

// UnitState reads systemd's state even when llama-swap's HTTP server is down.
func UnitState(ctx context.Context, unit string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "--property=ActiveState", "--value", unit).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reading service state: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func controlUnit(ctx context.Context, action, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", action, unit).CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = err.Error()
	}
	if strings.Contains(text, "Interactive authentication required") || strings.Contains(text, "Access denied") {
		text += " (the polkit rule in docs/install.md lets notus-swap do this)"
	}
	return errors.New(text)
}

func orUnknown(version string) string {
	if version == "" {
		return "the previous version"
	}
	return version
}

// keptName starts a sentence about the binary that was replaced.
func keptName(version string) string {
	if version == "" {
		return "The binary it replaced"
	}
	return version
}
