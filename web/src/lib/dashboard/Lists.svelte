<script lang="ts">
  import { loadTime, modelColor, when } from '../format'
  import ModelMark from '../ModelMark.svelte'
  import { live } from '../live.svelte'
  import { modelLabel, modelTitle } from '../modelNames.svelte'
  import { qualify } from '../peerModels'
  import { privacy } from '../privacy.svelte'
  import type { DashboardData } from './data'
  import { splitShared } from './shortNames'

  let { kind, data }: { kind: 'inflight' | 'swaps' | 'energy'; data: DashboardData } = $props()

  // Every server's requests in flight, oldest first. Another server's model is
  // named "@<id>:<model>" (peerModels.ts), as in the merged data.
  const flights = $derived(
    [['', live.flights] as const, ...[...live.peers].map(([server, f]) => [server, f] as const)]
      .flatMap(([server, f]) => [...f.values()].map((fl) => ({ server, f: fl, model: qualify(server, fl.start.model) })))
      .filter((x) => privacy.visible(x.model))
      .sort((a, b) => a.f.start.started_at - b.f.start.started_at),
  )
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
  // Names that start alike cut the shared start short, so the end that tells them apart stays in view.
  const jptNames = $derived(splitShared(perToken.map((p) => modelLabel(p.model))))
  const maxJpt = $derived(Math.max(...perToken.map((p) => p.jpt), 0.0001))
  const dot = (m: string) => `background:${modelColor(m)}`
</script>

<div class="h-full overflow-auto text-[12.5px]">
  {#if kind === 'inflight'}
    {#each flights as { server, f, model } (`${server}:${f.start.id}`)}
      <a href="/notus/requests?open={server ? `${server}:${f.start.id}` : f.start.id}" class="block border-t border-line-soft py-1.5 first:border-t-0 hover:bg-hover">
        <div class="flex items-center gap-1.5">
          <ModelMark {model} />
          <span class="font-mono">#{f.start.id}</span>
          <span class="truncate text-muted" title={modelTitle(model)}>{modelLabel(model)}</span>
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
        <ModelMark model={e.model} />
        <span class="truncate" title={modelTitle(e.model)}>{modelLabel(e.model)}</span>
        {#if e.auto}<span class="text-[11px] text-dim" title="notus-swap started this load after the idle timeout">auto</span>{/if}
        <span class="flex-1"></span>
        <span class="num">{e.load_ms != null ? loadTime(e.load_ms) : ''}</span>
      </div>
    {:else}
      <div class="text-muted">No model loads in this range.</div>
    {/each}
  {:else}
    {#each perToken as p}
      {@const [head, tail] = jptNames.get(modelLabel(p.model)) ?? ['', modelLabel(p.model)]}
      <div class="my-1.5 flex items-center gap-2">
        <ModelMark model={p.model} />
        <span class="flex max-w-[60%] min-w-0 flex-none" title={modelTitle(p.model)}
          >{#if head}<span class="min-w-0 truncate whitespace-pre">{head}</span>{/if}<span class="max-w-full truncate whitespace-pre {head ? 'flex-none' : ''}">{tail}</span></span
        >
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
