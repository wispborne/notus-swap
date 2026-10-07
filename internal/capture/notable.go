package capture

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/wispborne/notus-swap/internal/store"
)

// Kinds of tag for the Requests page's Notable column. The UI has a label
// and a popover for each.
const (
	TagToolCalls   = "tool_calls"  // tool calls returned in the answer; Items lists them
	TagThinking    = "thinking"    // thinking requested or returned
	TagNoThinking  = "no_thinking" // thinking disabled by the request, with no reasoning returned
	TagImages      = "images"      // images in the request; Items lists their formats
	TagJSON        = "json"        // the request asked for any JSON object
	TagSchema      = "schema"      // the request asked for JSON matching a schema
	TagResponses   = "responses"   // an OpenAI Responses API call
	TagCompletions = "completions" // a plain text completion
	TagFIM         = "fim"         // fill in the middle
	TagEmbeddings  = "embeddings"
	TagRerank      = "rerank"
	TagMessages    = "messages"     // an Anthropic Messages API call
	TagNotStreamed = "not_streamed" // a request that could stream, but didn't
	TagCacheMiss   = "cache_miss"   // a big prompt with little of it cached
)

// TagsVersion goes up whenever the tags change. At startup, stored requests
// get their tags worked out again if the last run used an older version.
const TagsVersion = 3

// A prompt this big with under a tenth of it cached is a cache miss.
const (
	cacheMissTokens = 8000
	cacheMissShare  = 0.10
)

// Tag is one thing worth noticing about a request. Only the fields that
// apply are set.
type Tag struct {
	Kind  string   `json:"kind"`
	N     int64    `json:"n,omitempty"`     // a count: tool calls, images, words of thinking, cached tokens
	Of    int64    `json:"of,omitempty"`    // cache_miss: the prompt's size
	Text  string   `json:"text,omitempty"`  // what the request asked for, a schema name, or the path
	Items []string `json:"items,omitempty"` // tool calls or image formats
	// thinking: how many output tokens were thinking, and whether that
	// number is an estimate (see Parsed.ReasoningTokens).
	Tokens int64 `json:"tokens,omitempty"`
	Est    bool  `json:"est,omitempty"`
}

// RequestTags reads the tags that are known as soon as a request arrives.
func RequestTags(path string, body []byte, streaming bool) []Tag {
	var tags []Tag
	x := readTagRequest(body)
	if asked, off := x.thinking(); off {
		tags = append(tags, Tag{Kind: TagNoThinking, Text: asked})
	} else if asked != "" {
		tags = append(tags, Tag{Kind: TagThinking, Text: asked})
	}
	if images := x.images(); len(images) > 0 {
		tags = append(tags, Tag{Kind: TagImages, N: int64(len(images)), Items: images})
	}
	if t := x.format(); t.Kind != "" {
		tags = append(tags, t)
	}
	kind, streams := pathKind(path, x.fim())
	if kind != "" {
		tags = append(tags, Tag{Kind: kind, Text: path})
	}
	if streams && !streaming {
		tags = append(tags, Tag{Kind: TagNotStreamed})
	}
	return tags
}

// Tags gives every tag of a finished request: the request's own, plus those
// read from its answer. Answer tags come first. resp is nil when the answer
// couldn't be read. prompt and cached are the request's token counts, if known.
func Tags(path string, body []byte, streaming bool, resp *Parsed, prompt, cached *int64) []Tag {
	req := RequestTags(path, body, streaming)
	var out []Tag
	if resp != nil {
		if n := len(resp.ToolCalls); n > 0 {
			items := make([]string, n)
			for i, tc := range resp.ToolCalls {
				items[i] = tc.Name + "(" + clip(strings.Join(strings.Fields(tc.Arguments), " "), 80) + ")"
			}
			out = append(out, Tag{Kind: TagToolCalls, N: int64(n), Items: items})
		}
		if words := len(strings.Fields(resp.Reasoning)); words > 0 {
			t := Tag{Kind: TagThinking, N: int64(words)}
			t.Tokens, t.Est = resp.ReasoningTokens()
			for _, r := range req {
				if r.Kind == TagThinking || r.Kind == TagNoThinking {
					t.Text = r.Text
				}
			}
			out = append(out, t)
		}
	}
	for _, r := range req {
		// Thinking that came back replaces the request's own thinking tag.
		if (r.Kind == TagThinking || r.Kind == TagNoThinking) && hasKind(out, TagThinking) {
			continue
		}
		out = append(out, r)
	}
	if prompt != nil && cached != nil && *prompt > cacheMissTokens && float64(*cached) < float64(*prompt)*cacheMissShare {
		out = append(out, Tag{Kind: TagCacheMiss, N: *cached, Of: *prompt})
	}
	return out
}

func hasKind(tags []Tag, kind string) bool {
	for _, t := range tags {
		if t.Kind == kind {
			return true
		}
	}
	return false
}

// pathKind names the kind of call a path is, or "" for Chat Completions.
// streams is whether that kind of call can stream an answer.
func pathKind(path string, fim bool) (kind string, streams bool) {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return "", true
	case strings.HasSuffix(path, "/infill"):
		return TagFIM, true
	case strings.HasSuffix(path, "/completions") || strings.HasSuffix(path, "/completion"):
		if fim {
			return TagFIM, true
		}
		return TagCompletions, true
	case strings.HasSuffix(path, "/responses"):
		return TagResponses, true
	case strings.HasSuffix(path, "/messages"):
		return TagMessages, true
	case strings.HasSuffix(path, "/embeddings") || strings.HasSuffix(path, "/embedding"):
		return TagEmbeddings, false
	case strings.HasSuffix(path, "/rerank") || strings.HasSuffix(path, "/reranking"):
		return TagRerank, false
	}
	return "", false
}

type tagRequest struct {
	ReasoningEffort string `json:"reasoning_effort"`
	Reasoning       *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"` // Responses API
	Thinking *struct {
		Type         string `json:"type"`
		BudgetTokens int64  `json:"budget_tokens"`
	} `json:"thinking"` // Anthropic
	ThinkingBudget     *int64 `json:"thinking_budget_tokens"`
	ChatTemplateKwargs struct {
		EnableThinking  *bool  `json:"enable_thinking"`
		Thinking        *bool  `json:"thinking"`
		ReasoningEffort string `json:"reasoning_effort"`
	} `json:"chat_template_kwargs"`
	ResponseFormat *struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name string `json:"name"`
		} `json:"json_schema"`
	} `json:"response_format"`
	Text *struct {
		Format *struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"format"`
	} `json:"text"` // Responses API
	JSONSchema  json.RawMessage `json:"json_schema"` // llama.cpp's own
	Messages    []tagMessage    `json:"messages"`
	Input       json.RawMessage `json:"input"`
	InputPrefix json.RawMessage `json:"input_prefix"`
	InputSuffix json.RawMessage `json:"input_suffix"`
	Suffix      json.RawMessage `json:"suffix"`
}

type tagMessage struct {
	Content json.RawMessage `json:"content"`
}

func readTagRequest(b []byte) tagRequest {
	var x tagRequest
	_ = json.Unmarshal(b, &x)
	return x
}

func (x tagRequest) fim() bool {
	return x.InputPrefix != nil || x.InputSuffix != nil || (x.Suffix != nil && string(x.Suffix) != "null")
}

// thinking reads what the request asked for about thinking: a short text
// such as "effort high", and whether it turned thinking off.
func (x tagRequest) thinking() (asked string, off bool) {
	effort := func(e string) (string, bool) {
		if e == "none" {
			return "effort none", true
		}
		return "effort " + e, false
	}
	k := x.ChatTemplateKwargs
	switch {
	case k.EnableThinking != nil && !*k.EnableThinking:
		return "enable_thinking: false", true
	case k.Thinking != nil && !*k.Thinking:
		return "thinking: false", true
	case x.Thinking != nil && x.Thinking.Type == "disabled":
		return "thinking disabled", true
	case x.ReasoningEffort != "":
		return effort(x.ReasoningEffort)
	case x.Reasoning != nil && x.Reasoning.Effort != "":
		return effort(x.Reasoning.Effort)
	case k.ReasoningEffort != "":
		return effort(k.ReasoningEffort)
	case x.Thinking != nil && x.Thinking.BudgetTokens > 0:
		return "budget " + itoa(x.Thinking.BudgetTokens) + " tokens", false
	case x.ThinkingBudget != nil && *x.ThinkingBudget == 0:
		return "budget 0 tokens", true
	case x.ThinkingBudget != nil && *x.ThinkingBudget > 0:
		return "budget " + itoa(*x.ThinkingBudget) + " tokens", false
	case k.EnableThinking != nil && *k.EnableThinking:
		return "enable_thinking: true", false
	case k.Thinking != nil && *k.Thinking:
		return "thinking: true", false
	}
	return "", false
}

func (x tagRequest) format() Tag {
	if f := x.ResponseFormat; f != nil {
		switch f.Type {
		case "json_object":
			return Tag{Kind: TagJSON}
		case "json_schema":
			t := Tag{Kind: TagSchema}
			if f.JSONSchema != nil {
				t.Text = f.JSONSchema.Name
			}
			return t
		}
	}
	if x.Text != nil && x.Text.Format != nil {
		switch x.Text.Format.Type {
		case "json_object":
			return Tag{Kind: TagJSON}
		case "json_schema":
			return Tag{Kind: TagSchema, Text: x.Text.Format.Name}
		}
	}
	if len(x.JSONSchema) > 0 && string(x.JSONSchema) != "null" {
		return Tag{Kind: TagSchema}
	}
	return Tag{}
}

// images lists the format of each image in the request's messages, or in a
// Responses API input.
func (x tagRequest) images() []string {
	messages := x.Messages
	if len(messages) == 0 {
		_ = json.Unmarshal(x.Input, &messages)
	}
	var out []string
	for _, m := range messages {
		var parts []struct {
			Type     string          `json:"type"`
			ImageURL json.RawMessage `json:"image_url"`
			Source   *struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
			} `json:"source"` // Anthropic
		}
		if json.Unmarshal(m.Content, &parts) != nil {
			continue
		}
		for _, p := range parts {
			switch p.Type {
			case "image_url", "input_image":
				var url string
				if json.Unmarshal(p.ImageURL, &url) != nil {
					var obj struct {
						URL string `json:"url"`
					}
					_ = json.Unmarshal(p.ImageURL, &obj)
					url = obj.URL
				}
				out = append(out, imageFormat(url))
			case "image":
				if p.Source != nil && p.Source.MediaType != "" {
					out = append(out, mediaFormat(p.Source.MediaType))
				} else {
					out = append(out, "image")
				}
			}
		}
	}
	return out
}

// imageFormat names an image from its URL: "PNG" for a data URL holding a
// PNG, "URL" for a link.
func imageFormat(url string) string {
	if rest, ok := strings.CutPrefix(url, "data:"); ok {
		media, _, _ := strings.Cut(rest, ";")
		return mediaFormat(media)
	}
	if url == "" {
		return "image"
	}
	return "URL"
}

func mediaFormat(media string) string {
	_, sub, ok := strings.Cut(media, "/")
	if !ok || sub == "" {
		return "image"
	}
	return strings.ToUpper(strings.TrimPrefix(sub, "x-"))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// startTools gives the names of tool calls that start in a streamed chunk.
func startTools(x body) []string {
	var names []string
	for _, c := range x.Choices {
		if c.Delta == nil {
			continue
		}
		for _, tc := range c.Delta.ToolCalls {
			// A tool call's first piece carries its name.
			if tc.Function.Name != "" {
				names = append(names, tc.Function.Name)
			}
		}
	}
	if x.Type == "response.output_item.added" && x.Item != nil && x.Item.Type == "function_call" {
		names = append(names, x.Item.Name)
	}
	return names
}

// MarshalTags turns tags into the JSON stored with a request.
func MarshalTags(tags []Tag) string {
	if tags == nil {
		tags = []Tag{}
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

// RetagIfChanged works out the tags again for every stored request that still
// has its bodies, when the tags have changed since the last run. It returns
// how many requests it tagged.
func RetagIfChanged(ctx context.Context, st *store.Store) (int, error) {
	const key = "tags_version"
	v, err := st.Setting(ctx, key)
	if err != nil {
		return 0, err
	}
	want := itoa(TagsVersion)
	if v == want {
		return 0, nil
	}
	tagged := 0
	var after int64
	for {
		// Read a small batch and let it go before writing, because the store
		// has a single connection.
		rows, err := st.TagRows(ctx, after, 20)
		if err != nil {
			return tagged, err
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			after = r.ID
			var resp *Parsed
			if !r.Truncated && r.ResponseBody != nil {
				p := ParseStored(r.ResponseBody)
				resp = &p
			}
			tags := Tags(r.Path, r.RequestBody, r.Streaming, resp, r.PromptTokens, r.CachedTokens)
			if err := st.SetTags(ctx, r.ID, MarshalTags(tags)); err != nil {
				return tagged, err
			}
			tagged++
		}
	}
	return tagged, st.SetSetting(ctx, key, want)
}
