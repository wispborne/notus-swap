package api

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
)

// paletteSize is how many model colours the UI has (palette in
// web/src/lib/modelColors.svelte.ts).
const paletteSize = 9

// modelColors gives each model a colour once and keeps it, in the
// model_colors setting (model name to palette index). So a model's colour
// stays the same across restarts, config edits, and pages, and in every
// browser.
//
// A new model gets a colour that its neighbours in llama-swap's config don't
// have, and that the fewest config models use. On a fresh database this gives
// the same colours the UI picked before they were stored: config models in
// config order, then other models in name order.
type modelColors struct {
	mu     sync.Mutex
	loaded bool
	colors map[string]int
	config []string // llama-swap's model order, from the last status poll
	// seeded is set once every model in stored requests has a colour.
	seeded bool
}

// assign gives colours to any models that don't have one yet and returns a
// copy of every stored colour. config is llama-swap's model list in order, or
// empty to keep the last one. others are models seen in requests.
func (a *API) assignColors(ctx context.Context, config []string, others []string) map[string]int {
	c := &a.colors
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.colors = map[string]int{}
		if v, err := a.Store.Setting(ctx, "model_colors"); err != nil {
			a.Log.Error("read model colours", "err", err)
			return nil
		} else if v != "" {
			json.Unmarshal([]byte(v), &c.colors)
		}
		c.loaded = true
	}
	if len(config) > 0 {
		c.config = config
		if !c.seeded {
			if all, err := a.Store.Models(ctx); err == nil {
				others = append(slices.Clone(others), all...)
				c.seeded = true
			}
		}
	}

	var add []string
	for _, m := range c.config {
		if _, ok := c.colors[m]; !ok && m != "" {
			add = append(add, m)
		}
	}
	// Wait for llama-swap's list first, so config models come first.
	var rest []string
	for _, m := range others {
		if len(c.config) == 0 {
			break
		}
		if _, ok := c.colors[m]; !ok && m != "" && !slices.Contains(add, m) && !slices.Contains(rest, m) {
			rest = append(rest, m)
		}
	}
	slices.Sort(rest)
	add = append(add, rest...)

	for k, m := range add {
		c.colors[m] = c.pick(m, add[:k])
	}
	if len(add) > 0 {
		if b, err := json.Marshal(c.colors); err == nil {
			if err := a.Store.SetSetting(ctx, "model_colors", string(b)); err != nil {
				a.Log.Error("save model colours", "err", err)
			}
		}
	}
	out := make(map[string]int, len(c.colors))
	for m, i := range c.colors {
		out[m] = i
	}
	return out
}

// pick chooses a colour for a model that has none: not a colour of its
// neighbours in the config, then the one the fewest models use, then the
// lowest. Only config models and the models given colours just before it
// (added) count, so colours of models gone from the config can be used again.
func (c *modelColors) pick(model string, added []string) int {
	used := make([]int, paletteSize)
	for _, m := range c.config {
		if i, ok := c.colors[m]; ok && i >= 0 && i < paletteSize {
			used[i]++
		}
	}
	for _, m := range added {
		if !slices.Contains(c.config, m) {
			used[c.colors[m]]++
		}
	}
	avoid := map[int]bool{}
	if k := slices.Index(c.config, model); k >= 0 {
		for _, n := range []int{k - 1, k + 1} {
			if n >= 0 && n < len(c.config) {
				if i, ok := c.colors[c.config[n]]; ok {
					avoid[i] = true
				}
			}
		}
	}
	best := -1
	for i := range paletteSize {
		if avoid[i] {
			continue
		}
		if best < 0 || used[i] < used[best] {
			best = i
		}
	}
	if best < 0 {
		best = 0
	}
	return best
}

// statusColors is the model_colors for the status poll, without hidden models
// while they are hidden.
func (a *API) statusColors(ctx context.Context, known []string, p Privacy) map[string]int {
	colors := a.assignColors(ctx, known, nil)
	if !p.ShowHidden {
		for _, m := range p.HiddenModels {
			delete(colors, m)
		}
	}
	if colors == nil {
		colors = map[string]int{}
	}
	return colors
}
