// Package gpu reads AMD GPU sensors from sysfs. It handles any number of
// cards. On machines without AMD cards (or without sysfs, like Windows) it
// finds no cards and every reading is empty.
package gpu

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reading is one card's sensors at one moment. A nil field means the card
// does not report it.
type Reading struct {
	Card        string   `json:"card"`          // "card1"
	PCI         string   `json:"pci,omitempty"` // "0000:03:00.0"
	Name        string   `json:"name,omitempty"`
	Watts       *float64 `json:"watts"`
	PowerCapW   *float64 `json:"power_cap_watts,omitempty"`
	TempC       *float64 `json:"temp_c"` // junction (hotspot) if available, else edge
	VRAMUsed    *int64   `json:"vram_used"`
	VRAMTotal   *int64   `json:"vram_total"`
	BusyPercent *float64 `json:"busy_percent"`
	// Integrated is set for a GPU built into the CPU (such as a Ryzen's).
	// Totals leave these out.
	Integrated bool `json:"integrated"`
	// PowerLabel is the power sensor's label. On AMD built-in graphics it is
	// "PPT", meaning the reading covers the whole CPU socket, not just the
	// graphics.
	PowerLabel string `json:"power_label,omitempty"`
}

// SocketPower reports whether this card's power reading is really the whole
// CPU socket (AMD built-in graphics with a PPT sensor).
func (r Reading) SocketPower() bool { return r.Integrated && r.PowerLabel == "PPT" }

// integratedBelow is the VRAM size under which a card counts as built-in
// graphics. Those get a small slice of system memory (usually 512 MB-2 GB);
// the cards notus-swap cares about have far more.
const integratedBelow = 4 << 30

type card struct {
	name  string
	dev   string // .../cardN/device
	hwmon string // .../device/hwmon/hwmonM, may be ""
	pci   string
	label string
}

// Find lists the AMD cards under root, normally /sys/class/drm.
func find(root string) []card {
	matches, _ := filepath.Glob(filepath.Join(root, "card*"))
	var cards []card
	for _, m := range matches {
		name := filepath.Base(m)
		if strings.Contains(name, "-") { // connectors such as card1-DP-1
			continue
		}
		dev := filepath.Join(m, "device")
		if strings.TrimSpace(readString(filepath.Join(dev, "vendor"))) != "0x1002" {
			continue
		}
		c := card{name: name, dev: dev, label: strings.TrimSpace(readString(filepath.Join(dev, "product_name")))}
		if hw, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*")); len(hw) > 0 {
			c.hwmon = hw[0]
		}
		for _, line := range strings.Split(readString(filepath.Join(dev, "uevent")), "\n") {
			if v, ok := strings.CutPrefix(line, "PCI_SLOT_NAME="); ok {
				c.pci = strings.TrimSpace(v)
			}
		}
		cards = append(cards, c)
	}
	sort.Slice(cards, func(i, j int) bool {
		return cards[i].pci < cards[j].pci || (cards[i].pci == cards[j].pci && cards[i].name < cards[j].name)
	})
	return cards
}

func (c card) read() Reading {
	r := Reading{Card: c.name, PCI: c.pci, Name: c.label}
	if c.hwmon != "" {
		// power1_average on most cards, power1_input on some newer ones. Microwatts.
		r.Watts = scaled(readNum(filepath.Join(c.hwmon, "power1_average")), 1e-6)
		if r.Watts == nil {
			r.Watts = scaled(readNum(filepath.Join(c.hwmon, "power1_input")), 1e-6)
		}
		r.PowerCapW = scaled(readNum(filepath.Join(c.hwmon, "power1_cap")), 1e-6)
		r.PowerLabel = strings.TrimSpace(readString(filepath.Join(c.hwmon, "power1_label")))
		r.TempC = c.temp()
	}
	r.VRAMUsed = asInt(readNum(filepath.Join(c.dev, "mem_info_vram_used")))
	r.VRAMTotal = asInt(readNum(filepath.Join(c.dev, "mem_info_vram_total")))
	r.BusyPercent = readNum(filepath.Join(c.dev, "gpu_busy_percent"))
	r.Integrated = r.VRAMTotal != nil && *r.VRAMTotal < integratedBelow
	return r
}

// temp prefers the "junction" sensor, then "edge", then temp1. Millidegrees.
func (c card) temp() *float64 {
	var edge, first *float64
	labels, _ := filepath.Glob(filepath.Join(c.hwmon, "temp*_label"))
	for _, l := range labels {
		v := scaled(readNum(strings.TrimSuffix(l, "_label")+"_input"), 1e-3)
		switch strings.TrimSpace(readString(l)) {
		case "junction":
			return v
		case "edge":
			edge = v
		}
	}
	if edge != nil {
		return edge
	}
	first = scaled(readNum(filepath.Join(c.hwmon, "temp1_input")), 1e-3)
	return first
}

// Sampler reads every card once a second and keeps the latest readings.
type Sampler struct {
	root string

	mu     sync.Mutex
	cards  []card
	latest []Reading
	at     time.Time
}

// NewSampler reads cards under root. Pass "" for /sys/class/drm.
func NewSampler(root string) *Sampler {
	if root == "" {
		root = "/sys/class/drm"
	}
	return &Sampler{root: root}
}

// Sample reads all cards now. Cards are looked up again each minute, so a
// card that appears later (or a driver reload) is picked up.
func (s *Sampler) Sample() []Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cards == nil || time.Since(s.at) > time.Minute {
		s.cards = find(s.root)
		s.at = time.Now()
	}
	out := make([]Reading, len(s.cards))
	for i, c := range s.cards {
		out[i] = c.read()
	}
	s.latest = out
	return out
}

// Latest returns the readings from the last Sample.
func (s *Sampler) Latest() []Reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Reading(nil), s.latest...)
}

// Run samples every interval until stop is closed. onSample, if set, gets
// each set of readings (the Dashboard will store them).
func (s *Sampler) Run(stop <-chan struct{}, interval time.Duration, onSample func(time.Time, []Reading)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		now := time.Now()
		r := s.Sample()
		if onSample != nil {
			onSample(now, r)
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readNum(path string) *float64 {
	s := strings.TrimSpace(readString(path))
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func scaled(v *float64, f float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v * f
	return &x
}

func asInt(v *float64) *int64 {
	if v == nil {
		return nil
	}
	x := int64(*v)
	return &x
}
