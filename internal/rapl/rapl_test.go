package rapl

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func write(t *testing.T, path, v string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(v+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWattsFromCounter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sysfs names like intel-rapl:0 cannot be created on Windows; CI runs this on Linux")
	}
	root := t.TempDir()
	pkg := filepath.Join(root, "intel-rapl:0")
	write(t, filepath.Join(pkg, "name"), "package-0")
	write(t, filepath.Join(pkg, "max_energy_range_uj"), "1000000000")
	write(t, filepath.Join(pkg, "energy_uj"), "999000000")
	// A subzone, which must not be counted twice.
	write(t, filepath.Join(root, "intel-rapl:0:0", "name"), "core")
	write(t, filepath.Join(root, "intel-rapl:0:0", "energy_uj"), "5")

	m := NewMeter(root)
	m.Sample()
	if w, _ := m.Watts(); w != nil {
		t.Fatalf("first sample should only set a starting point, got %v", *w)
	}

	// Wrap past max_energy_range_uj: 999,000,000 -> 1,000,000,000 (=0) -> 49,000,000
	// is 50 J. Backdate the last reading by one second so the time is known.
	m.zones[0].lastAt = time.Now().Add(-time.Second)
	write(t, filepath.Join(pkg, "energy_uj"), "49000000")
	m.Sample()
	w, problem := m.Watts()
	if w == nil || *w < 45 || *w > 51 {
		t.Fatalf("watts %v, problem %q", w, problem)
	}
}

func TestNoZones(t *testing.T) {
	m := NewMeter(t.TempDir())
	m.Sample()
	if w, problem := m.Watts(); w != nil || problem == "" {
		t.Errorf("watts %v problem %q", w, problem)
	}
}
