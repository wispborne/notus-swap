package capture

import (
	"bytes"
	"context"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

// Remeasure fixes requests stored before measureSpeeds started the clock at
// the first Chat Completions chunk. Those without llama.cpp's timings whose
// stream stalled after its first chunk got speeds of thousands of tokens a
// second. The first byte stored is that first chunk, so it becomes the first
// token time and the speeds are measured again. It runs once: a setting
// records that it finished. It returns how many requests it changed.
func Remeasure(ctx context.Context, st *store.Store) (int, error) {
	const done = "remeasured_speeds"
	if v, err := st.Setting(ctx, done); err != nil || v != "" {
		return 0, err
	}
	changed := 0
	var after int64
	for {
		rows, err := st.SpeedRows(ctx, after)
		if err != nil {
			return changed, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			after = r.ID
			if ParseStored(r.Body).Timings != nil || !firstChunkHasChoices(r.Body) {
				continue
			}
			f := store.Finish{State: store.StateDone, FirstTokenAt: r.FirstTokenAt, FinishedAt: r.FinishedAt,
				PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens, CachedTokens: r.CachedTokens}
			var queued time.Duration
			if r.QueuedMs != nil {
				queued = time.Duration(*r.QueuedMs) * time.Millisecond
			}
			// No stored prompt time means it was skipped after a model load.
			measureSpeeds(&f, r.StartedAt, r.FirstByteAt, queued, r.PromptMs == nil)
			if err := st.SetSpeeds(ctx, r.ID, f.FirstTokenAt, f.PromptMs, f.PromptPerSecond, f.PredictedMs, f.PredictedPerSecond); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, st.SetSetting(ctx, done, time.Now().UTC().Format(time.RFC3339))
}

// firstChunkHasChoices reports whether a stored stream's first event is a
// Chat Completions chunk, so the stored first byte time is when it arrived.
func firstChunkHasChoices(b []byte) bool {
	for _, line := range bytes.Split(b, []byte("\n")) {
		if data, ok := sseData(line); ok {
			x, _, _, _ := chunk(data)
			return len(x.Choices) > 0
		}
	}
	return false
}
