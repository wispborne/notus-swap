package llamaconfig

import (
	"context"
	"strings"
	"testing"
)

const routingModels = "models:\n  llama: {cmd: x}\n  qwen: {cmd: x}\n"

func TestRoutingProblems(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string // each must appear in one problem, in order
	}{
		{"no routing", routingModels, nil},
		{"group defaults to group engine", routingModels + `
routing:
  router:
    settings:
      groups:
        g:
          members: [llama, nope]
`, []string{`groups.g.members: model "nope"`}},
		{"valid groups", routingModels + `
routing:
  router:
    use: group
    settings:
      groups:
        a: {members: [llama]}
        b: {members: [qwen]}
`, nil},
		{"several groups, in file order", routingModels + `
routing:
  router:
    settings:
      groups:
        a:
          members:
            - ghost1
            - llama
        b:
          members:
            - ghost2
`, []string{`"ghost1"`, `"ghost2"`}},
		{"group engine ignores the matrix", routingModels + `
routing:
  router:
    use: group
    settings:
      matrix:
        sets: {s: "llama & ghost"}
`, nil},
		{"matrix engine ignores groups", routingModels + `
routing:
  router:
    use: matrix
    settings:
      groups:
        g: {members: [ghost]}
`, nil},
		{"valid matrix with vars", routingModels + `
routing:
  router:
    use: matrix
    settings:
      matrix:
        vars: {l: llama, q: qwen}
        evict_costs: {l: 5, qwen: 2}
        sets:
          a: "(l | qwen) & q"
          b: "+a & llama"
`, nil},
		{"matrix problems", routingModels + `
routing:
  router:
    use: matrix
    settings:
      matrix:
        vars: {l: llama, x: ghost-var}
        evict_costs: {l: 5, ghost-cost: 2}
        sets:
          a: "(l | ghost-set) & qwen"
`, []string{`vars.x: model "ghost-var"`, `evict_costs: model "ghost-cost"`, `sets.a: model "ghost-set"`}},
		{"a set naming an unknown model twice is reported once", routingModels + `
routing:
  router:
    use: matrix
    settings:
      matrix:
        sets: {a: "ghost & ghost | llama"}
`, []string{`"ghost"`}},
		{"group members and priority may be aliases", `
models:
  llama:
    cmd: x
    aliases: [gpt-4]
routing:
  scheduler:
    settings:
      fifo:
        priority: {llama: 2, gpt-4: 1}
  router:
    settings:
      groups:
        g: {members: [gpt-4]}
`, nil},
		{"matrix vars can't be aliases", `
models:
  llama:
    cmd: x
    aliases: [gpt-4]
routing:
  router:
    use: matrix
    settings:
      matrix:
        vars: {a: gpt-4}
        sets: {s: a}
`, []string{`vars.a: model "gpt-4"`}},
		{"priority names an unknown model, whichever engine is used", routingModels + `
routing:
  scheduler:
    settings:
      fifo:
        priority: {llama: 5, ghost: 1}
  router:
    use: matrix
    settings:
      matrix:
        sets: {s: llama}
`, []string{`fifo.priority: model "ghost"`}},
		{"older top-level groups", routingModels + `
groups:
  g:
    members: [llama, ghost]
`, []string{`groups.g.members: model "ghost"`}},
		{"older top-level matrix", routingModels + `
matrix:
  vars: {l: llama}
  sets: {s: "l & ghost"}
`, []string{`matrix.sets.s: model "ghost"`}},
		{"empty top-level groups leave routing in charge", routingModels + `
groups: {}
routing:
  router:
    settings:
      groups:
        g: {members: [ghost]}
`, []string{`"ghost"`}},
		{"no models section", `
routing:
  router:
    settings:
      groups:
        g: {members: [llama]}
`, []string{`"llama"`}},
		{"YAML aliases in members", `
models:
  llama: {cmd: x}
x-members: &m [llama, ghost]
routing:
  router:
    settings:
      groups:
        g:
          members: *m
`, []string{`"ghost"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RoutingProblems(tc.yaml)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d problems, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i].Message, w) || got[i].Line == 0 {
					t.Errorf("problem %d = %+v, want it to mention %s on a known line", i, got[i], w)
				}
			}
		})
	}
}

func TestRoutingProblemLine(t *testing.T) {
	got := RoutingProblems("models:\n  a: {cmd: x}\nrouting:\n  router:\n    settings:\n      groups:\n        g:\n          members:\n            - a\n            - b\n")
	if len(got) != 1 || got[0].Line != 10 {
		t.Errorf("problems = %+v, want one problem on line 10", got)
	}
}

func TestRoutingProblemsBlockSave(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	f, _ := e.Read()
	bad := routingModels + "routing:\n  router:\n    settings:\n      groups:\n        g: {members: [ghost]}\n"

	c := e.Check(ctx, bad)
	if !c.YAMLOK || len(c.RoutingProblems) != 1 {
		t.Fatalf("check: %+v", c)
	}
	// Routing errors block both immediate and deferred saves.
	if _, _, err := e.Save(ctx, bad, f.Hash, false, false); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("saved a config with an undefined routing model: %v", err)
	}
	if _, cur, _ := e.CheckSave(ctx, bad, f.Hash, false, false); cur != nil {
		t.Error("CheckSave accepted an undefined routing model")
	}
	if _, _, err := e.Save(ctx, bad, f.Hash, true, false); err != nil {
		t.Errorf("save anyway: %v", err)
	}
}
