package llamaswap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Strata is what a Strata server reports on its /metrics, in JSON: its
// engine's settings and its own record of each recent request.
type Strata struct {
	// Engine is the settings the engine runs with, such as "kv": "int8".
	Engine map[string]any
	// Requests is the newest finished requests, newest first. Strata keeps
	// the last 12 in /metrics.
	Requests []StrataRequest
}

// StrataRequest is Strata's record of one finished request. Fields Strata
// leaves out, as an older version may, are nil.
type StrataRequest struct {
	ID           string  `json:"id"`
	Time         float64 `json:"time"` // when Strata started on it, unix seconds
	DurationS    float64 `json:"duration_s"`
	PromptTokens int64   `json:"prompt_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	// FirstTokenS is the time to the first token on Strata's own clock.
	FirstTokenS *float64 `json:"first_engine_token_s"`
	// HitRate is the share of expert lookups found in VRAM. The rest were
	// read from RAM.
	HitRate *float64 `json:"hit_rate"`
	// PCIeShare is the share of expert work read over PCIe or done by
	// another GPU.
	PCIeShare *float64 `json:"pcie_share"`
	// Recoveries is how often Strata restarted the thinking because it was
	// repeating itself.
	Recoveries int64 `json:"reasoning_recoveries"`
	// RepeatCoverage is the share of recent thinking words inside passages
	// seen three times, at the last check.
	RepeatCoverage float64 `json:"reasoning_repeat_coverage"`
	// FileMB is the expert data read from disk, in MB.
	FileMB *float64 `json:"file_mb"`
}

// FetchStrata reads a Strata model's /metrics, through llama-swap's
// /upstream/<model>/metrics. Like FetchProps, call it only while the model
// is ready. Another server gives an error.
func (m *Monitor) FetchStrata(ctx context.Context, model string) (Strata, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return getUpstream(m, ctx, model, "metrics", parseStrata)
}

func parseStrata(r io.Reader) (Strata, error) {
	var x struct {
		Engine   map[string]any  `json:"engine"`
		Requests []StrataRequest `json:"requests"`
	}
	if err := json.NewDecoder(r).Decode(&x); err != nil {
		return Strata{}, fmt.Errorf("reading Strata's /metrics: %w", err)
	}
	if x.Engine == nil {
		return Strata{}, fmt.Errorf("/metrics isn't Strata's")
	}
	return Strata{Engine: x.Engine, Requests: x.Requests}, nil
}

// Loaded reports whether the engine has loaded. Before then, Strata reports
// only the model's name, context and whether it takes images.
func (s Strata) Loaded() bool {
	_, ok := s.Engine["version"]
	return ok
}

// engineState is the engine fields that describe the moment, not the
// settings: the model's name and version are known elsewhere, and free VRAM
// changes as the engine runs.
var engineState = []string{"model", "version", "engine", "vram_free_mib", "drafting_note"}

// SettingArgs turns the engine's settings into arguments such as
// "--strata.kv=int8", sorted by name, to add to the model's command. The
// command alone only names Strata's config file, so without these a changed
// config would look like the same settings.
func (s Strata) SettingArgs() []string {
	var out []string
	for k, v := range s.Engine {
		if slices.Contains(engineState, k) {
			continue
		}
		var text string
		switch v := v.(type) {
		case string:
			text = v
		case float64, bool, nil:
			b, _ := json.Marshal(v)
			text = string(b)
		default:
			continue // lists and objects aren't settings
		}
		out = append(out, "--strata."+k+"="+text)
	}
	slices.SortFunc(out, strings.Compare)
	return out
}
