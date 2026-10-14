package api

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/wispborne/notus-swap/internal/store"
)

func TestModelColorsStayPut(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &API{Store: st, Log: log}

	// Other models wait for llama-swap's list.
	if got := a.assignColors(ctx, nil, []string{"zed"}); len(got) != 0 {
		t.Fatalf("colours before the config: %v", got)
	}

	// A fresh database matches the old order: config first, then others by name.
	config := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
	a.assignColors(ctx, config, nil)
	got := a.assignColors(ctx, nil, []string{"y", "x"})
	for i, m := range config {
		if got[m] != i%paletteSize {
			t.Errorf("%s = %d, want %d", m, got[m], i%paletteSize)
		}
	}
	if got["x"] != 2 || got["y"] != 3 {
		t.Errorf("x = %d, y = %d, want 2 and 3", got["x"], got["y"])
	}

	// Removing a model and adding one in the middle changes no other colour.
	// The new one avoids its neighbours' colours and takes a free one.
	next := []string{"a", "new", "b", "d", "e"}
	got2 := a.assignColors(ctx, next, nil)
	for m, i := range got {
		if got2[m] != i {
			t.Errorf("%s changed from %d to %d", m, i, got2[m])
		}
	}
	if n := got2["new"]; n == got["a"] || n == got["b"] || n == got["d"] || n == got["e"] {
		t.Errorf("new = %d, which a config model already uses", n)
	}

	// The colours survive a restart.
	b := &API{Store: st, Log: log}
	got3 := b.assignColors(ctx, next, nil)
	for m, i := range got2 {
		if got3[m] != i {
			t.Errorf("after restart %s = %d, want %d", m, got3[m], i)
		}
	}
}
