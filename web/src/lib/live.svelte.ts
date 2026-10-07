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

class Live {
  flights = new SvelteMap<number, LiveOutput>()
  connected = $state(false)
  private listeners = new Set<(e: LiveEvent) => void>()
  private source?: EventSource

  connect() {
    if (this.source) return
    const es = new EventSource('/notus/api/live')
    this.source = es
    es.onopen = () => (this.connected = true)
    // EventSource reconnects by itself; a fresh "hello" then resets the state.
    es.onerror = () => (this.connected = false)
    es.onmessage = (m) => this.handle(JSON.parse(m.data) as LiveEvent)
  }

  /** Listen to every event. Returns a function that stops listening. */
  on(fn: (e: LiveEvent) => void) {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  private handle(e: LiveEvent) {
    switch (e.type) {
      case 'hello':
        this.flights.clear()
        for (const f of e.in_flight)
          this.flights.set(
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
        this.flights.set(e.id, output(e.start))
        break
      case 'delta': {
        const f = this.flights.get(e.id)
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
        const f = this.flights.get(e.id)
        if (f) f.progress = e.progress
        break
      }
      case 'finish':
        this.flights.delete(e.id)
        break
    }
    for (const fn of this.listeners) fn(e)
  }
}

export const live = new Live()
