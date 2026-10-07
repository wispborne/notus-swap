<script lang="ts">
  import type uPlot from 'uplot'
  import { base, bars, C, xAxis, yAxis, type Window } from './charts'
  import type { DashboardData } from './data'
  import UChart from './UChart.svelte'

  let { data }: { data: DashboardData } = $props()

  const win: Window = { from: 0, to: 0 }
  $effect.pre(() => {
    win.from = data.from
    win.to = data.to
  })

  const nice = [60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]
  const step = $derived(nice.find((s) => ((data.to - data.from) / 1000) / s <= 90) ?? 86400)
  const label = $derived(step < 3600 ? `${step / 60} min` : step < 86400 ? `${step / 3600} h` : 'day')
  const options = $derived({
    ...base(win),
    scales: { ...base(win).scales, y: { range: (_u: uPlot, _min: number, max: number) => [0, Math.max(1, max * 1.1)] as [number, number] } },
    axes: [xAxis(), yAxis((v) => (Number.isInteger(v) ? String(v) : ''), 2)],
    series: [{}, bars('requests', C.primary)],
  } as Omit<uPlot.Options, 'width' | 'height'>)
  const chartData = $derived.by<uPlot.AlignedData>(() => {
    const first = Math.floor(data.from / 1000 / step) * step
    const n = Math.ceil((data.to / 1000 - first) / step)
    const xs = Array.from({ length: n }, (_, i) => first + i * step)
    const counts = new Array(n).fill(0)
    for (const r of data.requests) {
      const i = Math.floor((r.started_at / 1000 - first) / step)
      if (i >= 0 && i < n) counts[i]++
    }
    return [xs, counts]
  })
</script>

<div class="flex h-full flex-col">
  <div class="h-4 text-[11px] text-muted">Requests started per {label}</div>
  <div class="min-h-0 flex-1"><UChart {options} data={chartData} /></div>
</div>
