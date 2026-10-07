package capture

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/wispborne/notus-swap/internal/store"
)

// Kinds of issue found in a request's output. The UI has a title and a
// suggested fix for each.
const (
	IssueTokenLimit      = "token_limit"       // stopped at the request's own max_tokens
	IssueServerLimit     = "server_limit"      // stopped at llama-server's default output limit (-n)
	IssueContextFull     = "context_full"      // prompt + output filled the context
	IssueCutOff          = "cut_off"           // stopped early; the context size isn't known
	IssueThinkingBudget  = "thinking_budget"   // stopped early while still thinking, with no answer
	IssueContextError    = "context_error"     // llama.cpp refused a prompt too long for the context
	IssueContextNearFull = "context_near_full" // finished, but the prompt left little room
)

// IssueChecksVersion goes up whenever a check changes. At startup, stored
// requests are checked again if the last run used an older version.
const IssueChecksVersion = 3

const (
	levelWarning = "warning"
	levelInfo    = "info"
)

// Requests that set a very small limit do so on purpose: to warm up a
// model, test that it answers, or get a one-word label.
const (
	smallLimit         = 64 // no "token limit" issue at or below this
	smallThinkingLimit = 16 // no "thinking budget" issue at or below this
)

// contextSlack allows for the few tokens llama.cpp keeps free when it stops
// at the end of the context.
const contextSlack = 16

// IssueDetail is the numbers behind an issue. Only the fields that apply
// are set.
type IssueDetail struct {
	Cause       string `json:"cause,omitempty"` // for thinking_budget: which limit stopped it
	Prompt      int64  `json:"prompt,omitempty"`
	Output      int64  `json:"output,omitempty"`
	Limit       int64  `json:"limit,omitempty"`        // the request's max_tokens
	ServerLimit int64  `json:"server_limit,omitempty"` // llama-server's default output limit
	NCtx        int64  `json:"n_ctx,omitempty"`        // context size per slot
	Slots       int64  `json:"slots,omitempty"`
	Room        int64  `json:"room,omitempty"` // context left after the prompt
	Message     string `json:"message,omitempty"`
}

// CheckInput is what the issue checks read.
type CheckInput struct {
	Path       string
	State      string
	StatusCode int
	Truncated  bool // a body was cut short when stored
	Request    []byte
	Response   Parsed
	Props      *store.ModelProps // nil when the model's limits aren't known
}

// Check finds problems in a request's output. It only looks at chat and
// text completions and Responses API calls, and only at responses that
// arrived in full.
func Check(in CheckInput) []store.Issue {
	if !checkedPath(in.Path) || in.Truncated {
		return nil
	}
	req := readRequest(in.Request)
	if req.fim {
		return nil // fill-in-the-middle often stops at its limit or returns nothing, by design
	}
	var nCtx, serverLimit, slots int64
	if p := in.Props; p != nil {
		nCtx, serverLimit, slots = p.NCtx, p.NPredict, p.Slots
	}
	resp := in.Response

	if e := resp.Error; e != nil && in.StatusCode >= 400 && isContextError(e) {
		d := IssueDetail{Message: e.Message, NCtx: nCtx, Slots: slots}
		if e.NCtx != nil {
			d.NCtx = *e.NCtx
		}
		if e.NPromptTokens != nil {
			d.Prompt = *e.NPromptTokens
		}
		return []store.Issue{issue(IssueContextError, levelWarning, d)}
	}
	if in.State != store.StateDone || in.StatusCode < 200 || in.StatusCode >= 300 {
		return nil // the client hung up or the stream broke, so the output is incomplete for another reason
	}

	prompt, output := deref(resp.PromptTokens()), deref(resp.CompletionTokens())
	finish := resp.FinishReason
	// llama-server marks every Responses answer "completed", even one that
	// stopped at a limit, so the token counts have to show it.
	if strings.HasSuffix(in.Path, "/responses") && finish != "length" &&
		((req.limit > 0 && output >= req.limit) ||
			(nCtx > 0 && prompt+output >= nCtx-contextSlack) ||
			(serverLimit > 0 && output >= serverLimit)) {
		finish = "length"
	}
	switch finish {
	case "length":
		d := IssueDetail{Prompt: prompt, Output: output, Limit: req.limit, NCtx: nCtx, Slots: slots}
		var cause string
		switch {
		case req.limit > 0 && output >= req.limit:
			cause = IssueTokenLimit
		case nCtx > 0 && prompt+output >= nCtx-contextSlack:
			cause = IssueContextFull
		case serverLimit > 0 && output >= serverLimit:
			cause, d.ServerLimit = IssueServerLimit, serverLimit
		default:
			cause = IssueCutOff
		}
		if strings.TrimSpace(resp.Content) == "" && len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Reasoning) != "" {
			if cause == IssueTokenLimit && req.limit <= smallThinkingLimit {
				return nil
			}
			d.Cause = cause
			return []store.Issue{issue(IssueThinkingBudget, levelWarning, d)}
		}
		if cause == IssueTokenLimit && req.limit <= smallLimit {
			return nil
		}
		return []store.Issue{issue(cause, levelWarning, d)}

	case "stop", "tool_calls":
		// The answer fit, but the next turn of this conversation may not.
		if nCtx <= 0 || prompt <= 0 {
			return nil
		}
		room := nCtx - prompt
		want := nCtx / 10
		// Treat limits above half the context as ceilings, not expected output.
		if req.limit > want && req.limit <= nCtx/2 {
			want = req.limit
		}
		if room < want {
			d := IssueDetail{Prompt: prompt, Output: output, Limit: req.limit, NCtx: nCtx, Slots: slots, Room: room}
			return []store.Issue{issue(IssueContextNearFull, levelInfo, d)}
		}
	}
	return nil
}

// checkedPath reports whether a path is an OpenAI-style chat or text
// completion, or a Responses API call. Other captured calls, such as
// embeddings and reranking, have no text answer, and llama.cpp's own
// /completion has no finish_reason.
func checkedPath(path string) bool {
	return strings.HasSuffix(path, "/completions") || strings.HasSuffix(path, "/responses")
}

func isContextError(e *APIError) bool {
	return e.Type == "exceed_context_size_error" ||
		strings.Contains(e.Message, "exceeds the available context size") ||
		strings.Contains(e.Message, "exceed_context_size")
}

type requestFields struct {
	limit int64 // the output limit the request set; 0 for none
	fim   bool  // a fill-in-the-middle request
}

func readRequest(b []byte) requestFields {
	var x struct {
		MaxTokens           *float64        `json:"max_tokens"`
		MaxCompletionTokens *float64        `json:"max_completion_tokens"`
		MaxOutputTokens     *float64        `json:"max_output_tokens"` // Responses API
		NPredict            *float64        `json:"n_predict"`
		InputPrefix         json.RawMessage `json:"input_prefix"`
		InputSuffix         json.RawMessage `json:"input_suffix"`
		Suffix              json.RawMessage `json:"suffix"`
	}
	_ = json.Unmarshal(b, &x)
	var f requestFields
	for _, v := range []*float64{x.MaxCompletionTokens, x.MaxOutputTokens, x.MaxTokens, x.NPredict} {
		if v != nil && *v > 0 {
			f.limit = int64(*v)
			break
		}
	}
	f.fim = x.InputPrefix != nil || x.InputSuffix != nil || (x.Suffix != nil && string(x.Suffix) != "null")
	return f
}

func issue(kind, level string, d IssueDetail) store.Issue {
	b, _ := json.Marshal(d)
	return store.Issue{Kind: kind, Level: level, Detail: b}
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// Recheck runs the issue checks again over stored requests that still have
// their bodies. With models set, it only checks those models' requests whose
// context size wasn't known, which is how older requests pick up a context
// size learned later. props looks up a model's limits. It returns how many
// requests it checked.
func Recheck(ctx context.Context, st *store.Store, props func(model string) *store.ModelProps, models []string) (int, error) {
	checked := 0
	var after int64
	for {
		// Read a small batch and let it go before writing, because the store
		// has a single connection.
		rows, err := st.CheckRows(ctx, after, 20, models)
		if err != nil {
			return checked, err
		}
		if len(rows) == 0 {
			return checked, nil
		}
		for _, r := range rows {
			after = r.ID
			p := props(r.Model)
			// The context size stored with the request beats the current one,
			// which may have changed since.
			if r.NCtx != nil {
				var q store.ModelProps
				if p != nil {
					q = *p
				}
				q.NCtx = *r.NCtx
				p = &q
			}
			resp := ParseStored(r.ResponseBody)
			issues := Check(CheckInput{Path: r.Path, State: r.State, StatusCode: r.StatusCode, Truncated: r.Truncated,
				Request: r.RequestBody, Response: resp, Props: p})
			var nCtx *int64
			if p != nil {
				nCtx = &p.NCtx
			}
			if err := st.SetChecked(ctx, r.ID, resp.FinishReason, nCtx, issues); err != nil {
				return checked, err
			}
			checked++
		}
	}
}

// RecheckIfChanged checks every stored request again when the checks have
// changed since the last run. It returns how many requests it checked.
func RecheckIfChanged(ctx context.Context, st *store.Store, props func(model string) *store.ModelProps) (int, error) {
	const key = "issue_checks_version"
	v, err := st.Setting(ctx, key)
	if err != nil {
		return 0, err
	}
	want, _ := json.Marshal(IssueChecksVersion)
	if v == string(want) {
		return 0, nil
	}
	n, err := Recheck(ctx, st, props, nil)
	if err != nil {
		return n, err
	}
	return n, st.SetSetting(ctx, key, string(want))
}
