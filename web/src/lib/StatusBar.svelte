<script lang="ts">
  import { onMount } from 'svelte'
  import { modelColor } from './format'
  import { live } from './live.svelte'
  import { privacy } from './privacy.svelte'
  import { isSocketSensor, status, type GPU } from './status.svelte'
  import { itemLabels, statusItems, type ItemId } from './statusItems.svelte'
  import UsageBar from './UsageBar.svelte'

  onMount(() => {
    status.start()
    live.connect()
  })

  const s = $derived(status.current)
  const ls = $derived(s?.llama_swap)
  const loaded = $derived((ls?.models ?? []).filter((m) => privacy.visible(m.model)))
  // Every card counts toward the total, built-in graphics labelled and last,
  // except a sensor that reads the whole CPU socket: that is CPU power.
  const cardWatts = $derived(
    [...(s?.gpus ?? [])]
      .filter((g) => g.watts != null && !isSocketSensor(g))
      .sort((a, b) => Number(a.integrated) - Number(b.integrated)),
  )
  const tooltip = $derived(cardWatts.map((g) => (g.integrated ? `${cardLine(g)} (built-in graphics)` : cardLine(g))).join('\n'))
  // The system estimate adds only dedicated cards; see systemTooltip.
  const dedicatedWatts = $derived(cardWatts.filter((g) => !g.integrated).reduce((t, g) => t + (g.watts ?? 0), 0))
  const inFlight = $derived(live.flights.size)

  // VRAM of dedicated cards: one bar segment per card, as wide as its VRAM, filled by its use.
  const vramCards = $derived((s?.gpus ?? []).filter((g) => !g.integrated && g.vram_total))
  const vramUsed = $derived(vramCards.reduce((t, g) => t + (g.vram_used ?? 0), 0))
  const vramTotal = $derived(vramCards.reduce((t, g) => t + (g.vram_total ?? 0), 0))
  const vramTooltip = $derived(vramCards.map((g) => `${g.card}: VRAM ${gb(g.vram_used)} / ${gb(g.vram_total)} GB`).join('\n'))

  // Amber when nearly full and red when almost out, like the Dashboard's RAM gauge.
  const ramColor = $derived.by(() => {
    const f = s?.ram_used != null && s.ram_total ? s.ram_used / s.ram_total : 0
    return f >= 0.95 ? 'var(--color-err)' : f >= 0.85 ? 'var(--color-warn)' : undefined
  })

  const systemTooltip = $derived.by(() => {
    if (!s) return ''
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
  })

  // Items with something to show right now, in the chosen order.
  function available(id: ItemId) {
    if (id === 'requests') return true
    if (!status.reachable || !ls || !s) return false
    if (id === 'llama-swap') return true
    if (id === 'models') return ls.up
    if (id === 'power') return s.total_watts != null
    if (id === 'system') return s.system_watts != null || !!s.cpu_problem
    if (id === 'vram') return vramCards.length > 0
    return s.ram_used != null && !!s.ram_total
  }
  const shown = $derived(statusItems.list.filter((i) => i.on && available(i.id)).map((i) => i.id))

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
</script>

<svelte:window onclick={closeMenu} onresize={() => (menuOpen = false)} onkeydown={(e) => e.key === 'Escape' && (menuOpen = false)} />

<!-- Dots sit 1px low so they line up with the middle of the letters, not the middle of the line. -->
{#snippet item(id: ItemId, i: number)}
  {#if id === 'llama-swap' && ls}
    <a
      href="/ui/"
      target="_blank"
      rel="noopener"
      draggable="false"
      class="-mx-1.5 flex items-center gap-1.5 rounded px-1.5 py-0.5 text-muted hover:bg-hover"
      title={ls.error ? `${ls.error}\nOpen llama-swap's page` : "Open llama-swap's page"}
    >
      <span class="relative top-px inline-block size-2 rounded-full {!ls.up ? 'bg-err' : ls.error ? 'bg-warn' : 'bg-primary'}"></span>
      <b class="font-semibold {ls.up ? 'text-text' : 'text-err'}">llama-swap {ls.up ? 'up' : 'down'}</b>
    </a>
  {:else if id === 'requests'}
    <span class="flex items-center gap-1.5 text-muted">
      <span class="relative top-px inline-block size-2 rounded-full {inFlight ? 'pulse bg-secondary' : 'bg-dim'}"></span>
      <b class="num font-semibold text-text">{inFlight}</b> active requests
    </span>
  {:else if id === 'models'}
    <span class="flex items-center gap-1.5 text-muted">
      {#if loaded.length === 0}
        No model loaded
      {:else}
        Loaded
        {#each loaded as m}
          <span class="flex items-center gap-1 {m.state === 'stopping' ? 'opacity-50' : ''}" title="{m.model}: {m.state}">
            <span class="relative top-px inline-block size-2 rounded-full {m.state === 'starting' ? 'pulse' : ''}" style="background:{modelColor(m.model)}"></span>
            <b class="font-semibold text-text">{m.model}</b>
            {#if m.state !== 'ready'}<span class="text-dim">({m.state === 'starting' ? 'loading' : 'unloading'})</span>{/if}
          </span>
        {/each}
      {/if}
    </span>
  {:else if id === 'power' && s}
    <span class="flex items-center gap-1 text-muted" title={tooltip}>
      <svg viewBox="0 0 16 16" class="size-[12px] text-warn" fill="currentColor" stroke="currentColor" stroke-width="1" stroke-linejoin="round" aria-hidden="true">
        <path d="M9.5 1.2 3 9.2h4.3l-1 5.6 6.7-8.2H8.6z" />
      </svg>
      <b class="num font-semibold text-text">{Math.round(s.total_watts ?? 0)}</b> W
      {#if cardWatts.length > 1}
        <span class="num text-dim">({cardWatts.map((g) => `${Math.round(g.watts!)}${g.integrated ? ' iGPU' : ''}`).join(' + ')})</span>
      {/if}
    </span>
  {:else if id === 'system' && s}
    <!-- Right after GPU power it reads as a note on it. Elsewhere it stands alone. -->
    <span class="text-dim {shown[i - 1] === 'power' ? '-ml-2.5' : ''}" title={systemTooltip}>
      {shown[i - 1] === 'power' ? '· system' : 'System'}
      {s.system_watts != null ? `≈ ${Math.round(s.system_watts)} W` : '?'}
    </span>
  {:else if id === 'vram'}
    <span class="flex items-center gap-1.5 text-muted" title={vramTooltip}>
      VRAM
      <UsageBar segments={vramCards.map((g) => ({ used: g.vram_used ?? 0, total: g.vram_total! }))} color="#b69cff" />
      <span><b class="num font-semibold text-text">{gb(vramUsed)}</b> / {gb(vramTotal)} GB</span>
    </span>
  {:else if id === 'ram' && s}
    <span class="flex items-center gap-1.5 text-muted" title="RAM in use. Available RAM includes reclaimable cache.">
      RAM
      <UsageBar segments={[{ used: s.ram_used ?? 0, total: s.ram_total!, color: ramColor }]} color="#40d7a3" />
      <span><b class="num font-semibold text-text">{gb(s.ram_used)}</b> / {gb(s.ram_total)} GB</span>
    </span>
  {/if}
{/snippet}

<header class="sticky top-0 z-20 flex h-[34px] items-center gap-[18px] overflow-x-auto border-b border-line bg-sunken px-3 text-xs whitespace-nowrap">
  <span class="font-bold tracking-wide text-primary">notus-swap</span>

  {#if !status.reachable}
    <span class="flex items-center gap-1.5 text-err"><span class="relative top-px inline-block size-2 rounded-full bg-err"></span><b>notus-swap unavailable</b></span>
  {/if}

  {#each shown as id, i (id)}
    <span
      role="listitem"
      draggable="true"
      class="flex items-center {dragging === id ? 'opacity-40' : ''}"
      ondragstart={(e) => dragStart(e, id)}
      ondragover={(e) => dragOver(e, id, 'x')}
      ondrop={(e) => e.preventDefault()}
      ondragend={dragEnd}
    >
      {@render item(id, i)}
    </span>
  {/each}
  {#if !live.connected && status.reachable}<span class="text-warn">live updates reconnecting…</span>{/if}

  <span class="flex-1"></span>
  {#if s}<span class="text-dim" title="notus-swap version">{s.version}</span>{/if}
  <button
    bind:this={menuButton}
    class="-mx-1 rounded px-1 py-0.5 text-dim hover:bg-hover hover:text-text"
    title="Choose and order the status bar's items"
    aria-label="Status bar items"
    onclick={toggleMenu}
  >
    <svg viewBox="0 0 16 16" class="size-[13px]" fill="currentColor" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
  </button>
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
    <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5">
      <button
        class="rounded px-1.5 py-0.5 text-muted hover:bg-hover hover:text-text disabled:opacity-40"
        disabled={statusItems.isDefault}
        onclick={() => statusItems.reset()}>Reset to default</button
      >
    </div>
  </div>
{/if}
