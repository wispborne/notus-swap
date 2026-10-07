// Zooming the Dashboard's charts. Scrolling over a chart zooms around the
// cursor, on both axes. The time axis is shared, so every chart zooms and pans
// in time together. The y axis zooms only on the chart under the cursor.
// Dragging a zoomed chart pans it, and a double-click goes back to the whole
// range on every chart. Zooming only changes the axes; no data is fetched again.
import type uPlot from 'uplot'

/** The shortest time window that can be zoomed to, in ms. */
const MIN_SPAN = 60_000

/** One chart's y zoom. Kept by the chart's component, so it outlasts a rebuild of the chart. */
export interface YView {
  /** The zoomed y range, or null for the chart's own range. */
  y: [number, number] | null
  /** The chart's own y range when the zoom began. Zooming out to it ends the zoom. */
  full: [number, number] | null
}

type Range = uPlot.Scale['range']

class Zoom {
  /** The zoomed time window in unix ms, or null to show the whole range. */
  span = $state<[number, number] | null>(null)
  /** True while any chart has its y axis zoomed. */
  yZoomed = $state(false)
  /** The whole range the Dashboard loaded, in unix ms. Set by the Dashboard. */
  full: [number, number] = [0, 0]
  private plots = new Map<uPlot, YView | null>()

  /** The time window charts should show now. */
  window(): [number, number] {
    return this.span ?? this.full
  }

  /** Zooms time to [from, to], kept inside the whole range. A window as wide as the whole range ends the time zoom. */
  set(from: number, to: number) {
    const [f0, f1] = this.full
    const len = Math.max(MIN_SPAN, to - from)
    if (len >= f1 - f0) {
      if (!this.span) return
      this.span = null
    } else {
      if (from < f0) (from = f0), (to = f0 + len)
      else if (to > f1) (to = f1), (from = f1 - len)
      else to = from + len
      this.span = [from, to]
    }
    const [a, b] = this.window()
    for (const u of this.plots.keys()) u.setScale('x', { min: a / 1000, max: b / 1000 })
  }

  /** Ends every zoom, in time and on every chart's y axis. */
  reset() {
    this.span = null
    this.yZoomed = false
    for (const [u, view] of this.plots) {
      if (view) (view.y = null), (view.full = null)
      // Ranging again from the data lets each y axis fit its values.
      u.setData(u.data, true)
    }
  }

  /** A y range for chart options: the zoomed range while zoomed, else the chart's own. */
  yRange(view: YView, own: Range): Range {
    return (u, min, max, key) => {
      if (view.y) return view.y
      if (typeof own === 'function') return own(u, min, max, key)
      if (Array.isArray(own)) return own as uPlot.Range.MinMax
      return [min, max]
    }
  }

  private setY(u: uPlot, view: YView, y: [number, number] | null) {
    view.y = y
    if (!y) view.full = null
    this.yZoomed = [...this.plots.values()].some((v) => v?.y)
    if (y) u.setScale('y', { min: y[0], max: y[1] })
    else u.setData(u.data, true)
  }

  /** Adds the zoom controls to a chart. Without `view`, its y axis doesn't zoom. Returns a function that removes them. */
  attach(u: uPlot, view: YView | null): () => void {
    this.plots.set(u, view)
    const el = u.over
    // A log scale zooms evenly in powers of ten.
    const isLog = () => u.scales.y?.distr === 3
    const toY = (v: number) => (isLog() ? Math.log10(v) : v)
    const fromY = (v: number) => (isLog() ? 10 ** v : v)
    const yNow = (): [number, number] | null => {
      const { min, max } = u.scales.y ?? {}
      return min == null || max == null ? null : [toY(min), toY(max)]
    }

    const onWheel = (e: WheelEvent) => {
      if (!e.deltaY) return
      e.preventDefault()
      // A mouse wheel step is about 100; a trackpad sends many small steps.
      const k = Math.exp(Math.max(-300, Math.min(300, e.deltaY)) * 0.002)
      const [from, to] = this.window()
      const at = u.posToVal(e.offsetX, 'x') * 1000
      this.set(at - (at - from) * k, at + (to - at) * k)

      const now = view && yNow()
      if (!view || !now) return
      if (!view.full) view.full = now
      const y = toY(u.posToVal(e.offsetY, 'y'))
      const lo = y - (y - now[0]) * k, hi = y + (now[1] - y) * k
      const fullLen = view.full[1] - view.full[0]
      if (hi - lo >= fullLen) return this.setY(u, view, null)
      if (hi - lo < fullLen * 1e-4) return
      this.setY(u, view, [fromY(lo), fromY(hi)])
    }

    let start: { x: number; y: number; span: [number, number] | null; yRange: [number, number] | null } | null = null
    const onDown = (e: MouseEvent) => {
      if (e.button !== 0 || (!this.span && !view?.y)) return
      start = { x: e.clientX, y: e.clientY, span: this.span, yRange: view?.y ? yNow() : null }
      el.style.cursor = 'grabbing'
      window.addEventListener('mousemove', onMove)
      window.addEventListener('mouseup', onUp)
    }
    const onMove = (e: MouseEvent) => {
      if (!start) return
      // The chart follows the mouse: dragging right shows earlier times, dragging down shows higher values.
      if (start.span) {
        const shift = ((start.x - e.clientX) / (el.clientWidth || 1)) * (start.span[1] - start.span[0])
        this.set(start.span[0] + shift, start.span[1] + shift)
      }
      if (start.yRange && view) {
        const shift = ((e.clientY - start.y) / (el.clientHeight || 1)) * (start.yRange[1] - start.yRange[0])
        this.setY(u, view, [fromY(start.yRange[0] + shift), fromY(start.yRange[1] + shift)])
      }
    }
    const onUp = () => {
      start = null
      el.style.cursor = ''
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    const onDouble = () => this.reset()

    el.addEventListener('wheel', onWheel, { passive: false })
    el.addEventListener('mousedown', onDown)
    el.addEventListener('dblclick', onDouble)
    return () => {
      this.plots.delete(u)
      onUp()
      el.removeEventListener('wheel', onWheel)
      el.removeEventListener('mousedown', onDown)
      el.removeEventListener('dblclick', onDouble)
    }
  }
}

export const zoom = new Zoom()
