package llamaupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// entry is one file in a test archive. A link is set for a symlink.
type entry struct {
	name, body, link string
	mode             int64
}

func tarGz(t *testing.T, entries ...entry) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if e.link != "" {
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.link, 0
		}
		if strings.HasSuffix(e.name, "/") {
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// fakeGitHub serves releases and their files. files maps a file name to its
// content; each release lists the files named in it.
type fakeGitHub struct {
	srv      *httptest.Server
	files    map[string][]byte
	releases map[string][]map[string]any // "owner/repo" -> newest first
	badSum   bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{files: map[string][]byte{}, releases: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) add(repo, tag string, prerelease bool, files ...string) {
	var assets []map[string]any
	for _, name := range files {
		sum := sha256.Sum256(f.files[name])
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if f.badSum {
			digest = "sha256:" + strings.Repeat("0", 64)
		}
		assets = append(assets, map[string]any{"name": name, "size": len(f.files[name]),
			"browser_download_url": f.srv.URL + "/download/" + name, "digest": digest})
	}
	rel := map[string]any{"tag_name": tag, "body": "Notes for " + tag, "html_url": "https://github.com/" + repo + "/releases/tag/" + tag,
		"published_at": "2026-09-25T10:00:00Z", "prerelease": prerelease, "assets": assets}
	f.releases[repo] = append([]map[string]any{rel}, f.releases[repo]...)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		if b, ok := f.files[name]; ok {
			w.Write(b)
			return
		}
		w.WriteHeader(404)
		return
	}
	for repo, rels := range f.releases {
		base := "/repos/" + repo + "/releases"
		switch {
		case r.URL.Path == base:
			json.NewEncoder(w).Encode(rels)
			return
		case r.URL.Path == base+"/latest":
			for _, rel := range rels {
				if !rel["prerelease"].(bool) {
					json.NewEncoder(w).Encode(rel)
					return
				}
			}
		case strings.HasPrefix(r.URL.Path, base+"/tags/"):
			for _, rel := range rels {
				if rel["tag_name"] == strings.TrimPrefix(r.URL.Path, base+"/tags/") {
					json.NewEncoder(w).Encode(rel)
					return
				}
			}
		}
	}
	w.WriteHeader(404)
}

func (f *fakeGitHub) client() *GitHub { return &GitHub{API: f.srv.URL, Client: f.srv.Client()} }

type steps []string

func (s *steps) Step(text string)        { *s = append(*s, text) }
func (s *steps) Bytes(done, total int64) {}

func mkdir(t *testing.T, path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func needSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows can't rename a link over a folder link; CI runs this on Linux")
	}
}

// fakeSwapBinary is a shell script that answers -version with version, and
// -validate with success unless the config holds "bad".
func fakeSwapBinary(version string) string {
	return "#!/bin/sh\ncase \"$1\" in\n  -version) echo 'version: " + version + " (abc1234), built at 2026-09-25T00:00:00Z' ;;\n" +
		"  -validate) if grep -q bad \"$3\"; then echo 'model bad has no cmd'; exit 1; fi ;;\nesac\n"
}

// swapSetup makes a llama-swap folder running v255, a fake GitHub with
// v258, and a fake llama-swap that answers /api/version with *running.
func swapSetup(t *testing.T) (*LlamaSwap, *fakeGitHub, *string, *int) {
	if runtime.GOOS == "windows" {
		t.Skip("uses shell scripts as fake llama-swap binaries; CI runs this on Linux")
	}
	dir := t.TempDir()
	s := &LlamaSwap{Bin: filepath.Join(dir, "llama-swap"), Config: filepath.Join(dir, "config.yaml"), Unit: "llama-swap.service"}
	write(t, s.Bin, fakeSwapBinary("v255"), 0o755)
	write(t, s.Config, "models: {}\n", 0o644)

	gh := newFakeGitHub(t)
	gh.files[swapAsset("v258")] = tarGz(t, entry{name: "README.md", body: "readme"}, entry{name: "llama-swap", body: fakeSwapBinary("v258"), mode: 0o755})
	gh.add(swapRepo, "v258", false, swapAsset("v258"))

	running := "v255"
	restarts := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"version":%q}`, running)
	}))
	t.Cleanup(up.Close)
	s.Upstream = up.URL
	s.Restart = func(ctx context.Context, unit string) error {
		restarts++
		running = s.Installed()
		return nil
	}
	swapWait = 2 * time.Second
	t.Cleanup(func() { swapWait = time.Minute })
	return s, gh, &running, &restarts
}

func TestSwapInstall(t *testing.T) {
	s, gh, _, restarts := swapSetup(t)
	var st steps
	msg, err := s.Install(context.Background(), gh.client(), "v258", &st)
	if err != nil {
		t.Fatalf("install: %v (steps %v)", err, st)
	}
	if s.Installed() != "v258" || s.Backup() != "v255" || *restarts != 1 {
		t.Errorf("installed %q, backup %q, restarts %d", s.Installed(), s.Backup(), *restarts)
	}
	if !strings.Contains(msg, "v255 is kept as llama-swap.bak") {
		t.Errorf("message %q", msg)
	}

	msg, err = s.Rollback(context.Background(), &st)
	if err != nil || s.Installed() != "v255" || s.Backup() != "v258" || *restarts != 2 {
		t.Errorf("rollback: %q %v; installed %q, backup %q", msg, err, s.Installed(), s.Backup())
	}
}

func TestSwapInstallRefusesAConfigTheNewVersionRejects(t *testing.T) {
	s, gh, _, restarts := swapSetup(t)
	write(t, s.Config, "models: {bad: {}}\n", 0o644)
	_, err := s.Install(context.Background(), gh.client(), "v258", &steps{})
	if err == nil || !strings.Contains(err.Error(), "model bad has no cmd") {
		t.Fatalf("expected the config check to fail, got %v", err)
	}
	if s.Installed() != "v255" || s.HasBackup() || *restarts != 0 {
		t.Errorf("nothing should change: installed %q, backup %v, restarts %d", s.Installed(), s.HasBackup(), *restarts)
	}
}

func TestSwapInstallPutsTheOldVersionBackWhenTheNewOneDoesNotAnswer(t *testing.T) {
	s, gh, running, restarts := swapSetup(t)
	s.Restart = func(ctx context.Context, unit string) error {
		*restarts++
		*running = "v255" // the new version never comes up
		return nil
	}
	_, err := s.Install(context.Background(), gh.client(), "v258", &steps{})
	if err == nil || !strings.Contains(err.Error(), "v255 was put back") {
		t.Fatalf("expected a roll back, got %v", err)
	}
	if s.Installed() != "v255" || *restarts != 2 {
		t.Errorf("installed %q, restarts %d", s.Installed(), *restarts)
	}
}

func TestSwapInstallChecksTheDownload(t *testing.T) {
	s, gh, _, _ := swapSetup(t)
	gh.badSum = true
	gh.releases = map[string][]map[string]any{}
	gh.add(swapRepo, "v258", false, swapAsset("v258"))
	_, err := s.Install(context.Background(), gh.client(), "v258", &steps{})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum failure, got %v", err)
	}
	if s.Installed() != "v255" {
		t.Errorf("installed %q", s.Installed())
	}
}

const flavor = "ubuntu-rocm-10.0-x64"

// cppSetup makes a llama.cpp folder with builds b50 and b100, current
// pointing at b100, and a fake GitHub with build b200 and weekly v0.5.0.
func cppSetup(t *testing.T) (*LlamaCpp, *fakeGitHub) {
	needSymlinks(t)
	c := &LlamaCpp{Dir: t.TempDir()}
	for _, b := range []string{"b50", "b100"} {
		write(t, filepath.Join(c.folder(b, flavor), "llama-server"), "server "+b, 0o755)
	}
	if err := os.Symlink(filepath.Base(c.folder("b100", flavor)), c.link()); err != nil {
		t.Fatal(err)
	}
	gh := newFakeGitHub(t)
	name := "llama-b200-bin-" + flavor + ".tar.gz"
	gh.files[name] = tarGz(t,
		entry{name: "llama-b200/"},
		entry{name: "llama-b200/llama-server", body: "server b200", mode: 0o755},
		entry{name: "llama-b200/libllama.so.0.5.0", body: "lib"},
		entry{name: "llama-b200/libllama.so.0", link: "libllama.so.0.5.0"})
	gh.files["llama-b200-bin-ubuntu-vulkan-x64.tar.gz"] = []byte("x")
	gh.files["nightly-tag.txt"] = []byte("b200\n")
	gh.add(cppRepo, "b200", true, name, "llama-b200-bin-ubuntu-vulkan-x64.tar.gz")
	gh.add(cppRepo, "v0.5.0", false, "nightly-tag.txt")
	gh.add(cppRepo, "b201", true)
	return c, gh
}

func TestCppInstall(t *testing.T) {
	c, gh := cppSetup(t)
	inUse = func(string) bool { return false }
	t.Cleanup(func() { inUse = procInUse })

	msg, err := c.Install(context.Background(), gh.client(), "b200", &steps{})
	if err != nil {
		t.Fatal(err)
	}
	if cur, fl, _ := c.Current(); cur != "b200" || fl != flavor {
		t.Errorf("current is %s %s", cur, fl)
	}
	if b, err := os.ReadFile(filepath.Join(c.link(), "llama-server")); err != nil || string(b) != "server b200" {
		t.Errorf("llama-server through current: %q %v", b, err)
	}
	if target, err := os.Readlink(filepath.Join(c.folder("b200", flavor), "libllama.so.0")); err != nil || target != "libllama.so.0.5.0" {
		t.Errorf("symlink inside the build: %q %v", target, err)
	}
	if exists(c.folder("b50", flavor)) || !exists(c.folder("b100", flavor)) {
		t.Errorf("b50 should be deleted and b100 kept: %v", c.builds(flavor))
	}
	if !strings.Contains(msg, "b100 is kept") || !strings.Contains(msg, "Deleted older builds: b50") {
		t.Errorf("message %q", msg)
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, ".*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	// Roll back, then roll back again to return.
	if _, err := c.Rollback(context.Background(), &steps{}); err != nil {
		t.Fatal(err)
	}
	if cur, _, _ := c.Current(); cur != "b100" || c.Previous() != "b200" {
		t.Errorf("after roll back: current %s, previous %s", cur, c.Previous())
	}
	c.Rollback(context.Background(), &steps{})
	if cur, _, _ := c.Current(); cur != "b200" {
		t.Errorf("after second roll back: current %s", cur)
	}
}

func TestCppInstallKeepsBuildsInUse(t *testing.T) {
	c, gh := cppSetup(t)
	inUse = func(dir string) bool { return strings.Contains(dir, "b50") }
	t.Cleanup(func() { inUse = procInUse })
	msg, err := c.Install(context.Background(), gh.client(), "b200", &steps{})
	if err != nil || !exists(c.folder("b50", flavor)) || !strings.Contains(msg, "Kept b50") {
		t.Errorf("%q %v", msg, err)
	}
}

func TestCppInstallNamesOtherFlavors(t *testing.T) {
	c, gh := cppSetup(t)
	c.Flavor = "ubuntu-cuda-13.4-x64"
	_, err := c.Install(context.Background(), gh.client(), "b200", &steps{})
	if err == nil || !strings.Contains(err.Error(), "It has: "+flavor+", ubuntu-vulkan-x64") {
		t.Errorf("got %v", err)
	}
}

func TestCppFlavorNeededWithoutCurrent(t *testing.T) {
	c := &LlamaCpp{Dir: t.TempDir()}
	if _, err := c.Install(context.Background(), &GitHub{}, "b200", &steps{}); err == nil || !strings.Contains(err.Error(), "NOTUS_LLAMA_CPP_FLAVOR") {
		t.Errorf("got %v", err)
	}
}

func TestCheckAndStatus(t *testing.T) {
	c, gh := cppSetup(t)
	gh.files[swapAsset("v258")] = []byte("x")
	gh.add(swapRepo, "v258", false, swapAsset("v258"))
	m := &Manager{GitHub: gh.client(), Cpp: c}
	if m.UpdateAvailable() {
		t.Error("nothing is known before the first check")
	}
	m.Check(context.Background())
	s := m.Status().LlamaCpp
	if s.CheckError != "" || s.Installed != "b100" || s.Release.Tag != "v0.5.0" || s.ReleaseBuild != "b200" ||
		s.Nightly.Tag != "b201" || s.Previous != "b50" || s.Flavor != flavor || !s.Newer {
		t.Errorf("status %+v", s)
	}
	if !m.UpdateAvailable() {
		t.Error("b200 is newer than b100")
	}
	if off := m.Status().LlamaSwap.Off; !strings.Contains(off, "NOTUS_LLAMA_SWAP_BIN") {
		t.Errorf("llama-swap should be off: %q", off)
	}
}

func TestStartRunsOneJobAtATime(t *testing.T) {
	c, gh := cppSetup(t)
	inUse = func(string) bool { return false }
	t.Cleanup(func() { inUse = procInUse })
	m := &Manager{GitHub: gh.client(), Cpp: c}
	m.Check(context.Background())
	if err := m.Start(ComponentLlamaCpp, "install", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ComponentLlamaCpp, "rollback", ""); err != ErrBusy {
		t.Errorf("second job: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().Job.Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	j := m.Status().Job
	if j.Running || j.Error != "" || j.Target != "b200" || !strings.HasPrefix(j.Result, "Installed llama.cpp b200") {
		t.Errorf("job %+v", j)
	}
	if err := m.Start(ComponentLlamaSwap, "install", "v258"); err == nil || !strings.Contains(err.Error(), "off") {
		t.Errorf("llama-swap is off: %v", err)
	}
}

func TestUntarRefusesPathsOutside(t *testing.T) {
	for _, e := range []entry{
		{name: "../evil", body: "x"},
		{name: "ok/link", link: "../../evil"},
		{name: "abs", link: "/etc/passwd"},
	} {
		dir := t.TempDir()
		archive := filepath.Join(dir, "a.tar.gz")
		write(t, archive, string(tarGz(t, e)), 0o644)
		if err := untar(archive, filepath.Join(dir, "out")); err == nil {
			t.Errorf("%s -> %s: expected an error", e.name, e.link)
		}
	}
}

func TestRateLimitMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1790000000")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	_, err := (&GitHub{API: srv.URL}).Latest(context.Background(), swapRepo)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("got %v", err)
	}
}

func TestChangelog(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.add(swapRepo, "v257", false)
	gh.add(swapRepo, "v258-rc1", true)
	gh.add(swapRepo, "v258", false)
	gh.add(cppRepo, "b200", true)
	gh.releases[cppRepo][0]["body"] = "<details open>\n\nHIP: faster kernels (#123)\n\n* more detail\n\n</details>\n\n**Website:**\n- <https://llama.app>"
	gh.add(cppRepo, "v0.5.0", false)
	m := &Manager{GitHub: gh.client(), Swap: &LlamaSwap{}, Cpp: &LlamaCpp{}}

	rs, err := m.Changelog(context.Background(), ComponentLlamaSwap)
	if err != nil || len(rs) != 2 || rs[0].Tag != "v258" || rs[1].Tag != "v257" || rs[0].Notes != "Notes for v258" {
		t.Errorf("llama-swap changelog %+v, %v", rs, err)
	}
	rs, err = m.Changelog(context.Background(), ComponentLlamaCpp)
	if err != nil || len(rs) != 2 || rs[0].Notes != "Notes for v0.5.0" || rs[1].Notes != "HIP: faster kernels (#123)" {
		t.Errorf("llama.cpp changelog %+v, %v", rs, err)
	}

	gh.add(swapRepo, "v259", false)
	if rs, _ := m.Changelog(context.Background(), ComponentLlamaSwap); rs[0].Tag != "v258" {
		t.Error("the changelog should come from the cache for a while")
	}
	if _, err := (&Manager{GitHub: gh.client()}).Changelog(context.Background(), ComponentLlamaSwap); err == nil {
		t.Error("llama-swap updates are off, so there is no changelog")
	}
}
