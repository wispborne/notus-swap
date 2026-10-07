<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Markdown from '../Markdown.svelte'
  import Changelog, { type Entry } from './Changelog.svelte'
  import { bytes, when } from '../format'
  import { status } from '../status.svelte'

  interface Release {
    tag: string
    notes: string
    url: string
    published: string
  }
  interface Job {
    component: 'llama-swap' | 'llama.cpp'
    action: 'install' | 'rollback'
    target?: string
    step: string
    done: number
    total: number
    running: boolean
    result?: string
    error?: string
    finished?: string
  }
  interface Info {
    llama_swap: {
      off?: string
      bin?: string
      installed: string
      has_backup: boolean
      backup?: string
      latest?: Release
      newer: boolean
      check_error?: string
    }
    llama_cpp: {
      off?: string
      dir?: string
      flavor?: string
      installed: string
      previous?: string
      release?: Release
      release_build?: string
      nightly?: Release
      newer: boolean
      problem?: string
      check_error?: string
    }
    checked_at?: string
    job?: Job
  }

  let info = $state<Info | null>(null)
  let error = $state('')
  let checking = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  async function load(url = '/notus/api/llama-updates', method = 'GET') {
    try {
      const res = await fetch(url, { method })
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      info = await res.json()
      error = ''
    } catch (e) {
      error = e instanceof Error ? e.message : String(e)
    }
    clearTimeout(timer)
    if (info?.job?.running) timer = setTimeout(() => load(), 1000)
  }
  onMount(() => load())
  onDestroy(() => clearTimeout(timer))

  async function check() {
    checking = true
    await load('/notus/api/llama-updates/check', 'POST')
    checking = false
  }

  async function start(component: Job['component'], action: Job['action'], tag = '', question = '') {
    if (question && !confirm(question)) return
    const res = await fetch(`/notus/api/llama-updates/${component}/${action}`, { method: 'POST', body: JSON.stringify({ tag }) })
    if (!res.ok) {
      error = (await res.text()).trim() || res.statusText
      return
    }
    await load()
  }

  // The open changelog, fetched from GitHub when its button is first pressed.
  let shown = $state<Job['component'] | null>(null)
  let logs = $state<Partial<Record<Job['component'], Entry[]>>>({})
  let logError = $state('')
  async function toggleChangelog(component: Job['component']) {
    shown = shown === component ? null : component
    if (!shown || logs[component]) return
    logError = ''
    try {
      const res = await fetch(`/notus/api/llama-updates/${component}/changelog`)
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
      logs[component] = await res.json()
    } catch (e) {
      logError = e instanceof Error ? e.message : String(e)
    }
  }

  let unloaded = $state(false)
  async function unloadAll() {
    await fetch('/notus/api/models/unload', { method: 'POST' })
    unloaded = true
  }

  const job = $derived(info?.job)
  const busy = $derived(!!job?.running)
  const swap = $derived(info?.llama_swap)
  const cpp = $derived(info?.llama_cpp)
  const loadedModels = $derived(status.current?.llama_swap.models.length ?? 0)
  const checked = $derived(info?.checked_at ? `GitHub was last checked at ${when(Date.parse(info.checked_at))}, and is checked every hour.` : '')

  /** "today", or a date such as "Sep 23". */
  function day(iso: string) {
    const d = new Date(iso)
    return d.toDateString() === new Date().toDateString() ? 'today' : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  }

  /** "v258" and "b11146" as numbers, to tell a roll back from a switch forward. */
  const num = (tag?: string) => Number(tag?.replace(/^[vb]/, '')) || 0
  const backLabel = (to: string | undefined, from: string) => (!to ? 'Roll back' : num(to) > num(from) ? `Switch back to ${to}` : `Roll back to ${to}`)

  /** A nightly build is new when its number is higher than the build in use. A weekly release is new when the build it was made from is. */
  function cppIsNew(tag: string) {
    if (!cpp?.installed) return false
    if (/^b\d+$/.test(tag)) return num(tag) > num(cpp.installed)
    return tag === cpp.release?.tag && cpp.newer
  }

  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50 disabled:hover:border-line'
  const primary = 'rounded-md border border-primary bg-primary px-3 py-1 font-semibold text-sunken disabled:opacity-40'
  const section = 'mb-4 rounded-lg border border-line bg-panel p-4'
</script>

{#snippet notes(r: Release)}
  <details class="mt-2 rounded-md border border-line bg-panel2 text-[12.5px]">
    <summary class="cursor-pointer px-3 py-1.5 text-muted hover:text-text">Release notes for {r.tag}</summary>
    {#if r.notes}<div class="max-h-72 overflow-auto border-t border-line px-3 py-2"><Markdown text={r.notes} /></div>{/if}
    <a href={r.url} target="_blank" rel="noreferrer" class="block border-t border-line px-3 py-1.5 text-primary">Full notes on GitHub ↗</a>
  </details>
{/snippet}

{#snippet jobBox(j: Job)}
  <div
    class="mt-3 rounded-md border border-l-3 border-line bg-panel2 px-3 py-2 text-[12.5px]
      {j.running ? 'border-l-secondary' : j.error ? 'border-l-err' : 'border-l-primary'}"
  >
    {#if j.running}
      <span class="pulse mr-1 text-secondary">●</span>{j.step}…
      {#if j.total}
        <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-line">
          <div class="h-full bg-secondary" style="width:{Math.min(100, (100 * j.done) / j.total)}%"></div>
        </div>
        <div class="num mt-1 text-dim">{bytes(j.done)} of {bytes(j.total)}</div>
      {/if}
    {:else}
      {#if j.finished}<span class="text-dim">{when(Date.parse(j.finished))}</span>{/if}
      {j.error ? `${j.action === 'install' ? 'Install' : 'Roll back'} failed: ${j.error}` : j.result}
      {#if !j.error && j.component === 'llama.cpp' && loadedModels && !unloaded}
        <div class="mt-2">
          <button class={btn} onclick={unloadAll}>Unload {loadedModels === 1 ? 'the loaded model' : `all ${loadedModels} loaded models`} now</button>
        </div>
      {/if}
    {/if}
  </div>
{/snippet}

{#if error}<div class="mb-3 text-err">{error}</div>{/if}

<section class={section}>
  <h2 class="text-sm font-semibold">llama-swap version</h2>
  {#if !swap}
    <p class="mt-1 text-muted">Loading…</p>
  {:else if swap.off}
    <p class="mt-1 text-[12.5px] text-muted">{swap.off}</p>
  {:else}
    <p class="mt-1 text-[12.5px] text-muted">
      {#if swap.installed}
        Installed <b class="font-mono text-text">{swap.installed}</b>.
      {:else}
        Could not read the installed version: <code>{swap.bin} -version</code> returned no version.
      {/if}
      {#if swap.check_error}
        <span class="text-err">Couldn't check GitHub: {swap.check_error}</span>
      {:else if swap.latest && (swap.newer || !swap.installed)}
        Newest release: <b class="font-mono text-primary">{swap.latest.tag}</b>, published {day(swap.latest.published)}.
      {:else if swap.latest}
        This is the newest release.
      {/if}
    </p>
    {#if swap.latest && swap.newer}{@render notes(swap.latest)}{/if}
    {#if job?.component === 'llama-swap'}{@render jobBox(job)}{/if}

    <div class="mt-3 flex flex-wrap gap-2">
      <button
        class={primary}
        disabled={busy || !swap.latest || !(swap.newer || !swap.installed)}
        onclick={() =>
          start('llama-swap', 'install', swap!.latest!.tag, `Update llama-swap to ${swap!.latest!.tag}? llama-swap restarts, so loaded models unload and running requests stop.`)}
      >
        {swap.latest && (swap.newer || !swap.installed) ? `Update to ${swap.latest.tag}` : 'Update'}
      </button>
      <button
        class={btn}
        disabled={busy || !swap.has_backup}
        title={swap.has_backup ? `Swap in ${swap.bin}.bak` : 'No previous version kept yet'}
        onclick={() =>
          start('llama-swap', 'rollback', '', `Go back to llama-swap ${swap!.backup || 'from the .bak file'}? llama-swap restarts, so loaded models unload and running requests stop.`)}
      >
        {backLabel(swap.backup, swap.installed)}
      </button>
      <button class={btn} disabled={busy || checking} onclick={check}>{checking ? 'Checking…' : 'Check for updates'}</button>
      <button class={btn} onclick={() => toggleChangelog('llama-swap')}>{shown === 'llama-swap' ? 'Hide changelog' : 'Changelog'}</button>
    </div>
    {#if shown === 'llama-swap'}
      <Changelog entries={logs['llama-swap'] ?? null} error={logError} isNew={(tag) => !!swap.installed && num(tag) > num(swap.installed)} />
    {/if}
    <p class="mt-2 text-xs text-dim">
      An update checks llama-swap's config with the new version first, then restarts llama-swap. If the new version doesn't answer within a minute,
      the old one is put back. The old binary is kept as <code>llama-swap.bak</code>. {checked}
    </p>
  {/if}
</section>

<section class={section}>
  <h2 class="text-sm font-semibold">llama.cpp version</h2>
  {#if !cpp}
    <p class="mt-1 text-muted">Loading…</p>
  {:else if cpp.off}
    <p class="mt-1 text-[12.5px] text-muted">{cpp.off}</p>
  {:else}
    <p class="mt-1 text-[12.5px] text-muted">
      {#if cpp.installed}
        In use: <b class="font-mono text-text">{cpp.installed}</b>{#if cpp.flavor}, the <code>{cpp.flavor}</code> build{/if}, in <code>{cpp.dir}</code>.
      {:else if !cpp.problem}
        No build is installed in <code>{cpp.dir}</code> yet.
      {/if}
    </p>
    {#if cpp.problem}<p class="mt-1 text-[12.5px] text-warn">{cpp.problem}</p>{/if}
    {#if cpp.check_error}<p class="mt-1 text-[12.5px] text-err">Couldn't check GitHub: {cpp.check_error}</p>{/if}

    {#if cpp.release || cpp.nightly}
      <table class="mt-2 text-[12.5px] [&_td]:py-0.5 [&_td]:pr-4">
        <tbody>
          {#if cpp.release}
            <tr>
              <td class="text-muted">Newest release</td>
              <td class="font-mono {cpp.newer ? 'text-primary' : ''}">{cpp.release.tag}</td>
              <td class="text-dim">
                made from build {cpp.release_build || '?'}, published {day(cpp.release.published)}{#if cpp.release_build && cpp.release_build === cpp.installed}. This is the build in use{/if}
              </td>
            </tr>
          {/if}
          {#if cpp.nightly}
            <tr>
              <td class="text-muted">Newest nightly</td>
              <td class="font-mono">{cpp.nightly.tag}</td>
              <td class="text-dim">
                published {day(cpp.nightly.published)}, <a href={cpp.nightly.url} target="_blank" rel="noreferrer" class="text-primary">changes ↗</a>
              </td>
            </tr>
          {/if}
        </tbody>
      </table>
    {/if}
    {#if cpp.release && cpp.newer}{@render notes(cpp.release)}{/if}
    {#if job?.component === 'llama.cpp'}{@render jobBox(job)}{/if}

    <div class="mt-3 flex flex-wrap gap-2">
      <button
        class={primary}
        disabled={busy || !!cpp.problem || !cpp.release_build || cpp.release_build === cpp.installed}
        onclick={() => start('llama.cpp', 'install', cpp!.release_build)}
      >
        {cpp.release && cpp.release_build ? `Install ${cpp.release.tag} (${cpp.release_build})` : 'Install the newest release'}
      </button>
      <button
        class={btn}
        disabled={busy || !!cpp.problem || !cpp.nightly || cpp.nightly.tag === cpp.installed}
        onclick={() => start('llama.cpp', 'install', cpp!.nightly!.tag)}
      >
        {cpp.nightly ? `Install nightly ${cpp.nightly.tag}` : 'Install the newest nightly'}
      </button>
      <button class={btn} disabled={busy || !cpp.previous} title={cpp.previous ? '' : 'No other build is installed'} onclick={() => start('llama.cpp', 'rollback')}>
        {backLabel(cpp.previous, cpp.installed)}
      </button>
      <button class={btn} disabled={busy || checking} onclick={check}>{checking ? 'Checking…' : 'Check for updates'}</button>
      <button class={btn} onclick={() => toggleChangelog('llama.cpp')}>{shown === 'llama.cpp' ? 'Hide changelog' : 'Changelog'}</button>
    </div>
    {#if shown === 'llama.cpp'}
      <Changelog entries={logs['llama.cpp'] ?? null} error={logError} isNew={cppIsNew} />
    {/if}
    <p class="mt-2 text-xs text-dim">
      Each build gets its own folder, and the <code>current</code> link points at the one in use. Models use a new build the next time they load. The build before is kept
      for rollback; older builds are removed unless a program is still running from them. Weekly releases are tested more than nightly builds. {checked}
    </p>
  {/if}
</section>
