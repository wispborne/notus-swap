// Package rapl reads CPU package power from the Linux powercap interface
// (RAPL), which works on AMD Zen CPUs as well as Intel.
//
// Each package zone has an energy counter in microjoules. Power is the change
// in that counter between two readings, divided by the time between them.
// The counter file is root-only by default; docs/install.md shows how
// to let notus-swap read it.
package rapl

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type zone struct {
	dir      string
	maxRange float64 // counter wraps back to 0 after this many microjoules
	last     float64
	lastAt   time.Time
	ok       bool
}

// Meter keeps the last CPU power reading.
type Meter struct {
	root string

	mu      sync.Mutex
	zones   []*zone
	watts   *float64
	problem string
}

// NewMeter reads zones under root. Pass "" for /sys/class/powercap.
func NewMeter(root string) *Meter {
	if root == "" {
		root = "/sys/class/powercap"
	}
	m := &Meter{root: root}
	dirs, _ := filepath.Glob(filepath.Join(root, "intel-rapl:*"))
	for _, d := range dirs {
		// Package zones are intel-rapl:N; subzones (intel-rapl:N:M) are parts of them.
		if strings.Count(filepath.Base(d), ":") != 1 {
			continue
		}
		if !strings.HasPrefix(read(filepath.Join(d, "name")), "package") {
			continue
		}
		z := &zone{dir: d}
		if v, err := num(filepath.Join(d, "max_energy_range_uj")); err == nil {
			z.maxRange = v
		}
		m.zones = append(m.zones, z)
	}
	if len(m.zones) == 0 {
		m.problem = "no RAPL package zone found under " + root
	}
	return m
}

// Sample reads the counters. The first call only sets a starting point.
func (m *Meter) Sample() {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total float64
	have := len(m.zones) > 0
	for _, z := range m.zones {
		now := time.Now()
		v, err := num(filepath.Join(z.dir, "energy_uj"))
		if err != nil {
			have = false
			if os.IsPermission(err) {
				m.problem = "no permission to read " + filepath.Join(z.dir, "energy_uj")
			} else {
				m.problem = err.Error()
			}
			z.ok = false
			continue
		}
		if !z.ok {
			z.last, z.lastAt, z.ok = v, now, true
			have = false
			continue
		}
		d := v - z.last
		if d < 0 { // the counter wrapped
			d += z.maxRange
		}
		secs := now.Sub(z.lastAt).Seconds()
		z.last, z.lastAt = v, now
		if secs <= 0 {
			have = false
			continue
		}
		total += d / 1e6 / secs
	}
	if have {
		m.watts, m.problem = &total, ""
	} else {
		m.watts = nil
	}
}

// Watts returns the last CPU package power, or nil with the reason.
func (m *Meter) Watts() (*float64, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watts, m.problem
}

// Run samples every interval until stop is closed.
func (m *Meter) Run(stop <-chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.Sample()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func num(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}
