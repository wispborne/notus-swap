<script lang="ts">
  import { duration, modelColor, when } from '../format'
  import { live } from '../live.svelte'
  import { privacy } from '../privacy.svelte'
  import type { DashboardData } from './data'

  let { kind, data }: { kind: 'inflight' | 'swaps' | 'energy'; data: DashboardData } = $props()

  const flights = $derived([...live.flights.values()].filter((f) => privacy.visible(f.start.model)).sort((a, b) => a.start.id - b.start.id))
  const swaps = $derived(data.model_events.filter((e) => e.to === 'ready').reverse())

  const perToken = $derived.by(() => {
    const m = new Map<string, { j: number; tok: number }>()
    for (const r of data.requests) {
      if (r.energy_j == null || !r.completion_tokens || r.state !== 'done') continue
      const e = m.get(r.model) ?? { j: 0, tok: 0 }
      e.j += r.energy_j
      e.tok += r.completion_tokens
      m.set(r.model, e)
    }
    return [...m].map(([model, e]) => ({ model, jpt: e.j / e.tok })).sort((a, b) => a.jpt - b.jpt)
  })
  const maxJpt = $derived(Math.max(...perToken.map((p) => p.jpt), 0.0001))
  const dot = (m: string) => `background:${modelColor(m)}`
</script>

<div class="h-full overflow-auto text-[12.5px]">
  {#if kind === 'inflight'}
    {#each flights as f (f.start.id)}
      <a href="/notus/requests?open={f.start.id}" class="block border-t border-line-soft py-1.5 first:border-t-0 hover:bg-hover">
        <div class="flex items-center gap-1.5">
          <span class="inline-block size-2 rounded-full" style={dot(f.start.model)}></span>
          <span class="font-mono">#{f.start.id}</span>
          <span class="truncate text-muted">{f.start.model}</span>
          <span class="flex-1"></span>
          <span class="num">{f.chunks} tok</span>
          <span class="rounded-full bg-secondary/12 px-1.5 text-[11px] font-semibold text-secondary pulse">{f.firstTokenAt ? 'streaming' : 'waiting'}</span>
        </div>
        <div class="mt-0.5 line-clamp-2 text-xs text-muted">{(f.content || f.reasoning).slice(-200) || f.start.preview}</div>
      </a>
    {:else}
      <div class="text-muted">No active requests.</div>
    {/each}
  {:else if kind === 'swaps'}
    {#each swaps.slice(0, 30) as e}
      <div class="flex items-center gap-2 py-0.5">
        <span class="num text-dim">{when(e.at)}</span>
        <span class="inline-block size-2 rounded-full" style={dot(e.model)}></span>
        <span class="truncate">{e.model}</span>
        {#if e.auto}<span class="text-[11px] text-dim" title="notus-swap started this load after the idle timeout">auto</span>{/if}
        <span class="flex-1"></span>
        <span class="num">{e.load_ms != null ? duration(e.load_ms) : ''}</span>
      </div>
    {:else}
      <div class="text-muted">No model loads in this range.</div>
    {/each}
  {:else}
    {#each perToken as p}
      <div class="my-1.5 flex items-center gap-2">
        <span class="inline-block size-2 flex-none rounded-full" style={dot(p.model)}></span>
        <span class="max-w-[60%] flex-none truncate" title={p.model}>{p.model}</span>
        <div class="h-2.5 min-w-10 flex-1 overflow-hidden rounded bg-panel2">
          <div class="h-full rounded" style="width:{(p.jpt / maxJpt) * 100}%;{dot(p.model)}"></div>
        </div>
        <span class="num flex-none text-right">{p.jpt.toFixed(2)} J/tok</span>
      </div>
    {:else}
      <div class="text-muted">No request energy data in this range.</div>
    {/each}
    {#if perToken.length}<div class="mt-1 text-[11px] text-muted">GPU joules per generated token</div>{/if}
  {/if}
</div>
