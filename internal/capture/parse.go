package capture

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"
)

// Usage and Timings follow the OpenAI "usage" object and llama.cpp's
// "timings" object, which llama-server adds to chat and completion responses.
type Usage struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`

	// The Responses API's names. input_tokens includes the cached tokens.
	InputTokens        *int64 `json:"input_tokens,omitempty"`
	OutputTokens       *int64 `json:"output_tokens,omitempty"`
	InputTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
	// Anthropic's name. Its input_tokens leaves these cached tokens out.
	CacheReadInputTokens *int64 `json:"cache_read_input_tokens,omitempty"`
}

type Timings struct {
	CacheN             *int64   `json:"cache_n"`  // prompt tokens reused from the cache
	PromptN            *int64   `json:"prompt_n"` // prompt tokens processed, not counting cached ones
	PromptMs           *float64 `json:"prompt_ms"`
	PromptPerSecond    *float64 `json:"prompt_per_second"`
	PredictedN         *int64   `json:"predicted_n"`
	PredictedMs        *float64 `json:"predicted_ms"`
	PredictedPerSecond *float64 `json:"predicted_per_second"`
}

type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Parsed is what notus-swap reads out of a response.
type Parsed struct {
	Content      string     `json:"content"`
	Reasoning    string     `json:"reasoning"`
	ToolCalls    []ToolCall `json:"tool_calls"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Usage        *Usage     `json:"usage,omitempty"`
	Timings      *Timings   `json:"timings,omitempty"`
	Error        *APIError  `json:"error,omitempty"`
	// Build is the server's build from system_fingerprint, such as
	// llama.cpp's "b11146-7fe450e19".
	Build string `json:"build,omitempty"`
}

// APIError is the "error" object of an error response. llama.cpp adds the
// prompt and context sizes when a prompt doesn't fit.
type APIError struct {
	Message       string `json:"message,omitempty"`
	Type          string `json:"type,omitempty"`
	NPromptTokens *int64 `json:"n_prompt_tokens,omitempty"`
	NCtx          *int64 `json:"n_ctx,omitempty"`
}

// PromptTokens is the whole prompt, cached tokens included. It prefers usage
// and falls back to llama.cpp timings.
func (p Parsed) PromptTokens() *int64 {
	if u := p.Usage; u != nil && u.PromptTokens != nil {
		return u.PromptTokens
	}
	if u := p.Usage; u != nil && u.InputTokens != nil {
		n := *u.InputTokens
		if u.CacheReadInputTokens != nil {
			n += *u.CacheReadInputTokens
		}
		return &n
	}
	if t := p.Timings; t != nil && t.PromptN != nil {
		n := *t.PromptN
		if t.CacheN != nil {
			n += *t.CacheN
		}
		return &n
	}
	return nil
}

// CachedTokens is how many prompt tokens were reused from the cache.
func (p Parsed) CachedTokens() *int64 {
	if u := p.Usage; u != nil {
		switch {
		case u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens != nil:
			return u.PromptTokensDetails.CachedTokens
		case u.InputTokensDetails != nil && u.InputTokensDetails.CachedTokens != nil:
			return u.InputTokensDetails.CachedTokens
		case u.CacheReadInputTokens != nil:
			return u.CacheReadInputTokens
		}
	}
	if p.Timings != nil {
		return p.Timings.CacheN
	}
	return nil
}

func (p Parsed) CompletionTokens() *int64 {
	if u := p.Usage; u != nil && u.CompletionTokens != nil {
		return u.CompletionTokens
	}
	if u := p.Usage; u != nil && u.OutputTokens != nil {
		return u.OutputTokens
	}
	if p.Timings != nil {
		return p.Timings.PredictedN
	}
	return nil
}

// ReasoningTokens uses the server's count when available. Otherwise it
// estimates thinking tokens from the character counts in reasoning, content,
// and tool calls. est is true for an estimate. No reasoning returns 0.
func (p Parsed) ReasoningTokens() (n int64, est bool) {
	if p.Reasoning == "" {
		return 0, false
	}
	if u := p.Usage; u != nil {
		if d := u.CompletionTokensDetails; d != nil && d.ReasoningTokens != nil && *d.ReasoningTokens > 0 {
			return *d.ReasoningTokens, false
		}
		if d := u.OutputTokensDetails; d != nil && d.ReasoningTokens != nil && *d.ReasoningTokens > 0 {
			return *d.ReasoningTokens, false
		}
	}
	out := p.CompletionTokens()
	if out == nil {
		return 0, false
	}
	reasoning := utf8.RuneCountInString(p.Reasoning)
	total := reasoning + utf8.RuneCountInString(p.Content)
	for _, tc := range p.ToolCalls {
		total += utf8.RuneCountInString(tc.Name) + utf8.RuneCountInString(tc.Arguments)
	}
	return int64(math.Round(float64(*out) * float64(reasoning) / float64(total))), true
}

type toolCallPart struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type message struct {
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content"`
	Reasoning        string         `json:"reasoning"` // vLLM's name for reasoning_content
	ToolCalls        []toolCallPart `json:"tool_calls"`
}

// thinking returns the message's reasoning text under either field name.
func (m *message) thinking() string {
	if m.ReasoningContent != "" {
		return m.ReasoningContent
	}
	return m.Reasoning
}

type choice struct {
	Delta        *message `json:"delta"`   // streaming chat
	Message      *message `json:"message"` // non-streaming chat
	Text         string   `json:"text"`    // legacy completions
	FinishReason string   `json:"finish_reason"`
}

type body struct {
	Choices           []choice        `json:"choices"`
	Usage             *Usage          `json:"usage"`
	Timings           *Timings        `json:"timings"`
	Error             json.RawMessage `json:"error"`
	SystemFingerprint string          `json:"system_fingerprint"`

	// Responses API events. Type names the event, such as
	// "response.output_text.delta". Delta is raw because Anthropic's
	// streams use the same name for an object.
	Type     string          `json:"type"`
	Delta    json.RawMessage `json:"delta"`
	ItemID   string          `json:"item_id"`
	Item     *respItem       `json:"item"`
	Response *respObject     `json:"response"`
	// Object is "response" for a non-streaming Responses answer.
	Object string `json:"object"`
}

// respItem is one output item of a Responses API answer.
type respItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // "message", "reasoning" or "function_call"
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}

// respObject is a whole Responses API answer: the body of a non-streaming
// answer, or the "response" in a stream's events.
type respObject struct {
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []respItem      `json:"output"`
	Usage  *Usage          `json:"usage"`
	Error  json.RawMessage `json:"error"`
}

// respDelta reads a Responses API delta event: a piece of the answer, of
// the reasoning, or of a tool call's arguments.
func (x body) respDelta() (content, reasoning, arguments string) {
	var d string
	if len(x.Delta) == 0 || json.Unmarshal(x.Delta, &d) != nil {
		return "", "", ""
	}
	switch x.Type {
	case "response.output_text.delta":
		return d, "", ""
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		return "", d, ""
	case "response.function_call_arguments.delta":
		return "", "", d
	}
	return "", "", ""
}

// parser builds a Parsed from one or more response bodies (SSE chunks).
type parser struct {
	p                  Parsed
	content, reasoning strings.Builder
	toolIndex          map[string]int // Responses API item ID to its place in ToolCalls
}

func (ps *parser) add(x body) {
	ps.addResponses(x)
	for _, c := range x.Choices {
		m := c.Delta
		if m == nil {
			m = c.Message
		}
		if m != nil {
			ps.content.WriteString(m.Content)
			ps.reasoning.WriteString(m.thinking())
			for i, tc := range m.ToolCalls {
				// Streamed tool calls arrive in pieces tagged with an index.
				// A whole message lists them in order.
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				for len(ps.p.ToolCalls) <= idx {
					ps.p.ToolCalls = append(ps.p.ToolCalls, ToolCall{})
				}
				t := &ps.p.ToolCalls[idx]
				if tc.ID != "" {
					t.ID = tc.ID
				}
				t.Name += tc.Function.Name
				t.Arguments += tc.Function.Arguments
			}
		}
		ps.content.WriteString(c.Text)
		if c.FinishReason != "" {
			ps.p.FinishReason = c.FinishReason
		}
	}
	if x.Usage != nil {
		ps.p.Usage = x.Usage
	}
	if x.Timings != nil {
		ps.p.Timings = x.Timings
	}
	if ps.p.Build == "" {
		ps.p.Build = x.SystemFingerprint
	}
	ps.setError(x.Error)
}

func (ps *parser) setError(raw json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	// Usually an object, but some servers send a plain string.
	var e APIError
	var msg string
	if json.Unmarshal(raw, &e) == nil {
		ps.p.Error = &e
	} else if json.Unmarshal(raw, &msg) == nil {
		ps.p.Error = &APIError{Message: msg}
	}
}

// addResponses reads a Responses API event. While the answer streams, the
// deltas build it up. The final event carries the whole answer, which
// replaces what the deltas built.
func (ps *parser) addResponses(x body) {
	content, reasoning, arguments := x.respDelta()
	ps.content.WriteString(content)
	ps.reasoning.WriteString(reasoning)
	if arguments != "" {
		if i, ok := ps.toolIndex[x.ItemID]; ok {
			ps.p.ToolCalls[i].Arguments += arguments
		}
	}
	if x.Type == "response.output_item.added" && x.Item != nil && x.Item.Type == "function_call" {
		if ps.toolIndex == nil {
			ps.toolIndex = map[string]int{}
		}
		ps.toolIndex[x.Item.ID] = len(ps.p.ToolCalls)
		ps.p.ToolCalls = append(ps.p.ToolCalls, ToolCall{ID: x.Item.CallID, Name: x.Item.Name, Arguments: x.Item.Arguments})
	}
	if r := x.Response; r != nil {
		ps.whole(r)
	}
}

// whole reads a whole Responses API answer. Status "in_progress" comes
// before any output, so only the finished statuses count.
func (ps *parser) whole(r *respObject) {
	if r.Usage != nil {
		ps.p.Usage = r.Usage
	}
	ps.setError(r.Error)
	switch r.Status {
	case "completed", "incomplete":
	default:
		return
	}
	ps.content.Reset()
	ps.reasoning.Reset()
	ps.p.ToolCalls, ps.toolIndex = nil, nil
	for _, it := range r.Output {
		switch it.Type {
		case "message":
			for _, c := range it.Content {
				if c.Type == "output_text" {
					ps.content.WriteString(c.Text)
				}
			}
		case "reasoning":
			for _, c := range it.Content {
				ps.reasoning.WriteString(c.Text)
			}
			if len(it.Content) == 0 {
				for _, s := range it.Summary {
					ps.reasoning.WriteString(s.Text)
				}
			}
		case "function_call":
			ps.p.ToolCalls = append(ps.p.ToolCalls, ToolCall{ID: it.CallID, Name: it.Name, Arguments: it.Arguments})
		}
	}
	// Give the finish reason Chat Completions would, so the issue checks
	// read both the same way.
	switch {
	case r.Status == "completed" && len(ps.p.ToolCalls) > 0:
		ps.p.FinishReason = "tool_calls"
	case r.Status == "completed":
		ps.p.FinishReason = "stop"
	case r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens":
		ps.p.FinishReason = "length"
	case r.IncompleteDetails != nil && r.IncompleteDetails.Reason != "":
		ps.p.FinishReason = r.IncompleteDetails.Reason
	default:
		ps.p.FinishReason = "incomplete"
	}
}

func (ps *parser) result() Parsed {
	p := ps.p
	p.Content, p.Reasoning = ps.content.String(), ps.reasoning.String()
	if p.ToolCalls == nil {
		p.ToolCalls = []ToolCall{}
	}
	return p
}

// Parse reads a response body, which may be cut short. It handles
// server-sent events and plain JSON. Anything it cannot read gives an empty
// result, never an error: the raw body is stored either way.
func Parse(b []byte, contentType string) Parsed {
	var ps parser
	if strings.HasPrefix(contentType, "text/event-stream") {
		for _, line := range bytes.Split(b, []byte("\n")) {
			if data, ok := sseData(line); ok {
				var x body
				if json.Unmarshal(data, &x) == nil {
					ps.add(x)
				}
			}
		}
	} else {
		var x body
		if json.Unmarshal(b, &x) == nil {
			// A non-streaming Responses answer is the same object a stream's
			// final event carries.
			if x.Object == "response" {
				var r respObject
				if json.Unmarshal(b, &r) == nil {
					x.Response = &r
				}
			}
			ps.add(x)
		}
	}
	return ps.result()
}

// ParseStored reads a stored response body, telling server-sent events from
// plain JSON by looking at how the body starts.
func ParseStored(b []byte) Parsed {
	t := bytes.TrimSpace(b)
	if bytes.HasPrefix(t, []byte("data:")) || bytes.HasPrefix(t, []byte("event:")) || bytes.HasPrefix(t, []byte(":")) {
		return Parse(b, "text/event-stream")
	}
	return Parse(b, "application/json")
}

// endsStream reports whether an SSE line is the last event of a stream: the
// Chat Completions [DONE] marker, or the Responses API's final event.
func endsStream(line []byte, x body) bool {
	if data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:")); ok && bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return true
	}
	return x.Type == "response.completed" || x.Type == "response.incomplete"
}

// sseData returns the payload of a "data:" line, skipping the [DONE] marker.
func sseData(line []byte) ([]byte, bool) {
	line = bytes.TrimRight(line, "\r")
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return nil, false
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return nil, false
	}
	return data, true
}

// chunk reads one SSE data payload. output is true when the chunk carries
// generated text or a piece of a tool call, so a response that only calls a
// tool still gets a first-token time.
func chunk(data []byte) (x body, content, reasoning string, output bool) {
	if json.Unmarshal(data, &x) != nil {
		return x, "", "", false
	}
	for _, c := range x.Choices {
		content += c.Text
		if d := c.Delta; d != nil {
			content += d.Content
			reasoning += d.thinking()
			output = output || len(d.ToolCalls) > 0
		}
	}
	c, r, arguments := x.respDelta()
	content += c
	reasoning += r
	output = output || arguments != "" || (x.Type == "response.output_item.added" && x.Item != nil && x.Item.Type == "function_call")
	return x, content, reasoning, output || content != "" || reasoning != ""
}

// peekRequest reads the model name, the stream flag, and a short preview of
// the prompt from a request body.
func peekRequest(b []byte) (model string, stream bool, preview string) {
	var x struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Prompt json.RawMessage `json:"prompt"`
		Input  json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(b, &x)
	messages := x.Messages
	if len(messages) == 0 {
		// A Responses API input can be a list of items, where messages
		// sit among tool calls and their results.
		_ = json.Unmarshal(x.Input, &messages)
	}
	for i := len(messages) - 1; i >= 0 && preview == ""; i-- {
		if messages[i].Role == "user" {
			preview = text(messages[i].Content)
		}
	}
	if preview == "" {
		preview = text(x.Prompt)
	}
	if preview == "" {
		preview = text(x.Input)
	}
	return x.Model, x.Stream, clip(strings.Join(strings.Fields(preview), " "), 200)
}

// text flattens a string, a list of strings, or a list of content parts.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return ""
	}
	var out []string
	for _, item := range list {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(item, &s) == nil {
			out = append(out, s)
		} else if json.Unmarshal(item, &part) == nil && part.Text != "" {
			out = append(out, part.Text)
		} else if part.Type == "image_url" {
			out = append(out, "[image]")
		}
	}
	return strings.Join(out, " ")
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
