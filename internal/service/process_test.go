package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A copy of this test binary stands in for llama-swap: it waits until it is
// stopped.
func TestMain(m *testing.M) {
	if os.Getenv("NOTUS_TEST_FAKE_LLAMA_SWAP") == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeLlamaSwap(t *testing.T) *Process {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "llama-swap"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTUS_TEST_FAKE_LLAMA_SWAP", "1")
	p := &Process{Bin: bin, Config: "config.yaml", Listen: "127.0.0.1:1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { p.Stop(context.Background()) })
	return p
}

func state(t *testing.T, p *Process) string {
	s, err := p.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProcessStartStopRestart(t *testing.T) {
	p := fakeLlamaSwap(t)
	ctx := context.Background()
	if s := state(t, p); s != "inactive" {
		t.Fatalf("before start: %s", s)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pids := p.running()
	if len(pids) != 1 || state(t, p) != "active" {
		t.Fatalf("after start: %v %s", pids, state(t, p))
	}
	// Starting again doesn't start a second one.
	if err := p.Start(ctx); err != nil || len(p.running()) != 1 {
		t.Fatalf("second start: %v %v", err, p.running())
	}
	if err := p.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	if after := p.running(); len(after) != 1 || after[0] == pids[0] {
		t.Fatalf("restart: before %v, after %v", pids, after)
	}
	if err := p.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if s := state(t, p); s != "inactive" {
		t.Fatalf("after stop: %s", s)
	}
}

// A llama-swap that notus-swap didn't start, such as one left running when
// notus-swap restarted, is found and can be stopped.
func TestProcessStopsOneItDidNotStart(t *testing.T) {
	p := fakeLlamaSwap(t)
	other := &Process{Bin: p.Bin, Config: p.Config, Listen: p.Listen, Log: p.Log}
	if err := other.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state(t, p) != "active" {
		t.Fatal("the running copy wasn't found")
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state(t, p) != "inactive" {
		t.Fatal("still running")
	}
}

func TestProcessStartNeedsAConfig(t *testing.T) {
	p := fakeLlamaSwap(t)
	p.Config = ""
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}
