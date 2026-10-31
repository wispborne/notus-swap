// Types for /notus/api/dashboard and the numbers the widgets derive from it.
import type { Summary } from '../api'
import { modelId } from '../modelIds.svelte'
import { qualify } from '../peerModels'
import { api } from '../serverPath'

/** 'all' runs from the oldest thing stored until now. */
export type Range = '15m' | '1h' | '6h' | '24h' | '7d' | '30d' | 'all'
export const RANGES: Range[] = ['15m', '1h', '6h', '24h', '7d', '30d', 'all']

export interface Point {
  t: number // unix seconds
  source: string
  kind: 'gpu' | 'igpu' | 'gpus' | 'cpu' | 'system'
  watts: number | null
  temp_c: number | null
  vram_used: number | null
  vram_total: number | null
  busy: number | null
}

export interface ModelEvent {
  at: number
  model: string
  from: string
  to: string
  load_ms?: number
  /** Set on a load notus-swap started itself, to bring back the default model. */
  auto?: boolean
}

export interface Card {
  source: string
  card: string
  name?: string
  integrated: boolean
}

export interface DashboardData {
  from: number
  to: number
  bucket_s: number
  series: Point[]
  cards: Card[]
  requests: DashRequest[]
  model_events: ModelEvent[]
  energy: {
    today_gpu_wh: number | null
    today_system_wh: number | null
    range_gpu_wh: number | null
    range_system_wh: number | null
  }
}

/**
 * The data with each request's model given by its ID, so a request sent to
 * an alias counts as its model in every widget. Model events already use IDs.
 */
export function byModelId(d: DashboardData): DashboardData {
  return { ...d, requests: d.requests.map((r) => (modelId(r.model) === r.model ? r : { ...r, model: modelId(r.model) })) }
}

/** The fields of a request that the Dashboard draws. The server sends only these. */
export type DashRequest = Pick<
  Summary,
  | 'id'
  | 'started_at'
  | 'first_token_at'
  | 'finished_at'
  | 'model'
  | 'state'
  | 'prompt_tokens'
  | 'completion_tokens'
  | 'cached_tokens'
  | 'prompt_ms'
  | 'prompt_per_second'
  | 'predicted_per_second'
  | 'energy_j'
  | 'queued_ms'
>

/** The range picked last time, saved per browser. */
export function savedRange(): Range {
  let r: string | null = null
  try {
    r = localStorage.getItem('dashboard_range')
  } catch {}
  return (RANGES as string[]).includes(r ?? '') ? (r as Range) : '1h'
}

export interface SavedLayout<Item, Opts> {
  v?: number
  items?: Item[]
  opts?: Opts
}

async function getDashboard(range: Range): Promise<DashboardData> {
  const res = await fetch(`/notus/api/dashboard?range=${range}`)
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
  return res.json()
}

async function getLayout(): Promise<SavedLayout<unknown, unknown> | null> {
  try {
    return await (await fetch('/notus/api/settings/dashboard_layout')).json()
  } catch {
    return null
  }
}

// Started by prefetchDashboard, and used once by the page's first fetches,
// if the page opens within FRESH_MS.
const FRESH_MS = 10_000
let early: { at: number; range: Range; data?: Promise<DashboardData>; layout?: Promise<SavedLayout<unknown, unknown> | null> } | null = null

/**
 * Starts loading the Dashboard's data and layout at once, while the page's
 * code and the privacy settings are still loading. The page's first
 * fetchDashboard and fetchLayout take these answers instead of asking again.
 */
export function prefetchDashboard() {
  const range = savedRange()
  const data = getDashboard(range)
  data.catch(() => {}) // the page sees the error when it takes this
  early = { at: Date.now(), range, data, layout: getLayout() }
}

function takeEarly<K extends 'data' | 'layout'>(key: K) {
  if (!early || Date.now() - early.at > FRESH_MS) return undefined
  const p = early[key]
  early[key] = undefined
  return p
}

export function fetchDashboard(range: Range): Promise<DashboardData> {
  const fresh = early?.range === range ? takeEarly('data') : undefined
  return fresh ?? getDashboard(range)
}

export function fetchLayout<Item, Opts>(): Promise<SavedLayout<Item, Opts> | null> {
  return (takeEarly('layout') ?? getLayout()) as Promise<SavedLayout<Item, Opts> | null>
}

/** Another server's data over the same span as this server's, so the buckets line up. */
export async function fetchPeerDashboard(server: string, from: number, to: number): Promise<DashboardData> {
  const res = await fetch(api(server, `/notus/api/dashboard?from=${from}&to=${to}`))
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
  return res.json()
}

/** One other server's data, for mergeDashboards. */
export interface PeerData {
  server: string
  name: string
  data: DashboardData
}

/**
 * Every server's data as one, so the widgets need not know about servers.
 * Another server's models are named "@<id>:<model>" (peerModels.ts). Each
 * card's source gets its server's ID, and its name gets its server's name.
 * The totals (all GPUs, CPU, system) and the energy are added up, bucket by
 * bucket: every server was asked for the same span, so the buckets match.
 */
export function mergeDashboards(home: DashboardData, homeName: string, others: PeerData[]): DashboardData {
  if (!others.length) return home
  const totals = new Set<Point['kind']>(['gpus', 'cpu', 'system'])
  const summed = new Map<string, Point>()
  const series: Point[] = []
  const plus = (a: number | null, b: number | null) => (a == null ? b : b == null ? a : a + b)
  const most = (a: number | null, b: number | null) => (a == null ? b : b == null ? a : Math.max(a, b))
  const add = (p: Point) => {
    if (!totals.has(p.kind)) return series.push(p)
    const key = `${p.source}|${p.t}`
    const s = summed.get(key)
    if (!s) return summed.set(key, { ...p })
    s.watts = plus(s.watts, p.watts)
    s.vram_used = plus(s.vram_used, p.vram_used)
    s.vram_total = plus(s.vram_total, p.vram_total)
    s.temp_c = most(s.temp_c, p.temp_c)
    s.busy = most(s.busy, p.busy)
  }
  home.series.forEach(add)
  for (const o of others) for (const p of o.data.series) add(totals.has(p.kind) ? p : { ...p, source: `${o.server}/${p.source}` })
  series.push(...summed.values())

  const energy = { ...home.energy }
  for (const o of others) for (const k of Object.keys(energy) as (keyof typeof energy)[]) energy[k] = plus(energy[k], o.data.energy[k])

  return {
    ...home,
    series,
    cards: [
      ...home.cards.map((c) => ({ ...c, card: `${homeName} ${c.card}` })),
      ...others.flatMap((o) => o.data.cards.map((c) => ({ ...c, source: `${o.server}/${c.source}`, card: `${o.name} ${c.card}` }))),
    ],
    requests: [...home.requests, ...others.flatMap((o) => o.data.requests.map((r) => ({ ...r, model: qualify(o.server, r.model) })))].sort(
      (a, b) => b.started_at - a.started_at,
    ),
    model_events: [...home.model_events, ...others.flatMap((o) => o.data.model_events.map((e) => ({ ...e, model: qualify(o.server, e.model) })))].sort((a, b) => a.at - b.at),
    energy,
  }
}

/** Series aligned on one time axis, as uPlot wants: xs in seconds, one array per key. */
export interface Aligned {
  xs: number[]
  bySource: Map<string, Point[]>
  /** Values for a source and field, null where that source has no bucket. */
  values(source: string, field: keyof Point): (number | null)[]
}

export function align(points: Point[]): Aligned {
  const bySource = new Map<string, Point[]>()
  const times = new Set<number>()
  for (const p of points) {
    times.add(p.t)
    let list = bySource.get(p.source)
    if (!list) bySource.set(p.source, (list = []))
    list.push(p)
  }
  const xs = [...times].sort((a, b) => a - b)
  const index = new Map(xs.map((t, i) => [t, i]))
  const cache = new Map<string, (number | null)[]>()
  return {
    xs,
    bySource,
    values(source, field) {
      const key = `${source}|${field}`
      let out = cache.get(key)
      if (!out) {
        out = new Array(xs.length).fill(null)
        for (const p of bySource.get(source) ?? []) out[index.get(p.t)!] = p[field] as number | null
        cache.set(key, out)
      }
      return out
    },
  }
}

export function median(values: number[]) {
  if (!values.length) return null
  const s = [...values].sort((a, b) => a - b)
  const m = Math.floor(s.length / 2)
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2
}

/** Time to first token, leaving out time spent waiting behind other requests. */
export const ttftOf = (r: DashRequest) => (r.first_token_at ? r.first_token_at - r.started_at - (r.queued_ms ?? 0) : null)

export interface ModelStats {
  model: string
  requests: number
  genSpeed: number | null // tokens/s, weighted by tokens
  promptSpeed: number | null
  cacheRate: number | null // cached prompt tokens over prompt tokens
  ttftP50: number | null
  joulesPerToken: number | null
  lastUsed: number | null
  speedTrend: [number, number][] // [finished ms, tokens/s]
}

/** Per-model numbers over the finished requests in range. */
export function modelStats(requests: DashRequest[]): Map<string, ModelStats> {
  const out = new Map<string, ModelStats>()
  const groups = new Map<string, DashRequest[]>()
  for (const r of requests) {
    if (!r.model) continue
    let g = groups.get(r.model)
    if (!g) groups.set(r.model, (g = []))
    g.push(r)
  }
  for (const [model, rs] of groups) {
    const done = rs.filter((r) => r.state === 'done')
    let genTok = 0, genSec = 0, ppTok = 0, ppSec = 0, joules = 0, jTok = 0
    for (const r of done) {
      if (r.predicted_per_second && r.completion_tokens) {
        genTok += r.completion_tokens
        genSec += r.completion_tokens / r.predicted_per_second
      }
      // prompt_per_second counts only the tokens processed, not the cached
      // ones, so weight by processing time rather than by prompt_tokens.
      if (r.prompt_per_second && r.prompt_ms) {
        ppTok += (r.prompt_ms / 1000) * r.prompt_per_second
        ppSec += r.prompt_ms / 1000
      }
      if (r.energy_j != null && r.completion_tokens) {
        joules += r.energy_j
        jTok += r.completion_tokens
      }
    }
    out.set(model, {
      model,
      requests: rs.length,
      genSpeed: genSec ? genTok / genSec : null,
      promptSpeed: ppSec ? ppTok / ppSec : null,
      cacheRate: cacheRate(done),
      ttftP50: median(done.map(ttftOf).filter((v): v is number => v != null)),
      joulesPerToken: jTok ? joules / jTok : null,
      lastUsed: Math.max(...rs.map((r) => r.started_at)),
      speedTrend: done
        .filter((r) => r.predicted_per_second && r.finished_at)
        .map((r) => [r.finished_at!, r.predicted_per_second!] as [number, number])
        .sort((a, b) => a[0] - b[0]),
    })
  }
  return out
}

/** Cached prompt tokens over prompt tokens, for requests that report a cache count. */
export function cacheRate(requests: DashRequest[]): number | null {
  let cached = 0, prompt = 0
  for (const r of requests)
    if (r.cached_tokens != null && r.prompt_tokens) {
      cached += r.cached_tokens
      prompt += r.prompt_tokens
    }
  return prompt ? cached / prompt : null
}
