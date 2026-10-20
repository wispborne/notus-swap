package gpu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Inspection is what the sampler sees right now, with the raw files and
// output behind each reading. It is for checking new hardware.
type Inspection struct {
	Root   string       `json:"root"`
	Cards  []CardDetail `json:"cards"`
	Nvidia NvidiaDetail `json:"nvidia"`
}

// CardDetail is one card* entry under the DRM folder.
type CardDetail struct {
	Path string `json:"path"`
	// Skipped says why the entry was not read, such as a connector or a card
	// that isn't AMD. Empty for cards that were read.
	Skipped string `json:"skipped,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
	PCI     string `json:"pci,omitempty"`
	// Device is the raw text of each file read from the card's device
	// folder. DeviceErrors holds the files that couldn't be read.
	Device       map[string]string `json:"device,omitempty"`
	DeviceErrors map[string]string `json:"device_errors,omitempty"`
	// Hwmon lists every hwmon folder. The sampler reads the first.
	Hwmon []HwmonDetail `json:"hwmon,omitempty"`
	// Kind is "dedicated", "integrated" or "socket_power", and Why says how
	// that was decided.
	Kind    string   `json:"kind,omitempty"`
	Why     string   `json:"why,omitempty"`
	Reading *Reading `json:"reading,omitempty"`
}

// HwmonDetail is one hwmon folder of a card.
type HwmonDetail struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"` // the hwmon "name" file, such as amdgpu
	Used bool   `json:"used"`
	// Files is every file in the folder except the _label files. Label is
	// the matching <sensor>_label file, such as "PPT" for power1_input.
	Files []HwmonFile `json:"files"`
}

type HwmonFile struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
	Error string `json:"error,omitempty"`
}

// NvidiaDetail is the nvidia-smi call and its raw output.
type NvidiaDetail struct {
	// Program is the setting; Found is where it was found on this machine.
	Program string    `json:"program"`
	Found   string    `json:"found,omitempty"`
	Args    []string  `json:"args,omitempty"`
	Output  string    `json:"output,omitempty"`
	Error   string    `json:"error,omitempty"`
	Cards   []Reading `json:"cards,omitempty"`
}

// deviceFiles are the files read from a card's device folder.
var deviceFiles = []string{"vendor", "product_name", "uevent", "mem_info_vram_used", "mem_info_vram_total", "gpu_busy_percent"}

// Inspect reads every card under root and runs nvidia-smi once, the same
// way Sample does, and keeps the raw values. nvidiaSMI "" skips nvidia-smi.
func Inspect(root, nvidiaSMI string) Inspection {
	if root == "" {
		root = "/sys/class/drm"
	}
	in := Inspection{Root: root, Cards: []CardDetail{}}
	matches, _ := filepath.Glob(filepath.Join(root, "card*"))
	sort.Strings(matches)
	for _, m := range matches {
		in.Cards = append(in.Cards, inspectCard(m))
	}
	in.Nvidia = inspectNvidia(nvidiaSMI)
	return in
}

// Inspect reads what this sampler reads, with the raw values.
func (s *Sampler) Inspect() Inspection { return Inspect(s.root, s.NvidiaSMI) }

func inspectCard(path string) CardDetail {
	d := CardDetail{Path: path}
	name := filepath.Base(path)
	if strings.Contains(name, "-") {
		d.Skipped = "a display connector, not a card"
		return d
	}
	dev := filepath.Join(path, "device")
	d.Vendor = strings.TrimSpace(readString(filepath.Join(dev, "vendor")))
	if d.Vendor != "0x1002" {
		d.Skipped = "vendor is not AMD (0x1002)"
		if d.Vendor == "" {
			d.Skipped = "no device/vendor file"
		}
		return d
	}
	d.Device, d.DeviceErrors = map[string]string{}, map[string]string{}
	for _, f := range deviceFiles {
		b, err := os.ReadFile(filepath.Join(dev, f))
		if err != nil {
			d.DeviceErrors[f] = errText(err)
			continue
		}
		d.Device[f] = strings.TrimSpace(string(b))
	}
	// find picks the card up the same way, so read it through card.
	c := card{name: name, dev: dev, label: d.Device["product_name"]}
	hws, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*"))
	sort.Strings(hws)
	for i, hw := range hws {
		if i == 0 {
			c.hwmon = hw
		}
		d.Hwmon = append(d.Hwmon, inspectHwmon(hw, i == 0))
	}
	for _, line := range strings.Split(readString(filepath.Join(dev, "uevent")), "\n") {
		if v, ok := strings.CutPrefix(line, "PCI_SLOT_NAME="); ok {
			c.pci = strings.TrimSpace(v)
		}
	}
	d.PCI = c.pci
	r := c.read()
	d.Reading = &r
	d.Kind, d.Why = kind(r)
	return d
}

// kind says how the sampler counts a card, and why.
func kind(r Reading) (string, string) {
	if r.VRAMTotal == nil {
		return "dedicated", "no mem_info_vram_total, so it can't be told apart from a dedicated card; it counts toward the GPU totals"
	}
	vram := fmt.Sprintf("VRAM total is %.2f GiB", float64(*r.VRAMTotal)/(1<<30))
	switch {
	case r.SocketPower():
		return "socket_power", vram + ", under 4 GiB, so it is built-in graphics; power1_label is PPT, so its power is the whole CPU socket. It is left out of the GPU totals and used as the CPU reading when RAPL can't be read"
	case r.Integrated:
		return "integrated", vram + ", under 4 GiB, so it is built-in graphics. It is left out of the dedicated GPU total and shown as iGPU"
	}
	return "dedicated", vram + ", 4 GiB or more, so it counts toward the GPU totals"
}

func inspectHwmon(dir string, used bool) HwmonDetail {
	h := HwmonDetail{Path: dir, Used: used, Name: strings.TrimSpace(readString(filepath.Join(dir, "name"))), Files: []HwmonFile{}}
	entries, _ := os.ReadDir(dir)
	labels := map[string]string{}
	for _, e := range entries {
		if sensor, ok := strings.CutSuffix(e.Name(), "_label"); ok {
			labels[sensor] = strings.TrimSpace(readString(filepath.Join(dir, e.Name())))
		}
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "name" || n == "uevent" || strings.HasSuffix(n, "_label") {
			continue
		}
		// Links such as "device" and "subsystem" point at folders.
		if info, err := os.Stat(filepath.Join(dir, n)); err != nil || info.IsDir() {
			continue
		}
		f := HwmonFile{Name: n}
		if sensor, _, ok := strings.Cut(n, "_"); ok {
			f.Label = labels[sensor]
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			f.Error = errText(err)
		} else {
			f.Value = strings.TrimSpace(string(b))
		}
		h.Files = append(h.Files, f)
	}
	return h
}

func inspectNvidia(program string) NvidiaDetail {
	d := NvidiaDetail{Program: program}
	if program == "" {
		d.Error = "turned off (the nvidia-smi setting is empty)"
		return d
	}
	found, err := exec.LookPath(program)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.Found = found
	d.Args = []string{"--query-gpu=" + nvidiaFields, "--format=csv,noheader,nounits"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, found, d.Args...).CombinedOutput()
	d.Output = string(out)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.Cards = parseNvidia(d.Output)
	return d
}

// errText shortens a file error to its cause, such as "permission denied".
func errText(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}
