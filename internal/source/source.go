// Package source names the client that sent a request, from its User-Agent
// header. The names match web/src/lib/source.ts, which the Requests page's
// Source column uses; change both together.
package source

import (
	"regexp"
	"strings"
)

// Checked in order; the first match names the client. Each pattern is
// matched against the whole User-Agent, ignoring case.
var known = []struct {
	re   *regexp.Regexp
	name string
}{
	{regexp.MustCompile(`(?i)codex`), "Codex"},
	{regexp.MustCompile(`(?i)claude-cli|claude-code`), "Claude Code"},
	{regexp.MustCompile(`(?i)opencode`), "opencode"},
	{regexp.MustCompile(`(?i)open-?webui`), "Open WebUI"},
	{regexp.MustCompile(`(?i)^OpenAI/Python`), "OpenAI Python"},
	{regexp.MustCompile(`(?i)^OpenAI/JS`), "OpenAI JS"},
	{regexp.MustCompile(`(?i)^Anthropic/Python`), "Anthropic Python"},
	{regexp.MustCompile(`(?i)^Anthropic/JS`), "Anthropic JS"},
	{regexp.MustCompile(`(?i)aiohttp`), "Python aiohttp"},
	{regexp.MustCompile(`(?i)python-httpx`), "Python httpx"},
	{regexp.MustCompile(`(?i)python-requests`), "Python requests"},
	{regexp.MustCompile(`(?i)^curl/`), "curl"},
	{regexp.MustCompile(`(?i)^Wget`), "Wget"},
	{regexp.MustCompile(`(?i)Go-http-client`), "Go"},
	{regexp.MustCompile(`(?i)node-fetch|undici|^node`), "Node"},
	{regexp.MustCompile(`(?i)^Mozilla/`), "Browser"},
}

// Name is a short name for the client, such as "Codex" or "curl". A retry
// from the web UI is "Retry". It is "" when nothing is known.
func Name(userAgent string, retry bool) string {
	if retry {
		return "Retry"
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		return ""
	}
	for _, k := range known {
		if k.re.MatchString(ua) {
			return k.name
		}
	}
	// Otherwise the first product name, such as "my-tool" from "my-tool/1.2 (Linux)".
	if i := strings.IndexAny(ua, " \t/;("); i > 0 {
		return ua[:i]
	}
	return ua
}
