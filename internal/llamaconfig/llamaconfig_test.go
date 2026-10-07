package llamaconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setup(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("models:\n  a:\n    cmd: run a\n"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	return &Editor{Path: path, Bin: filepath.Join(dir, "no-such-llama-swap")}
}

func TestYAMLErrorsHaveLines(t *testing.T) {
	e := setup(t)
	c := e.Check(context.Background(), "models:\n  a:\n    cmd: [unclosed\n")
	if c.YAMLOK || c.YAMLLine == 0 || c.YAMLError == "" {
		t.Errorf("%+v", c)
	}
	c = e.Check(context.Background(), "models: {}\n")
	if !c.YAMLOK || c.LlamaSwapRan || !strings.Contains(c.LlamaSwapNote, "not found") {
		t.Errorf("no binary: %+v", c)
	}
}

func TestSaveBackupsAndConflicts(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	f, err := e.Read()
	if err != nil {
		t.Fatal(err)
	}

	// Without the llama-swap check passing, a normal save refuses.
	if _, _, err := e.Save(ctx, "models: {}\n", f.Hash, false, false); err == nil {
		t.Fatal("saved without llama-swap's check")
	}
	// Invalid YAML is refused even when skipping llama-swap's check.
	if _, _, err := e.Save(ctx, "a: [", f.Hash, true, false); err == nil {
		t.Fatal("saved invalid YAML")
	}

	c, f2, err := e.Save(ctx, "models: {}\n", f.Hash, true, false)
	if err != nil || !c.YAMLOK || f2.Content != "models: {}\n" {
		t.Fatalf("save: %v %+v", err, f2)
	}
	if fi, _ := os.Stat(e.Path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Errorf("permissions changed to %v", fi.Mode().Perm())
	}
	bs, _ := e.Backups()
	if len(bs) != 1 {
		t.Fatalf("backups %+v", bs)
	}
	if old, _ := e.ReadBackup(bs[0].Name); !strings.Contains(old, "cmd: run a") {
		t.Errorf("backup holds %q", old)
	}
	if _, err := e.ReadBackup("../config.yaml"); err == nil {
		t.Error("read a file outside the backup folder")
	}

	// Saving from the old hash now conflicts, unless told to overwrite.
	if _, cur, err := e.Save(ctx, "x: 1\n", f.Hash, true, false); !errors.Is(err, ErrChanged) || cur.Hash != f2.Hash {
		t.Errorf("conflict: %v", err)
	}
	if _, _, err := e.Save(ctx, "x: 1\n", f.Hash, true, true); err != nil {
		t.Errorf("overwrite: %v", err)
	}
}

func TestKeepsTwentyBackups(t *testing.T) {
	e := setup(t)
	for i := 0; i < 25; i++ {
		if _, err := e.backup([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if bs, _ := e.Backups(); len(bs) != 20 {
		t.Errorf("%d backups kept", len(bs))
	}
}

// A fake llama-swap: valid unless the config contains "bad".
func TestLlamaSwapValidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake llama-swap; CI runs this on Linux")
	}
	e := setup(t)
	script := "#!/bin/sh\nif grep -q bad \"$3\"; then echo 'config validation failed: model bad has no cmd'; exit 1; fi\necho 'config is valid: 1 model(s), 0 peer(s)'\n"
	os.WriteFile(e.Bin, []byte(script), 0o755)

	if c := e.Check(context.Background(), "models: {ok: {cmd: x}}\n"); !c.LlamaSwapRan || !c.LlamaSwapOK || !strings.Contains(c.LlamaSwapNote, "config is valid") {
		t.Errorf("valid: %+v", c)
	}
	if c := e.Check(context.Background(), "models: {bad: {}}\n"); !c.LlamaSwapRan || c.LlamaSwapOK || !strings.Contains(c.LlamaSwapNote, "no cmd") {
		t.Errorf("invalid: %+v", c)
	}
	old := "#!/bin/sh\necho 'flag provided but not defined: -validate'; exit 2\n"
	os.WriteFile(e.Bin, []byte(old), 0o755)
	if c := e.Check(context.Background(), "models: {}\n"); c.LlamaSwapRan || !strings.Contains(c.LlamaSwapNote, "does not support -validate") {
		t.Errorf("old llama-swap: %+v", c)
	}
}
