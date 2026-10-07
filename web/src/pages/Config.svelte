<script lang="ts">
  import { structuredPatch } from 'diff'
  import { onDestroy, onMount } from 'svelte'
  import YamlEditor from '../lib/config/YamlEditor.svelte'
  import { when } from '../lib/format'
  import { privacy } from '../lib/privacy.svelte'

  interface File {
    path: string
    content: string
    hash: string
    modified: number
  }
  interface Check {
    yaml_ok: boolean
    yaml_error?: string
    yaml_line?: number
    routing_problems?: { line: number; message: string }[]
    llama_swap_ran: boolean
    llama_swap_ok: boolean
    llama_swap_note: string
  }
  interface Backup {
    name: string
    taken: number
    size: number
  }

  let editor: YamlEditor
  let file = $state<File | null>(null)
  let text = $state('')
  let off = $state('')
  let loadError = $state('')
  let check = $state<Check | null>(null)
  let checking = $state(false)
  let notice = $state<{ kind: 'ok' | 'warn' | 'err'; text: string } | null>(null)

  const dirty = $derived(!!file && text !== file.content)

  // ---- Unsaved edits ----
  // Keep the base hash so restored drafts still detect changes on disk.
  interface Draft {
    text: string
    base: File
    at: number
  }
  const draftKey = 'config_draft'
  function readDraft(): Draft | null {
    try {
      return JSON.parse(localStorage.getItem(draftKey) ?? 'null')
    } catch {
      return null
    }
  }
  $effect(() => {
    if (!file) return
    try {
      if (text === file.content) localStorage.removeItem(draftKey)
      else localStorage.setItem(draftKey, JSON.stringify({ text, base: $state.snapshot(file), at: Date.now() }))
    } catch {
      // Storage full or blocked: edits last only while the page is open.
    }
  })

  async function load() {
    loadError = ''
    const res = await fetch('/notus/api/config')
    if (res.status === 503) {
      off = (await res.json()).error
      return
    }
    if (!res.ok) {
      loadError = `${res.status} ${await res.text()}`
      return
    }
    const f: File = await res.json()
    const d = readDraft()
    if (d && d.base.path === f.path && d.text !== f.content) {
      file = d.base
      text = d.text
      notice =
        d.base.hash === f.hash
          ? { kind: 'ok', text: `Restored your unsaved edits from ${when(d.at)}.` }
          : { kind: 'warn', text: `Restored your unsaved edits from ${when(d.at)}. The file changed on disk; saving will open a conflict review.` }
    } else {
      file = f
      text = f.content
    }
    editor.setText(text)
    runCheck(text)
  }
  onMount(() => {
    load()
    loadHeld()
  })

  // Check while typing, a moment after the last key.
  let checkTimer: ReturnType<typeof setTimeout>
  let checkSeq = 0
  function onChange(t: string) {
    text = t
    clearTimeout(checkTimer)
    checkTimer = setTimeout(() => runCheck(t), 700)
  }
  async function runCheck(t: string) {
    const seq = ++checkSeq
    checking = true
    try {
      const res = await fetch('/notus/api/config/check', { method: 'POST', body: JSON.stringify({ content: t }) })
      const c: Check = await res.json()
      if (seq === checkSeq) check = c
    } finally {
      if (seq === checkSeq) checking = false
    }
  }
  const routingProblems = $derived(check?.routing_problems ?? [])
  const problem = $derived(check && !check.yaml_ok && check.yaml_line ? { line: check.yaml_line, message: check.yaml_error ?? '' } : (routingProblems[0] ?? null))

  // ---- Save, with a diff to review first ----
  let reviewing = $state(false)
  let skipCheck = $state(false)
  let saving = $state(false)
  let conflict = $state<File | null>(null)
  const hunks = $derived(reviewing && file ? structuredPatch('config.yaml', 'config.yaml', file.content, text, '', '', { context: 3 }).hunks : [])
  const canSave = $derived(!!check && check.yaml_ok && ((check.llama_swap_ok && !routingProblems.length) || skipCheck))

  function review() {
    skipCheck = false
    conflict = null
    reviewing = true
    runCheck(text)
  }

  async function save(overwrite = false) {
    if (!file) return
    saving = true
    try {
      const res = await fetch('/notus/api/config', {
        method: 'PUT',
        body: JSON.stringify({ content: text, base_hash: file.hash, skip_llama_swap: skipCheck, overwrite }),
      })
      const body = await res.json().catch(() => ({}))
      if (res.status === 409) {
        conflict = body.file
        return
      }
      if (res.status === 202) {
        reviewing = false
        conflict = null
        notice = null
        held = { id: body.held, inFlight: null, writing: false }
        watchHeld()
        return
      }
      if (!res.ok) {
        if (body.check) check = body.check
        notice = { kind: 'err', text: body.error ?? `Save failed (${res.status})` }
        reviewing = false
        return
      }
      file = body.file
      text = body.file.content
      reviewing = false
      conflict = null
      notice = savedNotice(body.check)
      loadBackups()
    } finally {
      saving = false
    }
  }

  function savedNotice(c: Check): typeof notice {
    return {
      kind: c.llama_swap_ok ? 'ok' : 'warn',
      text: c.llama_swap_ok
        ? 'Saved. llama-swap reloads the config within seconds.'
        : "Saved without llama-swap validation. If reload fails, llama-swap keeps the old config; check its log.",
    }
  }

  // ---- Waiting save ----
  interface HeldStatus {
    waiting: { id: number; since: string; writing: boolean } | null
    last: { id: number; at: string; check: Check; file?: File; error?: string } | null
    in_flight: number
  }
  let held = $state<{ id: number; inFlight: number | null; writing: boolean } | null>(null)
  let destroyed = false
  onDestroy(() => (destroyed = true))

  async function heldStatus(): Promise<HeldStatus | null> {
    const res = await fetch('/notus/api/config/held', { cache: 'no-store' }).catch(() => null)
    return res?.ok ? res.json() : null
  }

  async function loadHeld() {
    const s = await heldStatus()
    if (s?.waiting && !held) {
      held = { id: s.waiting.id, inFlight: s.in_flight, writing: s.waiting.writing }
      watchHeld()
    }
  }

  let watching = false
  async function watchHeld() {
    if (watching) return
    watching = true
    try {
      while (held && !destroyed) {
        const s = await heldStatus()
        if (!held) break
        if (s?.waiting?.id === held.id) {
          held.inFlight = s.in_flight
          held.writing = s.waiting.writing
        } else if (s?.last?.id === held.id) {
          heldDone(s.last)
          break
        } else if (s) {
          // Another client replaced or cancelled this save.
          held = null
          break
        }
        await new Promise((r) => setTimeout(r, 1000))
      }
    } finally {
      watching = false
    }
  }

  function heldDone(o: NonNullable<HeldStatus['last']>) {
    held = null
    if (o.error || !o.file) {
      notice = { kind: 'err', text: `Save failed: ${o.error}. Your edits are still here.` }
      return
    }
    file = o.file
    notice = savedNotice(o.check)
    loadBackups()
  }

  async function heldNow() {
    if (!confirm('Save now? Reloading the config can interrupt active responses.')) return
    await fetch('/notus/api/config/held/now', { method: 'POST' })
  }

  async function heldCancel() {
    const res = await fetch('/notus/api/config/held', { method: 'DELETE' })
    if (res.ok) {
      held = null
      notice = { kind: 'warn', text: 'Save cancelled. Your edits are still here.' }
    }
  }

  function takeDiskVersion() {
    if (!conflict) return
    file = conflict
    text = conflict.content
    editor.setText(conflict.content)
    conflict = null
    reviewing = false
    runCheck(text)
    notice = { kind: 'warn', text: 'Loaded the version on disk. Your edits were dropped.' }
  }

  function revert() {
    if (!file) return
    text = file.content
    editor.setText(file.content)
    runCheck(text)
  }

  // ---- Backups ----
  let backups = $state<Backup[]>([])
  let showBackups = $state(false)
  async function loadBackups() {
    const res = await fetch('/notus/api/config/backups')
    if (res.ok) backups = await res.json()
  }
  onMount(() => {
    loadBackups()
  })
  async function openBackup(b: Backup) {
    showBackups = false
    const res = await fetch(`/notus/api/config/backups/${encodeURIComponent(b.name)}`)
    if (!res.ok) return
    const { content } = await res.json()
    text = content
    editor.setText(content)
    runCheck(content)
    notice = { kind: 'warn', text: `Loaded the backup from ${when(b.taken)} into the editor. Review and save it to restore it.` }
  }

  const noticeColor = { ok: 'border-l-primary', warn: 'border-l-warn', err: 'border-l-err' }
  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50 disabled:hover:border-line'
</script>

<div class="flex h-[calc(100vh-34px-14px-40px)] min-h-[420px] flex-col">
  <div class="mb-2 flex flex-wrap items-center gap-3">
    <h1 class="text-[17px] font-semibold">Model Config</h1>
    {#if file}<span class="font-mono text-xs text-dim" title="Last changed {when(file.modified)}">{file.path}</span>{/if}
    {#if dirty}<span class="rounded-full bg-warn/15 px-2 text-[11px] font-semibold text-warn">unsaved changes</span>{/if}
    <span class="flex-1"></span>
    <div class="relative">
      <button class={btn} disabled={!file} onclick={() => (showBackups = !showBackups)}>Backups ({backups.length})</button>
      {#if showBackups}
        <div class="absolute right-0 z-30 mt-1 max-h-80 w-64 overflow-auto rounded-md border border-line bg-panel py-1 shadow-lg">
          {#each backups as b}
            <button class="flex w-full justify-between px-3 py-1 text-left hover:bg-hover" onclick={() => openBackup(b)}>
              <span>{when(b.taken)}</span><span class="text-dim">{(b.size / 1024).toFixed(1)} KB</span>
            </button>
          {:else}
            <div class="px-3 py-1 text-muted">No backups yet. One is made before each save.</div>
          {/each}
        </div>
      {/if}
    </div>
    <button class={btn} disabled={!dirty} onclick={revert}>Revert</button>
    <button class="rounded-md border border-primary bg-primary px-3 py-1 font-semibold text-sunken disabled:opacity-40" disabled={!dirty} onclick={review}>Save…</button>
  </div>

  {#if privacy.hidden.length && !privacy.showHidden}
    <div class="mb-2 rounded-md border border-l-3 border-line border-l-warn bg-panel2 px-3 py-1.5 text-xs text-muted">
      This page shows the config file as it is, so hidden models appear here. Avoid it in screenshots.
    </div>
  {/if}

  {#if off}
    <div class="rounded-md border border-line bg-panel p-4 text-muted">
      The config editor is off: {off}. Add <code class="text-text">NOTUS_LLAMA_SWAP_CONFIG=/path/to/config.yaml</code> to notus-swap's env file and restart it.
    </div>
  {:else if loadError}
    <div class="text-err">Could not load the config: {loadError}</div>
  {/if}

  <!-- The check line -->
  <div class="mb-2 flex min-h-5 flex-wrap items-center gap-x-4 text-xs">
    {#if checking && !check}
      <span class="text-muted">Checking…</span>
    {:else if check}
      <span class={check.yaml_ok ? 'text-primary' : 'text-err'}>
        {check.yaml_ok ? '✓ YAML' : `✗ YAML${check.yaml_line ? ` (line ${check.yaml_line})` : ''}: ${check.yaml_error}`}
      </span>
      {#if check.yaml_ok}
        {#if routingProblems.length}
          <span class="text-err" title={routingProblems.map((p) => `line ${p.line}: ${p.message}`).join('\n')}>
            ✗ routing check (line {routingProblems[0].line}): {routingProblems[0].message}{routingProblems.length > 1 ? ` (+${routingProblems.length - 1} more)` : ''}
          </span>
        {/if}
        <span class={!check.llama_swap_ran ? 'text-warn' : check.llama_swap_ok ? 'text-primary' : 'text-err'} title={check.llama_swap_note}>
          {!check.llama_swap_ran ? '? llama-swap check unavailable:' : check.llama_swap_ok ? '✓ llama-swap:' : '✗ llama-swap:'}
          <span class="text-muted">{check.llama_swap_note.split('\n')[0]}</span>
        </span>
      {/if}
      {#if checking}<span class="text-dim">checking…</span>{/if}
    {/if}
  </div>

  {#if held}
    <div class="mb-2 flex flex-wrap items-center gap-2 rounded-md border border-l-3 border-line border-l-secondary bg-panel2 px-3 py-1.5 text-xs">
      <span class="flex-1">
        {#if held.writing}
          Saving…
        {:else if held.inFlight}
          Waiting for {held.inFlight} {held.inFlight === 1 ? 'request' : 'requests'} to finish before reloading the config.
        {:else}
          Waiting for requests to finish before reloading the config.
        {/if}
      </span>
      {#if !held.writing}
        <button class={btn} onclick={heldNow}>Save now</button>
        <button class={btn} onclick={heldCancel}>Cancel</button>
      {/if}
    </div>
  {/if}

  {#if notice}
    <div class="mb-2 flex items-center rounded-md border border-l-3 border-line bg-panel2 px-3 py-1.5 text-xs {noticeColor[notice.kind]}">
      <span class="flex-1">{notice.text}</span>
      <button class="px-1 text-dim hover:text-text" onclick={() => (notice = null)}>✕</button>
    </div>
  {/if}

  <div class="min-h-0 flex-1 {off ? 'hidden' : ''}">
    <YamlEditor bind:this={editor} {onChange} {problem} />
  </div>
</div>

{#if reviewing}
  <!-- Review the change before saving -->
  <div class="fixed inset-0 z-40 flex items-start justify-center bg-black/60 p-6" role="presentation" onclick={(e) => e.target === e.currentTarget && (reviewing = false)}>
    <div class="flex max-h-full w-full max-w-[1000px] flex-col rounded-lg border border-line bg-surface p-4">
      <div class="mb-2 flex items-center gap-2">
        <h2 class="text-sm font-semibold">Save these changes?</h2>
        <span class="flex-1"></span>
        <button class="px-1 text-dim hover:text-text" onclick={() => (reviewing = false)}>✕</button>
      </div>

      <div class="min-h-0 flex-1 overflow-auto rounded-md border border-line bg-sunken font-mono text-xs">
        {#each hunks as h}
          <div class="border-b border-line-soft bg-panel px-2 py-0.5 text-dim">lines {h.oldStart}–{h.oldStart + h.oldLines - 1} → {h.newStart}–{h.newStart + h.newLines - 1}</div>
          {#each h.lines as l}
            <div class="px-2 whitespace-pre-wrap {l[0] === '+' ? 'bg-primary/12 text-primary' : l[0] === '-' ? 'bg-err/12 text-err' : 'text-muted'}">{l}</div>
          {/each}
        {:else}
          <div class="p-2 text-muted">No changes.</div>
        {/each}
      </div>

      <div class="mt-3 text-xs">
        {#if checking}
          <span class="text-muted">Checking…</span>
        {:else if check && !check.yaml_ok}
          <span class="text-err">The YAML is not valid, so this can't be saved: {check.yaml_error}</span>
        {:else if check && (routingProblems.length || !check.llama_swap_ok)}
          {#if routingProblems.length}
            <div class="text-warn">
              Routing settings refer to undefined models or variables:
              <ul class="mt-1 max-h-24 overflow-auto rounded bg-sunken p-2 text-muted">
                {#each routingProblems as p}<li>line {p.line}: {p.message}</li>{/each}
              </ul>
            </div>
          {/if}
          {#if !check.llama_swap_ok}
            <div class="text-warn {routingProblems.length ? 'mt-2' : ''}">
              {check.llama_swap_ran ? "llama-swap's check failed:" : "llama-swap's check couldn't run:"}
              <pre class="mt-1 max-h-24 overflow-auto rounded bg-sunken p-2 whitespace-pre-wrap text-muted">{check.llama_swap_note}</pre>
            </div>
          {/if}
          <label class="mt-2 flex items-center gap-2 text-muted">
            <input type="checkbox" bind:checked={skipCheck} class="accent-[var(--color-warn)]" />
            Save anyway, skipping the routing and llama-swap checks. YAML must still be valid.
          </label>
        {:else if check}
          <span class="text-primary">✓ Config checks passed. The current file will be backed up.</span>
          {#if held}<span class="text-muted"> This replaces the pending save.</span>{/if}
        {/if}
      </div>

      {#if conflict}
        <div class="mt-3 rounded-md border border-l-3 border-line border-l-err bg-panel2 p-3 text-xs">
          The file changed on disk after you opened it (at {when(conflict.modified)}). Saving now would replace that change.
          <div class="mt-2 flex gap-2">
            <button class={btn} onclick={takeDiskVersion}>Load the version on disk (drop my edits)</button>
            <button class="rounded-md border border-err px-2.5 py-1 text-err hover:bg-err/10" onclick={() => save(true)}>Save over it</button>
          </div>
        </div>
      {/if}

      <div class="mt-3 flex justify-end gap-2">
        <button class={btn} onclick={() => (reviewing = false)}>Keep editing</button>
        <button class="rounded-md border border-primary bg-primary px-3 py-1 font-semibold text-sunken disabled:opacity-40" disabled={!canSave || saving || checking || !hunks.length || !!conflict} onclick={() => save()}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  </div>
{/if}
