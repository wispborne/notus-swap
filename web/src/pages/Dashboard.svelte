<script lang="ts">
  import { GridStack, type GridStackNode } from 'gridstack'
  import 'gridstack/dist/gridstack.min.css'
  import { onMount, tick, untrack } from 'svelte'
  import { duration, num, pct, when } from '../lib/format'
  import { C } from '../lib/dashboard/charts'
  import { cacheRate, fetchDashboard, fetchLayout, median, RANGES, savedRange, ttftOf, type DashboardData, type Range } from '../lib/dashboard/data'
  import Gauge from '../lib/dashboard/Gauge.svelte'
  import Lists from '../lib/dashboard/Lists.svelte'
  import ModelScatter from '../lib/dashboard/ModelScatter.svelte'
  import ModelsTable from '../lib/dashboard/ModelsTable.svelte'
  import PowerChart from '../lib/dashboard/PowerChart.svelte'
  import RequestsPerBucket from '../lib/dashboard/RequestsPerBucket.svelte'
  import Timeline from '../lib/dashboard/Timeline.svelte'
  import { zoom } from '../lib/dashboard/zoom.svelte'
  import { noteModels } from '../lib/modelColors.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { isSocketSensor, status } from '../lib/status.svelte'

  // ---- Widgets and the default layout (12 columns) ----
  type Item = { id: string; x: number; y: number; w: number; h: number }
  // How a gauge shows more than one GPU: only the highest card, the arc split into one piece per
  // card, one nested ring per card, or one arc for the total of all cards.
  type GpuMode = 'highest' | 'split' | 'stacked' | 'combined'
  const GPU_MODES: Record<GpuMode, string> = { highest: 'Show highest only', split: 'Split', stacked: 'Stacked', combined: 'Combined' }
  // How a per-model chart shows its legend: each model's shortened name, only its coloured dot (the name
  // is the dot's tooltip), or nothing.
  type LegendMode = 'names' | 'dots' | 'hidden'
  const LEGEND_MODES: Record<LegendMode, string> = { names: 'Names', dots: 'Dots only', hidden: 'Hidden' }
  type Widget = { title: string; minW: number; minH: number; gpu?: { modes: GpuMode[]; def: GpuMode }; legend?: boolean }
  const WIDGETS: Record<string, Widget> = {
    g_power: { title: 'GPU power', minW: 2, minH: 3, gpu: { modes: ['highest', 'split', 'stacked', 'combined'], def: 'combined' } },
    g_vram: { title: 'VRAM', minW: 2, minH: 3, gpu: { modes: ['highest', 'split', 'stacked', 'combined'], def: 'split' } },
    g_ram: { title: 'RAM', minW: 2, minH: 3 },
    // Adding temperatures together means nothing, so there is no Combined.
    g_temp: { title: 'GPU temperature', minW: 2, minH: 3, gpu: { modes: ['highest', 'split', 'stacked'], def: 'stacked' } },
    t_energy: { title: 'Energy today', minW: 2, minH: 2 },
    t_speed: { title: 'Generation speed', minW: 2, minH: 2 },
    t_ttft: { title: 'Time to first token', minW: 2, minH: 2 },
    t_cache: { title: 'Prompt cache', minW: 2, minH: 2 },
    timeline: { title: 'Timeline', minW: 6, minH: 7 },
    inflight: { title: 'Active requests', minW: 3, minH: 2 },
    swaps: { title: 'Model loads', minW: 3, minH: 2 },
    energy: { title: 'Energy per token by model', minW: 3, minH: 2 },
    models: { title: 'Models', minW: 6, minH: 3 },
    gen: { title: 'Generation speed (tok/s)', minW: 3, minH: 3, legend: true },
    pp: { title: 'Prompt processing (tok/s)', minW: 3, minH: 3, legend: true },
    ttft: { title: 'Time to first token (s)', minW: 3, minH: 3, legend: true },
    cache: { title: 'Prompt cache hit (%)', minW: 3, minH: 3, legend: true },
    jpt: { title: 'Energy per token (J/tok)', minW: 3, minH: 3, legend: true },
    rpm: { title: 'Requests', minW: 4, minH: 3 },
    power: { title: 'Power', minW: 4, minH: 3 },
  }
  const DEFAULT: Item[] = [
    { id: 'g_power', x: 0, y: 0, w: 2, h: 3 },
    { id: 'g_vram', x: 2, y: 0, w: 2, h: 3 },
    { id: 'g_ram', x: 4, y: 0, w: 2, h: 3 },
    { id: 'g_temp', x: 6, y: 0, w: 2, h: 3 },
    { id: 't_energy', x: 8, y: 0, w: 2, h: 3 },
    { id: 't_speed', x: 10, y: 0, w: 2, h: 3 },
    { id: 'timeline', x: 0, y: 3, w: 8, h: 10 },
    { id: 't_ttft', x: 8, y: 3, w: 2, h: 2 },
    { id: 't_cache', x: 10, y: 3, w: 2, h: 2 },
    { id: 'inflight', x: 8, y: 5, w: 4, h: 3 },
    { id: 'swaps', x: 8, y: 8, w: 4, h: 3 },
    { id: 'energy', x: 8, y: 11, w: 4, h: 2 },
    { id: 'models', x: 0, y: 13, w: 12, h: 5 },
    { id: 'gen', x: 0, y: 18, w: 4, h: 5 },
    { id: 'pp', x: 4, y: 18, w: 4, h: 5 },
    { id: 'ttft', x: 8, y: 18, w: 4, h: 5 },
    { id: 'jpt', x: 0, y: 23, w: 4, h: 5 },
    { id: 'rpm', x: 4, y: 23, w: 8, h: 5 },
  ]

  let items = $state<Item[]>([])
  // Positions as gridstack reports them. Kept outside Svelte's state so that
  // dragging does not re-render the widgets.
  const pos = new Map<string, Item>()
  let gridEl: HTMLDivElement
  let grid: GridStack | undefined
  let editing = $state(false)
  let adding = $state(false)
  // Each widget's own settings, keyed by widget ID and saved with the layout.
  type WidgetOpts = { gpu?: GpuMode; legend?: LegendMode }
  let opts = $state<Record<string, WidgetOpts>>({})
  let settingsFor = $state<string | null>(null)
  const gpuMode = (id: string): GpuMode => {
    const g = WIDGETS[id].gpu!
    const m = opts[id]?.gpu
    return m && g.modes.includes(m) ? m : g.def
  }
  function setGpuMode(id: string, m: GpuMode) {
    opts = { ...opts, [id]: { ...opts[id], gpu: m } }
    settingsFor = null
    save()
  }
  const legendMode = (id: string): LegendMode => opts[id]?.legend ?? 'dots'
  function setLegendMode(id: string, m: LegendMode) {
    opts = { ...opts, [id]: { ...opts[id], legend: m } }
    settingsFor = null
    save()
  }

  // ---- Time range and data ----
  let range = $state<Range>(savedRange())
  let raw = $state<DashboardData | null>(null)
  // Hidden models are removed here, once, before any widget sees the data.
  const data = $derived(raw && privacy.dashboard(raw))
  let error = $state('')
  // The charts zoom inside the loaded range. Choosing another range ends the zoom.
  $effect.pre(() => {
    if (raw) zoom.full = [raw.from, raw.to]
  })
  $effect.pre(() => {
    void range
    untrack(() => zoom.reset())
  })
  // Models no longer in the config still need colours of their own.
  $effect.pre(() => {
    if (raw) noteModels([...raw.requests.map((r) => r.model), ...raw.model_events.map((e) => e.model)])
  })

  async function refresh() {
    try {
      raw = await fetchDashboard(range)
      error = ''
    } catch (e) {
      error = String(e)
    }
  }
  $effect(() => {
    const r = range
    try {
      localStorage.setItem('dashboard_range', r)
    } catch {}
    refresh()
    const every = r === '15m' || r === '1h' ? 5_000 : r === '6h' || r === 'all' ? 15_000 : 60_000
    const t = setInterval(refresh, every)
    return () => clearInterval(t)
  })

  // ---- Layout: load, grid, save ----
  function initGrid() {
    const g = GridStack.init(
      {
        column: 12,
        cellHeight: 42,
        margin: 5,
        mode: 'top',
        staticGrid: !editing,
        handle: '.drag-handle',
        columnOpts: { breakpoints: [{ w: 768, c: 1 }], layout: 'list' },
      },
      gridEl,
    )
    if (!g) return
    grid = g
    for (const it of items) pos.set(it.id, { ...it })
    g.on('change', (_e: Event, nodes: GridStackNode[]) => {
      if (g.getColumn() !== 12) return // the phone's one-column view is not saved
      for (const n of nodes) if (n.id) pos.set(String(n.id), { id: String(n.id), x: n.x ?? 0, y: n.y ?? 0, w: n.w ?? 1, h: n.h ?? 1 })
      save()
    })
  }

  let saveTimer: ReturnType<typeof setTimeout>
  function save() {
    clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      const layout = { v: 5, items: items.map((it) => pos.get(it.id) ?? it), opts }
      fetch('/notus/api/settings/dashboard_layout', { method: 'PUT', body: JSON.stringify(layout) })
    }, 600)
  }

  onMount(() => {
    status.start()
    let cancelled = false
    ;(async () => {
      const saved = await fetchLayout<Item, Record<string, WidgetOpts>>()
      if (cancelled) return
      items = (saved?.items ?? DEFAULT).filter((it) => WIDGETS[it.id])
      opts = saved?.opts ?? {}
      if (saved?.items && (saved.v ?? 1) < 2 && !items.some((it) => it.id === 'g_ram')) {
        const bottom = Math.max(0, ...items.map((it) => it.y + it.h))
        items = [...items, { id: 'g_ram', x: 0, y: bottom, w: 2, h: 3 }]
        save()
      }
      // Layouts saved before the prompt cache tile existed get it at the bottom.
      if (saved?.items && (saved.v ?? 1) < 3 && !items.some((it) => it.id === 't_cache')) {
        const bottom = Math.max(0, ...items.map((it) => it.y + it.h))
        items = [...items, { id: 't_cache', x: 0, y: bottom, w: 2, h: 2 }]
        save()
      }
      // Layouts saved before the energy per token chart existed get it at the bottom.
      if (saved?.items && (saved.v ?? 1) < 4 && !items.some((it) => it.id === 'jpt')) {
        const bottom = Math.max(0, ...items.map((it) => it.y + it.h))
        items = [...items, { id: 'jpt', x: 0, y: bottom, w: 4, h: 5 }]
        save()
      }
      // Layouts from before v5 got the energy per token chart added alone on
      // the bottom row, at a third of the width. Widen it to fill that row.
      if (saved?.items && (saved.v ?? 1) < 5) {
        const jpt = items.find((it) => it.id === 'jpt')
        const alone = jpt && !items.some((it) => it !== jpt && it.y < jpt.y + jpt.h && jpt.y < it.y + it.h)
        if (jpt && alone && jpt.w < 12) {
          items = items.map((it) => (it === jpt ? { ...it, x: 0, w: 12 } : it))
          save()
        }
      }
      await tick()
      initGrid()
    })()
    return () => {
      cancelled = true
      grid?.destroy(false)
    }
  })

  function toggleEdit() {
    editing = !editing
    adding = false
    settingsFor = null
    grid?.setStatic(!editing)
  }

  function remove(id: string) {
    const el = gridEl.querySelector(`[gs-id="${id}"]`) as HTMLElement | null
    if (el) grid?.removeWidget(el, false)
    items = items.filter((it) => it.id !== id)
    pos.delete(id)
    const { [id]: _, ...rest } = opts
    opts = rest
    save()
  }

  async function add(id: string) {
    const d = DEFAULT.find((it) => it.id === id) ?? { id, x: 0, y: 0, w: 6, h: 5 }
    items = [...items, { ...d, y: 1000 }]
    await tick()
    const el = gridEl.querySelector(`[gs-id="${id}"]`) as HTMLElement
    grid?.makeWidget(el)
    adding = false
    save()
  }

  async function reset() {
    grid?.destroy(false)
    pos.clear()
    items = []
    opts = {}
    settingsFor = null
    await tick()
    items = DEFAULT.map((it) => ({ ...it }))
    await tick()
    initGrid()
    save()
  }

  const missing = $derived(Object.keys(WIDGETS).filter((id) => !items.some((it) => it.id === id)))

  // ---- Numbers for the gauges and tiles ----
  const st = $derived(status.current)
  const cards = $derived((st?.gpus ?? []).filter((g) => !g.integrated))
  // The power gauge counts every card, built-in graphics included, like the status bar.
  const allCards = $derived((st?.gpus ?? []).filter((g) => !isSocketSensor(g)))
  const powerMax = $derived(allCards.reduce((s, g) => s + (g.power_cap_watts ?? (g.integrated ? 0 : 300)), 0) || 300)
  const powerDetail = $derived(allCards.length > 1 ? allCards.map((g) => `${g.card}${g.integrated ? ' (built-in)' : ''}: ${Math.round(g.watts ?? 0)} W`).join('\n') : '')
  const vramUsed = $derived(cards.reduce((s, g) => s + (g.vram_used ?? 0), 0) / 1024 ** 3)
  const vramTotal = $derived(cards.reduce((s, g) => s + (g.vram_total ?? 0), 0) / 1024 ** 3)
  const ramUsed = $derived(st?.ram_used == null ? null : st.ram_used / 1024 ** 3)
  const ramTotal = $derived(st?.ram_total == null ? 0 : st.ram_total / 1024 ** 3)
  const temp = $derived(cards.length ? Math.max(...cards.map((g) => g.temp_c ?? 0)) : null)
  const perCard = (f: (g: (typeof cards)[number]) => string) => (cards.length > 1 ? cards.map((g) => `${g.card}: ${f(g)}`).join('\n') : '')


  // Gauge settings for a widget that can show several GPUs, following its GPU setting. `whole` is
  // the combined reading, also the number in the middle for Split and Stacked. `rank` picks the
  // card for "Show highest only". The per-card modes show dedicated cards only. In Stacked, the
  // first card takes the widget's colour and the others take `LAYER_COLORS`. These are far from
  // every widget's colour, because layers of close colours could not be told apart.
  type Part = { card: string; value: number; max: number }
  const LAYER_COLORS = ['#ff7a9a', '#18ffff', '#7aa7ff']
  function byMode(id: string, color: string, parts: Part[], whole: { value: number | null; max: number }, rank: (p: Part) => number) {
    const mode = gpuMode(id)
    const none = { card: '', segments: [] as Part[], layers: [] as (Part & { label: string; color: string })[] }
    if (parts.length < 2) return { ...whole, ...none }
    if (mode === 'highest') {
      const p = parts.reduce((a, b) => (rank(b) > rank(a) ? b : a))
      return { ...none, value: p.value, max: p.max, card: p.card }
    }
    if (mode === 'split') return { ...whole, ...none, segments: parts }
    if (mode === 'stacked') return { ...whole, ...none, layers: parts.map((p, i) => ({ ...p, label: p.card, color: i === 0 ? color : LAYER_COLORS[(i - 1) % LAYER_COLORS.length] })) }
    return { ...whole, ...none }
  }
  const GB = 1024 ** 3
  const powerParts = $derived(cards.map((g) => ({ card: g.card, value: g.watts ?? 0, max: g.power_cap_watts ?? 300 })))
  const power = $derived(
    byMode(
      'g_power',
      C.power,
      powerParts,
      gpuMode('g_power') === 'combined' || powerParts.length < 2
        ? { value: st?.total_watts ?? null, max: powerMax }
        : { value: powerParts.reduce((s, p) => s + p.value, 0), max: powerParts.reduce((s, p) => s + p.max, 0) },
      (p) => p.value,
    ),
  )
  const vram = $derived(
    byMode(
      'g_vram',
      C.vram,
      cards.map((g) => ({ card: g.card, value: (g.vram_used ?? 0) / GB, max: (g.vram_total ?? 0) / GB })),
      { value: cards.length ? vramUsed : null, max: vramTotal || 32 },
      (p) => (p.max > 0 ? p.value / p.max : 0),
    ),
  )
  const gpuTemp = $derived(
    byMode(
      'g_temp',
      C.primary,
      cards.map((g) => ({ card: g.card, value: g.temp_c ?? 0, max: 110 })),
      { value: temp, max: 110 },
      (p) => p.value,
    ),
  )
  const withCard = (label: string, card: string) => (card ? `${label} · ${card}` : label)

  const done = $derived((data?.requests ?? []).filter((r) => r.state === 'done'))
  const genSpeed = $derived.by(() => {
    let tok = 0, sec = 0
    for (const r of done)
      if (r.predicted_per_second && r.completion_tokens) (tok += r.completion_tokens), (sec += r.completion_tokens / r.predicted_per_second)
    return sec ? tok / sec : null
  })
  const ttftP50 = $derived(median(done.map(ttftOf).filter((v): v is number => v != null)))
  const cacheHit = $derived(cacheRate(done))
  const cacheTokens = $derived(done.reduce((s, r) => s + (r.cached_tokens ?? 0), 0))
  const kwh = (wh: number | null | undefined) => (wh == null ? '–' : wh >= 1000 ? `${(wh / 1000).toFixed(2)} kWh` : `${Math.round(wh)} Wh`)
</script>

<div class="mb-3 flex flex-wrap items-center gap-3">
  <h1 class="text-[17px] font-semibold">Dashboard</h1>
  <span class="flex-1"></span>
  {#if zoom.span || zoom.yZoomed}
    <button class="rounded-md border border-primary/60 px-2.5 py-1 text-primary hover:bg-hover" title="Double-click a chart to do the same" onclick={() => zoom.reset()}>
      {zoom.span ? `Zoomed ${when(zoom.span[0])} – ${when(zoom.span[1])}` : 'Zoomed'} · Reset
    </button>
  {/if}
  <div class="inline-flex overflow-hidden rounded-md border border-line">
    {#each RANGES as r}
      <button class="px-2.5 py-1 {range === r ? 'bg-panel2 text-text' : 'text-muted hover:text-text'}" onclick={() => (range = r)}>{r === 'all' ? 'All' : r}</button>
    {/each}
  </div>
  <div class="hidden items-center gap-2 md:flex">
    {#if editing}
      <div class="relative">
        <button class="rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50" disabled={!missing.length} onclick={() => (adding = !adding)}>+ Add widget</button>
        {#if adding}
          <div class="absolute right-0 z-30 mt-1 w-56 rounded-md border border-line bg-panel py-1 shadow-lg">
            {#each missing as id}
              <button class="block w-full px-3 py-1 text-left hover:bg-hover" onclick={() => add(id)}>{WIDGETS[id].title}</button>
            {/each}
          </div>
        {/if}
      </div>
      <button class="rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary" onclick={reset}>Reset layout</button>
    {/if}
    <button class="rounded-md border px-2.5 py-1 {editing ? 'border-primary bg-primary font-semibold text-sunken' : 'border-line bg-panel2 hover:border-primary'}" onclick={toggleEdit}>
      {editing ? 'Done' : 'Edit layout'}
    </button>
  </div>
</div>

{#if error}<div class="mb-2 text-err">Could not load the dashboard: {error}</div>{/if}

<div class="grid-stack -mx-[5px]" bind:this={gridEl}>
  {#each items as it (it.id)}
    {@const w = WIDGETS[it.id]}
    <!-- While its settings menu is open, a widget lets the menu hang out below it, above the widgets around it. -->
    <div class="grid-stack-item {settingsFor === it.id ? 'z-30' : ''}" {...{ 'gs-id': it.id, 'gs-x': it.x, 'gs-y': it.y, 'gs-w': it.w, 'gs-h': it.h, 'gs-min-w': w.minW, 'gs-min-h': w.minH }}>
      <div class="grid-stack-item-content flex flex-col {settingsFor === it.id ? 'overflow-visible!' : 'overflow-hidden!'} rounded-lg border bg-panel {editing ? 'border-primary/50' : 'border-line'}">
        <div class="flex h-6 flex-none items-center gap-1.5 px-2.5 pt-1 text-xs text-muted">
          {#if editing}<span class="drag-handle cursor-grab text-dim select-none" title="Drag to move">⠿</span>{/if}
          <span class="truncate font-semibold text-text">{w.title}</span>
          <span class="flex-1"></span>
          {#if editing && (w.gpu || w.legend)}
            <button class="px-1 {settingsFor === it.id ? 'text-primary' : 'text-dim hover:text-text'}" title="Settings" onclick={() => (settingsFor = settingsFor === it.id ? null : it.id)}>⚙&#xFE0E;</button>
          {/if}
          {#if editing}<button class="px-1 text-dim hover:text-err" title="Remove" onclick={() => remove(it.id)}>✕</button>{/if}
        </div>
        {#if editing && w.gpu && settingsFor === it.id}
          <div class="absolute top-6 right-1 z-20 min-w-40 rounded-md border border-line bg-panel py-1 text-xs shadow-lg">
            <div class="px-3 pb-1 text-dim">With more than one GPU</div>
            {#each w.gpu.modes as m}
              <button class="flex w-full items-center gap-2 px-3 py-1 text-left hover:bg-hover" onclick={() => setGpuMode(it.id, m)}>
                <span class="w-3 text-primary">{gpuMode(it.id) === m ? '✓' : ''}</span>{GPU_MODES[m]}{#if m === w.gpu.def}<span class="text-dim">(default)</span>{/if}
              </button>
            {/each}
          </div>
        {/if}
        {#if editing && w.legend && settingsFor === it.id}
          <div class="absolute top-6 right-1 z-20 min-w-40 rounded-md border border-line bg-panel py-1 text-xs shadow-lg">
            <div class="px-3 pb-1 text-dim">Legend</div>
            {#each Object.entries(LEGEND_MODES) as [m, label]}
              <button class="flex w-full items-center gap-2 px-3 py-1 text-left hover:bg-hover" onclick={() => setLegendMode(it.id, m as LegendMode)}>
                <span class="w-3 text-primary">{legendMode(it.id) === m ? '✓' : ''}</span>{label}{#if m === 'dots'}<span class="text-dim">(default)</span>{/if}
              </button>
            {/each}
          </div>
        {/if}
        <div class="min-h-0 flex-1 px-2.5 pt-1 pb-2">
          {#if it.id === 'g_power'}
            <Gauge value={power.value} max={power.max} unit="W" label={withCard('GPU power', power.card)} color={C.power} segments={power.segments} layers={power.layers} detail={powerDetail} />
          {:else if it.id === 'g_vram'}
            <Gauge value={vram.value} max={vram.max} unit="GB" label={withCard('VRAM used', vram.card)} color={C.vram} segments={vram.segments} layers={vram.layers} detail={perCard((g) => `${((g.vram_used ?? 0) / GB).toFixed(1)} GB`)} />
          {:else if it.id === 'g_ram'}
            <Gauge value={ramUsed} max={ramTotal} unit="GB" label="RAM in use" color={C.ram} detail="Available RAM includes reclaimable cache" warnAt={ramTotal * 0.85} dangerAt={ramTotal * 0.95} warnNote="nearly full" />
          {:else if it.id === 'g_temp'}
            <Gauge value={gpuTemp.value} max={110} unit="°C" label={cards.length > 1 ? withCard('hottest GPU', gpuTemp.card) : 'GPU hotspot'} color={C.primary} segments={gpuTemp.segments} layers={gpuTemp.layers} detail={perCard((g) => `${Math.round(g.temp_c ?? 0)} °C`)} warnAt={90} dangerAt={100} />
          {:else if it.id === 't_energy'}
            <div class="text-[26px] leading-tight font-semibold num">{data?.energy.today_system_wh != null ? `≈ ${kwh(data.energy.today_system_wh)}` : kwh(data?.energy.today_gpu_wh)}</div>
            <div class="text-[11px] text-muted">
              {data?.energy.today_system_wh != null ? `whole system (estimate) · GPUs ${kwh(data?.energy.today_gpu_wh)}` : 'GPUs'}
            </div>
            <div class="text-[11px] text-muted">Range total: {kwh(data?.energy.range_system_wh ?? data?.energy.range_gpu_wh)}</div>
          {:else if it.id === 't_speed'}
            <div class="text-[26px] leading-tight font-semibold num">{num(genSpeed, 1)} <span class="text-sm font-normal text-muted">tok/s</span></div>
            <div class="text-[11px] text-muted">Average for this range</div>
          {:else if it.id === 't_ttft'}
            <div class="text-[26px] leading-tight font-semibold num">{duration(ttftP50)}</div>
            <div class="text-[11px] text-muted">Median for this range</div>
          {:else if it.id === 't_cache'}
            <div class="text-[26px] leading-tight font-semibold num">{pct(cacheHit)}</div>
            <div class="text-[11px] text-muted">of prompt tokens reused · {cacheTokens.toLocaleString('en-US')} tokens</div>
          {:else if !data}
            <div class="text-muted">Loading…</div>
          {:else if it.id === 'timeline'}
            <Timeline {data} />
          {:else if it.id === 'inflight'}
            <Lists kind="inflight" {data} />
          {:else if it.id === 'swaps'}
            <Lists kind="swaps" {data} />
          {:else if it.id === 'energy'}
            <Lists kind="energy" {data} />
          {:else if it.id === 'models'}
            <ModelsTable {data} known={(st?.llama_swap.known ?? []).filter((m) => privacy.visible(m.model))} />
          {:else if it.id === 'gen'}
            <ModelScatter {data} legend={legendMode(it.id)} value={(r) => r.predicted_per_second} format={(v) => v.toFixed(1)} unit=" tok/s" />
          {:else if it.id === 'pp'}
            <ModelScatter {data} legend={legendMode(it.id)} value={(r) => r.prompt_per_second} format={(v) => num(v)} unit=" tok/s" log />
          {:else if it.id === 'ttft'}
            <ModelScatter {data} legend={legendMode(it.id)} value={(r) => { const t = ttftOf(r); return t == null ? null : t / 1000 }} format={(v) => v.toFixed(1)} unit=" s" />
          {:else if it.id === 'cache'}
            <ModelScatter {data} legend={legendMode(it.id)} value={(r) => (r.cached_tokens != null && r.prompt_tokens ? (r.cached_tokens / r.prompt_tokens) * 100 : null)} format={(v) => `${Math.round(v)}`} unit="%" max={100} />
          {:else if it.id === 'jpt'}
            <ModelScatter {data} legend={legendMode(it.id)} value={(r) => (r.energy_j != null && r.completion_tokens ? r.energy_j / r.completion_tokens : null)} format={(v) => (v < 10 ? v.toFixed(2) : v.toFixed(1))} unit=" J/tok" log />
          {:else if it.id === 'rpm'}
            <RequestsPerBucket {data} />
          {:else if it.id === 'power'}
            <PowerChart {data} />
          {/if}
        </div>
      </div>
    </div>
  {/each}
</div>
