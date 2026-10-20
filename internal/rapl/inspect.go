package rapl

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Inspection is what the meter sees right now, with the raw counter files.
type Inspection struct {
	Root string `json:"root"`
	OS   string `json:"os"`
	// Zones lists every intel-rapl:* folder, subzones included.
	Zones []ZoneDetail `json:"zones"`
	// Watts and Problem are the meter's last reading, when there is a meter.
	Watts   *float64 `json:"watts"`
	Problem string   `json:"problem,omitempty"`
}

// ZoneDetail is one intel-rapl:* folder.
type ZoneDetail struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Counted is set for package zones, the ones the meter adds up.
	Counted        bool   `json:"counted"`
	EnergyUJ       string `json:"energy_uj,omitempty"`
	EnergyError    string `json:"energy_error,omitempty"`
	MaxEnergyRange string `json:"max_energy_range_uj,omitempty"`
	// Readable reports whether energy_uj could be read. It is root-only
	// unless the udev rule in docs/install.md is installed.
	Readable bool `json:"readable"`
}

// Inspect reads every zone under root once. Pass "" for /sys/class/powercap.
func Inspect(root string) Inspection {
	if root == "" {
		root = "/sys/class/powercap"
	}
	in := Inspection{Root: root, OS: runtime.GOOS, Zones: []ZoneDetail{}}
	dirs, _ := filepath.Glob(filepath.Join(root, "intel-rapl:*"))
	for _, d := range dirs {
		z := ZoneDetail{Path: d, Name: read(filepath.Join(d, "name"))}
		z.Counted = strings.Count(filepath.Base(d), ":") == 1 && strings.HasPrefix(z.Name, "package")
		z.MaxEnergyRange = read(filepath.Join(d, "max_energy_range_uj"))
		b, err := os.ReadFile(filepath.Join(d, "energy_uj"))
		if err != nil {
			z.EnergyError = err.Error()
		} else {
			z.EnergyUJ, z.Readable = strings.TrimSpace(string(b)), true
		}
		in.Zones = append(in.Zones, z)
	}
	return in
}

// Inspect reads the meter's zones once, and adds its last reading.
func (m *Meter) Inspect() Inspection {
	in := Inspect(m.root)
	in.Watts, in.Problem = m.Watts()
	return in
}
