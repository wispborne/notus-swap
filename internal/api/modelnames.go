package api

import (
	"context"
	"slices"

	"github.com/wispborne/notus-swap/internal/store"
)

// Requests store the model name the client sent, which may be an alias from
// llama-swap's config. Everything that counts or shows models per model goes
// by the model's ID, so an alias's requests count toward its model.

// modelID gives the model ID for a name that may be an alias. A name llama-swap
// doesn't know is returned as it is.
func (a *API) modelID(name string) string {
	if a.LlamaSwap == nil {
		return name
	}
	return a.LlamaSwap.ID(name)
}

// modelNames gives every name requests may use for a model: its ID and its
// aliases. The model may be named by an alias too.
func (a *API) modelNames(model string) []string {
	if a.LlamaSwap == nil {
		return []string{model}
	}
	return a.LlamaSwap.Names(a.LlamaSwap.ID(model))
}

// issueMutes reads the issue_mutes setting, with each mute also covering the
// model's other names.
func (a *API) issueMutes(ctx context.Context) ([]store.IssueMute, error) {
	mutes, err := a.Store.IssueMutes(ctx)
	if err != nil {
		return nil, err
	}
	out := []store.IssueMute{}
	for _, m := range mutes {
		for _, name := range a.modelNames(m.Model) {
			if k := (store.IssueMute{Kind: m.Kind, Model: name}); !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

// modelIDs turns a list of model names into their model IDs, each once, sorted.
func (a *API) modelIDs(names []string) []string {
	out := []string{}
	for _, n := range names {
		if id := a.modelID(n); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
