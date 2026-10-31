<script lang="ts">
  // The Servers section of Settings: other notus-swap servers to show here.
  // The browser only talks to this server, which passes calls on to the
  // others. See servers.svelte.ts and internal/peers.
  import { onMount } from 'svelte'
  import { lastUsed } from '../format'
  import { serverName, servers, stateWords, type Server } from '../servers.svelte'
  import { status } from '../status.svelte'

  interface Probe {
    url: string
    version: string
    name: string
    latency_ms: number
    clock_gap_ms: number
  }

  let error = $state('')

  async function refresh() {
    const res = await fetch('/notus/api/peers', { cache: 'no-store' })
    if (res.ok) servers.apply((await res.json()).servers)
  }
  onMount(() => {
    servers.load()
  })

  /** Sends a call, then reads the list again. Returns false on an error. */
  async function send(url: string, method: string, body?: unknown) {
    error = ''
    try {
      const res = await fetch(url, { method, body: body === undefined ? undefined : JSON.stringify(body) })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      await refresh()
      return true
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
      return false
    }
  }

  // ---- Adding ----
  let address = $state('')
  let name = $state('')
  let busy = $state('')
  let found = $state<Probe | null>(null)
  let addError = $state('')

  async function test() {
    busy = 'test'
    addError = ''
    found = null
    try {
      const res = await fetch('/notus/api/peers/test', { method: 'POST', body: JSON.stringify({ url: address }) })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      found = await res.json()
    } catch (e) {
      addError = e instanceof Error ? e.message : String(e)
    } finally {
      busy = ''
    }
  }

  async function add() {
    busy = 'add'
    addError = ''
    try {
      const res = await fetch('/notus/api/peers', { method: 'POST', body: JSON.stringify({ url: address, name }) })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      address = ''
      name = ''
      found = null
      await refresh()
    } catch (e) {
      addError = e instanceof Error ? e.message : String(e)
    } finally {
      busy = ''
    }
  }

  // ---- Editing a row ----
  let editing = $state<{ id: string; name: string; url: string } | null>(null)
  async function saveEdit(s: Server) {
    const e = editing!
    const body: { name?: string; url?: string } = {}
    if (e.name.trim() !== (s.name ?? '')) body.name = e.name
    if (e.url.trim() !== (s.url ?? '')) body.url = e.url
    if (!Object.keys(body).length || (await send(`/notus/api/peers/${s.id}`, 'PATCH', body))) editing = null
  }

  // Remove takes two clicks, like the Models page's buttons. The first turns
  // the button into "Click to confirm remove"; it goes back after 5 s or on a
  // click anywhere else.
  let armed = $state<string | null>(null)
  let disarm: ReturnType<typeof setTimeout> | undefined
  function remove(id: string) {
    clearTimeout(disarm)
    if (armed !== id) {
      armed = id
      disarm = setTimeout(() => (armed = null), 5000)
      return
    }
    armed = null
    send(`/notus/api/peers/${id}`, 'DELETE').then((ok) => {
      if (!ok) return
      // The sections above may show the server just removed.
      const u = new URL(location.href)
      u.searchParams.delete('server')
      history.replaceState(history.state, '', u)
      location.reload()
    })
  }
  function windowClick(e: MouseEvent) {
    if (!(e.target as Element).closest?.('[data-confirm]')) armed = null
  }

  // ---- Order ----
  // Rows are dragged, or moved with the arrow buttons. The order is saved
  // when a drag ends.
  let order = $state<string[] | null>(null)
  const rows = $derived.by(() => {
    if (!order) return servers.list
    return order.map((id) => servers.get(id)).filter((s): s is Server => !!s)
  })
  let dragging = $state<string | null>(null)
  function dragStart(e: DragEvent, id: string) {
    dragging = id
    order = servers.list.map((s) => s.id)
    e.dataTransfer!.effectAllowed = 'move'
    e.dataTransfer!.setData('text/plain', id)
  }
  function dragOver(e: DragEvent, id: string) {
    if (!dragging || !order || id === dragging) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const next = order.filter((x) => x !== dragging)
    const i = next.indexOf(id) + (e.clientY > r.top + r.height / 2 ? 1 : 0)
    next.splice(i, 0, dragging)
    order = next
  }
  async function dragEnd() {
    dragging = null
    if (order) await saveOrder(order)
  }
  async function move(id: string, by: number) {
    const ids = servers.list.map((s) => s.id)
    const i = ids.indexOf(id)
    const j = i + by
    if (j < 0 || j >= ids.length) return
    ;[ids[i], ids[j]] = [ids[j], ids[i]]
    order = ids
    await saveOrder(ids)
  }
  async function saveOrder(ids: string[]) {
    if (ids.join() !== servers.list.map((s) => s.id).join()) await send('/notus/api/peers/order', 'PUT', { ids })
    order = null
  }

  /** The clock gap in words, when it is large enough to put requests out of order. */
  function clockWarning(s: Server) {
    const g = s.clock_gap_ms
    if (g == null || Math.abs(g) < 2000) return ''
    const secs = Math.round(Math.abs(g) / 1000)
    return `Its clock is ${secs} s ${g > 0 ? 'ahead of' : 'behind'} this server's. Requests from the two will show out of order.`
  }

  const dot: Record<string, string> = {
    online: 'bg-primary',
    checking: 'bg-dim',
    needs_update: 'bg-warn',
    offline: 'bg-line',
    wrong_server: 'bg-err',
    not_notus: 'bg-err',
  }
  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 text-muted hover:border-dim hover:text-text disabled:opacity-50 disabled:hover:border-line'
  const icon = 'flex size-6 flex-none items-center justify-center rounded-md text-dim hover:bg-hover hover:text-text disabled:opacity-30 disabled:hover:bg-transparent'
  const input = 'rounded-md border border-line bg-panel2 px-2 py-1 text-text outline-none focus:border-primary'
</script>

<svelte:window onclick={windowClick} />

<section id="servers" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
  <h2 class="text-sm font-semibold">Servers</h2>
  <p class="mt-1 text-[12.5px] text-muted">
    Other notus-swap servers to show here. Calls to them go through this server, so each one only needs to be reachable from this server, not from your browser. The
    top server is the primary: it's shown first. Drag the rows to change the order. This list is kept on this server only; other servers have their own.
  </p>

  {#if error}<p class="mt-2 text-[12.5px] text-err">{error}</p>{/if}

  <ul class="mt-3 flex flex-col gap-1.5">
    {#each rows as s, i (s.id)}
      <li
        class="flex items-start gap-2 rounded-md border bg-panel2 px-2 py-2 text-[12.5px] {dragging === s.id ? 'border-primary opacity-70' : 'border-line'}"
        draggable={editing?.id !== s.id && servers.many}
        ondragstart={(e) => dragStart(e, s.id)}
        ondragover={(e) => dragOver(e, s.id)}
        ondrop={(e) => e.preventDefault()}
        ondragend={dragEnd}
      >
        {#if servers.many}
          <span class="mt-0.5 flex h-5 w-3 flex-none cursor-grab items-center justify-center text-dim" aria-hidden="true" title="Drag to reorder">
            <svg width="6" height="10" viewBox="0 0 6 10" fill="currentColor">
              <rect x="0" y="0" width="2" height="2" /><rect x="4" y="0" width="2" height="2" />
              <rect x="0" y="4" width="2" height="2" /><rect x="4" y="4" width="2" height="2" />
              <rect x="0" y="8" width="2" height="2" /><rect x="4" y="8" width="2" height="2" />
            </svg>
          </span>
        {/if}
        <span class="mt-1.5 size-2 flex-none rounded-full {s.self ? 'bg-primary' : (dot[s.state] ?? 'bg-dim')}"></span>
        <div class="min-w-0 flex-1">
          {#if editing?.id === s.id}
            <form
              class="flex flex-wrap items-center gap-2"
              onsubmit={(e) => {
                e.preventDefault()
                saveEdit(s)
              }}
            >
              <input class="{input} w-40" placeholder="Name" bind:value={editing.name} aria-label="Name" />
              <input class="{input} w-64 font-mono" placeholder="http://192.168.1.5:8080" bind:value={editing.url} aria-label="Address" />
              <button type="submit" class={btn}>Save</button>
              <button type="button" class="px-1 text-muted hover:text-text" onclick={() => (editing = null)}>Cancel</button>
            </form>
          {:else}
            <div class="flex flex-wrap items-baseline gap-x-2">
              <span class="font-semibold text-text">{serverName(s)}</span>
              {#if i === 0 && servers.many}<span class="rounded-full bg-primary/15 px-1.5 text-[11px] font-semibold text-primary">primary</span>{/if}
              {#if s.self}<span class="text-dim">this server</span>{:else}<span class="font-mono text-dim">{s.url}</span>{/if}
            </div>
            <div class="mt-0.5 text-muted">
              {#if s.self}
                notus-swap {status.current?.version ?? ''}
              {:else}
                {stateWords(s)}{#if s.state !== 'online' && s.state !== 'checking' && s.since}{' '}since {lastUsed(s.since)}{/if}
                {#if s.version}· notus-swap {s.version}{/if}
                {#if s.state === 'online' && s.last_seen}· last answered {lastUsed(s.last_seen)}{/if}
              {/if}
            </div>
            {#if !s.self && s.state === 'needs_update'}
              <div class="mt-0.5 text-warn">It runs a version that this server can't show together with its own. Update both to the same release.</div>
            {/if}
            {#if !s.self && s.error && s.state !== 'offline'}<div class="mt-0.5 text-err">{s.error}</div>{/if}
            {#if !s.self && clockWarning(s)}<div class="mt-0.5 text-warn">{clockWarning(s)}</div>{/if}
          {/if}
        </div>
        {#if servers.many && editing?.id !== s.id}
          <div class="flex flex-none items-center gap-0.5">
            <button class={icon} title="Move up" aria-label="Move up" disabled={i === 0} onclick={() => move(s.id, -1)}>
              <svg width="10" height="6" viewBox="0 0 10 6" fill="currentColor" shape-rendering="crispEdges"><path d="M5 0 10 5 9 6 5 2 1 6 0 5Z" /></svg>
            </button>
            <button class={icon} title="Move down" aria-label="Move down" disabled={i === rows.length - 1} onclick={() => move(s.id, 1)}>
              <svg width="10" height="6" viewBox="0 0 10 6" fill="currentColor" shape-rendering="crispEdges"><path d="M5 6 0 1 1 0 5 4 9 0 10 1Z" /></svg>
            </button>
            {#if !s.self}
              <button class="{btn} ml-1" onclick={() => (editing = { id: s.id, name: s.name ?? '', url: s.url ?? '' })}>Edit</button>
              <button class="{btn} {armed === s.id ? 'border-warn! bg-warn/10 text-warn' : ''}" data-confirm onclick={() => remove(s.id)}
                >{armed === s.id ? 'Click to confirm remove' : 'Remove'}</button
              >
            {/if}
          </div>
        {/if}
      </li>
    {/each}
  </ul>

  <form
    class="mt-3 flex flex-wrap items-center gap-2 text-[12.5px]"
    onsubmit={(e) => {
      e.preventDefault()
      add()
    }}
  >
    <input
      class="{input} w-64 font-mono"
      placeholder="http://192.168.1.5:8080"
      aria-label="Address of another notus-swap"
      bind:value={address}
      oninput={() => {
        found = null
        addError = ''
      }}
    />
    <input class="{input} w-40" placeholder="Name (optional)" aria-label="Name" bind:value={name} />
    <button type="button" class={btn} disabled={!address.trim() || !!busy} onclick={test}>{busy === 'test' ? 'Testing…' : 'Test'}</button>
    <button type="submit" class={btn} disabled={!address.trim() || !!busy}>{busy === 'add' ? 'Adding…' : 'Add'}</button>
  </form>
  {#if found}
    <p class="mt-2 text-[12.5px] text-primary">
      Found notus-swap {found.version}{found.name ? ` ("${found.name}")` : ''} at {found.url}. It answered in {found.latency_ms} ms.
      {#if Math.abs(found.clock_gap_ms) >= 2000}<span class="text-warn">Its clock is {Math.round(Math.abs(found.clock_gap_ms) / 1000)} s off from this server's.</span>{/if}
    </p>
  {/if}
  {#if addError}<p class="mt-2 text-[12.5px] text-err">{addError}</p>{/if}
  <p class="mt-2 text-[12.5px] text-dim">
    The other server must let this one reach its port. On Windows, that takes a Windows Firewall rule for inbound TCP on notus-swap's port.
  </p>
</section>
