// Types and calls for notus-swap's /notus/api/. Times are unix milliseconds.

export type State = 'in_flight' | 'done' | 'failed' | 'client_gone' | 'interrupted' | 'cancelled'

export interface Summary {
  id: number
  started_at: number
  first_byte_at: number | null
  first_token_at: number | null
  finished_at: number | null
  method: string
  path: string
  model: string
  streaming: boolean
  state: State
  status_code: number | null
  error: string | null
  prompt_tokens: number | null
  completion_tokens: number | null
  cached_tokens: number | null
  prompt_ms: number | null
  predicted_ms: number | null
  prompt_per_second: number | null
  predicted_per_second: number | null
  energy_j: number | null
  /** How long the request waited while the server worked on other requests. Null for older requests. */
  queued_ms: number | null
  request_bytes: number
  response_bytes: number
  preview: string
  has_bodies: boolean
  finish_reason: string | null
  /** The model's context size per slot, when known. */
  n_ctx: number | null
  /** The server's build, from system_fingerprint, such as "b11146-7fe450e19". */
  build: string | null
  /** What is worth noticing about the request, or null when not worked out. */
  tags: Tag[] | null
  /** Kinds of issue found in the output, leaving out muted ones. */
  issues: string[]
  /** The request this one was sent again from, with the Retry button. */
  retry_of: number | null
  /** The client's address, from X-Forwarded-For behind a reverse proxy. Null for older requests. */
  client_ip: string | null
  /** The client's User-Agent header. Null for older requests. */
  user_agent: string | null
}

/** One thing worth noticing about a request. Only the fields that apply are set. */
export interface Tag {
  kind: string
  /** A count: tool calls, images, words of thinking, or cached tokens. */
  n?: number
  /** For cache_miss: the prompt's size. */
  of?: number
  /** What the request asked for, a schema name, or the path. */
  text?: string
  /** Tool calls or image formats. */
  items?: string[]
  /** For thinking: how many output tokens were thinking. */
  tokens?: number
  /** Whether tokens is an estimate, made from the share of the output's text that is thinking. */
  est?: boolean
}

/** How far llama-server has got through a prompt, while it waits for the first token. */
export interface Progress {
  /** Prompt tokens done so far, cached ones included. */
  processed: number
  cache: number
  per_second?: number
}

/** The numbers behind an issue. Only the fields that apply are set. */
export interface IssueNumbers {
  cause?: string
  prompt?: number
  output?: number
  limit?: number
  server_limit?: number
  n_ctx?: number
  slots?: number
  room?: number
  message?: string
}

export interface Issue {
  kind: string
  level: 'warning' | 'info'
  detail: IssueNumbers
  muted: boolean
}

export interface ToolCall {
  id?: string
  name: string
  arguments: string
}

export interface Parsed {
  content: string
  reasoning: string
  tool_calls: ToolCall[]
  finish_reason?: string
}

export interface Detail extends Summary {
  request: unknown
  response_raw: string
  response: Parsed
  truncated: boolean
  live: boolean
  chunks?: number
  issue_details: Issue[]
}

export interface Start {
  id: number
  started_at: number
  method: string
  path: string
  model: string
  streaming: boolean
  preview: string
  request_bytes: number
  /** The tags known from the request alone. */
  tags: Tag[]
  retry_of?: number
  client_ip?: string
  user_agent?: string
}

export interface Flight {
  start: Start
  output: Parsed
  chunks: number
  /** How many of the chunks carried thinking. */
  reasoning_chunks?: number
  first_token_at?: number
  progress?: Progress
}

export type LiveEvent =
  | { type: 'hello'; in_flight: Flight[] }
  | { type: 'start'; id: number; start: Start; at: number }
  | { type: 'delta'; id: number; content?: string; reasoning?: string; chunks: number; reasoning_chunks?: number; tools?: string[]; at: number }
  | { type: 'progress'; id: number; progress: Progress; at: number }
  | { type: 'finish'; id: number; at: number }

export interface ListFilter {
  q?: string
  model?: string
  state?: string
  /** An issue kind, or "any". */
  issue?: string
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
  return res.json()
}

type RequestList = { requests: Summary[]; models?: string[] }

function listURL(f: ListFilter, before?: number, limit = 100) {
  const p = new URLSearchParams()
  if (f.q) p.set('q', f.q)
  if (f.model) p.set('model', f.model)
  if (f.state) p.set('state', f.state)
  if (f.issue) p.set('issue', f.issue)
  if (before) p.set('before', String(before))
  p.set('limit', String(limit))
  return `/notus/api/requests?${p}`
}

// Started by prefetchRequests, and used once by the first listRequests that
// asks for the same list within 10 s.
let early: { url: string; at: number; list: Promise<RequestList> } | null = null

/**
 * Starts loading the Requests page's first list while the privacy settings
 * are still loading.
 */
export function prefetchRequests(f: ListFilter, limit?: number) {
  const url = listURL(f, undefined, limit)
  const list = getJSON<RequestList>(url)
  list.catch(() => {}) // the page sees the error when it takes this
  early = { url, at: Date.now(), list }
}

export function listRequests(f: ListFilter, before?: number, limit = 100) {
  const url = listURL(f, before, limit)
  const e = early
  early = null
  if (e?.url === url && Date.now() - e.at <= 10_000) return e.list
  return getJSON<RequestList>(url)
}

export function getRequest(id: number) {
  return getJSON<Detail>(`/notus/api/requests/${id}`)
}

async function post(url: string) {
  const res = await fetch(url, { method: 'POST' })
  if (!res.ok) throw new Error((await res.text()).trim() || `${res.status}`)
  return res
}

/** Ends a request in flight. Its client gets an error, or a stream that stops. */
export async function cancelRequest(id: number) {
  await post(`/notus/api/requests/${id}/cancel`)
}

/** Sends a finished request's stored body again, and returns the new request's ID. */
export async function retryRequest(id: number): Promise<number> {
  const res = await post(`/notus/api/requests/${id}/retry`)
  return (await res.json()).id
}

/** A request row made from a live "start" event, before the store has more. */
export function summaryFromStart(s: Start): Summary {
  return {
    ...s,
    first_byte_at: null,
    first_token_at: null,
    finished_at: null,
    state: 'in_flight',
    status_code: null,
    error: null,
    prompt_tokens: null,
    completion_tokens: null,
    cached_tokens: null,
    prompt_ms: null,
    predicted_ms: null,
    prompt_per_second: null,
    predicted_per_second: null,
    energy_j: null,
    queued_ms: null,
    response_bytes: 0,
    has_bodies: true,
    finish_reason: null,
    n_ctx: null,
    build: null,
    issues: [],
    retry_of: s.retry_of ?? null,
    client_ip: s.client_ip ?? null,
    user_agent: s.user_agent ?? null,
  }
}
