package llamaswap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Props is what a model's llama-server reports about its limits and build.
type Props struct {
	NCtx     int64 // context size per slot
	NPredict int64 // default output limit; 0 or less for none
	Slots    int64
	Build    string // such as "b11146-7fe450e19"; "" when not reported
	Caps     Caps
}

// Caps is what a model's llama-server says the model can take in and do.
type Caps struct {
	Vision bool `json:"vision"`
	Audio  bool `json:"audio"`
	// Thinking is set when the chat template handles thinking text.
	Thinking bool `json:"thinking"`
	// Template is llama-server's chat_template_caps, such as
	// supports_tool_calls. Older llama-server versions don't send it.
	Template map[string]bool `json:"template,omitempty"`
}

// thinkingMarks are bits of chat template that only a template that handles
// thinking text contains.
var thinkingMarks = []string{"<think>", "reasoning_content", "enable_thinking", "reasoning_effort", ".thinking"}

// FetchProps reads a model's limits from llama-server's /props, through
// llama-swap's /upstream/<model>/props.
//
// llama-swap loads a model for any request to it, so call this only while
// the model is ready (see Ready), or it would load the model. A model that
// isn't run by llama-server gives an error.
func (m *Monitor) FetchProps(ctx context.Context, model string) (Props, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", m.base.JoinPath("upstream", model, "props").String(), nil)
	resp, err := m.client.Do(req)
	if err != nil {
		return Props{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Props{}, fmt.Errorf("/props returned HTTP %d", resp.StatusCode)
	}
	return parseProps(resp.Body)
}

func parseProps(r io.Reader) (Props, error) {
	var x struct {
		Settings struct {
			NCtx     int64  `json:"n_ctx"`
			NPredict *int64 `json:"n_predict"` // older llama-server versions
			Params   struct {
				NPredict *int64 `json:"n_predict"`
			} `json:"params"`
		} `json:"default_generation_settings"`
		TotalSlots int64  `json:"total_slots"`
		BuildInfo  string `json:"build_info"`
		Modalities struct {
			Vision bool `json:"vision"`
			Audio  bool `json:"audio"`
		} `json:"modalities"`
		Template     string          `json:"chat_template"`
		TemplateCaps map[string]bool `json:"chat_template_caps"`
	}
	if err := json.NewDecoder(r).Decode(&x); err != nil {
		return Props{}, fmt.Errorf("reading /props: %w", err)
	}
	if x.Settings.NCtx <= 0 {
		return Props{}, fmt.Errorf("/props has no context size")
	}
	p := Props{NCtx: x.Settings.NCtx, Slots: x.TotalSlots, Build: x.BuildInfo}
	p.Caps = Caps{Vision: x.Modalities.Vision, Audio: x.Modalities.Audio, Template: x.TemplateCaps}
	p.Caps.Thinking = x.TemplateCaps["supports_reasoning_effort"] || x.TemplateCaps["supports_preserve_reasoning"]
	for _, s := range thinkingMarks {
		p.Caps.Thinking = p.Caps.Thinking || strings.Contains(x.Template, s)
	}
	if n := x.Settings.Params.NPredict; n != nil {
		p.NPredict = *n
	} else if n := x.Settings.NPredict; n != nil {
		p.NPredict = *n
	}
	return p, nil
}
