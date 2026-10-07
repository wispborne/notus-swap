package capture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
)

func kinds(tags []Tag) []string {
	out := []string{}
	for _, t := range tags {
		out = append(out, t.Kind)
	}
	return out
}

func find(tags []Tag, kind string) Tag {
	for _, t := range tags {
		if t.Kind == kind {
			return t
		}
	}
	return Tag{}
}

func ptr(n int64) *int64 { return &n }

func TestRequestTags(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		body      string
		streaming bool
		want      []string
	}{
		{"plain chat", "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, true, []string{}},
		{"not streamed", "/v1/chat/completions", `{}`, false, []string{TagNotStreamed}},
		{"responses", "/v1/responses", `{"reasoning":{"effort":"high"}}`, true, []string{TagThinking, TagResponses}},
		{"thinking off", "/v1/chat/completions", `{"chat_template_kwargs":{"enable_thinking":false}}`, true, []string{TagNoThinking}},
		{"effort none", "/v1/chat/completions", `{"reasoning_effort":"none"}`, true, []string{TagNoThinking}},
		{"fim by suffix", "/v1/completions", `{"prompt":"a","suffix":"b"}`, true, []string{TagFIM}},
		{"infill", "/infill", `{}`, true, []string{TagFIM}},
		{"completions", "/v1/completions", `{"prompt":"a"}`, true, []string{TagCompletions}},
		{"embeddings never stream", "/v1/embeddings", `{}`, false, []string{TagEmbeddings}},
		{"rerank", "/upstream/bge/v1/rerank", `{}`, false, []string{TagRerank}},
		{"json", "/v1/chat/completions", `{"response_format":{"type":"json_object"}}`, true, []string{TagJSON}},
		{"schema", "/v1/chat/completions", `{"response_format":{"type":"json_schema","json_schema":{"name":"dates"}}}`, true, []string{TagSchema}},
		{"responses schema", "/v1/responses", `{"text":{"format":{"type":"json_schema","name":"plan"}}}`, true, []string{TagSchema, TagResponses}},
		{"images", "/v1/chat/completions", `{"messages":[{"role":"user","content":[
			{"type":"text","text":"what"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
			{"type":"image_url","image_url":{"url":"https://example.com/a.jpg"}}]}]}`, true, []string{TagImages}},
	}
	for _, c := range cases {
		got := kinds(RequestTags(c.path, []byte(c.body), c.streaming))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	imgs := find(RequestTags("/v1/chat/completions", []byte(`{"messages":[{"content":[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
		{"type":"image_url","image_url":{"url":"https://example.com/a.jpg"}}]}]}`), true), TagImages)
	if imgs.N != 2 || !reflect.DeepEqual(imgs.Items, []string{"PNG", "URL"}) {
		t.Errorf("images: %+v", imgs)
	}
	if s := find(RequestTags("/v1/responses", []byte(`{"text":{"format":{"type":"json_schema","name":"plan"}}}`), true), TagSchema); s.Text != "plan" {
		t.Errorf("schema name: %+v", s)
	}
	if r := find(RequestTags("/v1/responses", nil, true), TagResponses); r.Text != "/v1/responses" {
		t.Errorf("path: %+v", r)
	}
}

func TestTagsFromAnswer(t *testing.T) {
	resp := Parsed{
		Reasoning: "Let me look at the files first.",
		ToolCalls: []ToolCall{{Name: "shell", Arguments: `{"command": ["ls",   "-la"]}`}, {Name: "apply_patch", Arguments: "{}"}},
	}
	tags := Tags("/v1/responses", []byte(`{"reasoning":{"effort":"high"}}`), true, &resp, ptr(40000), ptr(39000))
	if got := kinds(tags); !reflect.DeepEqual(got, []string{TagToolCalls, TagThinking, TagResponses}) {
		t.Fatalf("got %v", got)
	}
	tc := tags[0]
	if tc.N != 2 || tc.Items[0] != `shell({"command": ["ls", "-la"]})` || tc.Items[1] != "apply_patch({})" {
		t.Errorf("tool calls: %+v", tc)
	}
	if th := tags[1]; th.N != 7 || th.Text != "effort high" {
		t.Errorf("thinking: %+v", th)
	}

	// Returned reasoning overrides the request's no-thinking tag.
	tags = Tags("/v1/chat/completions", []byte(`{"chat_template_kwargs":{"enable_thinking":false}}`), true, &Parsed{Reasoning: "hm"}, nil, nil)
	if got := kinds(tags); !reflect.DeepEqual(got, []string{TagThinking}) {
		t.Errorf("got %v", got)
	}
}

func TestCacheMiss(t *testing.T) {
	cases := []struct {
		prompt, cached *int64
		miss           bool
	}{
		{ptr(103805), ptr(0), true},
		{ptr(103805), ptr(10380), true},
		{ptr(103805), ptr(10381), false},
		{ptr(8000), ptr(0), false}, // too small to matter
		{ptr(20000), nil, false},   // not known
	}
	for _, c := range cases {
		tags := Tags("/v1/chat/completions", nil, true, &Parsed{}, c.prompt, c.cached)
		m := find(tags, TagCacheMiss)
		if (m.Kind != "") != c.miss {
			t.Errorf("prompt %d cached %v: got %+v", *c.prompt, c.cached, tags)
		}
		if c.miss && (m.N != *c.cached || m.Of != *c.prompt) {
			t.Errorf("numbers: %+v", m)
		}
	}
}

func TestStartTools(t *testing.T) {
	var x body
	json.Unmarshal([]byte(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"shell","arguments":""}}]}}]}`), &x)
	if got := startTools(x); !reflect.DeepEqual(got, []string{"shell"}) {
		t.Errorf("chat: %v", got)
	}
	x = body{}
	json.Unmarshal([]byte(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\""}}]}}]}`), &x)
	if got := startTools(x); got != nil {
		t.Errorf("a later piece names no tool: %v", got)
	}
	x = body{}
	json.Unmarshal([]byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"apply_patch"}}`), &x)
	if got := startTools(x); !reflect.DeepEqual(got, []string{"apply_patch"}) {
		t.Errorf("responses: %v", got)
	}
}

// fakeSlots answers /slots from a script, one answer per poll.
type fakeSlots struct {
	answers [][]llamaswap.Slot
	err     error
	calls   int
}

func (f *fakeSlots) fetch(ctx context.Context, model string) ([]llamaswap.Slot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	a := f.answers[min(f.calls-1, len(f.answers)-1)]
	return a, nil
}

func TestSlotPollerMeasuresPromptSpeed(t *testing.T) {
	h := NewHub()
	events, _, cancel := h.Subscribe()
	defer cancel()
	h.start(Start{ID: 7, Model: "qwen-alias"}, nil)
	<-events

	slot := func(n int64) []llamaswap.Slot {
		return []llamaswap.Slot{{TaskID: 1, Processing: true, Prompt: n + 1024, Processed: n, Cache: 0}}
	}
	// Prompt processing advances by 1,024 tokens every 2 s; poll every second.
	f := &fakeSlots{answers: [][]llamaswap.Slot{slot(56701), slot(56701), slot(57725), slot(57725), slot(58749), slot(58749), slot(59773)}}
	var asked string
	p := &SlotPoller{Hub: h, Fetch: func(ctx context.Context, m string) ([]llamaswap.Slot, error) { asked = m; return f.fetch(ctx, m) },
		Ready: func(string) bool { return true }, ID: func(n string) string { return "qwen" }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	start := time.Unix(1000, 0)
	var last Progress
	for i := range 7 {
		p.poll(context.Background(), func() time.Time { return start.Add(time.Duration(i) * time.Second) })
		e := <-events
		if e.Type != "progress" || e.ID != 7 {
			t.Fatalf("poll %d: event %+v", i, e)
		}
		last = *e.Progress
		// Two steps are needed before there is a speed: at polls 2 and 4.
		if (last.PerSecond != nil) != (i >= 4) {
			t.Errorf("poll %d: speed %v", i, last.PerSecond)
		}
	}
	if asked != "qwen" {
		t.Errorf("asked for %q, not the model's ID", asked)
	}
	// Steps at 2 s (57,725), 4 s (58,749) and 6 s (59,773): 2,048 tokens in 4 s.
	if last.Processed != 59773 || *last.PerSecond != 512 {
		t.Errorf("last progress %+v, speed %v", last, *last.PerSecond)
	}

	// Once the first token arrives, polling stops for the request.
	h.chunk(7, body{}, "hi", "", true, start)
	<-events
	calls := f.calls
	p.poll(context.Background(), time.Now)
	if f.calls != calls {
		t.Error("polled a request that already has its first token")
	}
}

func TestSlotPollerSkipsUnclearMatches(t *testing.T) {
	h := NewHub()
	events, _, cancel := h.Subscribe()
	defer cancel()
	h.start(Start{ID: 1, Model: "m"}, nil)
	<-events
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// One slot is still generating another request's answer, and one is
	// processing the prompt: the prompt one is picked.
	f := &fakeSlots{answers: [][]llamaswap.Slot{{
		{ID: 0, TaskID: 1, Processing: true, Processed: 500, Decoded: 40},
		{ID: 1, TaskID: 2, Processing: true, Processed: 1024},
	}}}
	p := &SlotPoller{Hub: h, Fetch: f.fetch, Ready: func(string) bool { return true }, ID: func(n string) string { return n }, Log: quiet}
	p.poll(context.Background(), time.Now)
	if e := <-events; e.Progress == nil || e.Progress.Processed != 1024 {
		t.Errorf("got %+v", e)
	}

	// Two requests waiting on one model: no guess.
	h.start(Start{ID: 2, Model: "m"}, nil)
	<-events
	calls := f.calls
	p.poll(context.Background(), time.Now)
	if f.calls != calls {
		t.Error("polled with two requests waiting")
	}
}

func TestSlotPollerStopsAfterFailure(t *testing.T) {
	h := NewHub()
	h.start(Start{ID: 1, Model: "m"}, nil)
	f := &fakeSlots{err: errors.New("HTTP 501")}
	p := &SlotPoller{Hub: h, Fetch: f.fetch, Ready: func(string) bool { return true }, ID: func(n string) string { return n },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.poll(context.Background(), time.Now)
	p.poll(context.Background(), time.Now)
	if f.calls != 1 {
		t.Errorf("polled %d times after a failure", f.calls)
	}
	p.Loaded("m")
	p.poll(context.Background(), time.Now)
	if f.calls != 2 {
		t.Errorf("didn't poll again after the model loaded")
	}
}

func TestReasoningTokens(t *testing.T) {
	// Without a count from the server, the output tokens are split by text.
	p := Parsed{Reasoning: "aaaaaaaaaaaaaaa", Content: "bbbbb", Usage: &Usage{CompletionTokens: ptr(100)}}
	if n, est := p.ReasoningTokens(); n != 75 || !est {
		t.Errorf("estimate: %d %v", n, est)
	}
	tags := Tags("/v1/chat/completions", []byte(`{}`), true, &p, nil, nil)
	if tags[0].Tokens != 75 || !tags[0].Est {
		t.Errorf("tag: %+v", tags[0])
	}

	// The server's own count wins.
	var u Usage
	if err := json.Unmarshal([]byte(`{"output_tokens":100,"output_tokens_details":{"reasoning_tokens":60}}`), &u); err != nil {
		t.Fatal(err)
	}
	p.Usage = &u
	if n, est := p.ReasoningTokens(); n != 60 || est {
		t.Errorf("reported: %d %v", n, est)
	}

	// No reasoning, or no output count: nothing.
	if n, _ := (Parsed{Content: "x", Usage: &Usage{CompletionTokens: ptr(5)}}).ReasoningTokens(); n != 0 {
		t.Errorf("no reasoning: %d", n)
	}
	if n, _ := (Parsed{Reasoning: "x"}).ReasoningTokens(); n != 0 {
		t.Errorf("no output count: %d", n)
	}
}
