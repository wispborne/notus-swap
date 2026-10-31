// One shared connection to /notus/api/live. It keeps the output of every
// request in flight, so any page can show it, and passes each event on to
// listeners.
import { SvelteMap } from 'svelte/reactivity'
import type { LiveEvent, Progress, Start } from './api'

export interface LiveOutput {
  start: Start
  content: string
  reasoning: string
  chunks: number
  /** How many of the chunks carried thinking, about one per token. */
  reasoningChunks: number
  firstTokenAt?: number
  /** When the latest output arrived. */
  lastAt?: number
  /** Names of the tool calls started so far. */
  tools: string[]
  /** How far the prompt has got, while waiting for the first token. */
  progress?: Progress
}

function output(
  start: Start,
  content = '',
  reasoning = '',
  chunks = 0,
  reasoningChunks = 0,
  firstTokenAt?: number,
  tools: string[] = [],
  progress?: Progress,
): LiveOutput {
  const o = $state<LiveOutput>({ start, content, reasoning, chunks, reasoningChunks, firstTokenAt, lastAt: undefined, tools, progress })
  return o
}

/**
 * Generation speed so far, in tokens a second: about one token per chunk
 * after the first, over the time from the first chunk to the latest.
 */
export function liveOutSpeed(f: LiveOutput | undefined) {
  if (!f?.firstTokenAt || !f.lastAt || f.chunks < 2 || f.lastAt <= f.firstTokenAt) return null
  return (f.chunks - 1) / ((f.lastAt - f.firstTokenAt) / 1000)
}

const count = (v: number) => v.toLocaleString('en-US')

/** The prompt's tokens while the request waits for its first token, such as "8,192 of 34,302 tok (24%)". */
export function liveIn(p: Progress) {
  if (!p.total) return `${count(p.processed ?? 0)} tok so far`
  if (p.processed == null) return `${count(p.total)} tok`
  return `${count(p.processed)} of ${count(p.total)} tok (${Math.round((p.processed / p.total) * 100)}%)`
}

/** A longer form of liveIn, for a tooltip. */
export function liveInTitle(p: Progress) {
  if (!p.total) return `${count(p.processed ?? 0)} processed so far. The total is known when the request ends.`
  if (p.processed == null) return `The prompt is ${count(p.total)} tokens. The server doesn't say how much of it is read.`
  return `${count(p.processed)} of the prompt's ${count(p.total)} tokens read so far`
}

/** An event on the merged feed: this server's own, or another server's with its ID. */
export type FeedEvent = (LiveEvent & { server?: string }) | { type: 'peer_down'; server: string; at: number }

class Live {
  /** This server's requests in flight, by ID. */
  flights = new SvelteMap<number, LiveOutput>()
  /** Other servers' requests in flight: by server ID, then by request ID. */
  peers = new SvelteMap<string, SvelteMap<number, LiveOutput>>()
  connected = $state(false)
  private listeners = new Set<(e: LiveEvent) => void>()
  private allListeners = new Set<(e: FeedEvent) => void>()
  private source?: EventSource

  connect() {
    if (this.source) return
    // all=1 adds the other servers in the Servers list (internal/peers).
    const es = new EventSource('/notus/api/live?all=1')
    this.source = es
    es.onopen = () => (this.connected = true)
    // EventSource reconnects by itself; a fresh "hello" then resets the state.
    es.onerror = () => (this.connected = false)
    es.onmessage = (m) => this.handle(JSON.parse(m.data) as FeedEvent)
  }

  /** A server's requests in flight. '' is this server. */
  of(server: string) {
    return server ? this.peers.get(server) : this.flights
  }

  /** How many requests a server has in flight. '' is this server. */
  count(server: string) {
    return this.of(server)?.size ?? 0
  }

  /** How many requests every server has in flight. */
  get total() {
    let n = this.flights.size
    for (const f of this.peers.values()) n += f.size
    return n
  }

  /** Listen to this server's events. Returns a function that stops listening. */
  on(fn: (e: LiveEvent) => void) {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  /** Listen to every server's events. Returns a function that stops listening. */
  onAll(fn: (e: FeedEvent) => void) {
    this.allListeners.add(fn)
    return () => this.allListeners.delete(fn)
  }

  private handle(e: FeedEvent) {
    if (e.type === 'peer_down') {
      this.peers.delete(e.server)
    } else if (e.server) {
      let flights = this.peers.get(e.server)
      if (!flights) this.peers.set(e.server, (flights = new SvelteMap()))
      apply(flights, e)
    } else {
      // A fresh hello from this server means the feed started again, and
      // the other servers send their own hellos after it.
      if (e.type === 'hello') this.peers.clear()
      apply(this.flights, e)
      for (const fn of this.listeners) fn(e)
    }
    for (const fn of this.allListeners) fn(e)
  }
}

/** Applies one server's event to its requests in flight. */
function apply(flights: SvelteMap<number, LiveOutput>, e: LiveEvent) {
  switch (e.type) {
    case 'hello':
      flights.clear()
      for (const f of e.in_flight)
        flights.set(
          f.start.id,
          output(
            f.start,
            f.output.content,
            f.output.reasoning,
            f.chunks,
            f.reasoning_chunks,
            f.first_token_at,
            f.output.tool_calls.map((t) => t.name),
            f.progress,
          ),
        )
      break
    case 'start':
      flights.set(e.id, output(e.start))
      break
    case 'delta': {
      const f = flights.get(e.id)
      if (f) {
        f.content += e.content ?? ''
        f.reasoning += e.reasoning ?? ''
        f.chunks = e.chunks
        f.reasoningChunks = e.reasoning_chunks ?? 0
        f.firstTokenAt ??= e.at
        f.lastAt = e.at
        if (e.tools) f.tools.push(...e.tools)
      }
      break
    }
    case 'progress': {
      const f = flights.get(e.id)
      if (f) f.progress = e.progress
      break
    }
    case 'finish':
      flights.delete(e.id)
      break
  }
}

export const live = new Live()
