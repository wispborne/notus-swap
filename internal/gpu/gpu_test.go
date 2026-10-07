package gpu

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCard builds a sysfs-like tree for one card under root.
func fakeCard(t *testing.T, root, name, pci string, files map[string]string) {
	t.Helper()
	dev := filepath.Join(root, name, "device")
	base := map[string]string{"vendor": "0x1002\n", "uevent": "DRIVER=amdgpu\nPCI_SLOT_NAME=" + pci + "\n"}
	for k, v := range files {
		base[k] = v
	}
	for rel, v := range base {
		p := filepath.Join(dev, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSampleTwoCards(t *testing.T) {
	root := t.TempDir()
	fakeCard(t, root, "card1", "0000:03:00.0", map[string]string{
		"hwmon/hwmon2/power1_average": "245000000\n",
		"hwmon/hwmon2/power1_cap":     "300000000\n",
		"hwmon/hwmon2/temp1_input":    "61000\n",
		"hwmon/hwmon2/temp1_label":    "edge\n",
		"hwmon/hwmon2/temp2_input":    "78000\n",
		"hwmon/hwmon2/temp2_label":    "junction\n",
		"mem_info_vram_used":          "22000000000\n",
		"mem_info_vram_total":         "34342961152\n",
		"gpu_busy_percent":            "97\n",
		"product_name":                "AMD Radeon RX 7900 XTX\n",
	})
	// Second card reports power as power1_input and has no labels.
	fakeCard(t, root, "card2", "0000:07:00.0", map[string]string{
		"hwmon/hwmon5/power1_input": "31000000\n",
		"hwmon/hwmon5/temp1_input":  "44000\n",
	})
	// Built-in graphics: 512 MB of VRAM.
	fakeCard(t, root, "card3", "0000:0e:00.0", map[string]string{
		"hwmon/hwmon6/power1_input": "9000000\n",
		"mem_info_vram_total":       "536870912\n",
	})
	// Not AMD: skipped. Connector entries: skipped.
	os.MkdirAll(filepath.Join(root, "card0", "device"), 0o755)
	os.WriteFile(filepath.Join(root, "card0", "device", "vendor"), []byte("0x10de\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "card1-DP-1"), 0o755)

	rs := NewSampler(root).Sample()
	if len(rs) != 3 {
		t.Fatalf("found %d cards: %+v", len(rs), rs)
	}
	a, b := rs[0], rs[1]
	if a.Card != "card1" || *a.Watts != 245 || *a.PowerCapW != 300 || *a.TempC != 78 || *a.VRAMUsed != 22000000000 || *a.BusyPercent != 97 || a.Name != "AMD Radeon RX 7900 XTX" {
		t.Errorf("card1: %+v", a)
	}
	if b.Card != "card2" || *b.Watts != 31 || *b.TempC != 44 || b.VRAMUsed != nil || b.Integrated {
		t.Errorf("card2: %+v", b)
	}
	if a.Integrated || !rs[2].Integrated {
		t.Errorf("integrated: card1 %v, card3 %v", a.Integrated, rs[2].Integrated)
	}
}

func TestNoSysfs(t *testing.T) {
	if rs := NewSampler(filepath.Join(t.TempDir(), "missing")).Sample(); len(rs) != 0 {
		t.Errorf("got %+v", rs)
	}
}
