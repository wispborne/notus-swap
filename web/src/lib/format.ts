const pad = (n: number) => String(n).padStart(2, '0')

/** 14:03:22 today, "Sep 23 14:03" on other days. */
export function when(ms: number) {
  const d = new Date(ms)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  if (d.toDateString() === new Date().toDateString()) return time
  return `${d.toLocaleString(undefined, { month: 'short' })} ${d.getDate()} ${time.slice(0, 5)}`
}

export function duration(ms: number | null | undefined) {
  if (ms == null || !isFinite(ms)) return '–'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`
}

export function num(v: number | null | undefined, digits = 0) {
  if (v == null || !isFinite(v)) return '–'
  if (v >= 10_000) return `${(v / 1000).toFixed(1)}k`
  return v.toFixed(digits)
}

/** 0.834 as "83%". */
export function pct(v: number | null | undefined) {
  if (v == null || !isFinite(v)) return '–'
  return `${Math.round(v * 100)}%`
}

export function joules(j: number) {
  if (j >= 3_600_000) return `${(j / 3_600_000).toFixed(2)} kWh`
  if (j >= 36_000) return `${(j / 3600).toFixed(1)} Wh`
  if (j >= 1000) return `${(j / 1000).toFixed(1)} kJ`
  return `${Math.round(j)} J`
}

export function bytes(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 / 1024).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(2)} GB`
}

/** llama.cpp's "b11146-7fe450e19" as "b11146". Other servers' builds are kept whole. */
export function shortBuild(build: string) {
  return /^b\d+-[0-9a-f]+$/.test(build) ? build.slice(0, build.indexOf('-')) : build
}

export { modelColor } from './modelColors.svelte'
