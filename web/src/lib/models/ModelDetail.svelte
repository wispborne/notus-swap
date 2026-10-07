<script lang="ts">
  import { listRequests, type Summary } from '../api'
  import { duration, num, pct, shortBuild, when } from '../format'
  import LogPanel from '../logs/LogPanel.svelte'
  import type { RunningModel } from '../status.svelte'
  import StatusPill from '../StatusPill.svelte'
  import { cmdDiff, cmdLine } from './cmdDiff'

  let { model, known }: { model: string; known?: RunningModel } = $props()

  interface ModelEvent {
    at: number
    from: string
    to: string
    load_ms?: number
    auto?: boolean
  }

  interface BuildStat {
    build: string
    cmd_hash: string
    requests: number
    first_used: number
    last_used: number
    gen_per_second: number | null
    prompt_per_second: number | null
    ttft_ms: number | null
  }

  let picked = $state<'log' | 'loads' | 'builds' | 'requests'>('log')
  // A model no longer in the config has no log to show.
  const tab = $derived(picked === 'log' && !known ? 'requests' : picked)
  let events = $state<ModelEvent[] | null>(null)
  let requests = $state<Summary[] | null>(null)
  let builds = $state<BuildStat[] | null>(null)
  let cmds = $state<Record<string, string[]>>({})
  let error = $state('')

  // Only a change of model refetches, not every change to the row it's in.
  const name = $derived(model)
  $effect(() => {
    const m = name
    fetch(`/notus/api/models/${encodeURIComponent(m)}/events?limit=30`)
      .then((r) => (r.ok ? r.json() : Promise.reject(r.statusText)))
      .then((e) => (events = e), (e) => (error = String(e)))
    fetch(`/notus/api/models/${encodeURIComponent(m)}/builds`)
      .then((r) => (r.ok ? r.json() : Promise.reject(r.statusText)))
      .then(
        (b: { rows: BuildStat[]; cmds: Record<string, string[]> }) => {
          builds = b.rows
          cmds = b.cmds
        },
        (e) => (error = String(e)),
      )
    listRequests({ model: m }, undefined, 15).then((r) => (requests = r.requests), (e) => (error = String(e)))
  })

  // Commands are numbered 1, 2, 3 in the order the model first used them.
  const cmdNumber = $derived.by(() => {
    const order = [...(builds ?? [])].filter((b) => b.cmd_hash).sort((a, b) => a.first_used - b.first_used)
    const n = new Map<string, number>()
    for (const b of order) if (!n.has(b.cmd_hash)) n.set(b.cmd_hash, n.size + 1)
    return n
  })

  // The flags that changed from the row below to row i, when both commands are known.
  function changed(i: number) {
    const now = builds?.[i]?.cmd_hash
    const before = builds?.[i + 1]?.cmd_hash
    if (!now || !before || now === before || !cmds[now] || !cmds[before]) return []
    return cmdDiff(cmds[before], cmds[now])
  }

  // Compare speed with the previous build and command, in the row below.
  function change(i: number, key: 'gen_per_second' | 'prompt_per_second') {
    const now = builds?.[i]?.[key]
    const before = builds?.[i + 1]?.[key]
    if (now == null || before == null || !before) return null
    return now / before - 1
  }

  const tabBtn = (t: string) => `px-2.5 py-1 text-xs border-b-2 ${tab === t ? 'border-primary text-primary' : 'border-transparent text-muted hover:text-text'}`
</script>

<div class="border-t border-line-soft bg-surface px-3 pt-1 pb-3">
  {#if known && (known.aliases?.length || known.description || known.name)}
    <div class="py-1 text-xs text-muted">
      {#if known.name && known.name !== model}<span>ID <code class="text-text">{model}</code>. </span>{/if}
      {#if known.aliases?.length}<span>Aliases: {known.aliases.join(', ')}. </span>{/if}
      {#if known.description}<em>{known.description}</em>{/if}
    </div>
  {/if}

  <div class="mb-2 flex border-b border-line">
    {#if known}<button class={tabBtn('log')} onclick={() => (picked = 'log')}>Log</button>{/if}
    <button class={tabBtn('loads')} onclick={() => (picked = 'loads')}>Loads and unloads</button>
    <button class={tabBtn('builds')} onclick={() => (picked = 'builds')}>Speed by build and settings</button>
    <button class={tabBtn('requests')} onclick={() => (picked = 'requests')}>Recent requests</button>
  </div>

  {#if error}<div class="text-xs text-err">{error}</div>{/if}

  {#if tab === 'log' && known}
    <div class="h-[380px]">
      <LogPanel source="model:{model}" id="model">
        {#snippet title()}<span class="text-xs font-semibold">{model}'s output</span>{/snippet}
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
              <td class="num text-muted">{e.load_ms != null ? `loaded in ${duration(e.load_ms)}` : ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {:else if tab === 'builds'}
    {#if !builds}
      <div class="text-xs text-muted">Loading…</div>
    {:else if !builds.length}
      <div class="text-xs text-muted">No finished requests for this model yet.</div>
    {:else}
      <table class="text-[12.5px] [&_td]:py-0.5 [&_td]:pr-5 [&_td]:whitespace-nowrap [&_th]:pr-5 [&_th]:pb-1 [&_th]:text-left [&_th]:font-normal [&_th]:text-muted">
        <thead>
          <tr><th>Build</th><th title="The command llama-swap started the model with">Settings</th><th>Used</th><th class="!text-right">Requests</th><th class="!text-right">Output speed</th><th class="!text-right">Prompt speed</th><th class="!text-right">Avg. time to first token</th></tr>
        </thead>
        <tbody>
          {#each builds as b, i (b.build + '/' + b.cmd_hash)}
            {@const diff = changed(i)}
            <tr>
              <td class="font-mono" title={b.build}>{b.build ? shortBuild(b.build) : 'unknown'}</td>
              <td class="text-muted" title={cmds[b.cmd_hash] ? cmdLine(cmds[b.cmd_hash]) : undefined}>{b.cmd_hash ? `#${cmdNumber.get(b.cmd_hash)}` : 'unknown'}</td>
              <td class="text-muted">{when(b.first_used)} – {when(b.last_used)}</td>
              <td class="num text-right">{b.requests}</td>
              {#each ['gen_per_second', 'prompt_per_second'] as const as key}
                {@const c = change(i, key)}
                <td class="num text-right">
                  {num(b[key], key === 'gen_per_second' ? 1 : 0)} tok/s
                  {#if c != null}<span class="ml-1 text-[11px] {c >= 0 ? 'text-primary' : 'text-err'}">{c >= 0 ? '+' : ''}{pct(c)}</span>{/if}
                </td>
              {/each}
              <td class="num text-right">{duration(b.ttft_ms)}</td>
            </tr>
            {#if diff.length}
              <tr>
                <td></td>
                <td colspan="6" class="!whitespace-normal pb-1 text-[11.5px] text-muted">
                  Settings changed from the row below: <span class="font-mono text-text">{diff.join(', ')}</span>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
      <p class="mt-1 text-[11px] text-dim">
        Finished requests only. Speeds and time to first token leave out requests that were more than half cached. Speeds use total tokens divided by total time; changes compare with the row below. Prompt speed excludes cached tokens. Prompt length affects speed, so compare similar requests. Settings are recorded when the model next loads after a notus-swap update.
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
