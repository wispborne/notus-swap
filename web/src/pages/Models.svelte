<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import { duration, joules, modelColor, num, pct, when } from '../lib/format'
  import ModelDetail from '../lib/models/ModelDetail.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status, type RunningModel } from '../lib/status.svelte'

  // Every model in llama-swap's config, with its state, notus-swap's numbers
  // for it, and load and unload buttons. Clicking a row opens its log, loads
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
    gen_per_second: number | null
    prompt_per_second: number | null
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
  // pressed twice while llama-swap acts on it. A change also brings new
  // numbers (a load time, for one).
  let busy = $state<Record<string, string>>({})
  let lastState: Record<string, string> = {}
  $effect(() => {
    let changed = false
    for (const m of known) {
      if (m.model in lastState && lastState[m.model] !== m.state) {
        changed = true
        if (untrack(() => busy[m.model])) delete busy[m.model]
      }
      lastState[m.model] = m.state
    }
    if (changed) loadStats()
  })
  let actionError = $state('')
  async function act(model: string, action: 'load' | 'unload') {
    busy[model] = action
    actionError = ''
    try {
      const res = await fetch(`/notus/api/models/${encodeURIComponent(model)}/${action}`, { method: 'POST' })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
    } catch (e) {
      actionError = `Couldn't ${action} ${model}: ${e instanceof Error ? e.message : e}`
      delete busy[model]
    }
    setTimeout(() => delete busy[model], 30_000) // give up waiting for a state change
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

  let open = $state<string | null>(null)
  const toggle = (m: string) => (open = open === m ? null : m)

  const dot: Record<string, string> = { ready: 'bg-primary', starting: 'bg-secondary pulse', stopping: 'bg-warn pulse' }
  const th = 'px-2 py-1.5 font-medium whitespace-nowrap'
  const btn = 'rounded border border-line bg-panel2 px-2 text-xs hover:border-primary disabled:opacity-50 disabled:hover:border-line'
</script>

{#snippet numbers(s: Stat | undefined)}
  <td class="num text-right">{s?.requests ?? 0}{#if s?.failed}{' '}<span class="text-err" title="failed">({s.failed})</span>{/if}</td>
  <td class="num text-right" title={s?.cached_tokens ? `${s.cached_tokens.toLocaleString('en-US')} of ${s.prompt_tokens.toLocaleString('en-US')} prompt tokens` : ''}>{pct(s?.cache_rate)}</td>
  <td class="num text-right">{num(s?.gen_per_second, 1)}</td>
  <td class="num text-right">{num(s?.prompt_per_second)}</td>
  <td class="num text-right" title={s?.loads ? `${s.loads} timed loads` : ''}>{duration(s?.load_ms)}</td>
  <td class="num text-right">{s?.energy_j != null ? joules(s.energy_j) : '–'}</td>
  <td class="text-muted">{s?.last_used ? when(s.last_used) : '–'}</td>
{/snippet}

{#snippet head()}
  <thead>
    <tr class="border-b border-line text-left text-[11px] text-dim">
      <th class={th}>Model</th><th class={th}>State</th><th class="{th} text-right">Context</th>
      <th class="{th} text-right">Requests</th>
      <th class="{th} text-right" title="Share of prompt tokens reused from the cache, over all requests">Cache</th>
      <th class="{th} text-right" title="Average generation speed over the last 50 requests that were at most half cached">Out t/s</th>
      <th class="{th} text-right" title="Average prompt speed over the last 50 requests that were at most half cached">In t/s</th>
      <th class="{th} text-right" title="Average time from starting to ready">Load time</th>
      <th class="{th} text-right" title="Total GPU energy for this model">Energy</th>
      <th class={th}>Last used</th><th class={th}></th>
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
  <button class={btn} disabled={!loadedCount || unloadingAll} onclick={unloadAll}>{unloadingAll ? 'Unloading…' : 'Unload all'}</button>
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
    <tbody>
      {#each listed as m (m.model)}
        {@const s = stats.get(m.model)}
        <tr class="cursor-pointer border-b border-line-soft hover:bg-hover [&>td]:px-2 [&>td]:py-1 [&>td]:whitespace-nowrap {open === m.model ? 'bg-hover' : ''}" onclick={() => toggle(m.model)}>
          <td class="max-w-[360px]">
            <div class="flex items-center gap-1.5">
              <span class="text-dim">{open === m.model ? '▾' : '▸'}</span>
              <span class="inline-block size-2 flex-none rounded-full" style="background:{modelColor(m.model)}"></span>
              <b class="truncate font-semibold">{m.name || m.model}</b>
              {#if m.unlisted}<span class="rounded bg-line px-1 text-[10px] text-muted uppercase">unlisted</span>{/if}
            </div>
            {#if m.name && m.name !== m.model}<div class="truncate pl-7 text-[11px] text-dim">{m.model}</div>{/if}
          </td>
          <td>
            <span class="inline-flex items-center gap-1.5">
              <span class="relative top-px size-2 rounded-full {dot[m.state] ?? 'bg-line'}"></span>
              <span class={m.state === 'stopped' ? 'text-dim' : ''}>{m.state === 'stopped' ? 'not loaded' : m.state}</span>
            </span>
          </td>
          <td class="num text-right text-muted">{m.context_length ? m.context_length.toLocaleString() : '–'}</td>
          {@render numbers(s)}
          <td class="text-right">
            {#if m.state === 'ready' || m.state === 'starting'}
              <button class={btn} disabled={!!busy[m.model]} onclick={(e) => (e.stopPropagation(), act(m.model, 'unload'))}>{busy[m.model] === 'unload' ? 'Unloading…' : 'Unload'}</button>
            {:else if m.state === 'stopped'}
              <button class={btn} disabled={!!busy[m.model] || !up} onclick={(e) => (e.stopPropagation(), act(m.model, 'load'))}>{busy[m.model] === 'load' ? 'Loading…' : 'Load'}</button>
            {/if}
          </td>
        </tr>
        {#if open === m.model}
          <tr><td colspan="10" class="p-0"><ModelDetail model={m.model} known={m} /></td></tr>
        {/if}
      {:else}
        <tr><td colspan="10" class="px-2 py-4 text-center text-muted">{up ? 'No models in the config.' : 'Waiting for llama-swap.'}</td></tr>
      {/each}
    </tbody>
  </table>
</div>

{#if gone.length}
  <h2 class="mt-5 mb-1 text-xs font-semibold tracking-wider text-dim uppercase">No longer in llama-swap's config ({gone.length})</h2>
  <div class="overflow-x-auto rounded-lg border border-line bg-panel">
    <table class="w-full border-collapse text-[12.5px]">
      {@render head()}
      <tbody>
        {#each gone as s (s.model)}
          <tr class="cursor-pointer border-b border-line-soft text-muted hover:bg-hover [&>td]:px-2 [&>td]:py-1 [&>td]:whitespace-nowrap" onclick={() => toggle(s.model)}>
            <td class="max-w-[360px]">
              <div class="flex items-center gap-1.5">
                <span class="text-dim">{open === s.model ? '▾' : '▸'}</span>
                <span class="inline-block size-2 flex-none rounded-full" style="background:{modelColor(s.model)}"></span>
                <span class="truncate">{s.model}</span>
              </div>
            </td>
            <td class="text-dim">removed</td>
            <td></td>
            {@render numbers(s)}
            <td></td>
          </tr>
          {#if open === s.model}
            <tr><td colspan="10" class="p-0"><ModelDetail model={s.model} /></td></tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
{/if}
