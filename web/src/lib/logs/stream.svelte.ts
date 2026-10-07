// Reads a plain-text log stream (llama-swap's /logs/stream/..., or
// notus-swap's /notus/api/logs/stream). Each connection starts with the
// history the server keeps, then sends new text as it is written. When the
// stream ends, for example because llama-swap restarted, it reconnects.

/** Where a log panel reads from. */
export type LogSource = 'proxy' | 'upstream' | 'notus' | `model:${string}`

export function sourceURL(s: LogSource) {
  if (s === 'notus') return '/notus/api/logs/stream'
  if (s === 'proxy' || s === 'upstream') return `/logs/stream/${s}`
  // Model IDs can hold slashes, which llama-swap expects as they are.
  return `/logs/stream/${s.slice(6).split('/').map(encodeURIComponent).join('/')}`
}

/** The newest lines kept in the browser. llama-swap itself keeps 100 KB. */
const MAX_LINES = 10_000
// The wait between tries doubles while they fail, up to 30 s. Each failed
// try adds a "llama-swap unreachable" line to notus-swap's log.
const RETRY_FIRST = 3
const RETRY_MAX = 30

export class LogStream {
  /** Whole lines, oldest first. */
  lines = $state.raw<string[]>([])
  /** An incomplete last line, shown until its line break arrives. */
  partial = $state('')
  connected = $state(false)
  error = $state('')
  /** Seconds until the next try, after a failure. */
  retryIn = $state(RETRY_FIRST)

  private abort?: AbortController
  private retry?: ReturnType<typeof setTimeout>
  private pending: string[] = []
  private pendingPartial = ''
  private flushTimer?: ReturnType<typeof setTimeout>

  constructor(private url: string) {}

  start() {
    this.stop()
    const ac = (this.abort = new AbortController())
    this.read(ac).catch(() => {})
  }

  stop() {
    this.abort?.abort()
    clearTimeout(this.retry)
    clearTimeout(this.flushTimer)
    this.flushTimer = undefined
  }

  /** Empties the panel. New text still arrives. */
  clear() {
    this.lines = []
    this.partial = ''
    this.pending = []
  }

  private async read(ac: AbortController) {
    try {
      const res = await fetch(this.url, { signal: ac.signal, cache: 'no-store' })
      if (!res.ok || !res.body) {
        const text = (await res.text()).trim()
        throw new Error(res.status === 503 ? "llama-swap isn't answering" : `${res.status}: ${text.slice(0, 200)}`)
      }
      // Each connection sends the history again, so start over.
      this.pending = []
      this.pendingPartial = ''
      this.lines = []
      this.partial = ''
      this.connected = true
      this.error = ''
      this.retryIn = RETRY_FIRST
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        this.add(value)
      }
      this.error = 'The stream ended'
    } catch (e) {
      if (ac.signal.aborted) return
      this.error = e instanceof Error ? e.message : String(e)
    }
    this.connected = false
    if (ac.signal.aborted) return
    const wait = this.retryIn
    this.retry = setTimeout(() => {
      this.retryIn = Math.min(wait * 2, RETRY_MAX)
      this.read(ac)
    }, wait * 1000)
  }

  // Text is batched and shown at most four times a second, so a burst of
  // output doesn't re-render the panel for every chunk.
  private add(text: string) {
    const parts = (this.pendingPartial + text.replace(/\r\n?/g, '\n')).split('\n')
    this.pendingPartial = parts.pop()!
    this.pending.push(...parts)
    if (!this.flushTimer) this.flushTimer = setTimeout(() => this.flush(), 250)
  }

  private flush() {
    this.flushTimer = undefined
    if (this.pending.length) {
      const next = this.lines.concat(this.pending)
      this.lines = next.length > MAX_LINES ? next.slice(-MAX_LINES) : next
      this.pending = []
    }
    this.partial = this.pendingPartial
  }
}
