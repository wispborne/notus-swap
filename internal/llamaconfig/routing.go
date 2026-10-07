package llamaconfig

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Problem describes a config issue found by notus-swap's routing check.
type Problem struct {
	Line    int    `json:"line"` // 1-based, 0 if unknown
	Message string `json:"message"`
}

const (
	routerPath    = "routing.router.settings"
	prioritiesKey = "routing.scheduler.settings.fifo.priority"
)

// RoutingProblems finds undefined model and variable references using
// llama-swap's rules (internal/config and internal/router):
//   - group engine: each entry in a group's members must be a model ID or alias.
//   - matrix engine: each value in vars must be a model ID (not an alias). Each
//     key in evict_costs and each name in the sets expressions must be a var or
//     a model ID.
//   - scheduler: each key in fifo.priority must be a model ID or alias.
//
// Only the selected engine is checked (group by default), since llama-swap
// ignores the other's settings. Legacy top-level groups and matrix settings
// follow the same rules.
//
// Invalid YAML returns no problems.
func RoutingProblems(content string) []Problem {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(content), &doc) != nil {
		return nil
	}
	root := resolve(&doc)

	models := map[string]bool{}
	aliases := map[string]bool{}
	for id, m := range pairs(child(root, "models")) {
		models[id.Value] = true
		if list := resolve(child(m, "aliases")); list != nil && list.Kind == yaml.SequenceNode {
			for _, a := range list.Content {
				aliases[resolve(a).Value] = true
			}
		}
	}
	isModel := func(name string) bool { return models[name] }
	isModelOrAlias := func(name string) bool { return models[name] || aliases[name] }

	var out []Problem
	seen := map[Problem]bool{}
	check := func(n *yaml.Node, where, name string, ok func(string) bool) {
		p := Problem{Line: n.Line, Message: fmt.Sprintf("%s: model %q is not defined in models", where, name)}
		if !ok(name) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	// Legacy top-level settings take precedence here. llama-swap rejects configs
	// that also use routing.router.settings, so validation handles that conflict.
	router := child(child(root, "routing"), "router")
	settings, prefix := child(router, "settings"), routerPath+"."
	use := "group"
	if u := child(router, "use"); u != nil && u.Value != "" {
		use = u.Value
	}
	if topMatrix, topGroups := child(root, "matrix"), child(root, "groups"); isMapping(topMatrix) || (isMapping(topGroups) && len(resolve(topGroups).Content) > 0) {
		settings, prefix, use = root, "", "group"
		if isMapping(topMatrix) {
			use = "matrix"
		}
	}

	switch use {
	case "group":
		for name, group := range pairs(child(settings, "groups")) {
			where := prefix + "groups." + name.Value + ".members"
			members := resolve(child(group, "members"))
			if members == nil || members.Kind != yaml.SequenceNode {
				continue
			}
			for _, m := range members.Content {
				check(m, where, resolve(m).Value, isModelOrAlias)
			}
		}
	case "matrix":
		matrix := child(settings, "matrix")
		vars := map[string]bool{}
		for k, v := range pairs(child(matrix, "vars")) {
			vars[k.Value] = true
			check(v, prefix+"matrix.vars."+k.Value, resolve(v).Value, isModel)
		}
		known := func(name string) bool { return vars[name] || models[name] }
		for k := range pairs(child(matrix, "evict_costs")) {
			check(k, prefix+"matrix.evict_costs", k.Value, known)
		}
		for k, expr := range pairs(child(matrix, "sets")) {
			for _, name := range setNames(resolve(expr).Value) {
				check(expr, prefix+"matrix.sets."+k.Value, name, known)
			}
		}
	}

	priority := child(child(child(child(child(root, "routing"), "scheduler"), "settings"), "fifo"), "priority")
	for k := range pairs(priority) {
		check(k, prioritiesKey, k.Value, isModelOrAlias)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// setNames returns the model names and vars in a matrix set expression such as
// "(g | qwen-model) & v". References to other sets (+name) are left out.
func setNames(expr string) []string {
	var names []string
	for _, t := range strings.FieldsFunc(expr, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("&|()", r)
	}) {
		if !strings.HasPrefix(t, "+") {
			names = append(names, t)
		}
	}
	return names
}

// resolve follows a document or alias to the node it stands for.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && (n.Kind == yaml.DocumentNode || n.Kind == yaml.AliasNode) {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else if len(n.Content) > 0 {
			n = n.Content[0]
		} else {
			return nil
		}
	}
	return n
}

func isMapping(n *yaml.Node) bool {
	n = resolve(n)
	return n != nil && n.Kind == yaml.MappingNode
}

// child returns the value under key in a mapping, or nil.
func child(n *yaml.Node, key string) *yaml.Node {
	for k, v := range pairs(n) {
		if k.Value == key {
			return v
		}
	}
	return nil
}

// pairs iterates a mapping's key and value nodes in file order. It yields
// nothing for anything that isn't a mapping.
func pairs(n *yaml.Node) func(yield func(k, v *yaml.Node) bool) {
	return func(yield func(k, v *yaml.Node) bool) {
		n := resolve(n)
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i], n.Content[i+1]) {
				return
			}
		}
	}
}
