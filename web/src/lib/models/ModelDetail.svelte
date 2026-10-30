<script lang="ts">
  import { untrack } from 'svelte'
  import { listRequests, type Summary } from '../api'
  import { fixed, loadTime, num, pct, shortBuild, when } from '../format'
  import LogPanel from '../logs/LogPanel.svelte'
  import MarkPicker from '../MarkPicker.svelte'
  import { modelLabel, modelShow } from '../modelNames.svelte'
  import type { RunningModel } from '../status.svelte'
  import StatusPill from '../StatusPill.svelte'
  import { capWords, engineTitle, engineWords, type ModelProps } from './caps'
  import { cmdDiff, cmdLine } from './cmdDiff'
  import SpeedFilterPanel from './SpeedFilterPanel.svelte'
  import { loadSpeedFilter, sameFilter, type SpeedFilter } from './speedFilter'
  import type { DetailTab } from './tabs'

  // The parent owns the tab: the buttons on its row's second line pick it.
  let { model, known, props, picked = $bindable('log') }: { model: string; known?: RunningModel; props?: ModelProps; picked?: DetailTab } = $props()
  const caps = $derived(props?.caps ? capWords(props.caps) : null)

  interface ModelEvent {
    at: number
    from: string
    to: string
    load_ms?: number
    auto?: boolean
  }

  // One build and command, from store.BuildStat.
  interface BuildStat {
    build: string
    cmd_hash: string
    requests: number
    first_used: number
    last_used: number
    current: boolean
    gen_per_second: number | null
    gen_counted: number
    prompt_per_second: number | null
    prompt_counted: number
    gen_change: number | null
    gen_compared: number
    prompt_change: number | null
    prompt_compared: number
    /** Strata only: the median share of expert work done from the VRAM cache, over the output speed requests. */
    experts_in_vram: number | null
  }
  interface Builds {
    rows: BuildStat[]
    clients: { name: string; requests: number }[]
    requests: number
    gen_counted: number
    prompt_counted: number
  }

  // A model no longer in the config has no log to show.
  const tab = $derived(picked === 'log' && !known ? 'requests' : picked)
  let events = $state<ModelEvent[] | null>(null)
  let requests = $state<Summary[] | null>(null)
  let builds = $state<Builds | null>(null)
  let cmds = $state<Record<string, string[]>>({})
  let error = $state('')

  // Only a change of model refetches, not every change to the row it's in.
  const name = $derived(model)
  $effect(() => {
    const m = name
    fetch(`/notus/api/models/${encodeURIComponent(m)}/events?limit=30`)
      .then((r) => (r.ok ? r.json() : Promise.reject(r.statusText)))
      .then((e) => (events = e), (e) => (error = String(e)))
    listRequests({ model: m }, undefined, 15).then((r) => (requests = r.requests), (e) => (error = String(e)))
  })

  // Which requests count toward speeds. While the draft differs from the
  // saved filter, the table shows the draft as a preview.
  let saved = $state<SpeedFilter | null>(null)
  let draft = $state<SpeedFilter | null>(null)
  let defaults = $state<SpeedFilter | null>(null)
  let showFilter = $state(false)
  loadSpeedFilter().then(
    (f) => {
      saved = f.filter
      draft = structuredClone(f.filter)
      defaults = f.default
    },
    (e) => (error = `Couldn't load speed filters: ${e}`),
  )
  const preview = $derived(draft && saved && !sameFilter(draft, saved) ? JSON.stringify(draft) : '')

  // Refetches when the model or the previewed filter changes, a moment
  // after the last change. Answers to older fetches are dropped.
  let asked = 0
  $effect(() => {
    const m = name
    const q = preview ? `?filter=${encodeURIComponent(preview)}` : ''
    const n = ++asked
    const t = setTimeout(
      () =>
        fetch(`/notus/api/models/${encodeURIComponent(m)}/builds${q}`)
          .then((r) => (r.ok ? r.json() : r.text().then((t) => Promise.reject(t.trim() || r.statusText))))
          .then(
            (b: { builds: Builds; cmds: Record<string, string[]> }) => {
              if (n !== asked) return
              builds = b.builds
              cmds = b.cmds
            },
            (e) => n === asked && (error = String(e)),
          ),
      untrack(() => builds) ? 300 : 0,
    )
    return () => clearTimeout(t)
  })
  const rows = $derived(builds?.rows ?? [])
  const showExperts = $derived(rows.some((b) => b.experts_in_vram != null))
  const count = (n: number) => n.toLocaleString('en-US')

  // Commands are numbered 1, 2, 3 in the order the model first used them.
  const cmdNumber = $derived.by(() => {
    const order = [...rows].filter((b) => b.cmd_hash).sort((a, b) => a.first_used - b.first_used)
    const n = new Map<string, number>()
    for (const b of order) if (!n.has(b.cmd_hash)) n.set(b.cmd_hash, n.size + 1)
    return n
  })

  // The flags that changed from the row below to row i, when both commands are known.
  function changed(i: number) {
    const now = rows[i]?.cmd_hash
    const before = rows[i + 1]?.cmd_hash
    if (!now || !before || now === before || !cmds[now] || !cmds[before]) return []
    return cmdDiff(cmds[before], cmds[now])
  }

  const speedCols = [
    { key: 'gen', label: 'Output speed', digits: 1, kinds: 'client, prompt size range, and JSON or schema output' },
    { key: 'prompt', label: 'Prompt speed', digits: 0, kinds: 'new-token count range and total prompt size range' },
  ] as const
</script>

<!-- Indented to line up with the name and buttons of the model's row. -->
<div class="border-t border-line-soft bg-surface pt-2 pr-3 pb-3 pl-[32px]">
  {#if known && (known.aliases?.length || known.description || known.name)}
    <div class="py-1 text-xs text-muted">
      {#if known.name && known.name !== model}<span>{#if modelShow.show === 'name'}ID <code class="text-text">{model}</code>{:else}Name <span class="text-text">{known.name}</span>{/if}. </span>{/if}
      {#if known.aliases?.length}<span>Aliases: {known.aliases.join(', ')}. </span>{/if}
      {#if known.description}<em>{known.description}</em>{/if}
    </div>
  {/if}

  <!-- Context size and capabilities. llama-swap's number is the configured size; llama-server's is what one request can use. -->
  <div class="py-1 text-xs text-muted">
    {#if known?.context_length}<span>Context: <span class="text-text">{fixed(known.context_length)}</span> tokens. </span>{/if}
    {#if props}<span>Each request can use up to <span class="text-text">{fixed(props.n_ctx)}</span> tokens, with {fixed(props.slots)} {props.slots === 1 ? 'slot' : 'slots'}. </span>{/if}
    {#if caps}
      {#if caps.yes.length}<span>Supports <span class="text-text">{caps.yes.join(', ')}</span>. </span>{/if}
      {#if caps.no.length}<span>Doesn't support {caps.no.join(', ')}.</span>{/if}
    {:else}
      <span class="text-dim">Capabilities are read from llama-server when the model next loads.</span>
    {/if}
  </div>
  {#if props?.engine}
    <div class="py-1 text-xs text-muted" title={engineTitle(props.engine)}>
      Strata engine{props.build ? ` ${props.build.replace(/^Strata\s*/, '')}` : ''}: <span class="text-text">{engineWords(props.engine).join(', ')}</span>.
    </div>
  {/if}

  <div class="mb-2 flex items-center gap-2 py-1 text-xs text-muted">
    <span class="shrink-0">Colour and shape</span>
    <MarkPicker {model} />
  </div>

  {#if error}<div class="text-xs text-err">{error}</div>{/if}

  {#if tab === 'log' && known}
    <div class="h-[380px]">
      <LogPanel source="model:{model}" id="model">
        {#snippet title()}<span class="text-xs font-semibold">{modelLabel(model)}'s output</span>{/snippet}
      </LogPanel>
    </div>
    <p class="mt-1 text-[11px] text-dim">llama-swap keeps the newest 100 KB of each model's output. This log is empty until the model runs after llama-swap starts.</p>
  {:else if tab === 'loads'}
    {#if !events}
      <div class="text-xs text-muted">Loading…</div>
    {:else if !events.length}
      <div class="text-xs text-muted">No loads recorded since notus-swap was installed.</div>
    {:else}
      <table class="text-[12.5px] [&_td]:py-0.5 [&_td]:pr-5">
        <tbody>
          {#each events as e}
            <tr>
              <td class="text-muted">{when(e.at)}</td>
              <td>{e.from} → <b class="font-semibold">{e.to}</b>{#if e.auto}<span class="ml-1.5 text-dim" title="notus-swap started this load after the idle timeout">(default model)</span>{/if}</td>
              <td class="num text-muted">{e.load_ms != null ? `loaded in ${loadTime(e.load_ms)}` : ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {:else if tab === 'builds'}
    <div class="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
      <button
        class="inline-flex items-center gap-1.5 rounded border px-2 py-0.5 {showFilter ? 'border-primary text-primary' : 'border-line bg-panel2 hover:text-text'}"
        aria-expanded={showFilter}
        onclick={() => (showFilter = !showFilter)}
      >
        <!-- A gear: a ring with eight teeth, on a 12-pixel grid. -->
        <svg viewBox="0 0 12 12" class="block size-3" fill="currentColor" aria-hidden="true">
          <path fill-rule="evenodd" d="M5 0h2v2.1l1.4.6 1.5-1.5 1.4 1.4-1.5 1.5.6 1.4H12v2H9.9l-.6 1.4 1.5 1.5-1.4 1.4-1.5-1.5-1.4.6V12H5V9.9l-1.4-.6-1.5 1.5-1.4-1.4 1.5-1.5L1.6 7H0V5h1.6l.6-1.4L.7 2.1 2.1.7l1.5 1.5L5 1.6zM6 4a2 2 0 1 0 0 4a2 2 0 1 0 0-4z" />
        </svg>
        Speed filters
      </button>
      {#if builds}<span>{count(builds.gen_counted)} of {count(builds.requests)} finished requests count toward output speed, {count(builds.prompt_counted)} toward prompt speed.</span>{/if}
      {#if preview}<span class="text-warn">Showing unsaved changes.</span>{/if}
    </div>
    {#if showFilter && draft && saved && defaults}
      <SpeedFilterPanel bind:draft bind:saved {defaults} clients={builds?.clients ?? []} counts={builds} />
    {/if}
    {#if !builds}
      <div class="text-xs text-muted">Loading…</div>
    {:else if !rows.length}
      <div class="text-xs text-muted">No finished requests for this model yet.</div>
    {:else}
      <table class="text-[12.5px] [&_td]:py-0.5 [&_td]:pr-5 [&_td]:whitespace-nowrap [&_th]:pr-5 [&_th]:pb-1 [&_th]:text-left [&_th]:font-normal [&_th]:text-muted">
        <thead>
          <tr>
            <th>Build</th><th title="The command llama-swap started the model with">Settings</th><th>Used</th><th class="!text-right">Requests</th>
            {#each speedCols as c}
              <th class="!text-right" title="Median speed and number of included requests">{c.label}</th>
              <th title="Change from the row below, grouped by {c.kinds}">Change</th>
            {/each}
            {#if showExperts}<th class="!text-right" title="Strata only: the median share of the expert work while generating that was done from the VRAM cache, over the requests counted for output speed">Experts in VRAM</th>{/if}
          </tr>
        </thead>
        <tbody>
          {#each rows as b, i (b.build + '/' + b.cmd_hash)}
            {@const diff = changed(i)}
            <tr>
              <td class="font-mono" title={b.build}>
                {b.build ? shortBuild(b.build) : 'unknown'}
                {#if b.current}<span class="ml-1 rounded bg-primary/15 px-1 font-sans text-[10px] text-primary uppercase" title="Build and settings from the latest finished request with speed data. Used for speeds in the Models table.">current</span>{/if}
              </td>
              <td class="text-muted" title={cmds[b.cmd_hash] ? cmdLine(cmds[b.cmd_hash]) : undefined}>{b.cmd_hash ? `#${cmdNumber.get(b.cmd_hash)}` : 'unknown'}</td>
              <td class="text-muted">{when(b.first_used)} – {when(b.last_used)}</td>
              <td class="num text-right">{count(b.requests)}</td>
              {#each speedCols as c}
                {@const speed = b[`${c.key}_per_second`]}
                {@const counted = b[`${c.key}_counted`]}
                {@const ch = b[`${c.key}_change`]}
                {@const compared = b[`${c.key}_compared`]}
                <td class="num text-right" title="Median of {count(counted)} included requests">
                  {#if speed != null}{num(speed, c.digits)} tok/s <span class="text-[11px] text-dim">({count(counted)})</span>{:else}<span class="text-dim">–</span>{/if}
                </td>
                <td class="num">
                  {#if ch != null}
                    <span class="text-[11px] {ch >= 0 ? 'text-primary' : 'text-err'}" title="Based on {count(compared)} requests across this row and the row below, grouped by {c.kinds}">{ch >= 0 ? '+' : ''}{pct(ch)}</span>
                  {:else if i + 1 < rows.length && speed != null && rows[i + 1][`${c.key}_per_second`] != null}
                    <span class="text-[11px] text-dim" title="No matching group has at least 10 requests in both rows. Groups use {c.kinds}.">not comparable</span>
                  {/if}
                </td>
              {/each}
              {#if showExperts}<td class="num text-right">{#if b.experts_in_vram != null}{pct(b.experts_in_vram)}{:else}<span class="text-dim">–</span>{/if}</td>{/if}
            </tr>
            {#if diff.length}
              <tr>
                <td></td>
                <td colspan={showExperts ? 8 : 7} class="!whitespace-normal pb-1 text-[11.5px] text-muted">
                  Settings changed from the row below: <span class="font-mono text-text">{diff.join(', ')}</span>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
      <p class="mt-1 max-w-[900px] text-[11px] text-dim">
        Speeds are medians of included requests, with counts in parentheses. Changes compare matching groups in adjacent rows, with at least 10 requests per group in each row. Output speed groups use client, prompt size range, and JSON or schema output. Prompt speed groups use new-token and total prompt size ranges; cached tokens are excluded from the speed. Settings are recorded when the model next loads after a notus-swap update.
      </p>
      {#if Object.keys(cmds).length}
        <details class="mt-2 text-[11.5px]">
          <summary class="cursor-pointer text-muted hover:text-text">Commands</summary>
          {#each [...cmdNumber] as [hash, n] (hash)}
            {#if cmds[hash]}
              <div class="mt-1"><span class="text-muted">#{n}</span> <code class="break-all">{cmdLine(cmds[hash])}</code></div>
            {/if}
          {/each}
        </details>
      {/if}
    {/if}
  {:else if tab === 'requests'}
    {#if !requests}
      <div class="text-xs text-muted">Loading…</div>
    {:else if !requests.length}
      <div class="text-xs text-muted">No requests for this model yet.</div>
    {:else}
      <table class="w-full text-[12.5px] [&_td]:py-0.5 [&_td]:pr-4 [&_td]:whitespace-nowrap">
        <tbody>
          {#each requests as r (r.id)}
            <tr class="hover:bg-hover">
              <td><a data-nav class="text-primary hover:underline" href="/notus/requests?open={r.id}">#{r.id}</a></td>
              <td class="text-muted">{when(r.started_at)}</td>
              <td><StatusPill row={r} /></td>
              <td class="num text-right">{num(r.completion_tokens)} tok</td>
              <td class="num text-right">{num(r.predicted_per_second, 1)} tok/s</td>
              <td class="font-mono text-muted" title={r.build ?? ''}>{r.build ? shortBuild(r.build) : ''}</td>
              <td class="max-w-[320px] truncate text-muted">{r.preview}</td>
            </tr>
          {/each}
        </tbody>
      </table>
      <a data-nav class="mt-1 inline-block text-xs text-primary hover:underline" href="/notus/requests?model={encodeURIComponent(model)}">All of this model's requests →</a>
    {/if}
  {/if}
</div>
