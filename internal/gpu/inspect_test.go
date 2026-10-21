package gpu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspect(t *testing.T) {
	root := t.TempDir()
	fakeCard(t, root, "card0", "0000:03:00.0", map[string]string{
		"hwmon/hwmon2/name":           "amdgpu\n",
		"hwmon/hwmon2/power1_average": "245000000\n",
		"hwmon/hwmon2/temp2_input":    "78000\n",
		"hwmon/hwmon2/temp2_label":    "junction\n",
		"mem_info_vram_total":         "34342961152\n",
	})
	// Built-in graphics whose power sensor reads the whole CPU socket.
	fakeCard(t, root, "card1", "0000:0e:00.0", map[string]string{
		"hwmon/hwmon6/power1_input": "62000000\n",
		"hwmon/hwmon6/power1_label": "PPT\n",
		"mem_info_vram_total":       "536870912\n",
	})
	// Built-in graphics without a PPT label.
	fakeCard(t, root, "card2", "0000:0f:00.0", map[string]string{"mem_info_vram_total": "536870912\n"})
	// No VRAM total at all.
	fakeCard(t, root, "card3", "0000:10:00.0", nil)
	os.MkdirAll(filepath.Join(root, "card4", "device"), 0o755)
	os.WriteFile(filepath.Join(root, "card4", "device", "vendor"), []byte("0x10de\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "card0-DP-1"), 0o755)

	in := Inspect(root, "")
	if len(in.Cards) != 6 {
		t.Fatalf("found %d entries: %+v", len(in.Cards), in.Cards)
	}
	byPath := map[string]CardDetail{}
	for _, c := range in.Cards {
		byPath[filepath.Base(c.Path)] = c
	}
	for name, want := range map[string]string{"card0": "dedicated", "card1": "socket_power", "card2": "integrated", "card3": "dedicated"} {
		if c := byPath[name]; c.Kind != want || c.Why == "" || c.Skipped != "" {
			t.Errorf("%s: kind %q, why %q, skipped %q; want %s", name, c.Kind, c.Why, c.Skipped, want)
		}
	}
	if byPath["card4"].Skipped == "" || byPath["card4"].Vendor != "0x10de" || byPath["card0-DP-1"].Skipped == "" {
		t.Errorf("skipped entries: %+v %+v", byPath["card4"], byPath["card0-DP-1"])
	}

	c0 := byPath["card0"]
	if c0.PCI != "0000:03:00.0" || c0.Device["mem_info_vram_total"] != "34342961152" || c0.DeviceErrors["gpu_busy_percent"] == "" {
		t.Errorf("card0 device files: %+v %+v", c0.Device, c0.DeviceErrors)
	}
	if len(c0.Hwmon) != 1 || !c0.Hwmon[0].Used || c0.Hwmon[0].Name != "amdgpu" {
		t.Fatalf("card0 hwmon: %+v", c0.Hwmon)
	}
	files := map[string]HwmonFile{}
	for _, f := range c0.Hwmon[0].Files {
		files[f.Name] = f
	}
	if f := files["temp2_input"]; f.Value != "78000" || f.Label != "junction" {
		t.Errorf("temp2_input: %+v", f)
	}
	if _, ok := files["temp2_label"]; ok {
		t.Error("label files should only show as labels")
	}
	if c0.Reading == nil || *c0.Reading.Watts != 245 {
		t.Errorf("card0 reading: %+v", c0.Reading)
	}
	if c1 := byPath["card1"]; c1.Hwmon[0].Files[0].Label != "PPT" {
		t.Errorf("card1 files: %+v", c1.Hwmon[0].Files)
	}

	if in.Nvidia.Error == "" || in.Nvidia.Found != "" {
		t.Errorf("nvidia-smi turned off: %+v", in.Nvidia)
	}
	if n := Inspect(root, "no-such-nvidia-smi-program").Nvidia; n.Error == "" || n.Output != "" {
		t.Errorf("missing nvidia-smi: %+v", n)
	}
}
