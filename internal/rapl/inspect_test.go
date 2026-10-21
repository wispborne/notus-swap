package rapl

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestInspect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sysfs names like intel-rapl:0 cannot be created on Windows; CI runs this on Linux")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "intel-rapl:0", "name"), "package-0")
	write(t, filepath.Join(root, "intel-rapl:0", "energy_uj"), "12345")
	write(t, filepath.Join(root, "intel-rapl:0", "max_energy_range_uj"), "1000000000")
	// A subzone, and a package zone with no counter file.
	write(t, filepath.Join(root, "intel-rapl:0:0", "name"), "core")
	write(t, filepath.Join(root, "intel-rapl:1", "name"), "package-1")

	in := NewMeter(root).Inspect()
	if len(in.Zones) != 3 {
		t.Fatalf("zones: %+v", in.Zones)
	}
	z := map[string]ZoneDetail{}
	for _, d := range in.Zones {
		z[filepath.Base(d.Path)] = d
	}
	if d := z["intel-rapl:0"]; !d.Counted || !d.Readable || d.EnergyUJ != "12345" || d.MaxEnergyRange != "1000000000" {
		t.Errorf("package zone: %+v", d)
	}
	if d := z["intel-rapl:0:0"]; d.Counted || d.Name != "core" {
		t.Errorf("subzone: %+v", d)
	}
	if d := z["intel-rapl:1"]; !d.Counted || d.Readable || d.EnergyError == "" {
		t.Errorf("zone without a counter: %+v", d)
	}
}

func TestInspectNoZones(t *testing.T) {
	in := NewMeter(t.TempDir()).Inspect()
	if len(in.Zones) != 0 || in.Problem == "" || in.Watts != nil {
		t.Errorf("got %+v", in)
	}
}
