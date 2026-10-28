package store

import (
	"cmp"
	"context"
	"database/sql"
	"math"
	"slices"

	"github.com/wispborne/notus-swap/internal/source"
)

// SpeedFilter says which finished requests count toward a model's speeds,
// on the Models page and its Speed tab. It is the speed_filter setting.
//
// Prompt size, client and output format affect speed, so build comparisons
// group similar requests before comparing them (see compareKinds).
type SpeedFilter struct {
	// MinOutput: output speed counts requests with at least this many
	// output tokens. Shorter answers are mostly fixed overhead.
	MinOutput int64 `json:"min_output"`
	// MinNewPrompt: prompt speed counts requests with at least this many
	// prompt tokens that weren't cached. Fewer don't fill a batch.
	MinNewPrompt int64 `json:"min_new_prompt"`
	// Alone counts only requests the server worked on alone for nearly all
	// their time (see alone). Sharing the GPU slows both requests.
	Alone bool `json:"alone"`
	// NoImages leaves requests with images out of prompt speed. Reading an
	// image takes far longer per token than text.
	NoImages bool `json:"no_images"`
	// ExcludeClients leaves out requests from these clients, by their short
	// names (source.Name). A client not listed counts, so new clients count
	// until left out.
	ExcludeClients []string `json:"exclude_clients"`
	// MinContext and MaxContext limit the prompt's size in tokens, cached
	// tokens included. 0 means no limit.
	MinContext int64 `json:"min_context"`
	MaxContext int64 `json:"max_context"`
	// Structured is "" to count requests whatever they asked for,
	// "exclude" to leave out requests that asked for JSON or a schema, or
	// "only" to count only those.
	Structured string `json:"structured"`
}

// DefaultSpeedFilter is the filter used until the user saves one.
func DefaultSpeedFilter() SpeedFilter {
	return SpeedFilter{MinOutput: 128, MinNewPrompt: 2048, Alone: true, NoImages: true, ExcludeClients: []string{}}
}

// Valid reports whether every field holds an allowed value.
func (f SpeedFilter) Valid() bool {
	return f.MinOutput >= 0 && f.MinNewPrompt >= 0 && f.MinContext >= 0 && f.MaxContext >= 0 &&
		(f.MaxContext == 0 || f.MaxContext >= f.MinContext) &&
		(f.Structured == "" || f.Structured == "exclude" || f.Structured == "only")
}

// aloneShare is how much of its time a request may share the server with
// others and still count as alone. A little overlap comes from timings that
// are only roughly placed.
const aloneShare = 0.05

// comparePerKind is how many counted requests of one kind both groups need
// before that kind is used to compare them.
const comparePerKind = 10

// speedRow is one finished request, with what the speed rules need.
type speedRow struct {
	id, started    int64
	build, cmd     string
	prompt, cached int64 // prompt is -1 when unknown
	output         int64 // -1 when unknown
	promptPS       *float64
	genPS          *float64
	workMs         float64 // prompt plus generation time; 0 when unknown
	overlapMs      int64   // -1 when unknown
	client         string
	images         bool
	structured     bool
}

// speedColumns must match the scan order in scanSpeedRows.
const speedColumns = `id, started_at, COALESCE(build, ''), COALESCE(cmd_hash, ''),
	COALESCE(prompt_tokens, -1), COALESCE(cached_tokens, 0), COALESCE(completion_tokens, -1),
	prompt_per_second, predicted_per_second, COALESCE(prompt_ms, 0) + COALESCE(predicted_ms, 0),
	COALESCE(overlap_ms, -1), COALESCE(user_agent, ''), retry_of IS NOT NULL,
	COALESCE(instr(tags, '"kind":"images"') > 0, 0),
	COALESCE(instr(tags, '"kind":"schema"') > 0 OR instr(tags, '"kind":"json"') > 0, 0)`

func scanSpeedRows(rows *sql.Rows) ([]speedRow, error) {
	defer rows.Close()
	var out []speedRow
	for rows.Next() {
		var r speedRow
		var ua string
		var retry bool
		if err := rows.Scan(&r.id, &r.started, &r.build, &r.cmd, &r.prompt, &r.cached, &r.output, &r.promptPS, &r.genPS,
			&r.workMs, &r.overlapMs, &ua, &retry, &r.images, &r.structured); err != nil {
			return nil, err
		}
		r.client = source.Name(ua, retry)
		out = append(out, r)
	}
	return out, rows.Err()
}

// alone reports whether the server worked on r alone for nearly all its time.
// Requests stored before overlap was measured count as alone.
func (r speedRow) alone() bool {
	if r.overlapMs <= 0 {
		return true
	}
	return r.workMs > 0 && float64(r.overlapMs) <= aloneShare*r.workMs
}

func (r speedRow) hasSpeeds() bool { return r.genPS != nil || r.promptPS != nil }

// kept applies the rules that both speeds share.
func (f SpeedFilter) kept(r speedRow) bool {
	if f.Alone && !r.alone() {
		return false
	}
	if slices.Contains(f.ExcludeClients, r.client) {
		return false
	}
	if f.MinContext > 0 && r.prompt < f.MinContext || f.MaxContext > 0 && (r.prompt < 0 || r.prompt > f.MaxContext) {
		return false
	}
	return f.Structured == "" || (f.Structured == "only") == r.structured
}

// countsGen reports whether r counts toward output speed.
func (f SpeedFilter) countsGen(r speedRow) bool {
	return r.genPS != nil && *r.genPS > 0 && r.output >= f.MinOutput && f.kept(r)
}

// countsPrompt reports whether r counts toward prompt speed.
func (f SpeedFilter) countsPrompt(r speedRow) bool {
	fresh := r.prompt - r.cached
	return r.promptPS != nil && *r.promptPS > 0 && r.prompt >= 0 && fresh > 0 && fresh >= f.MinNewPrompt &&
		!(f.NoImages && r.images) && f.kept(r)
}

// band names which of the ranges split by limits n falls in.
func band(n int64, limits ...int64) byte {
	for i, l := range limits {
		if n < l {
			return byte('a' + i)
		}
	}
	return byte('a' + len(limits))
}

// Requests are compared only with requests of the same kind. For output
// speed, groups use the client, prompt size range and structured output.
// Prompt speed groups use new-token and total prompt size ranges.
func genKind(r speedRow) string {
	s := "-"
	if r.structured {
		s = "s"
	}
	return r.client + "|" + string(band(r.prompt, 16_000, 64_000)) + s
}

func promptKind(r speedRow) string {
	return string([]byte{band(r.prompt-r.cached, 8_000, 32_000), band(r.prompt, 16_000, 64_000)})
}

// speeds holds the counted speeds of a set of requests, by kind.
type speeds map[string][]float64

func (s speeds) add(kind string, v float64) { s[kind] = append(s[kind], v) }

func (s speeds) all() []float64 {
	var out []float64
	for _, v := range s {
		out = append(out, v...)
	}
	return out
}

func median(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	v = slices.Clone(v)
	slices.Sort(v)
	m := v[len(v)/2]
	if len(v)%2 == 0 {
		m = (v[len(v)/2-1] + m) / 2
	}
	return &m
}

// compareKinds works out how much faster newer is than older, using only the
// kinds of requests both have at least comparePerKind of. Each kind gives
// the ratio of its medians, and the ratios are averaged on a log scale,
// weighted by the smaller of the two counts. It returns nil when no kind is
// shared, and how many requests the comparison used.
func compareKinds(newer, older speeds) (*float64, int) {
	var sum, weight float64
	used := 0
	for kind, a := range newer {
		b := older[kind]
		if len(a) < comparePerKind || len(b) < comparePerKind {
			continue
		}
		w := float64(min(len(a), len(b)))
		sum += w * math.Log(*median(a) / *median(b))
		weight += w
		used += len(a) + len(b)
	}
	if weight == 0 {
		return nil, 0
	}
	change := math.Exp(sum/weight) - 1
	return &change, used
}

// BuildStat is one model's speed on one server build and command.
type BuildStat struct {
	Build     string `json:"build"`    // empty for requests with no known build
	CmdHash   string `json:"cmd_hash"` // empty for requests with no known command
	Requests  int64  `json:"requests"`
	FirstUsed int64  `json:"first_used"` // unix ms
	LastUsed  int64  `json:"last_used"`
	// Current marks the build and command of the model's latest request
	// with speeds: what the model runs now, as far as is known.
	Current bool `json:"current"`
	// Medians over the requests that count (SpeedFilter), and how many did.
	GenPerSecond    *float64 `json:"gen_per_second"`
	GenCounted      int      `json:"gen_counted"`
	PromptPerSecond *float64 `json:"prompt_per_second"`
	PromptCounted   int      `json:"prompt_counted"`
	// The change from the next row, the group started before this one,
	// on the same kinds of requests (compareKinds), and how many requests
	// that used. Nil when they share no kind.
	GenChange      *float64 `json:"gen_change"`
	GenCompared    int      `json:"gen_compared"`
	PromptChange   *float64 `json:"prompt_change"`
	PromptCompared int      `json:"prompt_compared"`

	gen, prompt speeds
}

// ClientCount is how many of a model's finished requests came from a client.
type ClientCount struct {
	Name     string `json:"name"` // "" when the request named no client
	Requests int    `json:"requests"`
}

// Builds is a model's speed on each server build and command.
type Builds struct {
	Rows    []BuildStat   `json:"rows"`
	Clients []ClientCount `json:"clients"` // every client seen, the most requests first
	// Requests is every finished request; GenCounted and PromptCounted are
	// how many count toward each speed.
	Requests      int `json:"requests"`
	GenCounted    int `json:"gen_counted"`
	PromptCounted int `json:"prompt_counted"`
}

// ModelBuilds lists a model's finished requests grouped by server build and
// command, the group started most recently first. names are the model's ID
// and aliases.
func (s *Store) ModelBuilds(ctx context.Context, names []string, f SpeedFilter) (Builds, error) {
	in, args := inList(names)
	rows, err := s.db.QueryContext(ctx, `SELECT `+speedColumns+` FROM requests WHERE model IN `+in+` AND state = ? ORDER BY id`, append(args, StateDone)...)
	if err != nil {
		return Builds{}, err
	}
	reqs, err := scanSpeedRows(rows)
	if err != nil {
		return Builds{}, err
	}
	out := Builds{Rows: []BuildStat{}, Clients: []ClientCount{}, Requests: len(reqs)}
	type key struct{ build, cmd string }
	at := map[key]int{}
	clients := map[string]int{}
	var current key
	for _, r := range reqs {
		k := key{r.build, r.cmd}
		i, ok := at[k]
		if !ok {
			i = len(out.Rows)
			at[k] = i
			out.Rows = append(out.Rows, BuildStat{Build: r.build, CmdHash: r.cmd, FirstUsed: r.started, LastUsed: r.started, gen: speeds{}, prompt: speeds{}})
		}
		b := &out.Rows[i]
		b.Requests++
		b.FirstUsed, b.LastUsed = min(b.FirstUsed, r.started), max(b.LastUsed, r.started)
		if f.countsGen(r) {
			b.gen.add(genKind(r), *r.genPS)
			out.GenCounted++
		}
		if f.countsPrompt(r) {
			b.prompt.add(promptKind(r), *r.promptPS)
			out.PromptCounted++
		}
		if r.hasSpeeds() {
			current = k
		}
		clients[r.client]++
	}
	if i, ok := at[current]; ok {
		out.Rows[i].Current = true
	}
	slices.SortStableFunc(out.Rows, func(a, b BuildStat) int { return cmp.Compare(b.FirstUsed, a.FirstUsed) })
	for i := range out.Rows {
		b := &out.Rows[i]
		gen, prompt := b.gen.all(), b.prompt.all()
		b.GenPerSecond, b.GenCounted = median(gen), len(gen)
		b.PromptPerSecond, b.PromptCounted = median(prompt), len(prompt)
		if i+1 < len(out.Rows) {
			older := out.Rows[i+1]
			b.GenChange, b.GenCompared = compareKinds(b.gen, older.gen)
			b.PromptChange, b.PromptCompared = compareKinds(b.prompt, older.prompt)
		}
	}
	for name, n := range clients {
		out.Clients = append(out.Clients, ClientCount{name, n})
	}
	slices.SortFunc(out.Clients, func(a, b ClientCount) int {
		return cmp.Or(cmp.Compare(b.Requests, a.Requests), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// CurrentSpeeds is a model's speed on its current build and command (see
// BuildStat.Current): the same numbers as that row of ModelBuilds.
type CurrentSpeeds struct {
	Build           string   `json:"build"`
	CmdHash         string   `json:"cmd_hash"`
	GenPerSecond    *float64 `json:"gen_per_second"`
	GenCounted      int      `json:"gen_counted"`
	PromptPerSecond *float64 `json:"prompt_per_second"`
	PromptCounted   int      `json:"prompt_counted"`
}

// ModelCurrentSpeeds works out CurrentSpeeds. names are the model's ID and
// aliases. It returns nil when the model has no finished request with speeds.
func (s *Store) ModelCurrentSpeeds(ctx context.Context, names []string, f SpeedFilter) (*CurrentSpeeds, error) {
	var c CurrentSpeeds
	in, args := inList(names)
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(build, ''), COALESCE(cmd_hash, '') FROM requests
		WHERE model IN `+in+` AND state = ? AND (predicted_per_second IS NOT NULL OR prompt_per_second IS NOT NULL)
		ORDER BY id DESC LIMIT 1`, append(slices.Clone(args), StateDone)...).Scan(&c.Build, &c.CmdHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+speedColumns+` FROM requests
		WHERE model IN `+in+` AND state = ? AND COALESCE(build, '') = ? AND COALESCE(cmd_hash, '') = ?
		ORDER BY id`, append(args, StateDone, c.Build, c.CmdHash)...)
	if err != nil {
		return nil, err
	}
	reqs, err := scanSpeedRows(rows)
	if err != nil {
		return nil, err
	}
	var gen, prompt []float64
	for _, r := range reqs {
		if f.countsGen(r) {
			gen = append(gen, *r.genPS)
		}
		if f.countsPrompt(r) {
			prompt = append(prompt, *r.promptPS)
		}
	}
	c.GenPerSecond, c.GenCounted = median(gen), len(gen)
	c.PromptPerSecond, c.PromptCounted = median(prompt), len(prompt)
	return &c, nil
}
