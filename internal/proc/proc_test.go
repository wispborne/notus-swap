package proc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUnderFindsThisProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(Under(exe), os.Getpid()) {
		t.Fatalf("%s isn't listed as running", exe)
	}
	if !InDir(filepath.Dir(exe)) {
		t.Fatalf("%s isn't counted as in use", filepath.Dir(exe))
	}
	if InDir(t.TempDir()) {
		t.Fatal("an empty folder is counted as in use")
	}
}
