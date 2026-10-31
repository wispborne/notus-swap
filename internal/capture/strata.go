package capture

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/store"
)

// ServerStats is what the server knows about one request that its answer
// doesn't say, stored as JSON in requests.server_stats. Only Strata gives
// these.
type ServerStats struct {
	// ExpertHitRate is the share of expert lookups found in VRAM.
	ExpertHitRate *float64 `json:"expert_hit_rate,omitempty"`
	// PCIeShare is the share of expert work read over PCIe or done by
	// another GPU.
	PCIeShare *float64 `json:"pcie_share,omitempty"`
	// FirstTokenMs is the time to the first token on the server's own
	// clock, without the time spent in the proxy or waiting for a model load.
	FirstTokenMs *int64 `json:"first_token_ms,omitempty"`
	// ThinkingRestarts is how often the server restarted the thinking
	// because it was repeating itself.
	ThinkingRestarts int64 `json:"thinking_restarts,omitempty"`
	// ThinkingRepeat is the share of recent thinking words inside passages
	// seen three times, at the server's last check.
	ThinkingRepeat float64 `json:"thinking_repeat,omitempty"`
	// DiskMB is the expert data read from disk, in MB.
	DiskMB *float64 `json:"disk_mb,omitempty"`
}

// strataLoop is the share of repeated thinking at which Strata itself
// counts the thinking as a loop (its LOOP_COVERAGE).
const strataLoop = 0.25

// IsStrata reports whether a model's server is Strata, from the build its
// /props reports, such as "Strata 0.1.54".
func IsStrata(p *store.ModelProps) bool {
	return p != nil && strings.HasPrefix(p.Build, "Strata")
}

// serverIssues finds problems in what the server reported.
func serverIssues(s *ServerStats) []store.Issue {
	if s == nil || s.ThinkingRestarts == 0 && s.ThinkingRepeat < strataLoop {
		return nil
	}
	return []store.Issue{issue(IssueThinkingLoop, levelWarning,
		IssueDetail{Restarts: s.ThinkingRestarts, Repeat: math.Round(s.ThinkingRepeat*1000) / 1000})}
}

// StrataStats stores Strata's own numbers for each finished request on a
// Strata model. Strata's /metrics keeps a record of its last 12 requests,
// under its own IDs. A request is matched to the record with the same
// prompt and output sizes that started while it was in flight.
type StrataStats struct {
	Store *store.Store
	Hub   *Hub
	// Fetch reads a model's /metrics. Ready reports whether a model is
	// loaded, since asking one that isn't would load it. ID, if set, turns
	// an alias into the model's ID.
	Fetch func(ctx context.Context, model string) (llamaswap.Strata, error)
	Ready func(model string) bool
	ID    func(name string) string
	Log   *slog.Logger
	// Wait is the time between tries, while Strata hasn't recorded the
	// request yet. Zero means a second.
	Wait time.Duration

	mu   sync.Mutex
	used []string // Strata IDs already matched, newest last
}

// Finished looks for one finished request's record and stores it. It tries
// a few times, because Strata may record the request a moment after the
// answer ends.
func (s *StrataStats) Finished(ctx context.Context, id int64, model string, start, end time.Time, prompt, output int64) {
	wait := s.Wait
	if wait == 0 {
		wait = time.Second
	}
	if s.ID != nil {
		model = s.ID(model)
	}
	for try := 0; try < 3; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if !s.Ready(model) {
			return
		}
		m, err := s.Fetch(ctx, model)
		if err != nil {
			s.Log.Debug("could not read Strata's request records", "model", model, "err", err)
			return
		}
		rec, ok := s.match(m.Requests, start, end, prompt, output)
		if !ok {
			continue
		}
		stats := statsOf(rec)
		b, _ := json.Marshal(stats)
		if err := s.Store.SetServerStats(ctx, id, b, serverIssues(&stats)); err != nil {
			s.Log.Error("storing Strata's numbers for a request", "id", id, "err", err)
			return
		}
		s.Hub.settled([]int64{id})
		return
	}
}

// match picks the one record that fits, and marks it used so that it can't
// be matched twice.
func (s *StrataStats) match(recs []llamaswap.StrataRequest, start, end time.Time, prompt, output int64) (llamaswap.StrataRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Strata's clock starts once it takes the request, after notus-swap
	// saw it arrive. A second of slack allows for the two clocks' rounding.
	from, to := start.Add(-time.Second), end.Add(time.Second)
	var found []llamaswap.StrataRequest
	for _, r := range recs {
		at := time.UnixMilli(int64(r.Time * 1000))
		if r.PromptTokens == prompt && r.OutputTokens == output && !at.Before(from) && !at.After(to) && !slices.Contains(s.used, r.ID) {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		return llamaswap.StrataRequest{}, false
	}
	s.used = append(s.used, found[0].ID)
	if len(s.used) > 50 {
		s.used = s.used[len(s.used)-50:]
	}
	return found[0], true
}

func statsOf(r llamaswap.StrataRequest) ServerStats {
	out := ServerStats{ExpertHitRate: r.HitRate, PCIeShare: r.PCIeShare, ThinkingRestarts: r.Recoveries,
		ThinkingRepeat: r.RepeatCoverage, DiskMB: r.FileMB}
	if r.FirstTokenS != nil {
		ms := int64(math.Round(*r.FirstTokenS * 1000))
		out.FirstTokenMs = &ms
	}
	return out
}
