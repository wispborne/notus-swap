package llamaswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Running is one model llama-swap has started, from its /running list.
type Running struct {
	Model string `json:"model"`
	State string `json:"state"`
	// Cmd is the command from llama-swap's config, with every macro,
	// ${PORT} included, already filled in and comment lines removed.
	Cmd   string `json:"cmd"`
	Proxy string `json:"proxy"`
}

// FetchRunning reads llama-swap's list of models that are not stopped.
func (m *Monitor) FetchRunning(ctx context.Context) ([]Running, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", m.base.JoinPath("running").String(), nil)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/running returned HTTP %d", resp.StatusCode)
	}
	var x struct {
		Running []Running `json:"running"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&x); err != nil {
		return nil, fmt.Errorf("reading /running: %w", err)
	}
	return x.Running, nil
}

// Args splits the command into its arguments. The port llama-swap picked
// (read from the proxy address) is written back as ${PORT}, because it
// changes whenever models are added to or removed from the config.
func (r Running) Args() []string {
	args := splitCommand(r.Cmd)
	u, err := url.Parse(r.Proxy)
	if err != nil || u.Port() == "" {
		return args
	}
	port := regexp.MustCompile(`\b` + regexp.QuoteMeta(u.Port()) + `\b`)
	for i, a := range args {
		args[i] = port.ReplaceAllLiteralString(a, "${PORT}")
	}
	return args
}

// HashArgs names a command by its arguments: the first 12 hex digits of
// their SHA-256. Spacing, line breaks and comments in the config don't
// change it.
func HashArgs(args []string) string {
	h := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return hex.EncodeToString(h[:6])
}

// splitCommand splits a command line the way a POSIX shell would: on
// spaces, keeping quoted text together, with a backslash escaping the next
// character and a backslash at the end of a line joining it to the next.
// Lines starting with # are left out.
func splitCommand(cmd string) []string {
	var lines []string
	for _, line := range strings.Split(cmd, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, line)
		}
	}
	s := strings.Join(lines, "\n")

	var args []string
	var cur strings.Builder
	inArg := false
	var quote rune
	escaped := false
	for _, c := range s {
		switch {
		case escaped:
			escaped = false
			if c != '\n' {
				cur.WriteRune(c)
				inArg = true
			}
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\\':
			escaped = true
		case quote == '"':
			if c == '"' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inArg = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(c)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args
}
