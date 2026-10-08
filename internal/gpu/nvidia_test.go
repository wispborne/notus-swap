package gpu

import "testing"

func TestParseNvidia(t *testing.T) {
	out := "0, 00000000:01:00.0, NVIDIA GeForce RTX 4090, 63.80, 450.00, 45, 23378, 24564, 16\n" +
		"1, 00000000:02:00.0, Tesla T4, [N/A], [Not Supported], 40, 100, 15360, 0\n"
	got := parseNvidia(out)
	if len(got) != 2 {
		t.Fatalf("got %d cards", len(got))
	}
	a := got[0]
	if a.Card != "nvidia0" || a.PCI != "0000:01:00.0" || a.Name != "NVIDIA GeForce RTX 4090" {
		t.Errorf("names: %+v", a)
	}
	if *a.Watts != 63.8 || *a.PowerCapW != 450 || *a.TempC != 45 || *a.BusyPercent != 16 {
		t.Errorf("readings: %v %v %v %v", *a.Watts, *a.PowerCapW, *a.TempC, *a.BusyPercent)
	}
	if *a.VRAMUsed != 23378<<20 || *a.VRAMTotal != 24564<<20 || a.Integrated {
		t.Errorf("VRAM: %v of %v, integrated %v", *a.VRAMUsed, *a.VRAMTotal, a.Integrated)
	}
	if b := got[1]; b.Watts != nil || b.PowerCapW != nil || *b.TempC != 40 {
		t.Errorf("a card without power readings: %+v", b)
	}
}
