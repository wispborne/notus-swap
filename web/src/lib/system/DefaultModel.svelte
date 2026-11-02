<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { duration } from '../format'
  import { modelLabel } from '../modelNames.svelte'
  import { privacy } from '../privacy.svelte'
  import { api, peerStatus } from '../servers.svelte'
  import { status } from '../status.svelte'

  /** The server this section acts on. '' is this server. */
  let { server = '' }: { server?: string } = $props()
  // svelte-ignore state_referenced_locally
  const remote = server ? peerStatus.of(server) : null

  interface State {
    enabled: boolean
    model: string
    minutes: number
    status: 'off' | 'down' | 'missing' | 'loaded' | 'loading' | 'paused' | 'waiting'
    loads_at?: number
    /** Idle unload time in seconds from llama-swap's config; 0 is never. */
    ttls: Record<string, number> | null
  }

  let saved = $state<State | null>(null)
  let error = $state('')
  let enabled = $state(false)
  let model = $state('')
  let minutes = $state(15)
  let busy = $state(false)
  let now = $state(Date.now())

  async function load(fill = false) {
    try {
      const res = await fetch(api(server, '/notus/api/default-model'))
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
      saved = await res.json()
      error = ''
      if (fill) ({ enabled, model, minutes } = saved!)
    } catch (e) {
      error = String(e)
    }
  }

  let timer: ReturnType<typeof setInterval>
  onMount(() => {
    load(true)
    timer = setInterval(() => {
      now = Date.now()
      load()
    }, 5000)
  })
  onDestroy(() => clearInterval(timer))

  const dirty = $derived(!!saved && (enabled !== saved.enabled || model !== saved.model || Math.round(minutes) !== saved.minutes))

  async function save() {
    busy = true
    try {
      const res = await fetch(api(server, '/notus/api/default-model'), {
        method: 'PUT',
        body: JSON.stringify({ enabled, model, minutes: Math.round(minutes) }),
      })
      if (!res.ok) throw new Error(await res.text())
      saved = await res.json()
      ;({ enabled, model, minutes } = saved!)
      error = ''
    } catch (e) {
      error = String(e)
    } finally {
      busy = false
    }
  }

  // Keep the selected model available even when hidden; label() masks its name.
  const visible = (m: string) => (remote ? remote.visible(m) : privacy.visible(m))
  const choices = $derived(
    ((remote ? remote.current : status.current)?.llama_swap.known ?? [])
      .map((m) => m.model)
      .filter((m) => visible(m) || m === model)
      .sort(),
  )
  const label = (m: string) => (!visible(m) ? 'a hidden model' : remote ? remote.label(m) : modelLabel(m))

  const ttl = $derived(saved?.ttls?.[model] ?? 0)
  const left = $derived(saved?.loads_at ? Math.max(0, saved.loads_at - now) : 0)

  const btn = 'rounded-md border border-line bg-panel2 px-2.5 py-1 hover:border-primary disabled:opacity-50 disabled:hover:border-line'
  const input = 'rounded-md border border-line bg-panel2 px-2 py-1 outline-none focus:border-primary'
</script>

<section id="default-model" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
  <h2 class="flex items-center gap-1.5 text-sm font-semibold">Default model</h2>

  {#if error}<div class="mt-2 text-[12.5px] text-err">{error}</div>{/if}

  <div class="mt-3 flex flex-wrap items-center gap-3 text-[12.5px]">
    <label class="flex cursor-pointer items-center gap-2">
      <button
        role="switch"
        aria-checked={enabled}
        aria-label="Load a default model"
        class="relative h-5 w-9 flex-none rounded-full transition-colors {enabled ? 'bg-primary' : 'bg-line'}"
        onclick={() => (enabled = !enabled)}
      >
        <span class="absolute top-0.5 left-0.5 size-4 rounded-full bg-text transition-transform {enabled ? 'translate-x-4' : ''}"></span>
      </button>
      Load
    </label>
    <select bind:value={model} class="{input} max-w-[320px]" disabled={!enabled}>
      <option value="" disabled>Choose a model</option>
      {#each choices as m (m)}
        <option value={m}>{label(m)}</option>
      {/each}
    </select>
    <label class="flex items-center gap-2">after
      <input type="number" min="1" max="1440" bind:value={minutes} class="{input} w-20" disabled={!enabled} /> minutes with no models loaded
    </label>
    <button class={btn} disabled={!dirty || busy || (enabled && !model)} onclick={save}>Save</button>
  </div>

  {#if saved && !dirty}
    <div class="mt-2 text-[12.5px] text-muted">
      {#if saved.status === 'waiting'}
        No models are loaded. {label(saved.model)} loads in {duration(left)}.
      {:else if saved.status === 'loaded'}
        A model is loaded, so the countdown hasn't started.
      {:else if saved.status === 'loading'}
        <span class="pulse text-secondary">●</span> Loading {label(saved.model)}…
      {:else if saved.status === 'paused'}
        Paused after a manual unload. Loading any model, or saving here, resumes it.
      {:else if saved.status === 'missing'}
        <span class="text-err">{label(saved.model)} isn't in llama-swap's config, so no model will load.</span>
      {:else if saved.status === 'down'}
        llama-swap isn't reachable.
      {/if}
    </div>
  {/if}

  {#if enabled && model && ttl > 0}
    <div class="mt-2 rounded-md border border-l-3 border-line border-l-warn bg-panel2 px-3 py-2 text-[12.5px]">
      {label(model)} unloads after {duration(ttl * 1000)} idle, then reloads {Math.round(minutes)}
      {Math.round(minutes) === 1 ? 'minute' : 'minutes'} later. Set its <code>ttl</code> to 0 on the Model Config page to stop this.
    </div>
  {/if}
</section>
