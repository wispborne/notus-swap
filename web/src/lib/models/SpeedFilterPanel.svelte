<script lang="ts">
  import { sameFilter, saveSpeedFilter, type SpeedFilter } from './speedFilter'

  // Chooses which requests count toward speeds. Changes show at once in the
  // table as a preview, and apply everywhere once saved.
  let {
    draft = $bindable(),
    saved = $bindable(),
    defaults,
    clients,
    counts,
  }: {
    draft: SpeedFilter
    saved: SpeedFilter
    defaults: SpeedFilter
    clients: { name: string; requests: number }[]
    counts: { requests: number; gen_counted: number; prompt_counted: number } | null
  } = $props()

  const count = (n: number) => n.toLocaleString('en-US')

  let error = $state('')
  let saving = $state(false)
  const changed = $derived(!sameFilter(draft, saved))

  async function save() {
    saving = true
    error = ''
    try {
      await saveSpeedFilter(draft)
      saved = structuredClone($state.snapshot(draft))
    } catch (e) {
      error = `Couldn't save speed filters: ${e instanceof Error ? e.message : e}`
    }
    saving = false
  }

  function toggleClient(name: string) {
    const ex = draft.exclude_clients
    draft.exclude_clients = ex.includes(name) ? ex.filter((c) => c !== name) : [...ex, name]
  }
  // An empty number field means 0: no limit, or no minimum.
  const whole = (v: unknown) => Math.max(0, Math.round(Number(v) || 0))

  const field = 'w-20 rounded border border-line bg-panel px-1.5 py-0.5 text-right text-text num'
  const row = 'flex flex-wrap items-center gap-x-2 gap-y-1'
</script>

<div class="mb-3 rounded-md border border-line bg-panel px-3 py-2.5 text-[12.5px]">
  <div class="mb-2 flex flex-wrap items-baseline gap-x-3">
    <b class="font-semibold">Speed filters</b>
    <span class="text-xs text-muted">Preview changes here. Save to apply them to all models.</span>
  </div>

  <div class="grid gap-x-8 gap-y-2 md:grid-cols-2">
    <div class="flex flex-col gap-2">
      <label class={row} title="Short answers can make output speed less representative because of fixed overhead.">
        Output speed: at least
        <input type="number" min="0" step="16" class={field} value={draft.min_output} onchange={(e) => (draft.min_output = whole(e.currentTarget.value))} />
        output tokens
      </label>
      <label class={row} title="Small prompts may not fill a processing batch. Cached tokens are excluded from this minimum.">
        Prompt speed: at least
        <input type="number" min="0" step="256" class={field} value={draft.min_new_prompt} onchange={(e) => (draft.min_new_prompt = whole(e.currentTarget.value))} />
        new prompt tokens
      </label>
      <label class={row} title="Exclude requests that overlapped other requests, on any model, for more than 5% of their processing time.">
        <input type="checkbox" class="accent-primary" bind:checked={draft.alone} />
        Exclude overlapping requests
      </label>
      <label class={row} title="Image processing can distort prompt speed comparisons with text-only requests.">
        <input type="checkbox" class="accent-primary" bind:checked={draft.no_images} />
        Exclude images from prompt speed
      </label>
      <div class={row} title="The prompt's size, cached tokens included. Empty or 0 means no limit.">
        Prompt size from
        <input type="number" min="0" step="1000" class={field} placeholder="0" value={draft.min_context || ''} onchange={(e) => (draft.min_context = whole(e.currentTarget.value))} />
        to
        <input type="number" min="0" step="1000" class={field} placeholder="no limit" value={draft.max_context || ''} onchange={(e) => (draft.max_context = whole(e.currentTarget.value))} />
        tokens
      </div>
      <div class={row} title="Requests that asked for JSON or for output that follows a schema.">
        JSON or schema output:
        {#each [['', 'Include'], ['exclude', 'Exclude'], ['only', 'Only these']] as const as [v, label]}
          <label class="flex cursor-pointer items-center gap-1">
            <input type="radio" name="speed-structured" class="accent-primary" checked={draft.structured === v} onchange={() => (draft.structured = v)} />
            {label}
          </label>
        {/each}
      </div>
    </div>

    <div>
      <div class="mb-1 text-muted">Clients</div>
      {#if !clients.length}
        <div class="text-xs text-dim">No requests yet.</div>
      {/if}
      <div class="flex max-h-40 max-w-72 flex-col overflow-y-auto">
        {#each clients as c (c.name)}
          <label class="flex cursor-pointer items-center gap-2 rounded px-1 py-px hover:bg-hover">
            <input type="checkbox" class="accent-primary" checked={!draft.exclude_clients.includes(c.name)} onchange={() => toggleClient(c.name)} />
            <span class={c.name ? '' : 'text-muted'}>{c.name || 'Unknown'}</span>
            <span class="num ml-auto text-xs text-dim">{count(c.requests)}</span>
          </label>
        {/each}
      </div>
      {#if draft.exclude_clients.some((n) => !clients.some((c) => c.name === n))}
        <div class="mt-1 text-xs text-dim">Other excluded clients: {draft.exclude_clients.filter((n) => !clients.some((c) => c.name === n)).map((n) => n || 'Unknown').join(', ')}</div>
      {/if}
    </div>
  </div>

  <div class="mt-2.5 flex flex-wrap items-center gap-2">
    <button class="rounded border border-line bg-panel2 px-2.5 py-0.5 text-xs hover:border-primary disabled:opacity-50 disabled:hover:border-line" disabled={!changed || saving} onclick={save}>{saving ? 'Saving…' : 'Save'}</button>
    <button class="rounded px-2 py-0.5 text-xs text-muted hover:text-text disabled:opacity-50" disabled={!changed} onclick={() => (draft = structuredClone($state.snapshot(saved)))}>Undo changes</button>
    <button class="rounded px-2 py-0.5 text-xs text-muted hover:text-text disabled:opacity-50" disabled={sameFilter(draft, defaults)} onclick={() => (draft = structuredClone($state.snapshot(defaults)))}>Reset to defaults</button>
    {#if counts}
      <span class="text-xs text-muted">
        Included from {count(counts.requests)} finished requests: {count(counts.gen_counted)} for output speed, {count(counts.prompt_counted)} for prompt speed{changed ? ' (unsaved)' : ''}.
      </span>
    {/if}
    {#if error}<span class="text-xs text-err">{error}</span>{/if}
  </div>
</div>
