<script lang="ts">
  import type { Snippet } from 'svelte'
  import { privacy } from '../privacy.svelte'
  import { ansiToHtml } from './ansi'
  import { LogStream, sourceURL, type LogSource } from './stream.svelte'

  // One live log, with a filter, line wrapping and text size. `id` keys the
  // saved wrap and size choices; `title` sits in the header (a source picker
  // on the Logs page).
  let { source, id, title }: { source: LogSource; id: string; title: Snippet } = $props()

  const allowed = $derived(privacy.logSource(source))
  // A derived only signals when the text changes. Reading `source` straight
  // in the effect would reconnect whenever the parent's row object changes.
  const url = $derived(allowed ? sourceURL(source) : null)

  let stream = $state.raw<LogStream | null>(null)
  $effect(() => {
    if (!url) {
      stream = null
      return
    }
    const s = new LogStream(url)
    stream = s
    s.start()
    return () => s.stop()
  })

  // ---- Saved per panel ----
  function saved<T>(key: string, fallback: T): T {
    try {
      const v = localStorage.getItem(`log-${id}-${key}`)
      return v == null ? fallback : JSON.parse(v)
    } catch {
      return fallback
    }
  }
  function save(key: string, v: unknown) {
    try {
      localStorage.setItem(`log-${id}-${key}`, JSON.stringify(v))
    } catch {}
  }
  const sizes = [11, 12, 13.5]
  let wrap = $state(saved('wrap', false))
  let size = $state(saved('size', 1))

  // ---- Filter ----
  let filter = $state('')
  const matcher = $derived.by(() => {
    const f = filter.trim()
    if (!f) return { test: (_: string) => true, bad: false }
    try {
      const re = new RegExp(f, 'i')
      return { test: (l: string) => re.test(l), bad: false }
    } catch {
      const lower = f.toLowerCase()
      return { test: (l: string) => l.toLowerCase().includes(lower), bad: true }
    }
  })

  const shown = $derived.by(() => {
    if (!stream) return { html: '', count: 0 }
    const lines = stream.lines.filter((l) => privacy.logLine(l) && matcher.test(l))
    const p = stream.partial
    if (p && privacy.logLine(p) && matcher.test(p)) lines.push(p)
    return { html: ansiToHtml(lines.join('\n')), count: lines.length }
  })

  // ---- Scrolling: follow new output unless scrolled up ----
  let pre = $state<HTMLPreElement>()
  let following = $state(true)
  function onscroll() {
    if (!pre) return
    following = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40
  }
  function toEnd() {
    if (pre) pre.scrollTop = pre.scrollHeight
    following = true
  }
  $effect(() => {
    void shown.html
    if (following && pre) pre.scrollTop = pre.scrollHeight
  })

  const tool = 'rounded border border-line px-1.5 text-[11px] leading-5 hover:border-primary'
</script>

<div class="flex h-full min-h-0 flex-col overflow-hidden rounded-lg border border-line bg-panel">
  <div class="flex flex-wrap items-center gap-1.5 border-b border-line px-2 py-1">
    {@render title()}
    {#if stream}
      <span
        class="size-2 flex-none rounded-full {stream.connected ? 'bg-primary' : 'pulse bg-warn'}"
        title={stream.connected ? 'Connected, new lines show as they arrive' : `${stream.error || 'Connecting'}. Reconnecting in ${stream.retryIn} seconds.`}
      ></span>
    {/if}
    <span class="flex-1"></span>
    <input
      bind:value={filter}
      placeholder="Filter logs"
      class="w-40 rounded border bg-panel2 px-1.5 text-xs leading-5 outline-none focus:border-primary {matcher.bad ? 'border-warn' : 'border-line'}"
      title={matcher.bad ? 'Invalid regex; matching literal text.' : 'Case-insensitive. Supports regex.'}
    />
    <button class="{tool} {wrap ? 'border-primary text-primary' : 'text-muted'}" onclick={() => save('wrap', (wrap = !wrap))} title="Wrap long lines">Wrap</button>
    <button class="{tool} text-muted" onclick={() => save('size', (size = (size + 1) % sizes.length))} title="Text size">A{['−', '', '+'][size]}</button>
    <button class="{tool} text-muted" onclick={() => stream?.clear()} title="Clear this panel; new lines will still appear.">Clear</button>
  </div>

  <div class="relative min-h-0 flex-1">
    {#if !allowed}
      <div class="p-4 text-[12.5px] text-muted">
        {source === 'upstream'
          ? 'Combined logs may reveal hidden models. Choose one model or enable “Show hidden models” on the Settings page.'
          : 'This model is hidden.'}
      </div>
    {:else}
      <pre
        bind:this={pre}
        {onscroll}
        class="h-full overflow-auto bg-sunken px-2.5 py-1.5 font-mono leading-[1.45] {wrap ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'}"
        style="font-size:{sizes[size]}px">{@html shown.html}</pre>
      {#if stream && !stream.lines.length && !stream.partial}
        <div class="pointer-events-none absolute inset-0 flex items-center justify-center text-[12.5px] text-dim">
          {stream.connected ? 'Nothing logged yet.' : stream.error ? `${stream.error}. Reconnecting in ${stream.retryIn} seconds.` : 'Connecting…'}
        </div>
      {/if}
      {#if !following}
        <button class="absolute right-4 bottom-3 rounded-md border border-line bg-panel2 px-2 py-0.5 text-xs shadow hover:border-primary" onclick={toEnd}>
          ↓ Jump to latest
        </button>
      {/if}
    {/if}
  </div>
  {#if filter.trim() && stream}
    <div class="border-t border-line px-2 py-0.5 text-[11px] text-dim">{shown.count} of {stream.lines.length} lines match</div>
  {/if}
</div>
