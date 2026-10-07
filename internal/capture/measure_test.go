package capture

import (
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

func TestMeasureSpeedsWithoutTimings(t *testing.T) {
	start := time.Now()
	pt, cached, ct := int64(1100), int64(100), int64(101)
	f := store.Finish{
		State: store.StateDone, PromptTokens: &pt, CachedTokens: &cached, CompletionTokens: &ct,
		FirstTokenAt: start.Add(2 * time.Second), FinishedAt: start.Add(7 * time.Second),
	}
	measureSpeeds(&f, start, time.Time{}, 0, false)
	if f.PromptPerSecond == nil || *f.PromptPerSecond != 500 {
		t.Errorf("prompt speed = %v, want 500", f.PromptPerSecond)
	}
	if f.PredictedPerSecond == nil || *f.PredictedPerSecond != 20 {
		t.Errorf("generation speed = %v, want 20", f.PredictedPerSecond)
	}

	// Timings from the server are left alone.
	own := 75.0
	g := store.Finish{State: store.StateDone, CompletionTokens: &ct, PredictedPerSecond: &own,
		FirstTokenAt: start.Add(time.Second), FinishedAt: start.Add(2 * time.Second)}
	measureSpeeds(&g, start, time.Time{}, 0, false)
	if *g.PredictedPerSecond != 75 {
		t.Errorf("server's speed was replaced: %v", *g.PredictedPerSecond)
	}
}

func TestMeasureSpeedsSkipsPromptAfterLoad(t *testing.T) {
	start := time.Now()
	pt, ct := int64(1000), int64(101)
	f := store.Finish{State: store.StateDone, PromptTokens: &pt, CompletionTokens: &ct,
		FirstTokenAt: start.Add(30 * time.Second), FinishedAt: start.Add(35 * time.Second)}
	measureSpeeds(&f, start, time.Time{}, 0, true)
	if f.PromptPerSecond != nil {
		t.Errorf("prompt speed set for a request that waited for a load: %v", *f.PromptPerSecond)
	}
	if f.PredictedPerSecond == nil {
		t.Error("generation speed should still be measured")
	}
}

// A stream that stalls after its first chunk, then delivers every token at
// once, is timed from that first chunk.
func TestMeasureSpeedsFromFirstChunk(t *testing.T) {
	start := time.Now()
	pt, ct := int64(4000), int64(5401)
	f := store.Finish{State: store.StateDone, PromptTokens: &pt, CompletionTokens: &ct,
		FirstTokenAt: start.Add(57 * time.Second), FinishedAt: start.Add(58 * time.Second)}
	measureSpeeds(&f, start, start.Add(4*time.Second), 0, false)
	if !f.FirstTokenAt.Equal(start.Add(4 * time.Second)) {
		t.Errorf("first token = %v after start, want 4s", f.FirstTokenAt.Sub(start))
	}
	if f.PredictedPerSecond == nil || *f.PredictedPerSecond != 100 {
		t.Errorf("generation speed = %v, want 100", f.PredictedPerSecond)
	}
	if f.PromptPerSecond == nil || *f.PromptPerSecond != 1000 {
		t.Errorf("prompt speed = %v, want 1000", f.PromptPerSecond)
	}

	// With llama.cpp's timings, the first token time is left alone.
	ms := 100.0
	g := store.Finish{State: store.StateDone, CompletionTokens: &ct, PredictedMs: &ms,
		FirstTokenAt: start.Add(57 * time.Second), FinishedAt: start.Add(58 * time.Second)}
	measureSpeeds(&g, start, start.Add(4*time.Second), 0, false)
	if !g.FirstTokenAt.Equal(start.Add(57 * time.Second)) {
		t.Errorf("first token moved despite timings: %v", g.FirstTokenAt.Sub(start))
	}
}
