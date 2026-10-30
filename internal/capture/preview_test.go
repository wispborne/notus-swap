package capture

import (
	"strings"
	"testing"
)

func TestPeekRequestPreviews(t *testing.T) {
	long := strings.Repeat("same start ", 30) + "the part that differs"
	cases := []struct {
		name, body          string
		user, userEnd, last string
	}{
		{
			name: "chat ending with a tool result",
			body: `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"fix the tests"},
				{"role":"assistant","content":"","tool_calls":[{"function":{"name":"edit","arguments":"{\"f\":\"a.go\"}"}}]},
				{"role":"tool","content":"Edited a.go"}]}`,
			user: "fix the tests", userEnd: "fix the tests", last: "Tool result: Edited a.go",
		},
		{
			name: "chat ending with tool calls",
			body: `{"messages":[{"role":"user","content":"go"},
				{"role":"assistant","tool_calls":[{"function":{"name":"ls","arguments":"{}"}},{"function":{"name":"cat","arguments":"{\"f\":1}"}}]}]}`,
			user: "go", userEnd: "go", last: `Tool call: ls({}), cat({"f":1})`,
		},
		{
			name: "long user message",
			body: `{"messages":[{"role":"user","content":"` + long + `"}]}`,
			user: clip(strings.TrimSpace(long), previewChars), userEnd: "…" + string([]rune(strings.TrimSpace(long))[len([]rune(strings.TrimSpace(long)))-previewChars:]),
			last: clip(strings.TrimSpace(long), previewChars),
		},
		{
			name: "responses input",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
				{"type":"function_call","name":"shell","arguments":"{\"cmd\":\"ls\"}"},
				{"type":"function_call_output","output":"a.go  b.go"}]}`,
			user: "hi", userEnd: "hi", last: "Tool result: a.go b.go",
		},
		{
			name: "plain prompt",
			body: `{"prompt":"once upon"}`,
			user: "once upon", userEnd: "once upon", last: "once upon",
		},
	}
	for _, c := range cases {
		_, _, p := peekRequest([]byte(c.body))
		if p.User != c.user || p.UserEnd != c.userEnd || p.Last != c.last {
			t.Errorf("%s: got %+v, want user %q, end %q, last %q", c.name, p, c.user, c.userEnd, c.last)
		}
	}
	if !strings.HasSuffix(cases[2].userEnd, "the part that differs") {
		t.Errorf("the end preview should keep the end, got %q", cases[2].userEnd)
	}
}
