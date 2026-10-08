<script lang="ts">
  import { cancelRequest, getRequest, retryRequest, type Detail, type Summary } from './api'
  import { bytes, duration, joules, modelColor, num, pct, shortBuild, when } from './format'
  import { describe, issueMutes } from './issues.svelte'
  import CopyButton from './CopyButton.svelte'
  import { live, liveOutSpeed } from './live.svelte'
  import Markdown from './Markdown.svelte'
  import { modelLabel, modelTitle } from './modelNames.svelte'
  import RequestMessages from './RequestMessages.svelte'
  import StatusPill from './StatusPill.svelte'
  import { sourceLabel, sourceTitle } from './source'

  let { row, onopen }: { row: Summary; onopen?: (id: number) => void } = $props()

  let detail = $state<Detail | null>(null)
  let error = $state('')
  let tab = $state<'side' | 'request' | 'response'>('side')
  let now = $state(Date.now())

  // Fetch on open, and again when the request finishes.
  $effect(() => {
    const id = row.id
    void row.state
    void issueMutes.version
    getRequest(id).then(
      (d) => (detail = d),
      (e) => (error = String(e)),
    )
  })

  const flight = $derived(row.state === 'in_flight' ? live.flights.get(row.id) : undefined)
  const inFlight = $derived(row.state === 'in_flight')

  // Tick while in flight, so the durations grow.
  $effect(() => {
    if (!inFlight) return
    const t = setInterval(() => (now = Date.now()), 500)
    return () => clearInterval(t)
  })

  const content = $derived(flight ? flight.content : (detail?.response.content ?? ''))
  const reasoning = $derived(flight ? flight.reasoning : (detail?.response.reasoning ?? ''))
  // From the thinking tag once finished. While in flight, the chunks that
  // carried thinking, which is close to one token each.
  const reasoningTokens = $derived.by(() => {
    if (flight) return flight.reasoningChunks ? { n: flight.reasoningChunks, est: true } : null
    const t = row.tags?.find((t) => t.kind === 'thinking')
    return t?.tokens ? { n: t.tokens, est: !!t.est } : null
  })
  const firstToken =$derived(flight?.firstTokenAt ?? row.first_token_at ?? detail?.first_token_at ?? null)
  const end = $derived(row.finished_at ?? now)
  const ttft = $derived(firstToken ? firstToken - row.started_at : null)
  const outTokens = $derived(flight ? flight.chunks : row.completion_tokens)
  const genSpeed = $derived(row.predicted_per_second ?? liveOutSpeed(flight))
  const progress = $derived(flight?.progress)
  const tok = (v: number) => `${v.toLocaleString('en-US')} tok`

  // Timing bar: queued behind other requests, waiting (model load), prompt
  // processing, generation. Older requests may not have a queued time.
  const timing = $derived.by(() => {
    const total = end - row.started_at
    if (total <= 0) return []
    const untilFirst = (firstToken ?? end) - row.started_at
    const prompt = row.prompt_ms != null ? Math.min(row.prompt_ms, untilFirst) : null
    const waiting = prompt != null ? untilFirst - prompt : 0
    const queued = row.queued_ms != null ? Math.min(row.queued_ms, waiting) : null
    const segs = [
      queued ? { label: 'queued', ms: queued, color: 'bg-warn/70' } : null,
      prompt != null
        ? { label: 'waiting / model load', ms: waiting - (queued ?? 0), color: 'bg-violet/70' }
        : null,
      prompt != null
        ? { label: 'prompt', ms: prompt, color: 'bg-secondary' }
        : { label: firstToken ? 'until first token' : 'waiting', ms: untilFirst, color: 'bg-secondary' },
      firstToken ? { label: 'generation', ms: end - firstToken, color: 'bg-primary' } : null,
    ].filter((s) => s && s.ms > 0) as { label: string; ms: number; color: string }[]
    return segs.map((s) => ({ ...s, pct: (s.ms / total) * 100 }))
  })

  const chips = $derived(
    [
      ['TTFT', duration(ttft)],
      ['Queued', row.queued_ms ? duration(row.queued_ms) : null],
      ['In', row.prompt_tokens != null ? tok(row.prompt_tokens) : progress ? `${tok(progress.processed)} so far` : null],
      [
        'Cached',
        row.cached_tokens != null
          ? `${tok(row.cached_tokens)}${row.prompt_tokens ? ` (${pct(row.cached_tokens / row.prompt_tokens)})` : ''}`
          : progress
            ? `${tok(progress.cache)} so far`
            : null,
      ],
      ['Out', outTokens != null ? `${outTokens} tok` : null],
      ['Context', row.n_ctx != null ? `${row.n_ctx.toLocaleString('en-US')} tok` : null],
      [
        'In speed',
        row.prompt_per_second != null
          ? `${num(row.prompt_per_second)} tok/s`
          : progress?.per_second != null
            ? `${num(progress.per_second)} tok/s so far`
            : null,
      ],
      ['Out speed', genSpeed != null ? `${num(genSpeed, 1)} tok/s${row.predicted_per_second == null ? ' so far' : ''}` : null],
      ['Duration', duration(end - row.started_at)],
      ['Energy', row.energy_j != null ? joules(row.energy_j) : null],
      ['Per token', row.energy_j != null && row.completion_tokens ? `${(row.energy_j / row.completion_tokens).toFixed(2)} J` : null],
      ['Sent', bytes(row.request_bytes)],
      ['Received', row.response_bytes ? bytes(row.response_bytes) : null],
    ].filter(([, v]) => v != null) as [string, string][],
  )

  function curl() {
    const body = JSON.stringify(detail?.request ?? {}).replaceAll("'", "'\\''")
    const cmd = `curl ${location.origin}${row.path} -H 'Content-Type: application/json' -d '${body}'`
    navigator.clipboard.writeText(cmd)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }
  let copied = $state(false)

  let busy = $state(false)
  let actionError = $state('')

  async function cancel() {
    if (!confirm(`Cancel request #${row.id}? Its client gets an error, or a stream that stops.`)) return
    busy = true
    actionError = ''
    try {
      await cancelRequest(row.id)
    } catch (e) {
      actionError = `Could not cancel: ${(e as Error).message}`
    } finally {
      busy = false
    }
  }

  // The new request streams into the list, and opens in place of this one.
  async function retry() {
    busy = true
    actionError = ''
    try {
      onopen?.(await retryRequest(row.id))
    } catch (e) {
      actionError = `Could not retry: ${(e as Error).message}`
    } finally {
      busy = false
    }
  }

  const issues = $derived((detail?.issue_details ?? []).filter((i) => !i.muted))
  const muted = $derived((detail?.issue_details ?? []).filter((i) => i.muted))

  function pretty(v: unknown) {
    return typeof v === 'string' ? v : JSON.stringify(v, null, 2)
  }

  // Tool call arguments are a JSON string; show them indented when they parse.
  function args(s: string) {
    try {
      return pretty(JSON.parse(s))
    } catch {
      return s
    }
  }
</script>

<div class="max-w-[1300px]">
  <div class="mb-2 flex flex-wrap items-center gap-2">
    <span class="font-mono text-[15px] font-semibold">#{row.id}</span>
    <StatusPill {row} waiting={inFlight && !firstToken} />
    <span class="inline-block size-2 rounded-full" style="background:{modelColor(row.model)}"></span>
    <span title={modelTitle(row.model)}>{modelLabel(row.model) || '(no model)'}</span>
    {#if row.build}<span class="font-mono text-xs text-muted" title="Server build {row.build}">{shortBuild(row.build)}</span>{/if}
    <span class="text-muted">{when(row.started_at)}</span>
    <span class="font-mono text-xs text-dim">{row.method} {row.path}</span>
    {#if row.user_agent || row.client_ip}
      <span class="text-xs text-muted" title={sourceTitle(row)}>from {sourceLabel(row) || 'unknown client'}{#if row.client_ip}<span class="ml-1 font-mono text-dim">{row.client_ip}</span>{/if}</span>
    {/if}
    {#if row.retry_of}
      <span class="text-xs text-muted">
        retry of <button class="font-mono text-primary hover:underline" onclick={() => onopen?.(row.retry_of!)}>#{row.retry_of}</button>
      </span>
    {/if}
    <span class="flex-1"></span>
    {#if inFlight}
      <button class="rounded-md border border-line bg-panel2 px-2 py-0.5 text-xs hover:border-err hover:text-err" onclick={cancel} disabled={busy}>
        Cancel
      </button>
    {:else}
      <button
        class="rounded-md border border-line bg-panel2 px-2 py-0.5 text-xs hover:border-primary disabled:opacity-50 disabled:hover:border-line"
        onclick={retry}
        disabled={busy || !row.has_bodies}
        title={row.has_bodies ? 'Send this request again. The answer shows as a new request.' : 'The request body has been deleted, so it cannot be sent again.'}
      >
        Retry
      </button>
    {/if}
    <button class="rounded-md border border-line bg-panel2 px-2 py-0.5 text-xs hover:border-primary" onclick={curl} disabled={!detail}>
      {copied ? 'Copied' : 'Copy as curl'}
    </button>
  </div>

  {#if actionError}
    <div class="mb-2 rounded-md border border-l-3 border-line border-l-err bg-panel2 px-2.5 py-1.5 text-xs">{actionError}</div>
  {/if}

  {#each issues as issue (issue.kind)}
    {@const d = describe(issue)}
    <div class="mb-2 rounded-md border border-l-3 border-line bg-panel2 px-2.5 py-1.5 text-xs {issue.level === 'warning' ? 'border-l-warn' : 'border-l-secondary'}">
      <div class="flex flex-wrap items-baseline gap-x-2">
        <b class={issue.level === 'warning' ? 'text-warn' : 'text-secondary'}>{d.title}</b>
        <span class="text-muted">{d.what}</span>
      </div>
      <div class="mt-0.5 flex flex-wrap items-baseline gap-x-2">
        <span>{d.fix}</span>
        {#if d.config}<a data-nav class="text-primary hover:underline" href="/notus/config">Open Model Config</a>{/if}
        <span class="flex-1"></span>
        <button class="text-dim hover:text-text" onclick={() => issueMutes.set(issue.kind, row.model, true)}>
          Don't flag this for {row.model || 'requests with no model'}
        </button>
      </div>
    </div>
  {/each}
  {#if muted.length}
    <div class="mb-2 flex flex-wrap items-baseline gap-x-2 text-xs text-dim">
      Muted for this model:
      {#each muted as issue (issue.kind)}
        <span>{describe(issue).title} <button class="text-primary hover:underline" onclick={() => issueMutes.set(issue.kind, row.model, false)}>Unmute</button></span>
      {/each}
    </div>
  {/if}

  <div class="flex flex-wrap gap-1.5">
    {#each chips as [k, v]}
      <span class="rounded-md border border-line bg-panel2 px-2 py-0.5 text-xs"><span class="mr-1 text-dim">{k}</span><b class="num font-semibold">{v}</b></span>
    {/each}
  </div>

  {#if timing.length}
    <div class="mt-2 flex h-4 overflow-hidden rounded text-[10px] text-sunken">
      {#each timing as s}
        <div class="flex items-center justify-center overflow-hidden whitespace-nowrap {s.color}" style="width:{s.pct}%" title="{s.label} {duration(s.ms)}">
          {s.pct > 14 ? `${s.label} ${duration(s.ms)}` : ''}
        </div>
      {/each}
    </div>
  {/if}

  {#if row.error}
    <div class="mt-2 rounded-md border border-l-3 border-line border-l-err bg-panel2 px-2.5 py-1.5 text-xs">{row.error}</div>
  {/if}

  <div class="mt-2.5 mb-2.5 flex gap-0.5 border-b border-line">
    {#each [['side', 'Side by side'], ['request', 'Raw request'], ['response', 'Raw response']] as [k, label]}
      <button
        class="-mb-px border-b-2 px-2.5 py-1 {tab === k ? 'border-primary text-text' : 'border-transparent text-muted hover:text-text'}"
        onclick={() => (tab = k as typeof tab)}>{label}</button
      >
    {/each}
  </div>

  {#if error}
    <div class="text-err">{error}</div>
  {:else if !detail}
    <div class="text-muted">Loading…</div>
  {:else if !detail.has_bodies}
    <div class="text-muted">Request and response text was deleted to meet the retention limits. Timing and token counts are still available.</div>
  {:else if tab === 'side'}
    <div class="grid gap-3 md:grid-cols-2">
      <div class="min-w-0">
        <div class="mb-1.5 flex items-center gap-1 text-[11px] tracking-wider text-dim uppercase">
          Request <CopyButton title="Copy the full request (JSON)" text={() => pretty(detail?.request)} />
        </div>
        <RequestMessages
          request={detail.request}
          cached={row.cached_tokens != null && row.prompt_tokens ? { tokens: row.cached_tokens, total: row.prompt_tokens } : null}
          promptTokens={row.prompt_tokens}
        />
      </div>
      <div class="min-w-0">
        <div class="mb-1.5 flex items-center gap-1 text-[11px] tracking-wider text-dim uppercase">
          Response
          {#if !inFlight && detail.response_raw}<CopyButton title="Copy the full response (raw)" text={() => detail?.response_raw ?? ''} />{/if}
        </div>
        {#if reasoning}
          <details class="mb-2 rounded-md border border-dashed border-line px-2 py-1 text-muted" open={inFlight && !content}>
            <summary class="cursor-pointer text-xs">
              Reasoning ({reasoning.length.toLocaleString()} chars{#if reasoningTokens}, <span
                  title={reasoningTokens.est ? 'Estimated: the server did not report thinking tokens separately' : undefined}
                  >{reasoningTokens.est ? '≈' : ''}{reasoningTokens.n.toLocaleString()}t</span
                >{/if})
              <CopyButton class="align-middle" title="Copy reasoning" text={() => reasoning} />
            </summary>
            <div class="mt-1 text-xs whitespace-pre-wrap">{reasoning}</div>
          </details>
        {/if}
        {#each detail.response.tool_calls as tc}
          <div class="mb-2 rounded-lg border border-l-3 border-line border-l-warn bg-panel2 px-2.5 py-2">
            <div class="mb-0.5 flex items-center justify-between text-[10px] tracking-wider text-dim uppercase">
              tool call · {tc.name}
              <CopyButton title="Copy arguments" text={() => args(tc.arguments)} />
            </div>
            <pre class="overflow-auto rounded bg-sunken p-2 text-xs">{args(tc.arguments)}</pre>
          </div>
        {/each}
        {#if content || inFlight}
          <div class="mb-2 rounded-lg border border-l-3 border-line border-l-primary bg-panel2 px-2.5 py-2">
            <div class="mb-0.5 flex items-center justify-between text-[10px] tracking-wider text-dim uppercase">
              assistant
              <CopyButton title="Copy answer" text={() => content} />
            </div>
            <Markdown text={content} cursor={inFlight} />
          </div>
        {/if}
        {#if detail.response.finish_reason}
          <div class="text-xs text-dim">finish reason: {detail.response.finish_reason}</div>
        {/if}
        {#if detail.truncated}
          <div class="text-xs text-warn">This body was larger than 32 MB, so only the start is stored.</div>
        {/if}
      </div>
    </div>
  {:else}
    {@const raw = tab === 'request' ? pretty(detail.request) : inFlight ? '' : detail.response_raw}
    <div class="relative">
      <pre class="max-h-[520px] overflow-auto rounded-md border border-line bg-sunken p-2.5 text-xs">{raw ||
          (inFlight ? '(still streaming; the raw response is shown once it finishes)' : '(empty)')}</pre>
      {#if raw}
        <CopyButton class="absolute top-1.5 right-4 bg-sunken" title="Copy the full {tab}" text={() => raw} />
      {/if}
    </div>
  {/if}
</div>
