package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sync"
)

// paletteSize is how many model colours the UI has (palette in
// web/src/lib/modelColors.svelte.ts). Only the first autoColors are given out
// automatically; the rest can only be picked by the user.
const (
	paletteSize = 18
	autoColors  = 9
)

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
	// marks are the colours outside the palette and the shapes the user
	// chose, in the model_marks setting. Models not in it use their palette
	// colour and a circle.
	marks  map[string]Mark
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
	if !a.loadColors(ctx) {
		return nil
	}
	if len(config) > 0 {
		c.config = config
		if !c.seeded {
			if all, err := a.Store.Models(ctx); err == nil {
				others = append(slices.Clone(others), a.modelIDs(all)...)
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

// loadColors reads the model_colors and model_marks settings the first time.
// It reports false if they couldn't be read. c.mu must be held.
func (a *API) loadColors(ctx context.Context) bool {
	c := &a.colors
	if c.loaded {
		return true
	}
	c.colors = map[string]int{}
	c.marks = map[string]Mark{}
	for key, v := range map[string]any{"model_colors": &c.colors, "model_marks": &c.marks} {
		s, err := a.Store.Setting(ctx, key)
		if err != nil {
			a.Log.Error("read model colours", "setting", key, "err", err)
			return false
		}
		if s != "" {
			json.Unmarshal([]byte(s), v)
		}
	}
	c.loaded = true
	return true
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
		if i := c.colors[m]; !slices.Contains(c.config, m) && i >= 0 && i < paletteSize {
			used[i]++
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
	for i := range autoColors {
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
// while they are hidden. Aliases are left out too: the UI colours a model by
// its ID. Older versions gave aliases colours of their own.
func (a *API) statusColors(ctx context.Context, known []string, p Privacy) map[string]int {
	colors := a.assignColors(ctx, known, nil)
	for m := range colors {
		if id := a.modelID(m); id != m || !p.ShowHidden && slices.Contains(p.HiddenModels, id) {
			delete(colors, m)
		}
	}
	if colors == nil {
		colors = map[string]int{}
	}
	return colors
}

// statusMarks is the model_marks for the status poll, without hidden models
// while they are hidden.
func (a *API) statusMarks(ctx context.Context, p Privacy) map[string]Mark {
	c := &a.colors
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]Mark{}
	if !a.loadColors(ctx) {
		return out
	}
	for m, k := range c.marks {
		if id := a.modelID(m); id == m && (p.ShowHidden || !slices.Contains(p.HiddenModels, id)) {
			out[m] = k
		}
	}
	return out
}

// Mark is how the user chose to show a model, beyond its palette colour.
type Mark struct {
	// Color is a colour outside the palette, as "#rrggbb". Empty means the
	// model's palette colour.
	Color string `json:"color,omitempty"`
	// Shape is the shape of the model's dot. Empty means a circle. The
	// shapes are drawn by web/src/lib/modelColors.svelte.ts.
	Shape string `json:"shape,omitempty"`
}

var shapes = []string{"circle", "square", "diamond", "triangle", "triangle_down", "plus",
	"triangle_left", "triangle_right", "cross", "star", "hexagon", "hourglass"}

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// MarkChange is the body of PUT /notus/api/models/{model}/mark.
type MarkChange struct {
	// Palette, when set, is the palette colour to use. It also clears Color.
	Palette *int `json:"palette"`
	// Color is a colour outside the palette, "#rrggbb", or empty for none.
	Color string `json:"color"`
	Shape string `json:"shape"`
}

var errBadMark = errors.New("bad mark")

// setMark saves a model's colour and shape.
func (a *API) setMark(ctx context.Context, model string, ch MarkChange) error {
	if model == "" || ch.Palette != nil && (*ch.Palette < 0 || *ch.Palette >= paletteSize) ||
		ch.Color != "" && !hexColor.MatchString(ch.Color) || ch.Shape != "" && !slices.Contains(shapes, ch.Shape) {
		return errBadMark
	}
	c := &a.colors
	c.mu.Lock()
	defer c.mu.Unlock()
	if !a.loadColors(ctx) {
		return errors.New("couldn't read model colours")
	}
	if ch.Palette != nil {
		c.colors[model] = *ch.Palette
		ch.Color = ""
		b, _ := json.Marshal(c.colors)
		if err := a.Store.SetSetting(ctx, "model_colors", string(b)); err != nil {
			return err
		}
	}
	if ch.Shape == "circle" {
		ch.Shape = ""
	}
	if k := (Mark{Color: ch.Color, Shape: ch.Shape}); k == (Mark{}) {
		delete(c.marks, model)
	} else {
		c.marks[model] = k
	}
	b, _ := json.Marshal(c.marks)
	return a.Store.SetSetting(ctx, "model_marks", string(b))
}

// putMark: PUT /notus/api/models/{model}/mark.
func (a *API) putMark(w http.ResponseWriter, r *http.Request) {
	var ch MarkChange
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		http.Error(w, "Expected a JSON request body", http.StatusBadRequest)
		return
	}
	if err := a.setMark(r.Context(), a.modelID(r.PathValue("model")), ch); errors.Is(err, errBadMark) {
		http.Error(w, "Expected a palette index from 0 to 17, a colour like #40d7a3, and one of the shapes", http.StatusBadRequest)
		return
	} else if err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
