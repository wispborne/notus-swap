package llamaupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const cppRepo = "ggml-org/llama.cpp"

var (
	cppBuild  = regexp.MustCompile(`^b(\d+)$`)
	cppFolder = regexp.MustCompile(`^llama-(b\d+)-bin-(.+)$`)
)

// LlamaCpp installs llama.cpp's prebuilt releases side by side in Dir, one
// folder per build, such as llama-b11146-bin-ubuntu-rocm-10.0-x64. The link
// Dir/current points at the build in use. llama-swap's config starts models
// from Dir/current, so a model uses a new build the next time it loads.
//
// After an install, the build that was in use is kept for rolling back, and
// older builds of the same flavor are deleted unless a program is still
// running from them.
type LlamaCpp struct {
	Dir string
	// Flavor is the part of the download's name after "-bin-", such as
	// ubuntu-rocm-10.0-x64 or ubuntu-vulkan-x64. It depends on the machine's
	// GPU. "" means the flavor of the build current points at.
	Flavor string
}

func (c *LlamaCpp) link() string { return filepath.Join(c.Dir, "current") }

// Current returns the build that the current link points at, and its
// flavor. Both are "" when there is no link yet.
func (c *LlamaCpp) Current() (build, flavor string, err error) {
	target, err := os.Readlink(c.link())
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("%s is not a link: %w", c.link(), err)
	}
	m := cppFolder.FindStringSubmatch(filepath.Base(target))
	if m == nil {
		return "", "", fmt.Errorf("%s points at %s, which is not named like a llama.cpp release folder", c.link(), target)
	}
	return m[1], m[2], nil
}

// flavor is the flavor to download: Flavor if set, or else the one in use.
func (c *LlamaCpp) flavor() (string, error) {
	if c.Flavor != "" {
		return c.Flavor, nil
	}
	_, f, err := c.Current()
	if err != nil {
		return "", err
	}
	if f == "" {
		return "", errors.New("set NOTUS_LLAMA_CPP_FLAVOR in the env file to the build to download, such as ubuntu-vulkan-x64")
	}
	return f, nil
}

func (c *LlamaCpp) folder(build, flavor string) string {
	return filepath.Join(c.Dir, "llama-"+build+"-bin-"+flavor)
}

// builds lists the installed builds of flavor, newest first.
func (c *LlamaCpp) builds(flavor string) []string {
	entries, _ := os.ReadDir(c.Dir)
	var out []string
	for _, e := range entries {
		if m := cppFolder.FindStringSubmatch(e.Name()); m != nil && e.IsDir() && m[2] == flavor {
			out = append(out, m[1])
		}
	}
	sort.Slice(out, func(i, j int) bool { return buildNumber(out[i]) > buildNumber(out[j]) })
	return out
}

// Previous is the build a roll back switches to: the newest installed build
// other than the current one. "" when there is none.
func (c *LlamaCpp) Previous() string {
	cur, _, _ := c.Current()
	flavor, err := c.flavor()
	if err != nil {
		return ""
	}
	for _, b := range c.builds(flavor) {
		if b != cur {
			return b
		}
	}
	return ""
}

// buildNumber turns "b11146" into 11146, and anything else into 0.
func buildNumber(build string) int {
	m := cppBuild.FindStringSubmatch(build)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// Install downloads build (a nightly tag such as b11146) into its own
// folder, points current at it, and deletes builds older than the one it
// replaces.
func (c *LlamaCpp) Install(ctx context.Context, gh *GitHub, build string, r report) (string, error) {
	if !cppBuild.MatchString(build) {
		return "", fmt.Errorf("%q is not a llama.cpp build, such as b11146", build)
	}
	flavor, err := c.flavor()
	if err != nil {
		return "", err
	}
	before, _, err := c.Current()
	if err != nil {
		return "", err
	}
	if before == build {
		return "", fmt.Errorf("%s is already in use", build)
	}
	target := c.folder(build, flavor)
	if !exists(filepath.Join(target, "llama-server")) {
		if err := c.download(ctx, gh, build, flavor, target, r); err != nil {
			return "", err
		}
	}

	r.Step("Pointing current at " + build)
	if err := c.point(target); err != nil {
		return "", err
	}
	removed, busy := c.clean(flavor, build, before)
	msg := "Installed llama.cpp " + build + ". Models use it from their next load. Models that are loaded now keep the old build until they unload."
	if before != "" {
		msg += " " + before + " is kept for rolling back."
	}
	if len(removed) > 0 {
		msg += " Deleted older builds: " + strings.Join(removed, ", ") + "."
	}
	if len(busy) > 0 {
		msg += " Kept " + strings.Join(busy, ", ") + " because a program is still running from it."
	}
	return msg, nil
}

// download fetches and unpacks one build into target.
func (c *LlamaCpp) download(ctx context.Context, gh *GitHub, build, flavor, target string, r report) error {
	r.Step("Looking up " + build + " on GitHub")
	rel, err := gh.Tag(ctx, cppRepo, build)
	if err != nil {
		return err
	}
	name := filepath.Base(target) + ".tar.gz"
	a, ok := rel.asset(name)
	if !ok {
		var others []string
		for _, a := range rel.Assets {
			if m := cppFolder.FindStringSubmatch(strings.TrimSuffix(a.Name, ".tar.gz")); m != nil && strings.HasSuffix(a.Name, ".tar.gz") {
				others = append(others, m[2])
			}
		}
		return fmt.Errorf("build %s has no %s download. It has: %s", build, flavor, strings.Join(others, ", "))
	}

	tgz := filepath.Join(c.Dir, "."+name)
	defer os.Remove(tgz)
	r.Step("Downloading " + name)
	if err := gh.Download(ctx, a, tgz, func(n int64) { r.Bytes(n, a.Size) }); err != nil {
		return err
	}
	r.Step("Unpacking")
	partial := filepath.Join(c.Dir, "."+filepath.Base(target)+".partial")
	os.RemoveAll(partial) // left over from an install that was cut off
	defer os.RemoveAll(partial)
	if err := untar(tgz, partial); err != nil {
		return err
	}
	root := onlyFolder(partial)
	if !exists(filepath.Join(root, "llama-server")) {
		return fmt.Errorf("%s has no llama-server in it", name)
	}
	os.RemoveAll(target) // an earlier folder without llama-server
	return os.Rename(root, target)
}

// point makes current a link to target, replacing the old link in one step.
func (c *LlamaCpp) point(target string) error {
	if fi, err := os.Lstat(c.link()); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s is a real folder, not a link, so it was left alone", c.link())
	}
	tmp := c.link() + ".next"
	os.Remove(tmp)
	// A relative link keeps working if the whole folder moves.
	if err := os.Symlink(filepath.Base(target), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.link()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// clean deletes builds of flavor other than keep, unless a program is
// running from one.
func (c *LlamaCpp) clean(flavor string, keep ...string) (removed, busy []string) {
	for _, b := range c.builds(flavor) {
		if b == "" || contains(keep, b) {
			continue
		}
		dir := c.folder(b, flavor)
		if inUse(dir) {
			busy = append(busy, b)
			continue
		}
		if err := os.RemoveAll(dir); err == nil {
			removed = append(removed, b)
		}
	}
	return removed, busy
}

// Rollback points current at Previous.
func (c *LlamaCpp) Rollback(ctx context.Context, r report) (string, error) {
	prev := c.Previous()
	if prev == "" {
		return "", errors.New("there is no other build to switch to")
	}
	flavor, err := c.flavor()
	if err != nil {
		return "", err
	}
	cur, _, _ := c.Current()
	r.Step("Pointing current at " + prev)
	if err := c.point(c.folder(prev, flavor)); err != nil {
		return "", err
	}
	msg := "Switched llama.cpp to " + prev + ". Models use it from their next load."
	if cur != "" {
		msg += " " + cur + " is kept, so you can switch back."
	}
	return msg, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// inUse reports whether a running program was started from inside dir.
// Tests replace it.
var inUse = procInUse

// procInUse reads /proc, so on systems without /proc it always answers false.
func procInUse(dir string) bool {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	exes, _ := filepath.Glob("/proc/[0-9]*/exe")
	for _, p := range exes {
		if exe, err := os.Readlink(p); err == nil && strings.HasPrefix(exe, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
