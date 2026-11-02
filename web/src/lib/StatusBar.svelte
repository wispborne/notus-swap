<script lang="ts">
  import { onMount } from 'svelte'
  import { appTitle } from './appTitle.svelte'
  import type { Shape } from './modelColors.svelte'
  import ModelMark from './ModelMark.svelte'
  import { modelLabel, modelTitle } from './modelNames.svelte'
  import { live } from './live.svelte'
  import { privacy } from './privacy.svelte'
  import { peerStatus, reachable, serverName, servers, serverTone, stateWords, type Server } from './servers.svelte'
  import { isSocketSensor, status, type GPU, type Status } from './status.svelte'
  import { itemLabels, serversShown, statusItems, type ItemId } from './statusItems.svelte'
  import ServerMark, { type Tone } from './ServerMark.svelte'
  import UsageBar from './UsageBar.svelte'

  // The bar's height, for layout that fills the screen (--bar-h in app.css).
  let bar = $state<HTMLElement>()
  onMount(() => {
    status.start()
    live.connect()
    const ro = new ResizeObserver(() => document.documentElement.style.setProperty('--bar-h', `${bar!.offsetHeight}px`))
    ro.observe(bar!)
    return () => ro.disconnect()
  })

  // One server's row of the bar. With other servers in the Servers list, the
  // bar shows one row per server, in the list's order, up to the number
  // chosen in its menu. Like items line up in columns. Another server's
  // models use its own names, colours and hidden models.
  interface View {
    key: string
    /** Set when the bar shows groups. */
    server?: Server
    s: Status | null
    reachable: boolean
    inFlight: number
    /** This server: llama-swap's own page can be opened from here. */
    local: boolean
    visible: (m: string) => boolean
    label: (m: string) => string
    title: (m: string) => string | undefined
    color?: (m: string) => string | undefined
    shape?: (m: string) => Shape | undefined
  }
  const views = $derived.by((): View[] => {
    const home: View = {
      key: '',
      s: status.current,
      reachable: status.reachable,
      inFlight: live.flights.size,
      local: true,
      visible: privacy.visible,
      label: (m) => modelLabel(m),
      title: modelTitle,
    }
    if (!servers.many) return [home]
    return servers.list.slice(0, serversShown.count).map((sv): View => {
      if (sv.self) return { ...home, server: sv }
      const r = peerStatus.get(sv.id)
      const s = r?.current ?? null
      return {
        key: sv.id,
        server: sv,
        s,
        reachable: reachable(sv) && !!s,
        inFlight: live.count(sv.id),
        local: false,
        visible: (m) => r?.visible(m) ?? false,
        label: (m) => r?.label(m) ?? m,
        title: (m) => r?.title(m),
        color: (m) => r?.color(m),
        shape: (m) => r?.shape(m),
      }
    })
  })

  // Power of every server that answers, for the total after the groups.
  const total = $derived.by(() => {
    const on = views.filter((v) => v.reachable && v.s)
    const sum = (f: (s: Status) => number | null) => {
      const vals = on.map((v) => f(v.s!)).filter((w): w is number => w != null)
      return vals.length ? vals.reduce((t, w) => t + w, 0) : null
    }
    return { gpu: sum((s) => s.total_watts), system: sum((s) => s.system_watts), parts: on.filter((v) => v.s!.total_watts != null).length }
  })

  // The numbers one server's items show.
  function figures(s: Status) {
    // Every card counts toward the total, built-in graphics labelled and last,
    // except a sensor that reads the whole CPU socket: that is CPU power.
    const cardWatts = [...s.gpus].filter((g) => g.watts != null && !isSocketSensor(g)).sort((a, b) => Number(a.integrated) - Number(b.integrated))
    // The system estimate adds only dedicated cards; see systemTooltip.
    const dedicatedWatts = cardWatts.filter((g) => !g.integrated).reduce((t, g) => t + (g.watts ?? 0), 0)
    // VRAM of dedicated cards: one bar segment per card, as wide as its VRAM, filled by its use.
    const vramCards = s.gpus.filter((g) => !g.integrated && g.vram_total)
    // Amber when nearly full and red when almost out, like the Dashboard's RAM gauge.
    const ramFull = s.ram_used != null && s.ram_total ? s.ram_used / s.ram_total : 0
    return {
      cardWatts,
      tooltip: cardWatts.map((g) => (g.integrated ? `${cardLine(g)} (built-in graphics)` : cardLine(g))).join('\n'),
      vramCards,
      vramUsed: vramCards.reduce((t, g) => t + (g.vram_used ?? 0), 0),
      vramTotal: vramCards.reduce((t, g) => t + (g.vram_total ?? 0), 0),
      vramTooltip: vramCards.map((g) => `${g.card}: VRAM ${gb(g.vram_used)} / ${gb(g.vram_total)} GB`).join('\n'),
      ramColor: ramFull >= 0.95 ? 'var(--color-err)' : ramFull >= 0.85 ? 'var(--color-warn)' : undefined,
      systemTooltip: systemTooltip(s, dedicatedWatts),
    }
  }
  type Figures = ReturnType<typeof figures>

  function systemTooltip(s: Status, dedicatedWatts: number) {
    if (s.system_watts == null)
      return `System power needs the CPU's power reading, which is not available: ${s.cpu_problem ?? 'unknown reason'}.`
    const r = (v: number | null) => Math.round(v ?? 0)
    return [
      'Estimated power of the whole machine at the wall:',
      `  dedicated GPUs ${r(dedicatedWatts)} W + CPU socket ${r(s.cpu_watts)} W + everything else ~${r(s.system_base_watts)} W`,
      `  divided by ${Math.round(s.psu_efficiency * 100)}% power supply efficiency.`,
      'The "everything else" part is a guess, so this can be off by 20-40 W.',
      s.cpu_source === 'ppt'
        ? "The CPU socket figure comes from the built-in graphics' PPT sensor, which covers the whole CPU."
        : "The CPU socket figure comes from the CPU's own energy counter (RAPL).",
    ].join('\n')
  }

  // Items with something to show right now, in the chosen order.
  function shownFor(v: View, f: Figures | null) {
    const s = v.s
    const available = (id: ItemId) => {
      if (id === 'requests') return true
      if (!v.reachable || !s || !f) return false
      if (id === 'models') return s.llama_swap.up
      if (id === 'power') return s.total_watts != null
      if (id === 'system') return s.system_watts != null || !!s.cpu_problem
      if (id === 'vram') return f.vramCards.length > 0
      return s.ram_used != null && !!s.ram_total
    }
    return statusItems.list.filter((i) => i.on && available(i.id)).map((i) => i.id)
  }
  const powerOn = $derived(statusItems.list.some((i) => i.id === 'power' && i.on))

  // Each row's figures and items, and the columns: every item some row shows,
  // in the chosen order. A row without an item leaves its cell empty.
  const rows = $derived(
    views.map((v) => {
      const f = v.s ? figures(v.s) : null
      return { v, f, shown: shownFor(v, f) }
    }),
  )
  const columns = $derived(statusItems.list.map((i) => i.id).filter((id) => rows.some((r) => r.shown.includes(id))))
  // Servers past the number shown.
  const more = $derived(servers.many ? servers.list.slice(serversShown.count) : [])

  // Dragging an item, on the bar or in the menu, moves it as the pointer passes
  // the middle of another item. The order is saved when the drag ends.
  let dragging = $state<ItemId | null>(null)
  function dragStart(e: DragEvent, id: ItemId) {
    dragging = id
    e.dataTransfer!.effectAllowed = 'move'
    e.dataTransfer!.setData('text/plain', id)
  }
  function dragOver(e: DragEvent, id: ItemId, axis: 'x' | 'y') {
    if (!dragging) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    statusItems.place(dragging, id, axis === 'x' ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2)
  }
  function dragEnd() {
    dragging = null
    statusItems.save()
  }

  // The menu is fixed to the window, because the bar clips anything that sticks out of it.
  let menuOpen = $state(false)
  let menuPos = $state({ top: 0, right: 0 })
  let menuButton = $state<HTMLButtonElement>()
  let menu = $state<HTMLDivElement>()
  function toggleMenu() {
    const r = menuButton!.getBoundingClientRect()
    menuPos = { top: r.bottom + 4, right: window.innerWidth - r.right }
    menuOpen = !menuOpen
  }
  function closeMenu(e: MouseEvent) {
    const t = e.target as Node
    if (menuOpen && !menu?.contains(t) && !menuButton?.contains(t)) menuOpen = false
  }

  const gb = (b: number | null) => (b == null ? '?' : (b / 1024 ** 3).toFixed(1))
  function cardLine(g: GPU) {
    const parts = [`${g.card}${g.name ? ` (${g.name})` : ''}`]
    if (g.watts != null) parts.push(`${Math.round(g.watts)} W${g.power_cap_watts ? ` of ${Math.round(g.power_cap_watts)} W` : ''}`)
    if (g.temp_c != null) parts.push(`${Math.round(g.temp_c)} °C`)
    if (g.vram_used != null) parts.push(`VRAM ${gb(g.vram_used)} / ${gb(g.vram_total)} GB`)
    if (g.busy_percent != null) parts.push(`${Math.round(g.busy_percent)}% busy`)
    return parts.join(' · ')
  }
  // The colour of the server's mark before its name: llama-swap's state
  // while the server answers (up, up with an error, down), or else the
  // server's own state. `words` is its tooltip.
  function nameTone(v: View): { tone: Tone; words: string } {
    const sv = v.server && !v.server.self ? v.server : null
    if (!sv && !status.reachable) return { tone: 'down', words: 'notus-swap unavailable' }
    if (sv && !v.reachable) return { tone: serverTone(sv), words: stateWords(sv) }
    if (sv?.state === 'needs_update') return { tone: 'warn', words: stateWords(sv) }
    const ls = v.s?.llama_swap
    if (!ls) return { tone: 'checking', words: 'Checking…' }
    if (!ls.up) return { tone: 'down', words: `llama-swap down${ls.error ? `: ${ls.error}` : ''}` }
    if (ls.error) return { tone: 'warn', words: `llama-swap up, with an error: ${ls.error}` }
    return { tone: 'up', words: 'llama-swap up' }
  }
</script>

<svelte:window onclick={closeMenu} onresize={() => (menuOpen = false)} onkeydown={(e) => e.key === 'Escape' && (menuOpen = false)} />

<!-- Dots sit 1px low so they line up with the middle of the letters, not the middle of the line. -->
{#snippet item(id: ItemId, i: number, v: View, f: Figures | null, shown: ItemId[])}
  {@const s = v.s}
  {@const ls = s?.llama_swap}
  {#if id === 'requests'}
    <span class="flex items-center gap-1.5 text-muted" title="{v.inFlight} active {v.inFlight === 1 ? 'request' : 'requests'}">
      <span class="relative top-px inline-block size-[7px] rounded-full {v.inFlight ? 'pulse bg-secondary' : 'bg-dim'}"></span>
      <b class="num font-semibold text-text">{v.inFlight}</b>
    </span>
  {:else if id === 'models' && ls}
    {@const loaded = ls.models.filter((m) => v.visible(m.model))}
    <span class="flex items-center gap-1.5 text-muted">
      {#if loaded.length === 0}
        No model loaded
      {:else}
        Loaded
        {#each loaded as m}
          <span class="flex items-center gap-1 {m.state === 'stopping' ? 'opacity-50' : ''}" title="{v.title(m.model)}
State: {m.state}">
            <ModelMark model={m.model} color={v.color?.(m.model)} shape={v.shape?.(m.model)} class="relative top-px {m.state === 'starting' ? 'pulse' : ''}" />
            <b class="font-semibold text-text">{v.label(m.model)}</b>
            {#if m.state !== 'ready'}<span class="text-dim">({m.state === 'starting' ? 'loading' : 'unloading'})</span>{/if}
          </span>
        {/each}
      {/if}
    </span>
  {:else if id === 'power' && s && f}
    <span class="flex items-center gap-1 text-muted" title={f.tooltip}>
      <svg viewBox="0 0 16 16" class="size-[12px] text-warn" fill="currentColor" stroke="currentColor" stroke-width="1" stroke-linejoin="round" aria-hidden="true">
        <path d="M9.5 1.2 3 9.2h4.3l-1 5.6 6.7-8.2H8.6z" />
      </svg>
      <b class="num font-semibold text-text">{Math.round(s.total_watts ?? 0)}</b> W
      {#if f.cardWatts.length > 1}
        <span class="num text-dim">({f.cardWatts.map((g) => `${Math.round(g.watts!)}${g.integrated ? ' iGPU' : ''}`).join(' + ')})</span>
      {/if}
    </span>
  {:else if id === 'system' && s && f}
    <!-- Right after GPU power it reads as a note on it. Elsewhere it stands alone. -->
    <span class="text-dim {shown[i - 1] === 'power' ? '-ml-2.5' : ''}" title={f.systemTooltip}>
      {shown[i - 1] === 'power' ? '· system' : 'System'}
      {s.system_watts != null ? `≈ ${Math.round(s.system_watts)} W` : '?'}
    </span>
  {:else if id === 'vram' && f}
    <span class="flex items-center gap-1.5 text-muted" title={f.vramTooltip}>
      VRAM
      <UsageBar segments={f.vramCards.map((g) => ({ used: g.vram_used ?? 0, total: g.vram_total! }))} color="#b69cff" />
      <span><b class="num font-semibold text-text">{gb(f.vramUsed)}</b> / {gb(f.vramTotal)} GB</span>
    </span>
  {:else if id === 'ram' && s && f}
    <span class="flex items-center gap-1.5 text-muted" title="RAM in use. Available RAM includes reclaimable cache.">
      RAM
      <UsageBar segments={[{ used: s.ram_used ?? 0, total: s.ram_total!, color: f.ramColor }]} color="#40d7a3" />
      <span><b class="num font-semibold text-text">{gb(s.ram_used)}</b> / {gb(s.ram_total)} GB</span>
    </span>
  {/if}
{/snippet}

<!-- One row per server, on one grid so like items line up in columns: the
     server's name, one column per item, then the menu on the first row. -->
<header
  bind:this={bar}
  class="sticky top-0 z-20 grid min-h-[34px] auto-rows-[15px] items-center gap-x-[18px] gap-y-[2px] overflow-x-auto border-b border-line bg-sunken px-3 py-[7px] text-xs whitespace-nowrap"
  style:grid-template-columns="auto {columns.map(() => 'auto').join(' ')} minmax(0, 1fr) auto"
>
  {#each rows as { v, f, shown }, r (v.key)}
    {@const row = r + 1}
    {@const home = !v.server || v.server.self}
    {@const d = nameTone(v)}
    <!-- The server's name, after a dot for llama-swap's state. This server's
         name opens llama-swap's own page. -->
    <svelte:element
      this={v.local ? 'a' : 'span'}
      href={v.local ? '/ui/' : undefined}
      target={v.local ? '_blank' : undefined}
      rel={v.local ? 'noopener' : undefined}
      draggable="false"
      class="-mx-1.5 flex items-center gap-1.5 rounded px-1.5 {v.local ? 'hover:bg-hover' : ''} {home ? 'font-bold tracking-wide text-primary' : 'font-semibold text-text'}"
      style:grid-row={row}
      style:grid-column={1}
      title={[d.words, !home && v.server?.url, v.local && "Open llama-swap's page"].filter(Boolean).join('\n')}
    >
      <!-- Centred in the 15 px row, the 12 px mark would start on a half
           pixel. 1.5 px lower puts it on whole pixels, 2 px over the capitals
           and 3 px under the baseline. -->
      <ServerMark server={v.server} tone={d.tone} class="relative top-[1.5px]" />
      {home ? appTitle.text : serverName(v.server)}
    </svelte:element>
    {#if home && !status.reachable}
      <span class="flex items-center gap-1.5 text-err" style:grid-row={row} style:grid-column="2 / span {Math.max(columns.length, 1)}">
        <span class="relative top-px inline-block size-2 rounded-full bg-err"></span><b>notus-swap unavailable</b>
      </span>
    {:else if !home && !v.reachable}
      <span class="text-dim" style:grid-row={row} style:grid-column="2 / span {Math.max(columns.length, 1)}">{stateWords(v.server!)}</span>
    {:else}
      {#each columns as id, i (id)}
        {#if shown.includes(id)}
          <span
            role="listitem"
            draggable="true"
            class="flex items-center {dragging === id ? 'opacity-40' : ''}"
            style:grid-row={row}
            style:grid-column={i + 2}
            ondragstart={(e) => dragStart(e, id)}
            ondragover={(e) => dragOver(e, id, 'x')}
            ondrop={(e) => e.preventDefault()}
            ondragend={dragEnd}
          >
            {@render item(id, i, v, f, columns)}
          </span>
        {/if}
      {/each}
    {/if}
  {/each}

  <span class="flex items-center gap-[18px] justify-self-end" style:grid-row={1} style:grid-column={columns.length + 3}>
    {#if servers.many && powerOn && total.parts > 1 && total.gpu != null}
      <span class="flex items-center gap-1 text-muted" title="Every server that answers, added up">
        All servers
        <b class="num font-semibold text-text">{Math.round(total.gpu)}</b> W
        {#if total.system != null}<span class="text-dim">· system ≈ {Math.round(total.system)} W</span>{/if}
      </span>
    {/if}
    {#if more.length}
      <span class="text-dim" title={more.map((s) => `${serverName(s)}: ${stateWords(s)}`).join('\n')}>+{more.length} more {more.length === 1 ? 'server' : 'servers'}</span>
    {/if}
    {#if !live.connected && status.reachable}<span class="text-warn">live updates reconnecting…</span>{/if}
    {#if status.current}<span class="text-dim" title="notus-swap version">{status.current.version}</span>{/if}
    <button
      bind:this={menuButton}
      class="-mx-1 rounded px-1 py-0.5 text-dim hover:bg-hover hover:text-text"
      title="Choose and order the status bar's items"
      aria-label="Status bar items"
      onclick={toggleMenu}
    >
      <svg viewBox="0 0 16 16" class="size-[13px]" fill="currentColor" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
    </button>
  </span>
</header>

{#if menuOpen}
  <div
    bind:this={menu}
    class="fixed z-30 w-60 rounded-md border border-line bg-panel2 p-1.5 text-xs shadow-lg"
    style="top:{menuPos.top}px;right:{menuPos.right}px"
  >
    <div class="px-1.5 pt-0.5 pb-1.5 text-dim">Drag items here or on the bar to move them.</div>
    {#each statusItems.list as it, n (it.id)}
      <div
        role="listitem"
        draggable="true"
        class="flex cursor-grab items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover {dragging === it.id ? 'opacity-40' : ''}"
        ondragstart={(e) => dragStart(e, it.id)}
        ondragover={(e) => dragOver(e, it.id, 'y')}
        ondrop={(e) => e.preventDefault()}
        ondragend={dragEnd}
      >
        <span class="text-dim" aria-hidden="true">⠿</span>
        <label class="flex flex-1 cursor-pointer items-center gap-2">
          <input type="checkbox" class="accent-primary" checked={it.on} onchange={() => statusItems.toggle(it.id)} />
          {itemLabels[it.id]}
        </label>
        <button
          class="rounded px-1 text-dim hover:text-text disabled:opacity-30"
          disabled={n === 0}
          title="Move left"
          aria-label="Move {itemLabels[it.id]} left"
          onclick={() => statusItems.shift(it.id, -1)}>↑</button
        >
        <button
          class="rounded px-1 text-dim hover:text-text disabled:opacity-30"
          disabled={n === statusItems.list.length - 1}
          title="Move right"
          aria-label="Move {itemLabels[it.id]} right"
          onclick={() => statusItems.shift(it.id, 1)}>↓</button
        >
      </div>
    {/each}
    {#if servers.many}
      <label class="mt-1 flex items-center gap-2 border-t border-line-soft px-1.5 pt-1.5 text-muted">
        Servers shown, one row each
        <input
          type="number"
          min="1"
          max="9"
          class="ml-auto w-12 rounded border border-line bg-panel px-1 py-0.5 text-text outline-none focus:border-primary"
          value={serversShown.count}
          onchange={(e) => serversShown.set(e.currentTarget.valueAsNumber)}
        />
      </label>
    {/if}
    <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5">
      <button
        class="rounded px-1.5 py-0.5 text-muted hover:bg-hover hover:text-text disabled:opacity-40"
        disabled={statusItems.isDefault}
        onclick={() => statusItems.reset()}>Reset to default</button
      >
    </div>
  </div>
{/if}
