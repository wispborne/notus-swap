<script lang="ts">
  import { untrack } from 'svelte'
  import type uPlot from 'uplot'
  import { modelColor, when } from '../format'
  import { modelLabel, modelTitle } from '../modelNames.svelte'
  import { base, C, cardColors, dots, line, xAxis, yAxis, type Window } from './charts'
  import { align, type DashboardData } from './data'
  import UChart from './UChart.svelte'

  // Several charts stacked on one time axis. Hovering any of them moves the
  // cursor on all, and the line at the top reads out every value at that time.
  let { data }: { data: DashboardData } = $props()

  const win: Window = { from: 0, to: 0 }
  $effect.pre(() => {
    win.from = data.from
    win.to = data.to
  })

  const a = $derived(align(data.series))
  const cards = $derived(data.cards.filter((c) => !c.integrated))
  const cardKey = $derived(cards.map((c) => c.source).join(','))
  const hasSystem = $derived(a.bySource.has('system'))
  const hasCPU = $derived(a.bySource.has('cpu'))

  // Lanes: one per model seen in the range.
  const models = $derived(
    [...new Set([...data.requests.map((r) => r.model), ...data.model_events.map((e) => e.model)].filter(Boolean))].sort(),
  )
  const modelKey = $derived(models.join(','))

  // Lane labels have room for about 13 characters, and show the full name
  // on hover. Use each name's first part, such as "gemma4". When other names
  // share it, leave out the parts they all share: "dev-swift" and
  // "dev-swift-vision" for "qwen3.8-27b-dev-swift" and
  // "qwen3.8-27b-dev-swift-vision". A name that is all shared parts uses its
  // last part. If two labels still match, both use the full name.
  function laneLabels(names: string[]): string[] {
    const parts = names.map((n) => n.split(/[-:]/))
    const out = parts.map((p) => {
      const same = parts.filter((q) => q[0] === p[0])
      if (same.length === 1) return p[0]
      let k = 0
      while (same.every((q) => q[k] != null && q[k] === p[k])) k++
      return p.slice(k).join('-') || p[p.length - 1]
    })
    return out.map((l, i) => (out.indexOf(l) !== out.lastIndexOf(l) ? names[i] : l))
  }
  const labels = $derived(laneLabels(models.map((m) => modelLabel(m))))

  // Model loads as [model, start ms, end ms].
  const loads = $derived.by(() => {
    const out: [string, number, number][] = []
    const open = new Map<string, number>()
    for (const e of data.model_events) {
      if (e.to === 'starting') open.set(e.model, e.at)
      else if (e.to === 'ready' && open.has(e.model)) {
        out.push([e.model, open.get(e.model)!, e.at])
        open.delete(e.model)
      }
    }
    for (const [m, at] of open) out.push([m, at, data.to])
    return out
  })

  // Room above each strip's plot for its label, so lines and bars don't
  // draw over it.
  const LABEL_H = 17
  // Height of one model lane, and the y axis width shared by every strip so
  // they line up. Wider than other charts to fit the lane labels.
  const LANE_H = 14
  const AXIS = 84

  // Each chart's options are rebuilt only when the keys they read with `void` change. untrack keeps
  // other reads, such as arrays that are new on every refresh, from rebuilding the charts.
  const laneOpts = $derived.by((): Omit<uPlot.Options, 'width' | 'height'> => {
    void modelKey
    return untrack(() => laneOptions(models))
  })
  function laneOptions(names: string[]): Omit<uPlot.Options, 'width' | 'height'> {
    return {
      ...base(win, 'tl'),
      padding: [LABEL_H, 8, 0, 0],
      scales: { ...base(win).scales, y: { range: [0, Math.max(names.length, 1)] } },
      axes: [
        xAxis(false),
        // The labels are HTML, drawn over this axis, so each can show its full name on hover.
        { ...yAxis(), size: AXIS, grid: { show: false }, splits: () => [], values: () => [] },
      ],
      series: [{}, { label: 'lanes', show: false }],
      hooks: {
        draw: [
          (u: uPlot) => {
            const ctx = u.ctx
            // The first model is the top lane.
            const laneOf = new Map(names.map((m, i) => [m, names.length - 1 - i]))
            const y0 = (i: number) => u.valToPos(i + 1, 'y', true)
            const h = (u.valToPos(0, 'y', true) - u.valToPos(1, 'y', true)) * 0.7
            const pad = (u.valToPos(0, 'y', true) - u.valToPos(1, 'y', true)) * 0.15
            const x = (ms: number) => u.valToPos(ms / 1000, 'x', true)
            const rect = (lane: number, from: number, to: number, color: string, alpha: number) => {
              const x0 = x(from), x1 = Math.max(x(to), x0 + 2)
              ctx.globalAlpha = alpha
              ctx.fillStyle = color
              ctx.fillRect(x0, y0(lane) + pad, x1 - x0, h)
            }
            ctx.save()
            // When zoomed, bars outside the window would draw over the lane labels.
            ctx.beginPath()
            ctx.rect(u.bbox.left, u.bbox.top, u.bbox.width, u.bbox.height)
            ctx.clip()
            for (const [m, from, to] of loads) {
              const lane = laneOf.get(m)
              if (lane != null) rect(lane, from, to, modelColor(m), 0.28)
            }
            for (const r of dataRef.requests) {
              const lane = laneOf.get(r.model)
              if (lane == null) continue
              const end = r.finished_at ?? dataRef.to
              const first = r.first_token_at ?? end
              rect(lane, r.started_at, first, modelColor(r.model), 0.45)
              if (r.first_token_at) rect(lane, first, end, modelColor(r.model), 1)
            }
            ctx.restore()
          },
        ],
      },
    }
  }
  // The draw hook reads the newest requests without rebuilding the chart.
  let dataRef = { requests: [] as DashboardData['requests'], to: 0 }
  $effect.pre(() => {
    dataRef = { requests: data.requests, to: data.to }
  })
  const laneData = $derived<uPlot.AlignedData>([[data.from / 1000, data.to / 1000], [null, null]])

  function strip(series: uPlot.Series[], label: (v: number) => string, showX: boolean, yRange?: uPlot.Scale['range']) {
    return {
      ...base(win, 'tl'),
      padding: [LABEL_H, 8, 0, 0],
      scales: { ...base(win).scales, y: yRange ? { range: yRange } : { range: (_u: uPlot, _min: number, max: number) => [0, max > 0 ? max * 1.1 : 1] as [number, number] } },
      axes: [xAxis(showX), { ...yAxis(label, 2, { top: 0.85 }), size: AXIS }],
      series: [{}, ...series],
    } as Omit<uPlot.Options, 'width' | 'height'>
  }
  const cardLabel = (i: number) => (cards.length > 1 ? cards[i].card : 'GPU')

  const powerOpts = $derived.by(() => {
    void cardKey, hasSystem, hasCPU
    return untrack(() => powerOptions())
  })
  function powerOptions() {
    const s: uPlot.Series[] = [line('GPUs', C.power, true)]
    if (cards.length > 1) cards.forEach((c, i) => s.push(line(c.card, cardColors[(i + 1) % cardColors.length])))
    if (hasCPU) s.push(line('CPU', C.cpu))
    if (hasSystem) s.push(line('system', C.system, false, { dash: [4, 3] }))
    return strip(s, (v) => `${Math.round(v)}`, false)
  }
  const powerData = $derived.by<uPlot.AlignedData>(() => {
    const d: (number | null)[][] = [a.values('gpus', 'watts')]
    if (cards.length > 1) cards.forEach((c) => d.push(a.values(c.source, 'watts')))
    if (hasCPU) d.push(a.values('cpu', 'watts'))
    if (hasSystem) d.push(a.values('system', 'watts'))
    return [a.xs, ...d] as uPlot.AlignedData
  })

  const perCard = (field: 'busy' | 'vram_used' | 'temp_c', scale = 1) =>
    [a.xs, ...cards.map((c) => a.values(c.source, field).map((v) => (v == null ? null : v * scale)))] as uPlot.AlignedData
  const cardSeries = (color: string, fill: boolean) =>
    cards.map((c, i) => line(cardLabel(i), i === 0 ? color : cardColors[(i + 1) % cardColors.length], fill && i === 0))

  const vramMax = $derived(Math.max(1, ...cards.map((c) => Math.max(...a.values(c.source, 'vram_total').map((v) => v ?? 0)))) / 1024 ** 3)
  const utilOpts = $derived.by(() => (void cardKey, untrack(() => strip(cardSeries(C.util, true), (v) => `${Math.round(v)}`, false, [0, 100]))))
  // A function, so the top follows the cards' VRAM without rebuilding the chart.
  const vramTop = (): [number, number] => [0, Math.ceil(vramMax)]
  const vramOpts = $derived.by(() => (void cardKey, untrack(() => strip(cardSeries(C.vram, true), (v) => v.toFixed(0), false, vramTop))))
  const tempOpts = $derived.by(() => (void cardKey, untrack(() => strip(cardSeries(C.temp, false), (v) => `${Math.round(v)}`, false, [0, 110]))))
  const utilData = $derived(perCard('busy'))
  const vramData = $derived(perCard('vram_used', 1 / 1024 ** 3))
  const tempData = $derived(perCard('temp_c'))

  // Generation speed: one dot per finished request, one series per model.
  const speedPoints = $derived(data.requests.filter((r) => r.predicted_per_second && r.finished_at))
  const speedOpts = $derived.by(() => (void modelKey, untrack(() => strip(models.map((m) => dots(m, modelColor(m))), (v) => `${Math.round(v)}`, true))))
  const speedData = $derived.by<uPlot.AlignedData>(() => {
    const pts = [...speedPoints].sort((x, y) => x.finished_at! - y.finished_at!)
    const xs = pts.map((r) => r.finished_at! / 1000)
    return [xs, ...models.map((m) => pts.map((r) => (r.model === m ? r.predicted_per_second : null)))] as uPlot.AlignedData
  })

  // Readout at the cursor.
  let cursorT = $state<number | null>(null)
  function onCursor(_idx: number | null, u: uPlot) {
    const left = u.cursor.left
    cursorT = left == null || left < 0 ? null : u.posToVal(left, 'x')
  }
  const readout = $derived.by(() => {
    if (cursorT == null || !a.xs.length) return null
    const t = cursorT
    let i = 0
    let best = Infinity
    a.xs.forEach((x, j) => {
      const d = Math.abs(x - t)
      if (d < best) (best = d), (i = j)
    })
    const v = (src: string, f: 'watts' | 'busy' | 'vram_used' | 'temp_c') => a.values(src, f)[i]
    const ms = t * 1000
    const active = data.requests.filter((r) => r.started_at <= ms && (r.finished_at ?? data.to) >= ms)
    const parts = [when(ms)]
    const gpuW = v('gpus', 'watts')
    if (gpuW != null) parts.push(`GPU ${Math.round(gpuW)} W`)
    const sys = v('system', 'watts')
    if (sys != null) parts.push(`system ${Math.round(sys)} W`)
    for (const c of cards) {
      const name = cards.length > 1 ? `${c.card} ` : ''
      const busy = v(c.source, 'busy'), vram = v(c.source, 'vram_used'), temp = v(c.source, 'temp_c')
      const bits = [busy != null ? `${Math.round(busy)}%` : '', vram != null ? `${(vram / 1024 ** 3).toFixed(1)} GB` : '', temp != null ? `${Math.round(temp)} °C` : '']
      parts.push(name + bits.filter(Boolean).join(' '))
    }
    parts.push(active.length ? `active: ${active.map((r) => `#${r.id} ${modelLabel(r.model)}`).join(', ')}` : 'idle')
    return parts.join(' · ')
  })

  const label = 'pointer-events-none absolute top-0 left-(--tl-left) leading-[14px] z-10 text-[10px] text-muted'
</script>

<!-- When the widget is too short for every strip, it scrolls instead of cutting off the bottom strip and its time axis. -->
<div class="flex h-full flex-col overflow-x-hidden overflow-y-auto" style="--tl-left:{AXIS + 4}px">
  <div class="num mb-1 h-4 flex-none truncate text-[11px] {readout ? 'text-text' : 'text-dim'}">
    {readout ?? 'Hover to see values at that time. Scroll to zoom, drag to move, double-click to reset.'}
  </div>
  <div class="relative flex-none" style="height:{Math.max(models.length, 1) * LANE_H + 4 + LABEL_H}px">
    <span class={label}>Requests by model (faded during model loading or before the first token)</span>
    <UChart options={laneOpts} data={laneData} {onCursor} zoomY={false} />
    <div class="absolute left-0 z-10 flex flex-col pr-1.5" style="top:{LABEL_H}px;bottom:0;width:{AXIS}px">
      {#each models as m, i}
        <div class="min-h-0 flex-1 truncate text-right text-[10px] leading-[14px] text-muted" title={modelTitle(m)}>{labels[i]}</div>
      {/each}
    </div>
  </div>
  <div class="relative min-h-[48px] flex-[1.3]">
    <span class={label}>Power (W){hasSystem ? ' · dashed: whole system' : ''}{hasCPU ? ' · blue: CPU' : ''}</span>
    <UChart options={powerOpts} data={powerData} {onCursor} />
  </div>
  <div class="relative min-h-[40px] flex-1"><span class={label}>GPU utilisation (%)</span><UChart options={utilOpts} data={utilData} {onCursor} /></div>
  <div class="relative min-h-[40px] flex-1"><span class={label}>VRAM (GB)</span><UChart options={vramOpts} data={vramData} {onCursor} /></div>
  <div class="relative min-h-[40px] flex-1"><span class={label}>Temperature (°C)</span><UChart options={tempOpts} data={tempData} {onCursor} /></div>
  <div class="relative min-h-[60px] flex-[1.3]">
    <span class={label}>Generation speed (tok/s), one dot per request</span>
    <UChart options={speedOpts} data={speedData} {onCursor} />
  </div>
</div>
