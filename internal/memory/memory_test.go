package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("MemTotal: 8192 kB\nMemFree: 1024 kB\nCached: 2048 kB\nMemAvailable: 4096 kB\n")
	used, total := Read(path)
	if used == nil || total == nil || *used != 4096*1024 || *total != 8192*1024 {
		t.Fatalf("used %v total %v", used, total)
	}
	write("MemTotal: 8192 kB\nMemFree: 1024 kB\n")
	if used, total := Read(path); used != nil || total != nil {
		t.Fatalf("missing available memory: %v %v", used, total)
	}
	write("MemTotal: 8192 kB\nMemAvailable: 9000 kB\n")
	if used, total := Read(path); used != nil || total != nil {
		t.Fatalf("impossible reading: %v %v", used, total)
	}
}
