package llamaupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wispborne/notus-swap/internal/proc"
)

// These tests run on every system, Windows included: the llama.cpp folder
// link is a symbolic link on Linux and macOS and a junction on Windows.

func zipBytes(t *testing.T, entries ...entry) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A Windows CUDA build is a .zip with its files at the top, and needs the
// CUDA runtime from a second .zip.
func TestCppInstallFromZipWithCudaRuntime(t *testing.T) {
	const flavor = "win-cuda-12.4-x64"
	server := exeName("llama-server")
	c := &LlamaCpp{Dir: t.TempDir()}
	write(t, filepath.Join(c.folder("b100", flavor), server), "server b100", 0o755)
	if err := replaceLink(c.link(), c.folder("b100", flavor)); err != nil {
		t.Fatal(err)
	}
	inUse = func(string) bool { return false }
	t.Cleanup(func() { inUse = proc.InDir })

	gh := newFakeGitHub(t)
	name := "llama-b200-bin-" + flavor + ".zip"
	gh.files[name] = zipBytes(t, entry{name: server, body: "server b200"}, entry{name: "ggml.dll", body: "ggml"})
	gh.files["cudart-llama-bin-"+flavor+".zip"] = zipBytes(t, entry{name: "cudart64_12.dll", body: "cudart"})
	gh.add(cppRepo, "b200", true, name, "cudart-llama-bin-"+flavor+".zip")

	if _, err := c.Install(context.Background(), gh.client(), "b200", &steps{}); err != nil {
		t.Fatal(err)
	}
	if cur, fl, err := c.Current(); cur != "b200" || fl != flavor {
		t.Fatalf("current is %q %q (%v)", cur, fl, err)
	}
	for file, want := range map[string]string{server: "server b200", "cudart64_12.dll": "cudart"} {
		if b, err := os.ReadFile(filepath.Join(c.link(), file)); err != nil || string(b) != want {
			t.Errorf("%s through current: %q %v", file, b, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(c.Dir, ".*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	if _, err := c.Rollback(context.Background(), &steps{}); err != nil {
		t.Fatal(err)
	}
	if cur, _, _ := c.Current(); cur != "b100" {
		t.Errorf("after roll back: current %s", cur)
	}
	if !exists(c.folder("b200", flavor)) {
		t.Error("the build rolled back from should be kept")
	}
}

func TestPointLeavesARealFolderAlone(t *testing.T) {
	c := &LlamaCpp{Dir: t.TempDir()}
	mkdir(t, c.link())
	mkdir(t, c.folder("b1", "win-cpu-x64"))
	err := c.point(c.folder("b1", "win-cpu-x64"))
	if err == nil || !strings.Contains(err.Error(), "real folder") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestUnzipRefusesPathsOutsideItsFolder(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(archive, zipBytes(t, entry{name: "../evil.txt", body: "x"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unpack(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected an error")
	}
	if exists(filepath.Join(dir, "evil.txt")) {
		t.Fatal("a file was written outside the folder")
	}
}

func TestExtractFileFromZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "llama-swap_262_windows_amd64.zip")
	if err := os.WriteFile(archive, zipBytes(t, entry{name: "README.md", body: "readme"}, entry{name: "llama-swap.exe", body: "binary"}), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "next")
	if err := extractFile(archive, "llama-swap.exe", dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "binary" {
		t.Errorf("got %q", b)
	}
}
