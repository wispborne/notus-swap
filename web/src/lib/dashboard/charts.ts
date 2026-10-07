// Shared uPlot settings in the Sigma colours.
import uPlot from 'uplot'
import { zoom } from './zoom.svelte'

export const C = {
  grid: '#343a45',
  axis: '#636b79',
  text: '#8b93a1',
  power: '#f0b85a',
  system: '#c9ced6',
  cpu: '#7aa7ff',
  util: '#18ffff',
  vram: '#b69cff',
  ram: '#40d7a3',
  temp: '#ff7a9a',
  primary: '#40d7a3',
}

/** Colours for several cards on one chart. */
export const cardColors = ['#f0b85a', '#ff9f5a', '#ffd88a', '#e08a3c']

/** Left axis width, the same on every chart so stacked charts line up. */
export const AXIS_W = 44

/** Axis numbers: "5k" not "5000", "2.5" not "2.50", "0" not "0.0". */
export function axisNum(v: number): string {
  if (Math.abs(v) >= 1000) return `${+(v / 1000).toPrecision(3)}k`
  return `${+v.toPrecision(3)}`
}

/** The round step (1, 2, 2.5 or 5 times a power of ten) closest to `rough`. */
function niceStep(rough: number): number {
  if (!(rough > 0)) return 1
  const p = 10 ** Math.floor(Math.log10(rough))
  let best = p
  for (const m of [1, 2, 2.5, 5, 10]) if (Math.abs(Math.log(m * p / rough)) < Math.abs(Math.log(best / rough))) best = m * p
  return best
}

/**
 * About `n` ticks at round numbers between min and max. Ticks above
 * `top` (a fraction of the range) are left out, so the top number doesn't sit
 * next to a label drawn above the plot.
 */
export function niceSplits(min: number, max: number, n: number, top = 1): number[] {
  const step = niceStep((max - min) / n)
  const out: number[] = []
  const limit = min + (max - min) * top + step * 1e-9
  for (let v = Math.ceil(min / step) * step; v <= limit; v += step) out.push(+v.toPrecision(12))
  return out
}

/**
 * Ticks for a log scale, as many as fit in `room` gaps. It tries 1, 2 and 5
 * times each power of ten, then 1 and 3, then each power, then every second
 * or third power. The uneven gaps between 1, 2, 5 and 10 show that the scale
 * is a log scale.
 */
function logSplits(min: number, max: number, room: number): number[] {
  const lo = Math.floor(Math.log10(min)), hi = Math.ceil(Math.log10(max))
  const ticks = (mults: number[], every: number) => {
    const out: number[] = []
    for (let d = lo; d <= hi; d++) {
      if (d % every) continue
      for (const m of mults) {
        const v = +(m * 10 ** d).toPrecision(12)
        if (v >= min && v <= max) out.push(v)
      }
    }
    return out
  }
  const tries: [number[], number][] = [[[1, 2, 5], 1], [[1, 3], 1], [[1], 1], [[1], 2], [[1], 3]]
  for (const [mults, every] of tries) {
    const t = ticks(mults, every)
    if (t.length <= room + 1) return t
  }
  return ticks([1], 3)
}

/** How many tick gaps fit the plot's height, keeping labels at least `gap` px apart. */
const fit = (u: uPlot, want: number, gap = 22) => Math.max(1, Math.min(want, Math.floor(u.bbox.height / uPlot.pxRatio / gap)))

export function yAxis(label?: (v: number) => string, splits?: number, opts: { log?: boolean; top?: number } = {}): uPlot.Axis {
  return {
    stroke: C.text,
    size: AXIS_W,
    font: '10px system-ui, sans-serif',
    grid: { stroke: C.grid, width: 1 },
    ticks: { show: false },
    gap: 4,
    values: (_u, vals) => vals.map((v) => (v == null ? '' : (label ?? axisNum)(v))),
    // uPlot labels only the powers of ten on a log axis unless told otherwise.
    filter: opts.log ? (_u, splits) => splits : undefined,
    splits: opts.log
      ? (u, _i, min, max) => logSplits(min, max, fit(u, 100, 14))
      : splits
        ? (u, _i, min, max) => niceSplits(min, max, fit(u, splits), opts.top)
        : undefined,
  }
}

/** A y range from 0 to a little above the largest value. */
export const zeroToMax = (_u: uPlot, _min: number, max: number) => [0, max > 0 ? max * 1.1 : 1] as [number, number]

/** A log y range around the values, for data where a few large values would flatten the rest. */
export const logRange = (_u: uPlot, min: number, max: number) =>
  (min > 0 && max > 0 ? [min / 1.4, max * 1.4] : [1, 10]) as [number, number]

const pad2 = (n: number) => String(n).padStart(2, '0')

/**
 * One line per tick, so it fits the axis: 24-hour times like the rest of the
 * UI, with the date in place of 00:00 (and alone once ticks are a day apart).
 */
function timeLabels(_u: uPlot, splits: number[], _axis: number, _space: number, incr: number) {
  return splits.map((s) => {
    const d = new Date(s * 1000)
    const day = `${d.toLocaleString(undefined, { month: 'short' })} ${d.getDate()}`
    if (incr >= 86400 || (d.getHours() === 0 && d.getMinutes() === 0 && d.getSeconds() === 0)) return day
    const hm = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
    return incr < 60 ? `${hm}:${pad2(d.getSeconds())}` : hm
  })
}

export function xAxis(show = true): uPlot.Axis {
  return {
    show,
    stroke: C.text,
    font: '10px system-ui, sans-serif',
    grid: { stroke: C.grid, width: 1 },
    ticks: { show: false },
    size: show ? 20 : 0,
    gap: 2,
    values: timeLabels,
  }
}

export function line(label: string, stroke: string, fill = false, extra: Partial<uPlot.Series> = {}): uPlot.Series {
  return {
    label,
    stroke,
    width: 1.5,
    fill: fill ? stroke + '22' : undefined,
    points: { show: false },
    spanGaps: false,
    ...extra,
  }
}

export function dots(label: string, stroke: string): uPlot.Series {
  return {
    label,
    stroke,
    width: 0,
    paths: () => null,
    points: { show: true, size: 5, fill: stroke, stroke },
  }
}

export function bars(label: string, color: string): uPlot.Series {
  return {
    label,
    stroke: color,
    fill: color + 'cc',
    width: 0,
    paths: uPlot.paths.bars!({ size: [0.7, 40], align: 0 }),
    points: { show: false },
  }
}

/**
 * The time window charts show, in unix ms. Widgets update it in place before
 * new data arrives, so charts keep their state instead of being rebuilt.
 */
export interface Window {
  from: number
  to: number
}

/** Base options: no legend (widgets draw their own), x range from win, or the zoomed window. */
export function base(win: Window, sync?: string): Partial<uPlot.Options> {
  return {
    legend: { show: false },
    padding: [10, 8, 0, 0],
    scales: {
      x: {
        time: true,
        range: () => {
          const [from, to] = zoom.span ?? [win.from, win.to]
          return [from / 1000, to / 1000]
        },
      },
    },
    cursor: {
      sync: sync ? { key: sync } : undefined,
      points: { size: 6 },
      drag: { x: false, y: false },
    },
  }
}
