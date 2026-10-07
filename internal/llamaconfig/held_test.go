package llamaconfig

import (
	"os"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/idlewait"
)

// Closing idle releases the pending save.
func heldSetup(t *testing.T) (*Held, chan struct{}, *File) {
	t.Helper()
	e := setup(t)
	f, err := e.Read()
	if err != nil {
		t.Fatal(err)
	}
	idle := make(chan struct{})
	return &Held{Editor: e, Wait: &idlewait.Waiter{Idle: func() <-chan struct{} { return idle }}}, idle, f
}

func waitOutcome(t *testing.T, h *Held, id int64) *Outcome {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, o := h.Status(); o != nil && o.ID == id {
			return o
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the held save didn't finish")
	return nil
}

func fileText(t *testing.T, e *Editor) string {
	t.Helper()
	b, err := os.ReadFile(e.Path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHeldWaitsForIdle(t *testing.T) {
	h, idle, f := heldSetup(t)
	id := h.Hold(SaveArgs{Content: "models: {}\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	time.Sleep(50 * time.Millisecond)
	if fileText(t, h.Editor) != f.Content {
		t.Fatal("wrote while requests were in flight")
	}
	if w, _ := h.Status(); w == nil || w.ID != id {
		t.Fatalf("waiting %+v", w)
	}
	close(idle)
	o := waitOutcome(t, h, id)
	if o.Error != "" || o.File == nil || fileText(t, h.Editor) != "models: {}\n" {
		t.Fatalf("outcome %+v", o)
	}
	if w, _ := h.Status(); w != nil {
		t.Fatalf("still waiting after the save: %+v", w)
	}
}

func TestHeldNowSkipsWait(t *testing.T) {
	h, _, f := heldSetup(t)
	id := h.Hold(SaveArgs{Content: "models: {}\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	if !h.Now() {
		t.Fatal("Now found nothing held")
	}
	if o := waitOutcome(t, h, id); o.Error != "" {
		t.Fatal(o.Error)
	}
	if h.Now() {
		t.Fatal("Now found a save after it was written")
	}
}

func TestHeldCancelAndReplace(t *testing.T) {
	h, idle, f := heldSetup(t)
	h.Hold(SaveArgs{Content: "first: 1\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	h.Hold(SaveArgs{Content: "second: 2\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	if !h.Cancel() {
		t.Fatal("Cancel found nothing held")
	}
	if h.Cancel() {
		t.Fatal("Cancel twice")
	}
	id := h.Hold(SaveArgs{Content: "third: 3\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	close(idle)
	waitOutcome(t, h, id)
	time.Sleep(50 * time.Millisecond) // Give replaced saves time to expose an incorrect write.
	if got := fileText(t, h.Editor); got != "third: 3\n" {
		t.Fatalf("file is %q", got)
	}
}

func TestHeldReportsChangedFile(t *testing.T) {
	h, idle, f := heldSetup(t)
	id := h.Hold(SaveArgs{Content: "models: {}\n", BaseHash: f.Hash, SkipLlamaSwap: true})
	if err := os.WriteFile(h.Editor.Path, []byte("changed: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(idle)
	if o := waitOutcome(t, h, id); o.Error == "" {
		t.Fatal("saved over a file that changed while waiting")
	}
	if got := fileText(t, h.Editor); got != "changed: elsewhere\n" {
		t.Fatalf("file is %q", got)
	}
}
