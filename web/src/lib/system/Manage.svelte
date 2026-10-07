<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { bytes, when } from '../format'
  import LlamaUpdates from './LlamaUpdates.svelte'
  import Changelog from './Changelog.svelte'

  interface Release {
    tag: string
    notes: string
    published: string
  }
  interface Info {
    version: string
    update_configured: boolean
    update_source?: string
    releases: Release[]
    changelog: Release[]
    current_known: boolean
    release_error?: string
    has_previous: boolean
    pending?: { from: string; to: string; attempts: number }
    last_result?: { from: string; to: string; outcome: string; reason?: string; at: string }
    retention: { days: number; gb: number }
    llama_swap_unit: string
    database?: { path: string; file_bytes: number; wal_bytes: number; free_bytes: number; bodies_bytes: number; requests: number; with_bodies: number }
    restarting?: Restarting
  }
  interface Restarting {
    reason: string
    since: string
    in_flight: number
  }

  let info = $state<Info | null>(null)
  let error = $state('')
  let busy = $state('')
  let message = $state<{ kind: 'ok' | 'err' | 'wait'; text: string } | null>(null)
  // Set while the old process waits for requests to finish before restarting.
  let waiting = $state<Restarting | null>(null)

  async function load() {
    try {
      const res = await fetch('/notus/api/system')
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
      info = await res.json()
      days = info!.retention.days
      gb = info!.retention.gb
      // The page was opened (or reloaded) while a restart was waiting.
      if (info!.restarting && !busy) {
        busy = 'restart-self'
        message = { kind: 'wait', text: 'notus-swap will restart once requests finish…' }
        waitForRestart()
      }
    } catch (e) {
      error = String(e)
    }
  }
  onMount(load)

  let serviceState = $state('')
  let serviceError = $state('')
  let serviceTimer: ReturnType<typeof setInterval>
  let destroyed = false
  async function loadService() {
    try {
      const res = await fetch('/notus/api/service/llama-swap', { cache: 'no-store' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      const body = await res.json()
      if (destroyed) return
      serviceState = body.state
      serviceError = ''
    } catch (e) {
      if (destroyed) return
      serviceState = ''
      serviceError = String(e)
    }
  }
  onMount(() => {
    loadService()
    serviceTimer = setInterval(loadService, 5000)
  })
  onDestroy(() => {
    destroyed = true
    clearInterval(serviceTimer)
  })
  const serviceRunning = $derived(['active', 'reloading', 'activating'].includes(serviceState))
  const serviceChanging = $derived(['activating', 'deactivating', 'reloading'].includes(serviceState))

  async function toggleLlama() {
    const action = serviceRunning ? 'stop' : 'start'
    if (action === 'stop' && !confirm('Stop llama-swap? Active requests will stop; loaded models will unload.')) return
    busy = `${action}-llama`
    message = { kind: 'wait', text: action === 'stop' ? 'Stopping llama-swap…' : 'Starting llama-swap…' }
    try {
      await post(`/notus/api/${action}/llama-swap`)
      message = { kind: 'ok', text: action === 'stop' ? 'llama-swap stopped.' : 'llama-swap started.' }
    } catch (e) {
      message = { kind: 'err', text: `${action === 'stop' ? 'Stop' : 'Start'} failed: ${e instanceof Error ? e.message : e}` }
    } finally {
      await loadService()
      busy = ''
    }
  }

  // Releases newer than the running one (the list ends at the running one).
  const newer = $derived(info ? info.releases.filter((r) => r.tag !== info!.version) : [])
  let showChangelog = $state(false)

  const upToDate = $derived(!!info && info.current_known && newer.length === 0)

  /**
   * After asking for a restart, wait for the new process to answer, then
   * reload. The old process keeps answering, with "restarting" set, until no
   * requests are in flight. The 90 seconds count from when it stops.
   */
  async function waitForRestart(expect?: string) {
    cancelled = false
    let until = Date.now() + 90_000
    while (Date.now() < until) {
      try {
        const h = await (await fetch('/notus/api/health', { cache: 'no-store' })).json()
        if (cancelled) return
        if (h.restarting) {
          waiting = h.restarting
          until = Date.now() + 90_000
        } else if (!expect || h.version === expect) {
          location.reload()
          return
        }
      } catch {
        waiting = null
      }
      await new Promise((r) => setTimeout(r, 1000))
      if (cancelled) return
    }
    busy = ''
    waiting = null
    message = { kind: 'err', text: "notus-swap didn't come back within 90 seconds. Check its log: journalctl -u notus-swap.service -n 50" }
  }

  async function restartNow() {
    if (!confirm('Restart now? Answers still streaming get 10 seconds to finish, then they are cut off.')) return
    try {
      await post('/notus/api/restart/notus-swap/now')
      waiting = null
    } catch (e) {
      message = { kind: 'err', text: `Restart failed: ${e instanceof Error ? e.message : e}` }
    }
  }

  let cancelled = false

  async function cancelRestart() {
    const reason = waiting?.reason ?? ''
    try {
      await post('/notus/api/restart/notus-swap/cancel')
    } catch (e) {
      message = { kind: 'err', text: `Couldn't cancel restart: ${e instanceof Error ? e.message : e}` }
      return
    }
    cancelled = true
    busy = ''
    waiting = null
    message = {
      kind: 'ok',
      text: reason.startsWith('update')
        ? 'Update cancelled and download removed. The current version is still running.'
        : reason === 'roll back'
          ? 'Roll back cancelled. The current version is still running.'
          : 'Restart cancelled.',
    }
    load() // Cancelling an update changes the available rollback version.
  }

  async function post(url: string, body?: unknown) {
    const res = await fetch(url, { method: 'POST', body: body ? JSON.stringify(body) : undefined })
    if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    return res.status === 204 || res.status === 202 ? null : res.json()
  }

  async function update() {
    busy = 'update'
    message = { kind: 'wait', text: 'Downloading and checking the new version…' }
    try {
      const r = await post('/notus/api/update', { tag: newer[0]?.tag })
      message = { kind: 'wait', text: `Installed ${r.installing}. Restarting notus-swap…` }
      await waitForRestart(r.installing)
    } catch (e) {
      busy = ''
      message = { kind: 'err', text: `Update failed: ${e instanceof Error ? e.message : e}. Nothing was changed.` }
    }
  }

  async function rollback() {
    if (!confirm('Go back to the previous version of notus-swap?')) return
    busy = 'rollback'
    message = { kind: 'wait', text: 'Restoring the previous version and restarting…' }
    try {
      await post('/notus/api/update/rollback')
      await waitForRestart()
    } catch (e) {
      busy = ''
      message = { kind: 'err', text: `Roll back failed: ${e instanceof Error ? e.message : e}` }
    }
  }

  async function restartSelf() {
    busy = 'restart-self'
    message = { kind: 'wait', text: 'notus-swap is restarting…' }
    try {
      await post('/notus/api/restart/notus-swap')
      await waitForRestart()
    } catch (e) {
      busy = ''
      message = { kind: 'err', text: String(e) }
    }
  }

  async function restartLlama() {
    if (!confirm('Restart llama-swap? Active requests will stop; loaded models will unload.')) return
    busy = 'restart-llama'
    message = { kind: 'wait', text: 'Restarting llama-swap…' }
    try {
      await post('/notus/api/restart/llama-swap')
      await loadService()
      message = { kind: 'ok', text: 'llama-swap restarted.' }
    } catch (e) {
      message = { kind: 'err', text: `Restart failed: ${e instanceof Error ? e.message : e}` }
    }
    busy = ''
  }

  // ---- Retention ----
  let days = $state(90)
  let gb = $state(20)
  let savedRetention = $state(false)
  const retentionDirty = $derived(!!info && (days !== info.retention.days || gb !== info.retention.gb))
  async function saveRetention() {
    const value = { days: Math.max(1, Math.round(days)), gb: Math.max(1, Math.round(gb)) }
    await fetch('/notus/api/settings/retention', { method: 'PUT', body: JSON.stringify(value) })
    info!.retention = value
    savedRetention = true
    setTimeout(() => {
      savedRetention = false
      load() // pruning has run by now, so the sizes have changed
    }, 2000)
  }

  /** 0.4% rather than 0%, so a little use doesn't read as none. */
  function percent(f: number) {
    const p = f * 100
    if (p > 0 && p < 0.1) return 'under 0.1%'
    return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`
  }

  const msgColor ={ ok: 'border-l-primary', err: 'border-l-err', wait: 'border-l-secondary' }
  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50 disabled:hover:border-line'
  const section = 'mb-4 rounded-lg border border-line bg-panel p-4'
</script>

{#if error}<div class="mb-3 text-err">Could not load system information: {error}</div>{/if}

{#if message}
  <div class="mb-3 rounded-md border border-l-3 border-line bg-panel2 px-3 py-2 text-[12.5px] {msgColor[message.kind]}">
    {#if message.kind === 'wait'}<span class="pulse mr-1 text-secondary">●</span>{/if}{message.text}
    {#if waiting && waiting.in_flight > 0}
      <div class="mt-1.5 flex flex-wrap items-center gap-2 text-muted">
        Waiting for {waiting.in_flight} {waiting.in_flight === 1 ? 'request' : 'requests'} to finish, so no answer is cut off.
        <button class={btn} onclick={restartNow}>Restart now</button>
        <button class={btn} onclick={cancelRestart}>Cancel</button>
      </div>
    {/if}
  </div>
{/if}

<section class={section}>
  <h2 class="text-sm font-semibold">notus-swap version</h2>
  {#if info}
    <p class="mt-1 text-[12.5px] text-muted">
      Running <b class="font-mono text-text">{info.version}</b>.
      {#if info.update_source && !info.release_error}Releases come from {info.update_source}.{/if}
      {#if !info.update_configured}
        Updates are off, because NOTUS_GITHUB_REPO is empty in notus-swap's env file.
      {:else if info.release_error}
        <span class="text-err">Couldn't fetch releases from {info.update_source}: {info.release_error}</span>
      {:else if upToDate}
        This is the newest release.
      {:else if newer.length}
        {newer.length === 1 && info.current_known ? 'A newer release is available:' : 'Newer releases:'}
      {/if}
    </p>

    {#if info.pending}
      <p class="mt-2 text-[12.5px] text-secondary">If {info.pending.to} runs for 30 seconds, the update is kept.</p>
    {/if}
    {#if info.last_result}
      <p class="mt-2 text-[12.5px] {info.last_result.outcome === 'installed' ? 'text-muted' : 'text-warn'}">
        Last update ({when(Date.parse(info.last_result.at))}):
        {#if info.last_result.outcome === 'installed'}
          Installed {info.last_result.to}.
        {:else if info.last_result.outcome === 'rolled back'}
          Restored {info.last_result.from} after {info.last_result.to} failed{info.last_result.reason ? `: ${info.last_result.reason}` : ''}.
        {:else}
          Rolled back manually.
        {/if}
      </p>
    {/if}

    {#if newer.length}
      <ul class="mt-2 max-h-56 divide-y divide-line-soft overflow-auto rounded-md border border-line">
        {#each newer as r, i}
          <li class="px-3 py-1.5 text-[12.5px]">
            <div class="flex gap-2"><span class="font-mono {i === 0 ? 'text-primary' : 'text-muted'}">{r.tag}</span><span class="flex-1"></span><span class="text-dim">{when(Date.parse(r.published))}</span></div>
            {#if r.notes}<div class="mt-0.5 whitespace-pre-wrap text-muted">{r.notes.split('\n')[0]}</div>{/if}
          </li>
        {/each}
      </ul>
      {#if !info.current_known}
        <p class="mt-1 text-xs text-dim">The running version isn't among the recent releases (a local build, or an old one), so only the newest few are listed.</p>
      {/if}
    {/if}

    <div class="mt-3 flex flex-wrap gap-2">
      <button class="rounded-md border border-primary bg-primary px-3 py-1 font-semibold text-sunken disabled:opacity-40" disabled={!!busy || !newer.length} onclick={update}>
        {busy === 'update' ? 'Updating…' : newer.length ? `Update to ${newer[0].tag}` : 'Update'}
      </button>
      <button class={btn} disabled={!!busy || !info.has_previous} onclick={rollback} title={info.has_previous ? 'Swap the previous binary back in' : 'No previous version kept yet'}>
        Roll back to the previous version
      </button>
      <button class={btn} disabled={!!busy} onclick={load}>Check for updates</button>
      <button class={btn} disabled={!info.update_configured} onclick={() => (showChangelog = !showChangelog)}>{showChangelog ? 'Hide changelog' : 'Changelog'}</button>
    </div>
    {#if showChangelog}
      <Changelog entries={info.changelog} error={info.release_error} markdown={false} isNew={(tag) => newer.some((r) => r.tag === tag)} />
    {/if}
    <p class="mt-2 text-xs text-dim">
      Updates keep the current binary. If the new version stops before 30 seconds twice, notus-swap restores it automatically.
    </p>
  {:else if !error}
    <p class="mt-1 text-muted">Loading…</p>
  {/if}
</section>

<LlamaUpdates />

<section class={section}>
  <h2 class="text-sm font-semibold">Services</h2>
  <div class="mt-2 flex flex-wrap gap-2">
    <button class={btn} disabled={!!busy || !serviceState || serviceChanging} onclick={toggleLlama}>
      {busy === 'start-llama' ? 'Starting llama-swap…' : busy === 'stop-llama' ? 'Stopping llama-swap…' : serviceRunning ? 'Stop llama-swap' : 'Start llama-swap'}
    </button>
    <button class={btn} disabled={!!busy} onclick={restartLlama}>{busy === 'restart-llama' ? 'Restarting llama-swap…' : 'Restart llama-swap'}</button>
    <button class={btn} disabled={!!busy} onclick={restartSelf}>{busy === 'restart-self' ? 'Restarting…' : 'Restart notus-swap'}</button>
  </div>
  <p class="mt-2 text-xs text-dim">
    {#if serviceState}llama-swap: {serviceState}.{/if}
    Service controls use the polkit rule in the install guide.
  </p>
  {#if serviceError}<p class="mt-2 text-xs text-err">Could not read llama-swap's service state: {serviceError}</p>{/if}
</section>

<section class={section}>
  <h2 class="text-sm font-semibold">Keeping request and response text</h2>
  <p class="mt-1 text-[12.5px] text-muted">
    Request and response text is deleted oldest first to meet the retention limits. Timing, token counts and energy are kept.
  </p>
  <div class="mt-3 flex flex-wrap items-center gap-3 text-[12.5px]">
    <label class="flex items-center gap-2">Keep for
      <input type="number" min="1" bind:value={days} class="w-20 rounded-md border border-line bg-panel2 px-2 py-1 outline-none focus:border-primary" /> days
    </label>
    <label class="flex items-center gap-2">and at most
      <input type="number" min="1" bind:value={gb} class="w-20 rounded-md border border-line bg-panel2 px-2 py-1 outline-none focus:border-primary" /> GB
    </label>
    <button class={btn} disabled={!retentionDirty} onclick={saveRetention}>Save</button>
    {#if savedRetention}<span class="text-primary">Saved. Old bodies are being cleared now.</span>{/if}
  </div>

  {#if info?.database}
    {@const d = info.database}
    <h3 class="mt-4 mb-1.5 text-xs font-semibold tracking-wider text-dim uppercase">Database size</h3>
    <table class="text-[12.5px] [&_td]:py-0.5 [&_td]:pr-4 [&_td]:align-top">
      <tbody>
        <tr>
          <td class="whitespace-nowrap text-muted">On disk</td>
          <td class="num text-right font-semibold whitespace-nowrap">{bytes(d.file_bytes + d.wal_bytes)}</td>
          <td class="text-dim">
            <code class="break-all">{d.path}</code>{#if d.wal_bytes}, including {bytes(d.wal_bytes)} in the write-ahead log (the <code>-wal</code> file){/if}
          </td>
        </tr>
        <tr>
          <td class="whitespace-nowrap text-muted">Request and response text</td>
          <td class="num text-right whitespace-nowrap">{bytes(d.bodies_bytes)}</td>
          <td class="text-dim">
            {percent(d.bodies_bytes / (info.retention.gb * 1024 ** 3))} of the {info.retention.gb} GB limit.
            {d.with_bodies.toLocaleString()} of {d.requests.toLocaleString()} requests still have their text.
          </td>
        </tr>
        <tr>
          <td class="whitespace-nowrap text-muted">Free space inside the file</td>
          <td class="num text-right whitespace-nowrap">{bytes(d.free_bytes)}</td>
          <td class="text-dim">Space freed by deleting request text. New requests reuse it, but the file doesn't shrink.</td>
        </tr>
      </tbody>
    </table>
  {/if}
</section>
