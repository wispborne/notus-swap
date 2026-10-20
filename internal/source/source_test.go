package source

import "testing"

func TestName(t *testing.T) {
	for _, c := range []struct {
		ua    string
		retry bool
		want  string
	}{
		{"opencode/1.2.3 ai-sdk/provider-utils", false, "opencode"},
		{"codex_cli_rs/0.40.0 (Mac OS 15.6.1; arm64)", false, "Codex"},
		{"Mozilla/5.0 (X11; Linux x86_64)", false, "Browser"},
		{"curl/8.5.0", false, "curl"},
		{"hermes-agent/0.3 (Linux)", false, "hermes-agent"},
		{"plainname", false, "plainname"},
		{"  ", false, ""},
		{"curl/8.5.0", true, "Retry"},
	} {
		if got := Name(c.ua, c.retry); got != c.want {
			t.Errorf("Name(%q, %v) = %q, want %q", c.ua, c.retry, got, c.want)
		}
	}
}
