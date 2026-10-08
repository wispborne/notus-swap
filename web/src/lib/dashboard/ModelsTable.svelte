<script lang="ts">
  import { duration, modelColor, num, pct, when } from '../format'
  import { modelLabel, modelTitle } from '../modelNames.svelte'
  import type { RunningModel } from '../status.svelte'
  import { modelStats, type DashboardData } from './data'

  // Every model llama-swap knows, with its numbers over the range and a
  // load or unload button.
  let { data, known }: { data: DashboardData; known: RunningModel[] } = $props()

  const stats = $derived(modelStats(data.requests))
  // Known models first (in llama-swap's order), then any others seen in requests.
  const rows = $derived.by(() => {
    const names = [...known.filter((m) => !m.unlisted).map((m) => m.model)]
    for (const m of stats.keys()) if (!names.includes(m)) names.push(m)
    const state = new Map(known.map((m) => [m.model, m.state]))
    return names
      .map((m) => ({ model: m, state: state.get(m) ?? 'unknown', s: stats.get(m) }))
      .sort((a, b) => Number(b.state !== 'stopped') - Number(a.state !== 'stopped') || (b.s?.lastUsed ?? 0) - (a.s?.lastUsed ?? 0))
  })
  // Idle models with no requests in the range are folded into one row, so
  // the models with numbers fit in the widget.
  const quiet = (r: (typeof rows)[number]) => r.state === 'stopped' && !r.s?.requests
  let showQuiet = $state(false)
  const shown = $derived(showQuiet ? rows : rows.filter((r) => !quiet(r)))
  const quietCount = $derived(rows.filter(quiet).length)

  let busy = $state<string | null>(null)
  async function act(model: string, action: 'load' | 'unload') {
    busy = model
    try {
      await fetch(`/notus/api/models/${encodeURIComponent(model)}/${action}`, { method: 'POST' })
    } finally {
      setTimeout(() => (busy = null), 1500)
    }
  }

  function spark(trend: [number, number][]) {
    if (trend.length < 2) return ''
    const w = 100, h = 20
    const t0 = trend[0][0], t1 = trend.at(-1)![0]
    const max = Math.max(...trend.map((p) => p[1]))
    return trend
      .map(([t, v], i) => `${i ? 'L' : 'M'}${(((t - t0) / (t1 - t0 || 1)) * (w - 2) + 1).toFixed(1)},${(h - 2 - (v / max) * (h - 4)).toFixed(1)}`)
      .join('')
  }

  const pill: Record<string, string> = {
    ready: 'bg-primary/15 text-primary',
    starting: 'bg-secondary/12 text-secondary pulse',
    stopping: 'bg-warn/15 text-warn',
  }
  const th = 'px-2 py-1.5 font-medium whitespace-nowrap'
</script>

<div class="h-full overflow-auto">
  <table class="w-full border-collapse text-[12.5px]">
    <thead class="sticky top-0 bg-panel">
      <tr class="border-b border-line text-left text-[11px] text-dim">
        <th class={th}>Model</th><th class={th}>State</th><th class="{th} text-right">Requests</th><th class={th}>Generation speed trend</th>
        <th class="{th} text-right" title="Share of prompt tokens reused from the cache">Cache</th>
        <th class="{th} text-right">Out t/s</th><th class="{th} text-right">In t/s</th><th class="{th} text-right">TTFT p50</th>
        <th class="{th} text-right">J / token</th><th class={th}>Last used</th><th class={th}></th>
      </tr>
    </thead>
    <tbody>
      {#each shown as r (r.model)}
        <tr class="border-b border-line-soft [&>td]:px-2 [&>td]:py-1 [&>td]:whitespace-nowrap">
          <td><span class="mr-1.5 inline-block size-2 rounded-full" style="background:{modelColor(r.model)}"></span><b class="font-semibold" title={modelTitle(r.model)}>{modelLabel(r.model)}</b></td>
          <td><span class="rounded-full px-1.5 text-[11px] font-semibold {pill[r.state] ?? 'text-dim'}">{r.state === 'stopped' ? 'idle' : r.state}</span></td>
          <td class="num text-right">{r.s?.requests ?? 0}</td>
          <td>
            {#if r.s && r.s.speedTrend.length > 1}
              <svg width="100" height="20"><path d={spark(r.s.speedTrend)} fill="none" stroke={modelColor(r.model)} stroke-width="1.4" /></svg>
            {/if}
          </td>
          <td class="num text-right">{pct(r.s?.cacheRate)}</td>
          <td class="num text-right">{num(r.s?.genSpeed, 1)}</td>
          <td class="num text-right">{num(r.s?.promptSpeed)}</td>
          <td class="num text-right">{duration(r.s?.ttftP50)}</td>
          <td class="num text-right">{r.s?.joulesPerToken != null ? r.s.joulesPerToken.toFixed(2) : '–'}</td>
          <td class="text-muted">{r.s?.lastUsed ? when(r.s.lastUsed) : '–'}</td>
          <td class="text-right">
            {#if r.state === 'ready' || r.state === 'starting'}
              <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary disabled:opacity-50" disabled={busy === r.model} onclick={() => act(r.model, 'unload')}>Unload</button>
            {:else if r.state === 'stopped'}
              <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary disabled:opacity-50" disabled={busy === r.model} onclick={() => act(r.model, 'load')}>Load</button>
            {/if}
          </td>
        </tr>
      {:else}
        {#if !quietCount}<tr><td colspan="11" class="px-2 py-4 text-center text-muted">No models yet.</td></tr>{/if}
      {/each}
      {#if quietCount}
        <tr>
          <td colspan="11" class="px-2 py-1">
            <button class="text-xs text-muted hover:text-text" onclick={() => (showQuiet = !showQuiet)}>
              {showQuiet ? 'Hide' : 'Show'} {quietCount} idle {quietCount === 1 ? 'model' : 'models'} with no requests in this range
            </button>
          </td>
        </tr>
      {/if}
    </tbody>
  </table>
</div>
