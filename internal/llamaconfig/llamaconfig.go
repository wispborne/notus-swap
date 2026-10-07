// Package llamaconfig reads, checks and writes llama-swap's config file.
//
// A save always checks the YAML syntax. It also runs the installed llama-swap
// binary with -validate, which checks the config exactly as the running
// version would. Before writing, the current file is copied to a
// timestamped backup, and the 20 newest backups are kept. The new file is
// written to a temporary file and renamed into place, so llama-swap (which
// polls the file every 2 seconds with -watch-config) never reads half a file.
package llamaconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const keepBackups = 20

type Editor struct {
	Path string // llama-swap's config.yaml; "" turns the editor off
	Bin  string // llama-swap binary, for -validate; "" means next to Path
}

// ErrOff means no config path was set.
var ErrOff = errors.New("no llama-swap config path set (NOTUS_LLAMA_SWAP_CONFIG)")

// ErrChanged means the file changed on disk after it was loaded.
var ErrChanged = errors.New("the config file changed on disk since it was loaded")

type File struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Hash     string `json:"hash"`     // sha256 of Content, to detect changes made elsewhere
	Modified int64  `json:"modified"` // unix ms
}

func (e *Editor) Read() (*File, error) {
	if e.Path == "" {
		return nil, ErrOff
	}
	b, err := os.ReadFile(e.Path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(e.Path)
	if err != nil {
		return nil, err
	}
	return &File{Path: e.Path, Content: string(b), Hash: hash(b), Modified: fi.ModTime().UnixMilli()}, nil
}

// Check is the result of checking a config.
type Check struct {
	YAMLOK    bool   `json:"yaml_ok"`
	YAMLError string `json:"yaml_error,omitempty"`
	YAMLLine  int    `json:"yaml_line,omitempty"` // 1-based, 0 if unknown

	// RoutingProblems lists undefined model or variable references.
	RoutingProblems []Problem `json:"routing_problems,omitempty"`

	// LlamaSwapRan is false when the check could not run (no binary, or a
	// llama-swap too old for -validate); LlamaSwapNote says why.
	LlamaSwapRan  bool   `json:"llama_swap_ran"`
	LlamaSwapOK   bool   `json:"llama_swap_ok"`
	LlamaSwapNote string `json:"llama_swap_note"`
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

// Check checks content without writing anything.
func (e *Editor) Check(ctx context.Context, content string) Check {
	var c Check
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		c.YAMLError = strings.TrimPrefix(err.Error(), "yaml: ")
		if m := yamlLine.FindStringSubmatch(c.YAMLError); m != nil {
			c.YAMLLine, _ = strconv.Atoi(m[1])
		}
		c.LlamaSwapNote = "skipped until the YAML is valid"
		return c
	}
	c.YAMLOK = true
	c.RoutingProblems = RoutingProblems(content)
	c.LlamaSwapRan, c.LlamaSwapOK, c.LlamaSwapNote = e.validate(ctx, content)
	return c
}

// validate runs `llama-swap -validate -config <temp file>`.
func (e *Editor) validate(ctx context.Context, content string) (ran, ok bool, note string) {
	bin := e.Bin
	if bin == "" && e.Path != "" {
		bin = filepath.Join(filepath.Dir(e.Path), "llama-swap")
	}
	if _, err := os.Stat(bin); err != nil {
		return false, false, "llama-swap binary not found at " + bin + " (set NOTUS_LLAMA_SWAP_BIN)"
	}
	tmp, err := os.CreateTemp("", "notus-swap-check-*.yaml")
	if err != nil {
		return false, false, err.Error()
	}
	defer os.Remove(tmp.Name())
	tmp.WriteString(content)
	tmp.Close()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-validate", "-config", tmp.Name()).CombinedOutput()
	text := strings.TrimSpace(strings.ReplaceAll(string(out), tmp.Name(), "config.yaml"))
	if strings.Contains(text, "flag provided but not defined: -validate") {
		return false, false, "llama-swap does not support -validate; update it to check configs"
	}
	if err != nil && ctx.Err() != nil {
		return false, false, "llama-swap validation timed out"
	}
	return true, err == nil, text
}

// Save checks content and writes it. YAML must be valid; skipLlamaSwap bypasses
// the routing and llama-swap checks. baseHash is the original file's hash.
// A changed file returns ErrChanged unless overwrite is set.
func (e *Editor) Save(ctx context.Context, content, baseHash string, skipLlamaSwap, overwrite bool) (Check, *File, error) {
	c, cur, err := e.CheckSave(ctx, content, baseHash, skipLlamaSwap, overwrite)
	if err != nil {
		return c, cur, err
	}
	if _, err := e.backup([]byte(cur.Content)); err != nil {
		return c, nil, fmt.Errorf("could not back up the current config: %w", err)
	}
	if err := writeAtomic(e.Path, []byte(content)); err != nil {
		return c, nil, err
	}
	f, err := e.Read()
	return c, f, err
}

// CheckSave runs Save's checks without writing. It returns the current file on
// success or ErrChanged.
func (e *Editor) CheckSave(ctx context.Context, content, baseHash string, skipLlamaSwap, overwrite bool) (Check, *File, error) {
	cur, err := e.Read()
	if err != nil {
		return Check{}, nil, err
	}
	if cur.Hash != baseHash && !overwrite {
		return Check{}, cur, ErrChanged
	}
	c := e.Check(ctx, content)
	if !c.YAMLOK {
		return c, nil, fmt.Errorf("invalid YAML: %s", c.YAMLError)
	}
	if !skipLlamaSwap && len(c.RoutingProblems) > 0 {
		return c, nil, fmt.Errorf("routing check failed: %s", c.RoutingProblems[0].Message)
	}
	if !skipLlamaSwap && !c.LlamaSwapOK {
		return c, nil, fmt.Errorf("llama-swap config check failed: %s", c.LlamaSwapNote)
	}
	return c, cur, nil
}

// Backup is one saved copy of the config.
type Backup struct {
	Name  string `json:"name"`
	Taken int64  `json:"taken"` // unix ms
	Size  int64  `json:"size"`
}

func (e *Editor) backupDir() string { return filepath.Join(filepath.Dir(e.Path), "config-backups") }

const stamp = "20060102-150405.000000000"

func (e *Editor) backup(b []byte) (string, error) {
	dir := e.backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Names hold the time; step past any name already taken (coarse clocks).
	t := time.Now()
	name := filepath.Base(e.Path) + "." + t.Format(stamp)
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		t = t.Add(time.Microsecond)
		name = filepath.Base(e.Path) + "." + t.Format(stamp)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		return "", err
	}
	list, err := e.Backups()
	if err == nil && len(list) > keepBackups {
		for _, old := range list[keepBackups:] {
			os.Remove(filepath.Join(dir, old.Name))
		}
	}
	return name, nil
}

// Backups lists saved copies, newest first.
func (e *Editor) Backups() ([]Backup, error) {
	if e.Path == "" {
		return nil, ErrOff
	}
	entries, err := os.ReadDir(e.backupDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(e.Path) + "."
	out := []Backup{}
	for _, en := range entries {
		t, err := time.ParseInLocation(stamp, strings.TrimPrefix(en.Name(), prefix), time.Local)
		if en.IsDir() || !strings.HasPrefix(en.Name(), prefix) || err != nil {
			continue
		}
		info, _ := en.Info()
		b := Backup{Name: en.Name(), Taken: t.UnixMilli()}
		if info != nil {
			b.Size = info.Size()
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Taken > out[j].Taken })
	return out, nil
}

// ReadBackup returns one backup's content.
func (e *Editor) ReadBackup(name string) (string, error) {
	if e.Path == "" {
		return "", ErrOff
	}
	if name != filepath.Base(name) || !strings.HasPrefix(name, filepath.Base(e.Path)+".") {
		return "", errors.New("backup not found")
	}
	b, err := os.ReadFile(filepath.Join(e.backupDir(), name))
	return string(b), err
}

// writeAtomic writes b to a temporary file beside path, then renames it over
// path, keeping path's permissions.
func writeAtomic(path string, b []byte) error {
	mode := os.FileMode(0o664)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".notus-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
