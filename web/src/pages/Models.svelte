<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import ChipPopover from '../lib/ChipPopover.svelte'
  import { fixed, joules, lastUsed, loadTime, shortBuild } from '../lib/format'
  import ModelMark from '../lib/ModelMark.svelte'
  import { capChips, contextChip, type ModelProps } from '../lib/models/caps'
  import ModelDetail from '../lib/models/ModelDetail.svelte'
  import TabIcon from '../lib/models/TabIcon.svelte'
  import { detailTabs, type DetailTab } from '../lib/models/tabs'
  import { modelShow } from '../lib/modelNames.svelte'
  import NotableChips from '../lib/NotableChips.svelte'
  import { api } from '../lib/serverPath'
  import { modelView, peerStatus, reachable, serverName, servers, type ModelView } from '../lib/servers.svelte'
  import { status, type RunningModel } from '../lib/status.svelte'

  // Every model in llama-swap's config, with its state, notus-swap's numbers
  // for it, and a second line under each with load, unload and restart and a
  // button for each tab of its detail. Clicking a row opens its log, loads
  // and recent requests. Models from past requests that are no longer in the
  // config are listed after. With other servers in the Servers list, each
  // server that answers gets its own section, in the list's order.

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

  // The servers shown: this one ('') and every other server that answers.
  const targets = $derived(servers.many ? servers.list.filter((s) => s.self || reachable(s)).map((s) => (s.self ? '' : s.id)) : [''])
  const targetsKey = $derived(targets.join(','))

  // Each server's numbers by model ID, and what each model's llama-server
  // last reported.
  let statsBy = $state<Record<string, Map<string, Stat>>>({})
  let propsBy = $state<Record<string, Record<string, ModelProps>>>({})
  let errors = $state<Record<string, string>>({})
  async function loadStats() {
    await Promise.all(
      targets.map(async (server) => {
        try {
          const res = await fetch(api(server, '/notus/api/models'))
          if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
          const body: { stats: Stat[]; props: Record<string, ModelProps> } = await res.json()
          statsBy[server] = new Map(body.stats.map((s) => [s.model, s]))
          propsBy[server] = body.props ?? {}
          delete errors[server]
        } catch (e) {
          errors[server] = String(e)
        }
      }),
    )
  }
  onMount(() => {
    status.start()
    const t = setInterval(loadStats, 15_000)
    return () => clearInterval(t)
  })
  // Load at once, and again when a server joins or leaves.
  $effect(() => {
    void targetsKey
    untrack(loadStats)
  })

  /** A model's key on this page: the same ID can be on two servers. */
  const mk = (server: string, model: string) => `${server}:${model}`

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
  // The "No longer in llama-swap's config" tables can be collapsed, for every
  // server at once. Saved per browser.
  let goneCollapsed = $state(localGet('models_gone_collapsed') === '1')
  function toggleGone() {
    goneCollapsed = !goneCollapsed
    try {
      localStorage.setItem('models_gone_collapsed', goneCollapsed ? '1' : '0')
    } catch {}
  }

  // One section per server. Another server's models use its own names,
  // colours and hidden models.
  interface Section {
    server: string
    mv: ModelView
    up: boolean
    upError?: string
    known: RunningModel[]
    listed: RunningModel[]
    unlistedCount: number
    loadedCount: number
    /** Seen in requests, but not in llama-swap's config now. */
    gone: Stat[]
    stats: Map<string, Stat>
    props: Record<string, ModelProps>
    error?: string
  }
  const sections = $derived(
    targets.map((server): Section => {
      const st = server ? peerStatus.get(server)?.current : status.current
      const mv = modelView(server)
      const known = (st?.llama_swap.known ?? []).filter((m) => mv.visible(m.model))
      const stats = statsBy[server] ?? new Map<string, Stat>()
      return {
        server,
        mv,
        up: st?.llama_swap.up ?? false,
        upError: st?.llama_swap.error,
        known,
        listed: known.filter((m) => showUnlisted || !m.unlisted),
        unlistedCount: known.filter((m) => m.unlisted).length,
        loadedCount: known.filter((m) => m.state !== 'stopped').length,
        gone: [...stats.values()].filter((s) => !known.some((k) => k.model === s.model) && mv.visible(s.model)).sort((a, b) => (b.last_used ?? 0) - (a.last_used ?? 0)),
        stats,
        props: propsBy[server] ?? {},
        error: errors[server],
      }
    }),
  )
  const unlistedCount = $derived(sections.reduce((n, sec) => n + sec.unlistedCount, 0))

  // A button stays disabled until its model's state changes, so it can't be
  // pressed twice while llama-swap acts on it. A restart waits for ready, as
  // it passes through stopping and stopped on the way. A change also brings
  // new numbers (a load time, for one).
  // Keys are mk(server, model).
  let busy = $state<Record<string, string>>({})
  let lastState: Record<string, string> = {}
  $effect(() => {
    let changed = false
    for (const sec of sections) {
      for (const m of sec.known) {
        const key = mk(sec.server, m.model)
        if (key in lastState && lastState[key] !== m.state) {
          changed = true
          const b = untrack(() => busy[key])
          if (b && (b !== 'restart' || m.state === 'ready')) delete busy[key]
        }
        lastState[key] = m.state
      }
    }
    if (changed) untrack(loadStats)
  })
  let actionError = $state('')
  async function act(sec: Section, model: string, action: 'load' | 'unload' | 'restart') {
    const key = mk(sec.server, model)
    busy[key] = action
    actionError = ''
    try {
      const res = await fetch(api(sec.server, `/notus/api/models/${encodeURIComponent(model)}/${action}`), { method: 'POST' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    } catch (e) {
      actionError = `Couldn't ${action} ${sec.mv.label(model)}: ${e instanceof Error ? e.message : e}`
      delete busy[key]
    }
    // Give up waiting for a state change. A restart includes a load, which can take minutes.
    setTimeout(() => busy[key] === action && delete busy[key], action === 'restart' ? 300_000 : 30_000)
  }
  // The servers whose models are being unloaded.
  let unloadingAll = $state<Record<string, boolean>>({})
  async function unloadAll(sec: Section) {
    unloadingAll[sec.server] = true
    actionError = ''
    try {
      const res = await fetch(api(sec.server, '/notus/api/models/unload'), { method: 'POST' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    } catch (e) {
      actionError = `Couldn't unload: ${e instanceof Error ? e.message : e}`
    }
    unloadingAll[sec.server] = false
  }

  // Load, unload, restart and unload all take two clicks. The first turns the
  // button into "Click to confirm …", and the second acts. The button goes
  // back after 5 s, or on a click anywhere else. Keys are "<mk> <action>",
  // or "unload-all <server>".
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

  // The open model, by its mk key.
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
    { key: 'caps', label: 'Capabilities', title: 'Context size, and what the model can take in and do, as its llama-server reported when it last loaded' },
    { key: 'requests', label: 'Requests', num: true, title: 'Requests captured, and how many of them failed' },
    { key: 'cache', label: 'Cache %', num: true, title: 'Share of prompt tokens reused from the cache, over all requests' },
    { key: 'out_tps', label: 'Out t/s', num: true, title: "Median output speed for the model's current build and settings. Choose included requests in the Speed tab." },
    { key: 'in_tps', label: 'In t/s', num: true, title: "Median prompt speed for the model's current build and settings. Choose included requests in the Speed tab." },
    { key: 'load', label: 'Load time', num: true, title: 'Average time from starting to ready' },
    { key: 'energy', label: 'Energy', num: true, title: 'Total GPU energy for this model' },
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
<ChipPopover />

<!-- The unit is dimmed, and left off when there is no number. -->
{#snippet withUnit(v: number | null | undefined, digits: number, unit: string)}
  {fixed(v, digits)}{#if units && v != null && isFinite(v)}<span class="ml-0.5 text-dim">{unit}</span>{/if}
{/snippet}

<!-- Load, unload or restart, each needing a second click to confirm. -->
{#snippet actButton(sec: Section, model: string, action: 'load' | 'unload' | 'restart', disabled: boolean, title?: string)}
  {@const key = `${mk(sec.server, model)} ${action}`}
  <button
    class="{act2} {armed === key ? armedCls : ''}"
    data-confirm
    {disabled}
    {title}
    onclick={(e) => (e.stopPropagation(), confirmed(key) && act(sec, model, action))}
    >{busy[mk(sec.server, model)] === action ? doing[action] : armed === key ? `Click to confirm ${action}` : label[action]}</button
  >
{/snippet}

<!-- One cell of a movable column. m is the model's entry in llama-swap's
     config, missing for a model no longer in it. -->
{#snippet cell(k: Col, sec: Section, m: RunningModel | undefined, s: Stat | undefined)}
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
  {:else if k === 'caps'}
    {@const p = sec.props[m?.model ?? s?.model ?? '']}
    {@const ctx = contextChip(m?.context_length, p)}
    <td>
      <div class="flex items-center gap-[3px]">
        <!-- As wide as the widest size, such as 250k, so the chips after it line up. -->
        <div class="min-w-[38px]">{#if ctx}<NotableChips chips={[ctx]} {colorful} />{/if}</div>
        {#if p?.caps}
          <NotableChips chips={capChips(p.caps)} {colorful} />
        {:else}
          <span class="text-dim" title="Read from the model's llama-server when it next loads">–</span>
        {/if}
      </div>
    </td>
  {:else if k === 'requests'}<td class="num text-right">{fixed(s?.requests ?? 0)}{#if s?.failed}<span class="ml-1.5 text-err" title="Requests that ended with an error">{fixed(s.failed)} failed</span>{/if}</td>
  {:else if k === 'cache'}
    {#if s?.cache_rate != null}
      <!-- A bar under the number shows the share, as on the Requests page. -->
      <td class="num relative text-right" title="{fixed(s.cached_tokens)} of {fixed(s.prompt_tokens)} prompt tokens from the cache">
        {@render withUnit(s.cache_rate * 100, 0, '%')}
        <span class="absolute inset-x-1.5 bottom-0 h-[2px] rounded-sm bg-line-soft"><span class="block h-full rounded-sm" style:width="{s.cache_rate * 100}%" style:background={colorful ? 'var(--color-primary)' : 'var(--color-dim)'}></span></span>
      </td>
    {:else}<td class="num text-right">–</td>{/if}
  {:else if k === 'out_tps'}<td class="num text-right" title={speedTitle(s, 'gen')}>{@render withUnit(s?.current?.gen_per_second, 1, 't/s')}</td>
  {:else if k === 'in_tps'}<td class="num text-right" title={speedTitle(s, 'prompt')}>{@render withUnit(s?.current?.prompt_per_second, 0, 't/s')}</td>
  {:else if k === 'load'}<td class="num text-right" title={s?.loads ? `${s.loads} timed loads` : ''}>{loadTime(s?.load_ms)}</td>
  {:else if k === 'energy'}<td class="num text-right">{s?.energy_j != null ? joules(s.energy_j) : '–'}</td>
  {:else if k === 'last_used'}<td class="text-muted">{s?.last_used ? lastUsed(s.last_used) : '–'}</td>
  {/if}
{/snippet}

<!-- The second line of a model's entry, shown even when it is closed: load,
     unload and restart (for a model in the config), then one button per tab
     of the model's detail. m is missing for a model no longer in the config. -->
{#snippet actions(sec: Section, model: string, m: RunningModel | undefined)}
  {@const key = mk(sec.server, model)}
  <tr class="border-b border-line-soft group-hover:bg-hover {open === key ? 'bg-hover' : ''}" onclick={() => toggle(key)}>
    <td colspan={span} class="cursor-pointer px-2 pt-1 pb-2 {m ? (edge[m.state] ?? '') : ''}">
      <!-- Indented to start under the model's name: the arrow, the mark and their gaps are 25 px. -->
      <div class="flex flex-wrap items-center gap-1 pl-[25px]">
        {#if m}
          {@const b = busy[key]}
          {#if m.state === 'stopped'}
            {@render actButton(sec, model, 'load', !!b || !sec.up)}
          {:else}
            {@render actButton(sec, model, 'unload', !!b || m.state === 'stopping')}
          {/if}
          {@render actButton(
            sec,
            model,
            'restart',
            !!b || !sec.up || m.state !== 'ready',
            m.state === 'ready' ? 'Unload the model and load it again' : 'Only a loaded model can be restarted',
          )}
          <span class="mx-1.5 h-3.5 w-px bg-line"></span>
        {/if}
        {#each detailTabs as t (t.tab)}
          {#if t.tab !== 'log' || m}
            {@const on = open === key && (openTab === t.tab || (t.tab === 'requests' && openTab === 'log' && !m))}
            <button
              class="inline-flex h-[20px] items-center gap-1 rounded border border-transparent px-1.5 text-[11.5px] {on ? 'bg-panel2 text-primary' : 'text-muted hover:bg-panel2 hover:text-text'}"
              title={t.label}
              onclick={(e) => (e.stopPropagation(), openAt(key, t.tab))}
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

<!-- Unload all, and the counts, for one server. -->
{#snippet unloadAllButton(sec: Section)}
  {@const key = `unload-all ${sec.server}`}
  <button class="{btn} {armed === key ? armedCls : ''}" data-confirm disabled={!sec.loadedCount || unloadingAll[sec.server]} onclick={() => confirmed(key) && unloadAll(sec)}
    >{unloadingAll[sec.server] ? 'Unloading…' : armed === key ? 'Click to confirm unload all' : 'Unload all'}</button
  >
{/snippet}
{#snippet counts(sec: Section)}
  <span class="text-xs text-muted">{sec.listed.length} of {sec.known.length}, {sec.loadedCount} loaded</span>
{/snippet}

<div class="flex flex-wrap items-center gap-3">
  <h1 class="text-[17px] font-semibold">Models</h1>
  {#if !servers.many && sections[0]}{@render counts(sections[0])}{/if}
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
  {#if !servers.many && sections[0]}{@render unloadAllButton(sections[0])}{/if}
</div>
{#if actionError}<div class="mt-2 text-xs text-err">{actionError}</div>{/if}

{#each sections as sec (sec.server)}
  {#if servers.many}
    <div class="mt-4 flex flex-wrap items-center gap-3">
      <h2 class="text-sm font-semibold">{serverName(sec.server ? servers.get(sec.server) : servers.list.find((x) => x.self))}</h2>
      {@render counts(sec)}
      <span class="flex-1"></span>
      {@render unloadAllButton(sec)}
    </div>
  {/if}
  {#if !sec.up}
    <div class="mt-2 rounded-md border border-l-3 border-line border-l-warn bg-panel2 px-3 py-1.5 text-[12.5px]">
      llama-swap is unavailable{sec.upError ? ` (${sec.upError})` : ''}. Showing the last model list it returned.
    </div>
  {/if}
  {#if sec.error}<div class="mt-2 text-xs text-err">Couldn't load model statistics: {sec.error}</div>{/if}

  <div class="mt-2 overflow-x-auto rounded-lg border border-line bg-panel">
    <table class="w-full border-collapse text-[12.5px]">
      {@render head()}
      {#each sec.listed as m (m.model)}
        {@const s = sec.stats.get(m.model)}
        {@const key = mk(sec.server, m.model)}
        <tbody class="group">
          <tr class="cursor-pointer group-hover:bg-hover [&>td]:px-2 [&>td]:pt-1.5 [&>td]:whitespace-nowrap {open === key ? 'bg-hover' : ''}" onclick={() => toggle(key)}>
            <td class="max-w-[360px] {edge[m.state] ?? ''}">
              <div class="flex items-center gap-1.5">
                <span class="w-2.5 shrink-0 text-center text-dim">{open === key ? '▾' : '▸'}</span>
                <ModelMark model={m.model} color={sec.mv.color(m.model)} shape={sec.mv.shape(m.model)} />
                <b class="truncate font-semibold" title={sec.mv.title(m.model)}>{sec.mv.label(m.model)}</b>
                {#if m.unlisted}<span class="rounded bg-line px-1 text-[10px] text-muted uppercase">unlisted</span>{/if}
                {#if m.name && m.name !== m.model}<span class="truncate text-[11px] text-dim">{modelShow.show === 'name' ? m.model : m.name}</span>{/if}
              </div>
            </td>
            {#each shownCols as c (c.key)}{@render cell(c.key, sec, m, s)}{/each}
          </tr>
          {@render actions(sec, m.model, m)}
          {#if open === key}
            <tr><td colspan={span} class="p-0"><ModelDetail model={m.model} server={sec.server} known={m} props={sec.props[m.model]} bind:picked={openTab} /></td></tr>
          {/if}
        </tbody>
      {:else}
        <tbody>
          <tr><td colspan={span} class="px-2 py-4 text-center text-muted">{sec.up ? 'No models in the config.' : 'Waiting for llama-swap.'}</td></tr>
        </tbody>
      {/each}
    </table>
  </div>

  {#if sec.gone.length}
    <h3 class="mt-5 mb-1 text-xs font-semibold tracking-wider text-dim uppercase">
      <button class="flex items-center gap-1.5 uppercase hover:text-text" aria-expanded={!goneCollapsed} onclick={toggleGone}>
        <!-- A triangle on whole pixels: pointing right when collapsed, down when open. -->
        <svg class="translate-y-px" width="8" height="8" viewBox="0 0 8 8" fill="currentColor" shape-rendering="crispEdges" aria-hidden="true">
          {#if goneCollapsed}<path d="M2 0h1v1h1v1h1v1h1v2h-1v1h-1v1h-1v1h-1z" />{:else}<path d="M0 2h8v1h-1v1h-1v1h-1v1h-2v-1h-1v-1h-1v-1h-1z" />{/if}
        </svg>
        No longer in llama-swap's config ({sec.gone.length})
      </button>
    </h3>
  {/if}
  {#if sec.gone.length && !goneCollapsed}
    <div class="overflow-x-auto rounded-lg border border-line bg-panel">
      <table class="w-full border-collapse text-[12.5px]">
        {@render head()}
        {#each sec.gone as s (s.model)}
          {@const key = mk(sec.server, s.model)}
          <tbody class="group">
            <tr class="cursor-pointer text-muted group-hover:bg-hover [&>td]:px-2 [&>td]:pt-1.5 [&>td]:whitespace-nowrap {open === key ? 'bg-hover' : ''}" onclick={() => toggle(key)}>
              <td class="max-w-[360px]">
                <div class="flex items-center gap-1.5">
                  <span class="w-2.5 shrink-0 text-center text-dim">{open === key ? '▾' : '▸'}</span>
                  <ModelMark model={s.model} color={sec.mv.color(s.model)} shape={sec.mv.shape(s.model)} />
                  <span class="truncate" title={sec.mv.title(s.model)}>{sec.mv.label(s.model)}</span>
                </div>
              </td>
              {#each shownCols as c (c.key)}{@render cell(c.key, sec, undefined, s)}{/each}
            </tr>
            {@render actions(sec, s.model, undefined)}
            {#if open === key}
              <tr><td colspan={span} class="p-0"><ModelDetail model={s.model} server={sec.server} props={sec.props[s.model]} bind:picked={openTab} /></td></tr>
            {/if}
          </tbody>
        {/each}
      </table>
    </div>
  {/if}
{/each}
