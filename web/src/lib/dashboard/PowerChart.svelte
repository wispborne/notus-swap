<script lang="ts">
  import { untrack } from 'svelte'
  import type uPlot from 'uplot'
  import { base, C, line, xAxis, yAxis, type Window } from './charts'
  import { align, type DashboardData } from './data'
  import UChart from './UChart.svelte'

  let { data }: { data: DashboardData } = $props()

  const win: Window = { from: 0, to: 0 }
  $effect.pre(() => {
    win.from = data.from
    win.to = data.to
  })

  const a = $derived(align(data.series))
  const shown = $derived(
    [
      ['gpus', 'GPUs', C.power, true],
      ['cpu', 'CPU', C.cpu, false],
      ['system', 'whole system (estimate)', C.system, false],
    ].filter(([src]) => a.bySource.has(src as string)) as [string, string, string, boolean][],
  )
  const key = $derived(shown.map((s) => s[0]).join(','))
  // Rebuilt only when the lines shown change; untrack keeps other reads from rebuilding it.
  const options = $derived.by(() => {
    void key
    return untrack(() => ({
      ...base(win),
      scales: { ...base(win).scales, y: { range: (_u: uPlot, _min: number, max: number) => [0, max > 0 ? max * 1.1 : 1] as [number, number] } },
      axes: [xAxis(), yAxis((v) => `${Math.round(v)}`, 3)],
      series: [{}, ...shown.map(([, label, color, fill]) => line(label, color, fill, label.startsWith('whole') ? { dash: [4, 3] } : {}))],
    })) as Omit<uPlot.Options, 'width' | 'height'>
  })
  const chartData = $derived([a.xs, ...shown.map(([src]) => a.values(src, 'watts'))] as uPlot.AlignedData)
</script>

<div class="flex h-full flex-col">
  <div class="flex h-4 gap-3 text-[11px] text-muted">
    {#each shown as [, label, color]}<span class="flex items-center gap-1"><span class="inline-block h-0.5 w-3" style="background:{color}"></span>{label}</span>{/each}
  </div>
  <div class="min-h-0 flex-1"><UChart {options} data={chartData} /></div>
</div>
