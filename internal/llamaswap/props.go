package llamaswap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Props is what a model's llama-server reports about its limits and build.
type Props struct {
	NCtx     int64 // context size per slot
	NPredict int64 // default output limit; 0 or less for none
	Slots    int64
	Build    string // such as "b11146-7fe450e19"; "" when not reported
}

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
	}
	if err := json.NewDecoder(r).Decode(&x); err != nil {
		return Props{}, fmt.Errorf("reading /props: %w", err)
	}
	if x.Settings.NCtx <= 0 {
		return Props{}, fmt.Errorf("/props has no context size")
	}
	p := Props{NCtx: x.Settings.NCtx, Slots: x.TotalSlots, Build: x.BuildInfo}
	if n := x.Settings.Params.NPredict; n != nil {
		p.NPredict = *n
	} else if n := x.Settings.NPredict; n != nil {
		p.NPredict = *n
	}
	return p, nil
}
