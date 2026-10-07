<script lang="ts">
  import { onMount } from 'svelte'
  import { listRequests, getRequest, summaryFromStart, type ListFilter, type Summary } from '../lib/api'
  import { modelColor, shortBuild, when } from '../lib/format'
  import { issueKinds, issueMutes } from '../lib/issues.svelte'
  import { live, liveOutSpeed } from '../lib/live.svelte'
  import ChipPopover from '../lib/ChipPopover.svelte'
  import NotableChips from '../lib/NotableChips.svelte'
  import { heatColor, heatMap, type HeatColumn } from '../lib/heat'
  import { rowChips } from '../lib/notable'
  import { noteModels } from '../lib/modelColors.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import RequestDetail from '../lib/RequestDetail.svelte'
  import StatusPill from '../lib/StatusPill.svelte'

  const PAGE = 100
  const states = [
    ['', 'All'],
    ['in_flight', 'Streaming'],
    ['done', 'Completed'],
    ['failed', 'Errors'],
  ] as const

  let rows = $state<Summary[]>([])
  let models = $state<string[]>([])
  let q = $state('')
  // /notus/requests?model=M starts filtered to model M (the Models page links here).
  let model = $state(new URLSearchParams(location.search).get('model') ?? '')
  let stateFilter = $state('')
  let issue = $state('')
  // /notus/requests?open=12 opens request 12 (the Dashboard links here).
  let open = $state<number | null>(Number(new URLSearchParams(location.search).get('open')) || null)
  let loading = $state(false)
  let more = $state(true)
  let error = $state('')
  let now = $state(Date.now())

  const filter = $derived<ListFilter>({ q: q.trim(), model, state: stateFilter, issue })

  // In-flight requests first, then newest first.
  const visibleRows = $derived(rows.filter((r) => privacy.visible(r.model)))
  const shown = $derived([...visibleRows.filter((r) => r.state === 'in_flight'), ...visibleRows.filter((r) => r.state !== 'in_flight')])
  const visibleModels = $derived(models.filter(privacy.visible))

  async function load(reset: boolean) {
    loading = true
    error = ''
    try {
      const before = reset ? undefined : rows.at(-1)?.id
      const res = await listRequests(filter, before, PAGE)
      rows = reset ? res.requests : [...rows, ...res.requests]
      if (res.models) {
        models = res.models
        noteModels(res.models)
      }
      more = res.requests.length === PAGE
    } catch (e) {
      error = String(e)
    }
    loading = false
  }

  // Reload when the filters change, waiting a moment while typing.
  let timer: ReturnType<typeof setTimeout>
  $effect(() => {
    void filter
    void issueMutes.version
    clearTimeout(timer)
    timer = setTimeout(() => load(true), 200)
  })

  function matches(r: Summary) {
    const f = filter
    if (f.model && r.model !== f.model) return false
    if (f.state && r.state !== f.state) return false
    // A request still in flight may yet have an issue, so it stays listed.
    if (f.issue && r.state !== 'in_flight' && !(f.issue === 'any' ? r.issues.length : r.issues.includes(f.issue))) return false
    if (f.q) {
      const s = f.q.toLowerCase()
      if (!r.preview.toLowerCase().includes(s) && !r.model.toLowerCase().includes(s)) return false
    }
    return true
  }

  onMount(() => {
    live.connect()
    const stop = live.on(async (e) => {
      if (e.type === 'start') {
        const r = summaryFromStart(e.start)
        if (matches(r) && !rows.some((x) => x.id === r.id)) rows = [r, ...rows]
        if (r.model && !models.includes(r.model)) models = [...models, r.model].sort()
      } else if (e.type === 'finish') {
        if (!rows.some((x) => x.id === e.id)) return
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { request, response_raw, response, truncated, live: _, chunks, issue_details, ...done } = await getRequest(e.id)
        rows = rows.map((x) => (x.id === e.id ? done : x)).filter((x) => x.id === open || matches(x))
      }
    })
    const tick = setInterval(() => (now = Date.now()), 1000)
    return () => {
      stop()
      clearInterval(tick)
    }
  })

  function ttft(r: Summary) {
    const first = r.first_token_at ?? live.flights.get(r.id)?.firstTokenAt
    return first ? first - r.started_at : null
  }

  // Each column uses one unit and one number of decimals for every row, so
  // big and small values can be told apart at a glance.
  function fixed(v: number | null | undefined, digits = 0) {
    if (v == null || !isFinite(v)) return '–'
    return v.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits })
  }
  const secs = (ms: number | null) => (ms == null ? '–' : fixed(ms / 1000, 2))
  const elapsed = (r: Summary) => Math.max(0, (r.finished_at ?? now) - r.started_at)

  // Compare model-dependent metrics within each model. Exclude mostly cached
  // prompts from prompt speeds: too few processed tokens make them unrepresentative.
  const heatCols: Record<string, HeatColumn<Summary>> = {
    in: { value: (r) => r.prompt_tokens, high: true, full: 4, perModel: false },
    out: { value: (r) => r.completion_tokens, high: true, full: 4, perModel: false },
    in_tps: {
      value: (r) => ((r.cached_tokens ?? 0) * 2 > (r.prompt_tokens ?? 0) ? null : r.prompt_per_second),
      high: false,
      full: 2,
      perModel: true,
    },
    out_tps: { value: (r) => r.predicted_per_second, high: false, full: 1.5, perModel: true },
    ttft: { value: (r) => ttft(r), high: true, full: 4, perModel: true },
    duration: { value: (r) => elapsed(r), high: true, full: 4, perModel: false },
    energy: { value: (r) => r.energy_j, high: true, full: 4, perModel: false },
    watts: { value: (r) => (r.energy_j != null && elapsed(r) > 0 ? r.energy_j / elapsed(r) : null), high: true, full: 1.5, perModel: true },
    jtok: { value: (r) => (r.energy_j != null && r.completion_tokens ? r.energy_j / r.completion_tokens : null), high: true, full: 2, perModel: true },
  }
  const heat = $derived(heatMap(shown, heatCols))
  const heatFg = (k: string, r: Summary) => heatColor(heat(k, r), heatStrength)

  // `group` puts a shared header over neighbouring columns. Only the token
  // and speed columns get one, from `groupings` below.
  type Column = { key: string; label: string; group?: string; num?: boolean; title?: string }
  const columns = [
    { key: 'time', label: 'Time' },
    { key: 'id', label: 'ID' },
    { key: 'model', label: 'Model' },
    { key: 'status', label: 'Status' },
    { key: 'build', label: 'Build', title: 'The server build that answered, such as the llama.cpp build number' },
    { key: 'prompt', label: 'Prompt' },
    { key: 'notable', label: 'Notable', title: 'Problems first, then tool calls, thinking, images, and other request details. Hover or tap a chip for more.' },
    { key: 'cache_pct', label: 'Cache %', num: true, title: 'Share of the prompt tokens reused from the cache' },
    { key: 'cache', label: 'Cache', num: true, title: 'Prompt tokens reused from the cache' },
    { key: 'in', label: 'In', num: true, title: 'Prompt tokens, cached ones included' },
    { key: 'out', label: 'Out', num: true, title: 'Generated tokens' },
    { key: 'in_tps', label: 'In t/s', num: true, title: 'Prompt processing speed, tokens per second' },
    { key: 'out_tps', label: 'Out t/s', num: true, title: 'Generation speed, tokens per second' },
    { key: 'ttft', label: 'TTFT s', num: true, title: 'Time to first token, in seconds' },
    { key: 'duration', label: 'Duration s', num: true, title: 'Whole request, in seconds' },
    { key: 'energy', label: 'Energy Wh', num: true, title: 'GPU energy used, in watt-hours' },
    { key: 'watts', label: 'Avg W', num: true, title: 'Average GPU power during this request: energy divided by duration' },
    { key: 'jtok', label: 'J/tok', num: true, title: 'GPU energy per generated token, in joules' },
  ] as const satisfies readonly Column[]
  type Col = (typeof columns)[number]['key']
  const defaultCols: Col[] = ['time', 'model', 'status', 'prompt', 'notable', 'cache_pct', 'in', 'out', 'in_tps', 'out_tps', 'ttft', 'duration', 'energy']

  // The column choice is kept per browser, since a phone may want fewer.
  // COLS_VERSION goes up when new columns should appear in saved choices.
  const COLS_VERSION = 3
  function savedCols(): Col[] {
    try {
      let v = JSON.parse(localStorage.getItem('requests_columns') ?? 'null')
      if (Array.isArray(v)) {
        const version = Number(localStorage.getItem('requests_columns_version') ?? 1)
        if (version < 2) {
          // Version 2: Cache % takes the place of Cache, and Notable is added.
          v = v.map((k: string) => (k === 'cache' ? 'cache_pct' : k))
          v.push('notable')
        }
        // Version 3: Status is added, because failed and cancelled requests
        // have no chip and looked like any other row without it.
        if (version < 3 && !v.includes('status')) v.push('status')
        if (version < COLS_VERSION) {
          localStorage.setItem('requests_columns', JSON.stringify(v))
          localStorage.setItem('requests_columns_version', String(COLS_VERSION))
        }
        return columns.map((c) => c.key).filter((k) => v.includes(k))
      }
    } catch {
      // No storage, or a bad value: use the defaults.
    }
    return defaultCols
  }
  let chosen = $state<Col[]>(savedCols())
  // The token and speed columns can be grouped by phase (the prompt's
  // tokens and speed, then the output's) or by kind (both token counts,
  // then both speeds). Each entry gives the order, and each column's group
  // and its short label under that group.
  type Grouping = 'phase' | 'kind'
  const groupings: Record<Grouping, [Col, string, string][]> = {
    phase: [
      ['in', 'Prompt', 'Tokens'],
      ['in_tps', 'Prompt', 't/s'],
      ['out', 'Output', 'Tokens'],
      ['out_tps', 'Output', 't/s'],
    ],
    kind: [
      ['in', 'Tokens', 'In'],
      ['out', 'Tokens', 'Out'],
      ['in_tps', 'Speed t/s', 'In'],
      ['out_tps', 'Speed t/s', 'Out'],
    ],
  }
  let grouping = $state<Grouping>(savedGrouping())
  function savedGrouping(): Grouping {
    try {
      return localStorage.getItem('requests_grouping') === 'kind' ? 'kind' : 'phase'
    } catch {
      return 'phase'
    }
  }
  function setGrouping(g: Grouping) {
    grouping = g
    try {
      localStorage.setItem('requests_grouping', g)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  const orderedCols = $derived.by((): Column[] => {
    const block = groupings[grouping].map(([key, group, label]) => ({ ...columns.find((c) => c.key === key)!, group, label }))
    const rest = columns.filter((c) => !block.some((b) => b.key === c.key))
    const at = columns.findIndex((c) => c.key === 'in')
    return [...rest.slice(0, at), ...block, ...rest.slice(at)]
  })
  const visibleCols = $derived(orderedCols.filter((c) => chosen.includes(c.key as Col)))
  // The top header row: one cell per run of neighbouring columns with the
  // same group, and an empty cell per column without one.
  const groupRow = $derived.by(() => {
    const cells: { label: string; span: number }[] = []
    for (const c of visibleCols) {
      const last = cells.at(-1)
      if (c.group && last?.label === c.group) last.span++
      else cells.push({ label: c.group ?? '', span: 1 })
    }
    return cells
  })
  // A thin line where each group starts and ends sets the groups apart.
  const edges = $derived(
    new Set(
      visibleCols
        .filter((c, i) => i > 0 && c.group !== visibleCols[i - 1].group && (c.group || visibleCols[i - 1].group))
        .map((c) => c.key),
    ),
  )
  function setCols(next: Col[]) {
    chosen = next
    try {
      localStorage.setItem('requests_columns', JSON.stringify(next))
      localStorage.setItem('requests_columns_version', String(COLS_VERSION))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  function toggleCol(k: Col) {
    setCols(chosen.includes(k) ? chosen.filter((c) => c !== k) : [...chosen, k])
  }

  // The same size as the cache_miss tag's limit in internal/capture/notable.go.
  const cacheMissTokens = 8000

  // Saved per browser, like the column choices.
  let colorful = $state(savedColorful())
  function savedColorful() {
    try {
      return localStorage.getItem('requests_colorful') === 'true'
    } catch {
      return false
    }
  }
  function toggleColorful() {
    colorful = !colorful
    try {
      localStorage.setItem('requests_colorful', String(colorful))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  // Dimming percentage for typical values; 0 disables it. Saved per browser.
  let heatStrength = $state(savedHeat())
  function savedHeat() {
    try {
      const v = Number(localStorage.getItem('requests_heat') ?? 30)
      return isFinite(v) ? Math.min(60, Math.max(0, v)) : 30
    } catch {
      return 30
    }
  }
  function setHeat(v: number) {
    heatStrength = v
    try {
      localStorage.setItem('requests_heat', String(v))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  // Shows "t" after token counts and "t/s" after speeds. Saved per browser.
  let units = $state(savedUnits())
  function savedUnits() {
    try {
      return localStorage.getItem('requests_units') === 'true'
    } catch {
      return false
    }
  }
  function toggleUnits() {
    units = !units
    try {
      localStorage.setItem('requests_units', String(units))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  let picker = $state<HTMLDetailsElement>()
  let overflow = $state<HTMLDetailsElement>()
  function closePicker(e: MouseEvent) {
    for (const d of [picker, overflow]) if (d?.open && !d.contains(e.target as Node)) d.open = false
  }
</script>

<svelte:window onclick={closePicker} />

<!-- Live estimates are dimmed until stored values arrive. -->
{#snippet maybeLive(stored: number | null, estimate: number | null | undefined, digits: number, title: string, unit?: string, fg?: string)}
  {#if stored == null && estimate != null}
    <td class="num text-right text-muted" {title}>{@render withUnit(estimate, digits, unit)}</td>
  {:else}
    <td class="num text-right" style:color={fg}>{@render withUnit(stored, digits, unit)}</td>
  {/if}
{/snippet}

<!-- The unit is dimmed, and left off when there is no number. -->
{#snippet withUnit(v: number | null | undefined, digits: number, unit?: string)}
  {fixed(v, digits)}{#if units && unit && v != null && isFinite(v)}<span class="ml-0.5 text-dim">{unit}</span>{/if}
{/snippet}

<!-- A bar under the number shows the cache share. With Colorful on, a big
     prompt that mostly missed the cache turns amber: fully under 10%, softer
     under 50%. -->
{#snippet cachePct(r: Summary)}
  {#if r.cached_tokens != null && r.prompt_tokens}
    {@const pct = (r.cached_tokens / r.prompt_tokens) * 100}
    {@const big = r.prompt_tokens >= cacheMissTokens}
    {@const miss = colorful && big && pct < 10}
    {@const low = colorful && big && !miss && pct < 50}
    {@const bar = !colorful ? 'var(--color-dim)' : miss ? 'var(--color-warn)' : low ? 'color-mix(in srgb, var(--color-warn) 60%, var(--color-dim))' : 'var(--color-primary)'}
    <td class="num relative text-right" style:color={miss ? 'var(--color-warn)' : low ? 'color-mix(in srgb, var(--color-warn) 55%, var(--color-text))' : undefined} title="{fixed(r.cached_tokens)} of {fixed(r.prompt_tokens)} prompt tokens from the cache">
      {fixed(pct)}
      <span class="absolute inset-x-1.5 bottom-0.5 h-0.5 rounded-sm bg-line-soft"><span class="block h-full rounded-sm" style:width="{pct}%" style:background={bar}></span></span>
    </td>
  {:else}
    <td class="num text-right">–</td>
  {/if}
{/snippet}

{#snippet cell(k: string, r: Summary, flight: ReturnType<typeof live.flights.get>)}
  {@const progress = flight?.progress}
  {#if k === 'time'}<td class="num">{when(r.started_at)}</td>
  {:else if k === 'id'}<td class="font-mono text-dim">{r.id}</td>
  {:else if k === 'model'}<td><span class="mr-1.5 inline-block size-2 rounded-full {r.state === 'in_flight' ? 'pulse' : ''}" title={r.state === 'in_flight' ? (flight && !flight.firstTokenAt ? 'Waiting for the first token' : 'Streaming') : undefined} style="background:{modelColor(r.model)}"></span>{r.model || '–'}</td>
  {:else if k === 'status'}<td><StatusPill row={r} waiting={!!flight && !flight.firstTokenAt} /></td>
  {:else if k === 'build'}<td class="font-mono text-muted" title={r.build ?? undefined}>{r.build ? shortBuild(r.build) : '–'}</td>
  {:else if k === 'prompt'}<td class="max-w-[140px] overflow-hidden text-ellipsis text-muted" title={r.preview}>{r.preview}</td>
  {:else if k === 'notable'}<td><NotableChips chips={rowChips(r, flight)} {colorful} /></td>
  {:else if k === 'cache_pct'}{@render cachePct(r)}
  {:else if k === 'cache'}{@render maybeLive(r.cached_tokens, progress?.cache, 0, 'Reused from the cache so far')}
  {:else if k === 'in'}{@render maybeLive(r.prompt_tokens, progress?.processed, 0, `${fixed(progress?.processed)} processed so far. The total is known when the request ends.`, 't', heatFg(k, r))}
  {:else if k === 'out'}<td class="num text-right" style:color={heatFg(k, r)}>{@render withUnit(flight ? flight.chunks : r.completion_tokens, 0, 't')}</td>
  {:else if k === 'in_tps'}{@render maybeLive(r.prompt_per_second, progress?.per_second, 0, 'Prompt speed so far', 't/s', heatFg(k, r))}
  {:else if k === 'out_tps'}{@render maybeLive(r.predicted_per_second, liveOutSpeed(flight), 1, 'Generation speed so far', 't/s', heatFg(k, r))}
  {:else if k === 'ttft'}<td class="num text-right" style:color={heatFg(k, r)}>{secs(ttft(r))}</td>
  {:else if k === 'duration'}<td class="num text-right" style:color={heatFg(k, r)}>{secs(elapsed(r))}</td>
  {:else if k === 'energy'}<td class="num text-right" style:color={heatFg(k, r)}>{r.energy_j != null ? fixed(r.energy_j / 3600, 2) : '–'}</td>
  {:else if k === 'watts'}<td class="num text-right" style:color={heatFg(k, r)}>{r.energy_j != null && elapsed(r) > 0 ? fixed(r.energy_j / (elapsed(r) / 1000)) : '–'}</td>
  {:else if k === 'jtok'}<td class="num text-right" style:color={heatFg(k, r)}>{r.energy_j != null && r.completion_tokens ? fixed(r.energy_j / r.completion_tokens, 2) : '–'}</td>
  {/if}
{/snippet}

<div class="mb-3 flex flex-wrap items-center gap-3">
  <h1 class="text-[17px] font-semibold">Requests</h1>
  <input
    class="w-52 rounded-md border border-line bg-panel2 px-2 py-1 outline-none focus:border-primary"
    placeholder="Search prompts and models"
    bind:value={q}
  />
  <select class="rounded-md border border-line bg-panel2 px-2 py-1" bind:value={model}>
    <option value="">All models</option>
    {#each visibleModels as m}<option value={m}>{m}</option>{/each}
  </select>
  <select class="rounded-md border border-line bg-panel2 px-2 py-1" bind:value={issue} title="Problems found in the output">
    <option value="">All requests</option>
    <option value="any">Any issue</option>
    {#each issueKinds as [k, label]}<option value={k}>{label}</option>{/each}
  </select>
  <div class="inline-flex overflow-hidden rounded-md border border-line">
    {#each states as [v, label]}
      <button class="px-2.5 py-1 {stateFilter === v ? 'bg-panel2 text-text' : 'text-muted hover:text-text'}" onclick={() => (stateFilter = v)}>{label}</button>
    {/each}
  </div>
  <span class="flex-1"></span>
  {#if !live.connected}<span class="text-xs text-warn">Live updates reconnecting…</span>{/if}
  <details bind:this={picker} class="relative">
    <summary class="cursor-pointer list-none rounded-md border border-line bg-panel2 px-2.5 py-1 text-muted hover:text-text">Columns</summary>
    <div class="absolute right-0 z-20 mt-1 w-44 rounded-md border border-line bg-panel2 p-1.5 shadow-lg">
      {#each columns as c}
        <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover" title={(c as Column).title}>
          <input type="checkbox" class="accent-primary" checked={chosen.includes(c.key)} onchange={() => toggleCol(c.key)} />
          {c.label}
        </label>
      {/each}
      <button class="mt-1 w-full rounded px-1.5 py-0.5 text-left text-xs text-primary hover:bg-hover" onclick={() => setCols(defaultCols)}>Reset to default</button>
    </div>
  </details>
  <details bind:this={overflow} class="relative">
    <summary class="cursor-pointer list-none rounded-md border border-line bg-panel2 px-2 py-1 text-muted hover:text-text" title="More options" aria-label="More options">
      <svg viewBox="0 0 16 16" class="size-[15px]" fill="currentColor" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
    </summary>
    <div class="absolute right-0 z-20 mt-1 w-44 rounded-md border border-line bg-panel2 p-1.5 shadow-lg">
      <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover" title="Give each kind of Notable chip its own colour, and mark big prompts that mostly missed the cache in amber">
        <input type="checkbox" class="accent-primary" checked={colorful} onchange={toggleColorful} />
        Colorful
      </label>
      <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover" title="Show t after token counts and t/s after speeds">
        <input type="checkbox" class="accent-primary" checked={units} onchange={toggleUnits} />
        Show units
      </label>
      <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5 pb-0.5 text-xs text-dim">Highlight worse numbers</div>
      <label class="flex items-center gap-2 px-1.5 py-0.5" title="Dims the numbers right of Cache, except those worse than usual for that column. Further right is a stronger effect.">
        <input type="range" min="0" max="60" step="5" class="min-w-0 flex-1 accent-primary" value={heatStrength} oninput={(e) => setHeat(Number(e.currentTarget.value))} />
        <span class="num w-7 text-right text-xs text-muted">{heatStrength ? `${heatStrength}%` : 'Off'}</span>
      </label>
      <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5 pb-0.5 text-xs text-dim">Group token columns</div>
      {#each [['phase', 'Prompt / Output'], ['kind', 'Tokens / Speed']] as const as [g, label]}
        <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover">
          <input type="radio" name="requests-grouping" class="accent-primary" checked={grouping === g} onchange={() => setGrouping(g)} />
          {label}
        </label>
      {/each}
    </div>
  </details>
</div>

{#if error}<div class="mb-2 text-err">{error}</div>{/if}

<div class="overflow-x-auto rounded-lg border border-line bg-panel">
  <table class="w-full border-collapse">
    <colgroup>
      {#each visibleCols as c}<col class={edges.has(c.key) ? 'border-l border-line-soft' : ''} />{/each}
    </colgroup>
    <thead>
      {#if groupRow.some((g) => g.label)}
        <tr class="text-[11px] text-dim [&>th]:px-2 [&>th]:pt-1.5 [&>th]:font-medium [&>th]:whitespace-nowrap">
          {#each groupRow as g}
            <th colspan={g.span} class="text-center">
              {#if g.label}<span class="block border-b border-line pb-0.5">{g.label}</span>{/if}
            </th>
          {/each}
        </tr>
      {/if}
      <tr class="text-left text-[11px] text-dim [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:py-1.5 [&>th]:font-medium [&>th]:whitespace-nowrap">
        {#each visibleCols as c}
          <th class={c.num ? 'text-right' : ''} title={c.title}>{c.label}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each shown as r (r.id)}
        {@const flight = r.state === 'in_flight' ? live.flights.get(r.id) : undefined}
        <tr
          class="cursor-pointer [&>td]:border-b [&>td]:border-line-soft [&>td]:px-2 [&>td]:py-1.5 [&>td]:whitespace-nowrap {open === r.id ? '[&>td]:bg-primary/10' : 'hover:[&>td]:bg-hover'}"
          onclick={() => (open = open === r.id ? null : r.id)}
        >
          {#each visibleCols as c}{@render cell(c.key, r, flight)}{/each}
        </tr>
        {#if open === r.id}
          <tr><td colspan={visibleCols.length} class="border-b border-line-soft bg-[#252930] px-3.5 py-3"><RequestDetail row={r} onopen={(id) => (open = id)} /></td></tr>
        {/if}
      {:else}
        <tr><td colspan={visibleCols.length} class="px-3 py-6 text-center text-muted">{loading ? 'Loading…' : 'No matching requests.'}</td></tr>
      {/each}
    </tbody>
  </table>
</div>

<ChipPopover />

{#if more && rows.length}
  <div class="mt-3 text-center">
    <button class="rounded-md border border-line bg-panel2 px-3 py-1 hover:border-primary" disabled={loading} onclick={() => load(false)}>
      {loading ? 'Loading…' : 'Load older'}
    </button>
  </div>
{/if}
