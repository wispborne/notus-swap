// The Requests page's Notable column: turns a request's tags into small chips,
// each with a title and a few lines for its popover.
import type { Summary, Tag } from './api'
import { issueKinds } from './issues.svelte'
import type { LiveOutput } from './live.svelte'

export type Icon = 'tool' | 'think' | 'nothink' | 'image' | 'warn' | 'nostream'

export interface Chip {
  kind: string
  icon?: Icon
  label: string
  /** Drawn small and dimmed after the label, such as `t` for tokens. */
  unit?: string
  /** Chips about the answer, and images, are drawn in the primary colour; the rest in grey. */
  bright: boolean
  /** The chip's own colour, used instead when the Requests page's Colorful option is on. */
  color?: string
  /** How solid the chip's colour is, in percent: its text, and its background. Unset is 100 and 15. */
  strength?: { text: number; fill: number }
  /** Warning colour applies even when Colorful is off. */
  tone?: 'warn'
  /** Requested thinking level, from 1 (low) to 4 (max). Unset when unspecified. */
  level?: number
  /** Thinking: the request asked for "max", above xhigh. The top pip gets its own colour. */
  peak?: boolean
  title: string
  lines: string[]
  mono?: boolean
}

const n = (v: number) => v.toLocaleString('en-US')
const plural = (v: number, one: string, many = one + 's') => `${n(v)} ${v === 1 ? one : many}`

/** 850, 7.2k, 14k: short enough for a chip. */
function shortTokens(v: number) {
  if (v < 1000) return String(v)
  if (v < 10_000) return `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k`
  return `${Math.round(v / 1000)}k`
}

const paths: Record<string, [string, string]> = {
  responses: ['RSP', 'Responses API'],
  completions: ['CMP', 'Text completion'],
  fim: ['FIM', 'Fill in the middle'],
  embeddings: ['EMB', 'Embeddings'],
  rerank: ['RRK', 'Reranking'],
  messages: ['MSG', 'Anthropic Messages API'],
}

/** Colors for the Colorful option; unlisted kinds stay grey. */
const colors: Record<string, string> = {
  tool_calls: '#18ffff',
  thinking: '#b69cff',
  images: '#40d7a3',
  json: '#6aa8ff',
  schema: '#6aa8ff',
  cache_miss: '#f0b85a',
}

/** inFlight distinguishes live output from the finished answer. */
export function chip(t: Tag, inFlight = false): Chip {
  const c = baseChip(t, inFlight)
  c.color = colors[t.kind]
  if (t.kind === 'thinking') {
    const level = thinkingLevel(t.text)
    const { color, ...strength } = thinkingStrength[level ?? 'medium']
    c.color = color
    c.strength = strength
    if (level) c.level = levels.indexOf(level) + 1
    if (t.text === 'effort max') c.peak = true
  }
  return c
}

/**
 * Thinking gets more solid, and a little more violet, the more of it the
 * request asked for. When the request didn't say, the model's default applies, which
 * is drawn as medium.
 */
const thinkingStrength = {
  low: { color: '#bdb3e6', text: 60, fill: 8 },
  medium: { color: '#b7a3f7', text: 73, fill: 12 },
  high: { color: '#b08fff', text: 87, fill: 16 },
  max: { color: '#a078ff', text: 100, fill: 20 },
}

const levels = Object.keys(thinkingStrength) as (keyof typeof thinkingStrength)[]

function thinkingLevel(asked = ''): keyof typeof thinkingStrength | undefined {
  const effort = asked.match(/^effort (\w+)/)?.[1]
  if (effort) {
    if (effort === 'minimal' || effort === 'low') return 'low'
    if (effort === 'high') return 'high'
    if (effort === 'xhigh' || effort === 'max') return 'max'
    return 'medium'
  }
  const budget = Number(asked.match(/^budget (\d+)/)?.[1])
  if (budget) {
    if (budget <= 2048) return 'low'
    if (budget <= 8192) return 'medium'
    if (budget <= 32768) return 'high'
    return 'max'
  }
  return undefined
}

function baseChip(t: Tag, inFlight: boolean): Chip {
  const count = t.n && t.n > 1 ? n(t.n) : ''
  switch (t.kind) {
    case 'tool_calls':
      return { kind: t.kind, icon: 'tool', label: count, bright: true, title: plural(t.n ?? 0, 'tool call'), lines: t.items ?? [], mono: true }
    case 'thinking': {
      const suffix = inFlight ? ' so far' : ''
      const lines = [t.text ? `Asked for: ${t.text}` : 'No thinking setting in the request']
      if (t.tokens) lines.push(`${t.est ? 'About ' : ''}${plural(t.tokens, 'token')} of thinking${suffix}`)
      if (t.n) lines.push(`About ${plural(t.n, 'word')}${suffix}`)
      else lines.push(inFlight ? 'No thinking output yet' : 'No thinking output')
      return { kind: t.kind, icon: 'think', label: t.tokens ? shortTokens(t.tokens) : '', unit: t.tokens ? 't' : undefined, bright: true, title: 'Thinking', lines }
    }
    case 'no_thinking':
      return { kind: t.kind, icon: 'nothink', label: '', bright: false, title: 'Thinking off', lines: [`Turned off by the request: ${t.text ?? ''}`] }
    case 'images':
      return { kind: t.kind, icon: 'image', label: count, bright: true, title: plural(t.n ?? 0, 'image'), lines: [(t.items ?? []).join(', ')] }
    case 'json':
      return { kind: t.kind, label: '{}', bright: false, title: 'JSON output', lines: ['The request asked for any JSON object'] }
    case 'schema':
      return {
        kind: t.kind,
        label: '{S}',
        bright: false,
        title: 'Structured output',
        lines: [t.text ? `JSON schema "${t.text}"` : 'The request gave a JSON schema'],
      }
    case 'not_streamed':
      return { kind: t.kind, icon: 'nostream', label: '', bright: false, title: 'Not streamed', lines: ['Streaming was disabled for this request'] }
    case 'cache_miss':
      return {
        kind: t.kind,
        label: 'C0',
        bright: false,
        title: 'Cache miss',
        lines: [`${n(t.n ?? 0)} of ${n(t.of ?? 0)} prompt tokens reused from the cache`],
      }
  }
  const p = paths[t.kind]
  if (p) return { kind: t.kind, label: p[0], bright: false, title: p[1], lines: [`POST ${t.text ?? ''}`], mono: true }
  return { kind: t.kind, label: t.kind.slice(0, 3).toUpperCase(), bright: false, title: t.kind, lines: [] }
}

const issueLabels: Record<string, string> = {
  token_limit: 'MAX',
  server_limit: 'MAX',
  context_full: 'CTX',
  context_error: 'CTX',
  thinking_budget: 'THK',
  cut_off: 'CUT',
}

/**
 * Output warnings precede request tags. Context-near-full notices are too
 * common to be useful in this column.
 */
export function rowChips(r: Summary, flight: LiveOutput | undefined): Chip[] {
  const out: Chip[] = []
  for (const kind of r.issues) {
    if (kind === 'context_near_full') continue
    out.push({
      kind: `issue:${kind}`,
      icon: 'warn',
      label: issueLabels[kind] ?? '',
      bright: false,
      tone: 'warn',
      title: issueKinds.find(([k]) => k === kind)?.[1] ?? kind,
      lines: ['Open the request to see counts and suggested fixes.'],
    })
  }
  if (r.tags) out.push(...liveTags(r.tags, flight).map((t) => chip(t, !!flight)))
  return out
}

/** Live answer tags precede request tags, matching the stored order. */
export function liveTags(tags: Tag[], f: LiveOutput | undefined): Tag[] {
  if (!f) return tags
  const out: Tag[] = []
  if (f.tools.length) out.push({ kind: 'tool_calls', n: f.tools.length, items: f.tools })
  const asked = tags.find((t) => t.kind === 'thinking' || t.kind === 'no_thinking')
  // Each streamed chunk is about one token.
  if (f.reasoning) out.push({ kind: 'thinking', n: words(f.reasoning), tokens: f.reasoningChunks, est: true, text: asked?.text })
  for (const t of tags) if (!(f.reasoning && t === asked)) out.push(t)
  return out
}

function words(s: string) {
  let count = 0
  let inWord = false
  for (let i = 0; i < s.length; i++) {
    const space = s.charCodeAt(i) <= 32
    if (!space && !inWord) count++
    inWord = !space
  }
  return count
}
