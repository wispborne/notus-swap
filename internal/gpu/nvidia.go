package gpu

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// nvidiaFields are the fields asked of nvidia-smi, in this order.
const nvidiaFields = "index,pci.bus_id,name,power.draw,power.limit,temperature.gpu,memory.used,memory.total,utilization.gpu"

// readNvidia reads every NVIDIA card through nvidia-smi, which works the same
// on Linux and Windows. One call takes about 50 ms.
func readNvidia(smi string) []Reading {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, smi, "--query-gpu="+nvidiaFields, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidia(string(out))
}

// parseNvidia reads nvidia-smi's CSV: one line per card, in the order of
// nvidiaFields. Memory is in MiB. A field the card doesn't report reads
// "[N/A]" or "[Not Supported]".
func parseNvidia(text string) []Reading {
	var out []Reading
	for _, line := range strings.Split(text, "\n") {
		f := strings.Split(line, ",")
		if len(f) != 9 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		r := Reading{Card: "nvidia" + f[0], PCI: nvidiaPCI(f[1]), Name: f[2]}
		r.Watts = parseNum(f[3])
		r.PowerCapW = parseNum(f[4])
		r.TempC = parseNum(f[5])
		r.VRAMUsed = asInt(scaled(parseNum(f[6]), 1<<20))
		r.VRAMTotal = asInt(scaled(parseNum(f[7]), 1<<20))
		r.BusyPercent = parseNum(f[8])
		r.Integrated = r.VRAMTotal != nil && *r.VRAMTotal < integratedBelow
		out = append(out, r)
	}
	return out
}

// nvidiaPCI turns nvidia-smi's "00000000:01:00.0" into "0000:01:00.0", the
// form Linux uses, so a card keeps one name on both.
func nvidiaPCI(s string) string {
	s = strings.ToLower(s)
	if domain, rest, ok := strings.Cut(s, ":"); ok && len(domain) == 8 {
		return domain[4:] + ":" + rest
	}
	return s
}

func parseNum(s string) *float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}
