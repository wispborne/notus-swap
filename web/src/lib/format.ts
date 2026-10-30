const pad = (n: number) => String(n).padStart(2, '0')

/** 14:03:22 today, "Sep 23 14:03" on other days. */
export function when(ms: number) {
  const d = new Date(ms)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  if (d.toDateString() === new Date().toDateString()) return time
  return `${d.toLocaleString(undefined, { month: 'short' })} ${d.getDate()} ${time.slice(0, 5)}`
}

/** "today 14:03", "yesterday 22:10", or "Sep 23 14:03", for times that don't need seconds. */
export function lastUsed(ms: number) {
  const d = new Date(ms)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  const day = new Date()
  if (d.toDateString() === day.toDateString()) return `today ${time}`
  day.setDate(day.getDate() - 1)
  if (d.toDateString() === day.toDateString()) return `yesterday ${time}`
  return `${d.toLocaleString(undefined, { month: 'short' })} ${d.getDate()} ${time}`
}

export function duration(ms: number | null | undefined) {
  if (ms == null || !isFinite(ms)) return '–'
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`
}

/** A model's load time in whole seconds: "12 s", "1m 5s". */
export function loadTime(ms: number | null | undefined) {
  if (ms == null || !isFinite(ms)) return '–'
  const s = Math.round(ms / 1000)
  if (s < 1) return '<1 s'
  if (s < 60) return `${s} s`
  return `${Math.floor(s / 60)}m ${s % 60}s`
}

export function num(v: number | null | undefined, digits = 0) {
  if (v == null || !isFinite(v)) return '–'
  if (v >= 10_000) return `${(v / 1000).toFixed(1)}k`
  return v.toFixed(digits)
}

/** 12345.6 as "12,345.6" with digits=1. */
export function fixed(v: number | null | undefined, digits = 0) {
  if (v == null || !isFinite(v)) return '–'
  return v.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** 0.834 as "83%". */
export function pct(v: number | null | undefined) {
  if (v == null || !isFinite(v)) return '–'
  return `${Math.round(v * 100)}%`
}

/** Energy in watt-hours, the unit every page uses: "0.42 Wh", "668.7 Wh", "26.74 kWh". */
export function joules(j: number) {
  const wh = j / 3600
  if (wh >= 1000) return `${fixed(wh / 1000, 2)} kWh`
  if (wh >= 100) return `${fixed(wh, 1)} Wh`
  return `${fixed(wh, 2)} Wh`
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
