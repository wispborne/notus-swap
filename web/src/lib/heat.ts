// The Requests page's heatmap: numbers that are worse than usual are drawn
// brighter than the rest. Each column compares a request with the median of the
// requests listed, of the same model where the number depends on the model.

export interface HeatColumn<R> {
  /** The number, or null to leave the row out. */
  value: (r: R) => number | null | undefined
  /** Whether a high value is the bad side. Speeds are bad when low. */
  high: boolean
  /** How many times worse than the median gives full strength. */
  full: number
  /** Compare only with requests for the same model. */
  perModel: boolean
}

/** Values up to this many times worse than the median get no tint. */
const START = 1.25
/** Minimum sample count before a group is compared. */
const MIN_ROWS = 5

/**
 * Returns how hot each cell is, from 0 to 1. Only rows that finished
 * normally count, both for the medians and for the tint.
 */
export function heatMap<R extends { model: string; state: string }>(rows: R[], cols: Record<string, HeatColumn<R>>) {
  const medians = new Map<string, number>()
  for (const [key, col] of Object.entries(cols)) {
    const groups = new Map<string, number[]>()
    for (const r of rows) {
      const v = r.state === 'done' ? col.value(r) : null
      if (v == null || !isFinite(v) || v <= 0) continue
      const g = col.perModel ? r.model : ''
      let list = groups.get(g)
      if (!list) groups.set(g, (list = []))
      list.push(v)
    }
    for (const [g, list] of groups) {
      if (list.length < MIN_ROWS) continue
      list.sort((a, b) => a - b)
      const mid = list.length >> 1
      medians.set(`${key}\n${g}`, list.length % 2 ? list[mid] : (list[mid - 1] + list[mid]) / 2)
    }
  }
  return (key: string, r: R): number => {
    const col = cols[key]
    if (!col || r.state !== 'done') return 0
    const median = medians.get(`${key}\n${col.perModel ? r.model : ''}`)
    const v = col.value(r)
    if (median == null || v == null || !isFinite(v) || v <= 0) return 0
    const worse = col.high ? v / median : median / v
    if (worse <= START) return 0
    return Math.min(1, Math.log(worse / START) / Math.log(col.full / START))
  }
}

/**
 * The text colour for a cell of this heat. strength is how much a cell with
 * no heat is dimmed, in percent; the hottest cells are not dimmed at all.
 */
export function heatColor(heat: number, strength: number) {
  if (strength <= 0) return undefined
  return `color-mix(in srgb, var(--color-text) ${Math.round(100 - strength * (1 - heat))}%, transparent)`
}
