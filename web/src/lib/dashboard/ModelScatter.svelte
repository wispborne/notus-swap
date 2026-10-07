<script lang="ts">
  import { untrack } from 'svelte'
  import type uPlot from 'uplot'
  import type { Summary } from '../api'
  import { modelColor, when } from '../format'
  import { base, dots, logRange, xAxis, yAxis, zeroToMax, type Window } from './charts'
  import type { DashboardData } from './data'
  import { shortNames } from './shortNames'
  import UChart from './UChart.svelte'

  // One dot per finished request, one colour per model. `log` uses a log
  // scale, for values where a few large ones would flatten the rest. `max`
  // fixes the top of the scale, such as 100 for a percentage. `legend` shows
  // each model's name (shortened), only its dot (the name is the dot's tooltip), or nothing.
  let {
    data,
    value,
    format,
    unit = '',
    log = false,
    max,
    legend = 'dots',
  }: {
    data: DashboardData
    value: (r: Summary) => number | null
    format: (v: number) => string
    unit?: string
    log?: boolean
    max?: number
    legend?: 'names' | 'dots' | 'hidden'
  } = $props()

  const win: Window = { from: 0, to: 0 }
  $effect.pre(() => {
    win.from = data.from
    win.to = data.to
  })

  const pts = $derived(
    data.requests
      .filter((r) => {
        if (r.state !== 'done' || !r.finished_at) return false
        const v = value(r)
        return v != null && (!log || v > 0)
      })
      .sort((a, b) => a.finished_at! - b.finished_at!),
  )
  const models = $derived([...new Set(pts.map((r) => r.model))].sort())
  const short = $derived(shortNames(models))
  const key = $derived(models.join(','))
  // Rebuilt only when the models shown change; untrack keeps other reads from rebuilding it.
  const options = $derived.by(() => {
    void key, log, max
    return untrack(() => buildOptions())
  })
  function buildOptions() {
    const y = log ? { distr: 3, log: 10, range: logRange } : { range: max != null ? ([0, max] as [number, number]) : zeroToMax }
    return {
      ...base(win),
      scales: { ...base(win).scales, y },
      axes: [xAxis(), yAxis(undefined, 3, { log })],
      series: [{}, ...models.map((m) => dots(m, modelColor(m)))],
    } as Omit<uPlot.Options, 'width' | 'height'>
  }
  const chartData = $derived<uPlot.AlignedData>([
    pts.map((r) => r.finished_at! / 1000),
    ...models.map((m) => pts.map((r) => (r.model === m ? value(r) : null))),
  ] as uPlot.AlignedData)

  // The request nearest the cursor, so a dot can be matched to its model
  // without relying on colour.
  let hovered = $state<Summary | null>(null)
  function onCursor(idx: number | null) {
    hovered = idx == null ? null : (pts[idx] ?? null)
  }
</script>

<div class="flex h-full flex-col">
  {#if !models.length}
    <div class="flex-none text-[11px] leading-4 text-dim">No finished requests in this range.</div>
  {:else if legend === 'names'}
    <div class="flex flex-none flex-wrap gap-x-2.5 text-[11px] leading-4 text-muted">
      {#each models as m}<span class="flex items-center gap-1" title={short.get(m) === m ? undefined : m}><span class="inline-block size-2 rounded-full" style="background:{modelColor(m)}"></span>{short.get(m)}</span>{/each}
      {#if log}<span class="text-muted">· log scale</span>{/if}
    </div>
  {/if}
  <div class="relative min-h-0 flex-1">
    <!-- Dots only: drawn over the chart's top right corner, so they take no room of their own. -->
    {#if legend === 'dots' && models.length}
      <div class="absolute top-0.5 right-0 z-[5] flex max-w-[60%] flex-wrap justify-end gap-1.5 rounded border border-line bg-panel/90 px-1.5 py-1">
        {#each models as m}<span class="inline-block size-2 rounded-full" style="background:{modelColor(m)}" title={m}></span>{/each}
      </div>
    {/if}
    {#if hovered}
      <div class="num pointer-events-none absolute top-0 right-0 left-0 z-10 truncate bg-panel/90 text-[11px] text-text">
        #{hovered.id} {hovered.model} · {format(value(hovered)!)}{unit} · {when(hovered.finished_at!)}
      </div>
    {/if}
    <UChart {options} data={chartData} {onCursor} />
  </div>
</div>
