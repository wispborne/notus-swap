package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("MemTotal:        8192 kB\nMemFree: 1024 kB\nMemAvailable:    4096 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := InspectFile(path)
	if in.Source != "meminfo" || in.Lines["MemTotal"] != "8192 kB" || in.Lines["MemAvailable"] != "4096 kB" || len(in.Lines) != 2 {
		t.Errorf("got %+v", in)
	}
	if in.Used == nil || *in.Used != 4096*1024 || in.Error != "" {
		t.Errorf("used %v, error %q", in.Used, in.Error)
	}
	if in := InspectFile(filepath.Join(t.TempDir(), "missing")); in.Error == "" || in.Used != nil {
		t.Errorf("missing file: %+v", in)
	}
}
