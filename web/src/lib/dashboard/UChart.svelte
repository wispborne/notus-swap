<script lang="ts">
  import { untrack } from 'svelte'
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { zoom, type YView } from './zoom.svelte'

  // A uPlot chart that fills its container and follows its size. It is
  // rebuilt when `options` changes and updated in place when `data` changes.
  // Every chart joins the Dashboard's shared zoom. `zoomY` turns off zooming
  // its y axis, for charts whose y axis isn't a scale of values.
  let {
    options,
    data,
    onCursor,
    zoomY = true,
  }: {
    options: Omit<uPlot.Options, 'width' | 'height'>
    data: uPlot.AlignedData
    onCursor?: (idx: number | null, plot: uPlot) => void
    zoomY?: boolean
  } = $props()

  // Kept here, so the y zoom outlasts a rebuild of the chart.
  const view: YView = { y: null, full: null }

  let box: HTMLDivElement
  let plot: uPlot | undefined

  $effect(() => {
    const opts = options
    const size = { width: box.clientWidth || 300, height: box.clientHeight || 100 }
    const hooks = { ...opts.hooks }
    const cb = untrack(() => onCursor)
    if (cb) hooks.setCursor = [...(opts.hooks?.setCursor ?? []), (u: uPlot) => cb(u.cursor.idx ?? null, u)]
    const y = untrack(() => zoomY)
    const scales = y ? { ...opts.scales, y: { ...opts.scales?.y, range: zoom.yRange(view, opts.scales?.y?.range) } } : opts.scales
    // Only a change of options rebuilds the chart; data changes go through setData below.
    plot = new uPlot({ ...opts, ...size, hooks, scales }, untrack(() => data), box)
    const detach = zoom.attach(plot, y ? view : null)
    const ro = new ResizeObserver(() => {
      if (box.clientWidth > 0 && box.clientHeight > 0) plot?.setSize({ width: box.clientWidth, height: box.clientHeight })
    })
    ro.observe(box)
    return () => {
      ro.disconnect()
      detach()
      plot?.destroy()
      plot = undefined
    }
  })

  $effect(() => {
    const d = data
    plot?.setData(d, true)
  })
</script>

<div bind:this={box} class="h-full min-h-0 w-full min-w-0"></div>
