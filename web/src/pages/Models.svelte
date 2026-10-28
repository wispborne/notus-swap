<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import { duration, fixed, shortBuild, when } from '../lib/format'
  import ModelMark from '../lib/ModelMark.svelte'
  import ModelDetail from '../lib/models/ModelDetail.svelte'
  import TabIcon from '../lib/models/TabIcon.svelte'
  import { detailTabs, type DetailTab } from '../lib/models/tabs'
  import { modelLabel, modelShow, modelTitle } from '../lib/modelNames.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status, type RunningModel } from '../lib/status.svelte'

  // Every model in llama-swap's config, with its state, notus-swap's numbers
  // for it, and a second line under each with load, unload and restart and a
  // button for each tab of its detail. Clicking a row opens its log, loads
  // and recent requests. Models from past requests that are no longer in the
  // config are listed after.

  interface Stat {
    model: string
    requests: number
    failed: number
    last_used: number | null
    prompt_tokens: number
    completion_tokens: number
    cached_tokens: number
    cache_rate: number | null
    energy_j: number | null
    // Speeds on the model's current build and settings: medians over the
    // requests that count (see the Speed tab).
    current: {
      build: string
      cmd_hash: string
      gen_per_second: number | null
      gen_counted: number
      prompt_per_second: number | null
      prompt_counted: number
    } | null
    loads: number
    load_ms: number | null
    last_loaded: number | null
  }

  let stats = $state<Map<string, Stat>>(new Map())
  let error = $state('')
  async function loadStats() {
    try {
      const res = await fetch('/notus/api/models')
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
      const body: { stats: Stat[] } = await res.json()
      stats = new Map(body.stats.map((s) => [s.model, s]))
      error = ''
    } catch (e) {
      error = String(e)
    }
  }
  onMount(() => {
    status.start()
    loadStats()
    const t = setInterval(loadStats, 15_000)
    return () => clearInterval(t)
  })

  let showUnlisted = $state(localGet('models-unlisted') === '1')
  // Numbers look as they do on the Requests page, and follow its Show units
  // and Colorful choices. Show units can be changed here too.
  let units = $state(localGet('requests_units') !== 'false')
  const colorful = localGet('requests_colorful') !== 'false'
  function toggleUnits() {
    units = !units
    try {
      localStorage.setItem('requests_units', String(units))
    } catch {}
  }
  function localGet(k: string) {
    try {
      return localStorage.getItem(k)
    } catch {
      return null
    }
  }
  function toggleUnlisted() {
    showUnlisted = !showUnlisted
    try {
      localStorage.setItem('models-unlisted', showUnlisted ? '1' : '0')
    } catch {}
  }

  const up = $derived(status.current?.llama_swap.up ?? false)
  const known = $derived((status.current?.llama_swap.known ?? []).filter((m) => privacy.visible(m.model)))
  const unlistedCount = $derived(known.filter((m) => m.unlisted).length)
  const listed = $derived(known.filter((m) => showUnlisted || !m.unlisted))
  const loadedCount = $derived(known.filter((m) => m.state !== 'stopped').length)
  // Seen in requests, but not in llama-swap's config now.
  const gone = $derived(
    [...stats.values()].filter((s) => !known.some((k) => k.model === s.model) && privacy.visible(s.model)).sort((a, b) => (b.last_used ?? 0) - (a.last_used ?? 0)),
  )

  // A button stays disabled until its model's state changes, so it can't be
  // pressed twice while llama-swap acts on it. A restart waits for ready, as
  // it passes through stopping and stopped on the way. A change also brings
  // new numbers (a load time, for one).
  let busy = $state<Record<string, string>>({})
  let lastState: Record<string, string> = {}
  $effect(() => {
    let changed = false
    for (const m of known) {
      if (m.model in lastState && lastState[m.model] !== m.state) {
        changed = true
        const b = untrack(() => busy[m.model])
        if (b && (b !== 'restart' || m.state === 'ready')) delete busy[m.model]
      }
      lastState[m.model] = m.state
    }
    if (changed) loadStats()
  })
  let actionError = $state('')
  async function act(model: string, action: 'load' | 'unload' | 'restart') {
    busy[model] = action
    actionError = ''
    try {
      const res = await fetch(`/notus/api/models/${encodeURIComponent(model)}/${action}`, { method: 'POST' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    } catch (e) {
      actionError = `Couldn't ${action} ${modelLabel(model)}: ${e instanceof Error ? e.message : e}`
      delete busy[model]
    }
    // Give up waiting for a state change. A restart includes a load, which can take minutes.
    setTimeout(() => busy[model] === action && delete busy[model], action === 'restart' ? 300_000 : 30_000)
  }
  let unloadingAll = $state(false)
  async function unloadAll() {
    unloadingAll = true
    actionError = ''
    try {
      const res = await fetch('/notus/api/models/unload', { method: 'POST' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    } catch (e) {
      actionError = `Couldn't unload: ${e instanceof Error ? e.message : e}`
    }
    unloadingAll = false
  }

  // Load, unload, restart and unload all take two clicks. The first turns the
  // button into "Click to confirm …", and the second acts. The button goes
  // back after 5 s, or on a click anywhere else. Keys are "<model> <action>",
  // or "unload-all".
  let armed = $state<string | null>(null)
  let disarm: ReturnType<typeof setTimeout> | undefined
  function confirmed(key: string) {
    clearTimeout(disarm)
    if (armed === key) {
      armed = null
      return true
    }
    armed = key
    disarm = setTimeout(() => (armed = null), 5000)
    return false
  }
  const doing = { load: 'Loading…', unload: 'Unloading…', restart: 'Restarting…' }
  const label = { load: 'Load', unload: 'Unload', restart: 'Restart' }
  const armedCls = 'border-warn! bg-warn/10 text-warn'

  let open = $state<string | null>(null)
  let openTab = $state<DetailTab>('log')
  function toggle(m: string) {
    if (open !== m) openTab = 'log'
    open = open === m ? null : m
  }
  function openAt(m: string, t: DetailTab) {
    if (open === m && openTab === t) open = null
    else [open, openTab] = [m, t]
  }

  // The columns between Model and the buttons. Each can be hidden and moved,
  // from the Columns menu or by dragging its header; the choice is saved per
  // browser (models_hidden_columns, models_column_order). Hidden columns are
  // saved rather than shown ones, so a column added later shows by default.
  interface Column {
    key: string
    label: string
    num?: boolean
    title?: string
  }
  const columns = [
    { key: 'aliases', label: 'Aliases', title: "Other names clients can use for the model, from llama-swap's config" },
    { key: 'state', label: 'State', title: 'Whether llama-swap has the model loaded' },
    { key: 'context', label: 'Context', num: true, title: 'Context size, from llama-swap' },
    { key: 'requests', label: 'Requests', num: true, title: 'Requests captured, with failed ones in brackets' },
    { key: 'cache', label: 'Cache %', num: true, title: 'Share of prompt tokens reused from the cache, over all requests' },
    { key: 'out_tps', label: 'Out t/s', num: true, title: "Median output speed for the model's current build and settings. Choose included requests in the Speed tab." },
    { key: 'in_tps', label: 'In t/s', num: true, title: "Median prompt speed for the model's current build and settings. Choose included requests in the Speed tab." },
    { key: 'load', label: 'Load time', num: true, title: 'Average time from starting to ready' },
    { key: 'energy', label: 'Energy Wh', num: true, title: 'Total GPU energy for this model, in watt-hours' },
    { key: 'last_used', label: 'Last used', title: 'When the model last got a request' },
  ] as const satisfies readonly Column[]
  type Col = (typeof columns)[number]['key']
  const allKeys = columns.map((c) => c.key) as Col[]

  function stored(key: string): unknown {
    try {
      return JSON.parse(localStorage.getItem(key) ?? 'null')
    } catch {
      return null
    }
  }
  function store(key: string, v: unknown) {
    try {
      if (v == null) localStorage.removeItem(key)
      else localStorage.setItem(key, JSON.stringify(v))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  function savedHidden(): Col[] {
    const v = stored('models_hidden_columns')
    return Array.isArray(v) ? allKeys.filter((k) => v.includes(k)) : []
  }
  function savedOrder(): Col[] {
    const v = stored('models_column_order')
    if (!Array.isArray(v)) return allKeys
    const list = v.filter((k, n): k is Col => allKeys.includes(k) && v.indexOf(k) === n)
    // A column added later goes after the column it follows in the default order.
    for (const [n, k] of allKeys.entries()) {
      if (list.includes(k)) continue
      const prev = allKeys.slice(0, n).findLast((p) => list.includes(p))
      list.splice(prev ? list.indexOf(prev) + 1 : 0, 0, k)
    }
    return list
  }
  let hidden = $state<Col[]>(savedHidden())
  let order = $state<Col[]>(savedOrder())
  const shownCols = $derived(order.filter((k) => !hidden.includes(k)).map((k) => columns.find((c) => c.key === k)!))
  const span = $derived(shownCols.length + 1)
  function toggleCol(k: Col) {
    hidden = hidden.includes(k) ? hidden.filter((c) => c !== k) : [...hidden, k]
    store('models_hidden_columns', hidden)
  }
  function resetCols() {
    hidden = []
    order = allKeys
    store('models_hidden_columns', null)
    store('models_column_order', null)
  }
  // Dragging a header, or an entry in the Columns menu, moves the column as
  // the pointer passes the middle of another. The order is saved when the
  // drag ends.
  let dragging = $state<Col | null>(null)
  function dragStart(e: DragEvent, k: Col) {
    dragging = k
    e.dataTransfer!.effectAllowed = 'move'
    e.dataTransfer!.setData('text/plain', k)
  }
  function dragOver(e: DragEvent, k: Col, axis: 'x' | 'y') {
    if (!dragging) return
    e.preventDefault()
    if (dragging === k) return
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const after = axis === 'x' ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2
    const rest = order.filter((c) => c !== dragging)
    const at = rest.indexOf(k) + (after ? 1 : 0)
    order = [...rest.slice(0, at), dragging, ...rest.slice(at)]
  }
  function dragEnd() {
    dragging = null
    store('models_column_order', order)
  }
  let picker = $state<HTMLDetailsElement>()
  function windowClick(e: MouseEvent) {
    if (picker?.open && !picker.contains(e.target as Node)) picker.open = false
    if (!(e.target as Element).closest?.('[data-confirm]')) armed = null
  }

  function speedTitle(s: Stat | undefined, key: 'gen' | 'prompt') {
    const c = s?.current
    if (!c) return 'No finished requests with speeds yet'
    const on = `build ${c.build ? shortBuild(c.build) : 'unknown'}`
    const n = c[`${key}_counted`]
    return n ? `Median of ${n.toLocaleString('en-US')} included requests on ${on}` : `No requests on ${on} match the speed filters. Adjust filters in the Speed tab.`
  }

  const dot: Record<string, string> = { ready: 'bg-primary', starting: 'bg-secondary pulse', stopping: 'bg-warn pulse' }
  // The State column's label for a model that isn't stopped, and the bar on
  // the left edge of both its rows, which shows even when that column is hidden.
  const pill: Record<string, string> = { ready: 'bg-primary/15 text-primary', starting: 'bg-secondary/15 text-secondary', stopping: 'bg-warn/15 text-warn' }
  const edge: Record<string, string> = {
    ready: 'shadow-[inset_3px_0_0_var(--color-primary)]',
    starting: 'shadow-[inset_3px_0_0_var(--color-secondary)]',
    stopping: 'shadow-[inset_3px_0_0_var(--color-warn)]',
  }
  const th = 'px-2 py-1.5 font-medium whitespace-nowrap'
  const btn = 'rounded border border-line bg-panel2 px-2 text-xs hover:border-primary disabled:opacity-50 disabled:hover:border-line'
  // The second line's buttons are all 20 px tall. The tab buttons have a
  // clear border so they match the bordered ones.
  const act2 = 'inline-flex h-[20px] items-center rounded border border-line bg-panel2 px-2 text-[11.5px] hover:border-primary disabled:opacity-50 disabled:hover:border-line'
</script>

<svelte:window onclick={windowClick} />

<!-- The unit is dimmed, and left off when there is no number. -->
{#snippet withUnit(v: number | null | undefined, digits: number, unit: string)}
  {fixed(v, digits)}{#if units && v != null && isFinite(v)}<span class="ml-0.5 text-dim">{unit}</span>{/if}
{/snippet}

<!-- Load, unload or restart, each needing a second click to confirm. -->
{#snippet actButton(model: string, action: 'load' | 'unload' | 'restart', disabled: boolean, title?: string)}
  {@const key = `${model} ${action}`}
  <button
    class="{act2} {armed === key ? armedCls : ''}"
    data-confirm
    {disabled}
    {title}
    onclick={(e) => (e.stopPropagation(), confirmed(key) && act(model, action))}
    >{busy[model] === action ? doing[action] : armed === key ? `Click to confirm ${action}` : label[action]}</button
  >
{/snippet}

<!-- One cell of a movable column. m is the model's entry in llama-swap's
     config, missing for a model no longer in it. -->
{#snippet cell(k: Col, m: RunningModel | undefined, s: Stat | undefined)}
  {#if k === 'aliases'}
    <td class="text-muted" title={m?.aliases?.join(', ')}><div class="max-w-[240px] truncate">{m ? m.aliases?.join(', ') || '–' : ''}</div></td>
  {:else if k === 'state'}
    {#if m}
      <td>
        {#if m.state === 'stopped'}
          <span class="text-dim">not loaded</span>
        {:else}
          <span class="inline-flex items-center gap-1.5 rounded-full px-2 py-px font-medium {pill[m.state] ?? 'bg-line text-text'}">
            <span class="size-2 rounded-full {dot[m.state] ?? 'bg-line'}"></span>
            {m.state}
          </span>
        {/if}
      </td>
    {:else}<td class="text-dim">removed</td>{/if}
  {:else if k === 'context'}<td class="num text-right text-muted">{#if m}{@render withUnit(m.context_length || null, 0, 't')}{/if}</td>
  {:else if k === 'requests'}<td class="num text-right">{fixed(s?.requests ?? 0)}{#if s?.failed}{' '}<span class="text-err" title="failed">({fixed(s.failed)})</span>{/if}</td>
  {:else if k === 'cache'}
    {#if s?.cache_rate != null}
      <!-- A bar under the number shows the share, as on the Requests page. -->
      <td class="num relative text-right" title="{fixed(s.cached_tokens)} of {fixed(s.prompt_tokens)} prompt tokens from the cache">
        {fixed(s.cache_rate * 100)}
        <span class="absolute inset-x-1.5 bottom-0 h-0.5 rounded-sm bg-line-soft"><span class="block h-full rounded-sm" style:width="{s.cache_rate * 100}%" style:background={colorful ? 'var(--color-primary)' : 'var(--color-dim)'}></span></span>
      </td>
    {:else}<td class="num text-right">–</td>{/if}
  {:else if k === 'out_tps'}<td class="num text-right" title={speedTitle(s, 'gen')}>{@render withUnit(s?.current?.gen_per_second, 1, 't/s')}</td>
  {:else if k === 'in_tps'}<td class="num text-right" title={speedTitle(s, 'prompt')}>{@render withUnit(s?.current?.prompt_per_second, 0, 't/s')}</td>
  {:else if k === 'load'}<td class="num text-right" title={s?.loads ? `${s.loads} timed loads` : ''}>{duration(s?.load_ms)}</td>
  {:else if k === 'energy'}<td class="num text-right">{s?.energy_j != null ? fixed(s.energy_j / 3600, 2) : '–'}</td>
  {:else if k === 'last_used'}<td class="text-muted">{s?.last_used ? when(s.last_used) : '–'}</td>
  {/if}
{/snippet}

<!-- The second line of a model's entry, shown even when it is closed: load,
     unload and restart (for a model in the config), then one button per tab
     of the model's detail. m is missing for a model no longer in the config. -->
{#snippet actions(model: string, m: RunningModel | undefined)}
  <tr class="border-b border-line-soft group-hover:bg-hover {open === model ? 'bg-hover' : ''}" onclick={() => toggle(model)}>
    <td colspan={span} class="cursor-pointer px-2 pt-1 pb-2 {m ? (edge[m.state] ?? '') : ''}">
      <!-- Indented to start under the model's name: the arrow, the mark and their gaps are 25 px. -->
      <div class="flex flex-wrap items-center gap-1 pl-[25px]">
        {#if m}
          {@const b = busy[model]}
          {#if m.state === 'stopped'}
            {@render actButton(model, 'load', !!b || !up)}
          {:else}
            {@render actButton(model, 'unload', !!b || m.state === 'stopping')}
          {/if}
          {@render actButton(
            model,
            'restart',
            !!b || !up || m.state !== 'ready',
            m.state === 'ready' ? 'Unload the model and load it again' : 'Only a loaded model can be restarted',
          )}
          <span class="mx-1.5 h-3.5 w-px bg-line"></span>
        {/if}
        {#each detailTabs as t (t.tab)}
          {#if t.tab !== 'log' || m}
            {@const on = open === model && (openTab === t.tab || (t.tab === 'requests' && openTab === 'log' && !m))}
            <button
              class="inline-flex h-[20px] items-center gap-1 rounded border border-transparent px-1.5 text-[11.5px] {on ? 'bg-panel2 text-primary' : 'text-muted hover:bg-panel2 hover:text-text'}"
              title={t.label}
              onclick={(e) => (e.stopPropagation(), openAt(model, t.tab))}
            >
              <TabIcon name={t.tab} />{t.short}
            </button>
          {/if}
        {/each}
      </div>
    </td>
  </tr>
{/snippet}

{#snippet head()}
  <thead>
    <tr class="border-b border-line text-left text-[11px] text-dim">
      <th class={th}>Model</th>
      {#each shownCols as c (c.key)}
        <th
          draggable="true"
          class="{th} cursor-grab {'num' in c ? 'text-right' : ''} {dragging === c.key ? 'opacity-40' : ''}"
          title={c.title}
          ondragstart={(e) => dragStart(e, c.key)}
          ondragover={(e) => dragOver(e, c.key, 'x')}
          ondrop={(e) => e.preventDefault()}
          ondragend={dragEnd}>{c.label}</th
        >
      {/each}
    </tr>
  </thead>
{/snippet}

<div class="flex flex-wrap items-center gap-3">
  <h1 class="text-[17px] font-semibold">Models</h1>
  <span class="text-xs text-muted">{listed.length} of {known.length}, {loadedCount} loaded</span>
  <span class="flex-1"></span>
  <label class="flex cursor-pointer items-center gap-1.5 text-xs text-muted">
    <input type="checkbox" checked={showUnlisted} onchange={toggleUnlisted} class="accent-primary" />
    Show unlisted ({unlistedCount})
  </label>
  <label class="flex cursor-pointer items-center gap-1.5 text-xs text-muted" title="Show t after token counts and t/s after speeds. The Requests page uses the same choice.">
    <input type="checkbox" checked={units} onchange={toggleUnits} class="accent-primary" />
    Show units
  </label>
  <details bind:this={picker} class="relative text-xs">
    <summary class="cursor-pointer list-none rounded border border-line bg-panel2 px-2 text-muted hover:text-text">Columns</summary>
    <div class="absolute right-0 z-20 mt-1 w-44 rounded-md border border-line bg-panel2 p-1.5 shadow-lg">
      <div class="px-1.5 pt-0.5 pb-1 text-dim">Drag columns here or by their headers to move them.</div>
      {#each order as k (k)}
        {@const c = columns.find((x) => x.key === k)!}
        <div
          role="listitem"
          draggable="true"
          class="flex cursor-grab items-center gap-1.5 rounded px-1.5 py-0.5 hover:bg-hover {dragging === k ? 'opacity-40' : ''}"
          title={c.title}
          ondragstart={(e) => dragStart(e, k)}
          ondragover={(e) => dragOver(e, k, 'y')}
          ondrop={(e) => e.preventDefault()}
          ondragend={dragEnd}
        >
          <span class="text-dim" aria-hidden="true">⠿</span>
          <label class="flex flex-1 cursor-pointer items-center gap-2 text-text">
            <input type="checkbox" class="accent-primary" checked={!hidden.includes(k)} onchange={() => toggleCol(k)} />
            {c.label}
          </label>
        </div>
      {/each}
      <button class="mt-1 w-full rounded px-1.5 py-0.5 text-left text-primary hover:bg-hover" onclick={resetCols}>Reset to default</button>
    </div>
  </details>
  <button class="{btn} {armed === 'unload-all' ? armedCls : ''}" data-confirm disabled={!loadedCount || unloadingAll} onclick={() => confirmed('unload-all') && unloadAll()}
    >{unloadingAll ? 'Unloading…' : armed === 'unload-all' ? 'Click to confirm unload all' : 'Unload all'}</button
  >
</div>

{#if !up}
  <div class="mt-2 rounded-md border border-l-3 border-line border-l-warn bg-panel2 px-3 py-1.5 text-[12.5px]">
    llama-swap is unavailable{status.current?.llama_swap.error ? ` (${status.current.llama_swap.error})` : ''}. Showing the last model list it returned.
  </div>
{/if}
{#if error}<div class="mt-2 text-xs text-err">Couldn't load model statistics: {error}</div>{/if}
{#if actionError}<div class="mt-2 text-xs text-err">{actionError}</div>{/if}

<div class="mt-2 overflow-x-auto rounded-lg border border-line bg-panel">
  <table class="w-full border-collapse text-[12.5px]">
    {@render head()}
    {#each listed as m (m.model)}
      {@const s = stats.get(m.model)}
      <tbody class="group">
        <tr class="cursor-pointer group-hover:bg-hover [&>td]:px-2 [&>td]:pt-1.5 [&>td]:whitespace-nowrap {open === m.model ? 'bg-hover' : ''}" onclick={() => toggle(m.model)}>
          <td class="max-w-[360px] {edge[m.state] ?? ''}">
            <div class="flex items-center gap-1.5">
              <span class="w-2.5 shrink-0 text-center text-dim">{open === m.model ? '▾' : '▸'}</span>
              <ModelMark model={m.model} />
              <b class="truncate font-semibold" title={modelTitle(m.model)}>{modelLabel(m.model)}</b>
              {#if m.unlisted}<span class="rounded bg-line px-1 text-[10px] text-muted uppercase">unlisted</span>{/if}
              {#if m.name && m.name !== m.model}<span class="truncate text-[11px] text-dim">{modelShow.show === 'name' ? m.model : m.name}</span>{/if}
            </div>
          </td>
          {#each shownCols as c (c.key)}{@render cell(c.key, m, s)}{/each}
        </tr>
        {@render actions(m.model, m)}
        {#if open === m.model}
          <tr><td colspan={span} class="p-0"><ModelDetail model={m.model} known={m} bind:picked={openTab} /></td></tr>
        {/if}
      </tbody>
    {:else}
      <tbody>
        <tr><td colspan={span} class="px-2 py-4 text-center text-muted">{up ? 'No models in the config.' : 'Waiting for llama-swap.'}</td></tr>
      </tbody>
    {/each}
  </table>
</div>

{#if gone.length}
  <h2 class="mt-5 mb-1 text-xs font-semibold tracking-wider text-dim uppercase">No longer in llama-swap's config ({gone.length})</h2>
  <div class="overflow-x-auto rounded-lg border border-line bg-panel">
    <table class="w-full border-collapse text-[12.5px]">
      {@render head()}
      {#each gone as s (s.model)}
        <tbody class="group">
          <tr class="cursor-pointer text-muted group-hover:bg-hover [&>td]:px-2 [&>td]:pt-1.5 [&>td]:whitespace-nowrap {open === s.model ? 'bg-hover' : ''}" onclick={() => toggle(s.model)}>
            <td class="max-w-[360px]">
              <div class="flex items-center gap-1.5">
                <span class="w-2.5 shrink-0 text-center text-dim">{open === s.model ? '▾' : '▸'}</span>
                <ModelMark model={s.model} />
                <span class="truncate" title={modelTitle(s.model)}>{modelLabel(s.model)}</span>
              </div>
            </td>
            {#each shownCols as c (c.key)}{@render cell(c.key, undefined, s)}{/each}
          </tr>
          {@render actions(s.model, undefined)}
          {#if open === s.model}
            <tr><td colspan={span} class="p-0"><ModelDetail model={s.model} bind:picked={openTab} /></td></tr>
          {/if}
        </tbody>
      {/each}
    </table>
  </div>
{/if}
