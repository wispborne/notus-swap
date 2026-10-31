<script lang="ts">
  // One server's models on the Settings page: a hide switch and a colour and
  // shape picker for each. Both are saved on the server that runs the model
  // ('' for this one). This server's "Show hidden models" switch applies to
  // every server's.
  import { onMount } from 'svelte'
  import { listRequests } from '../api'
  import MarkPicker from '../MarkPicker.svelte'
  import ModelMark from '../ModelMark.svelte'
  import { modelLabel } from '../modelNames.svelte'
  import { privacy } from '../privacy.svelte'
  import { modelView, peerStatus } from '../servers.svelte'
  import { status } from '../status.svelte'

  let { server = '' }: { server?: string } = $props()

  // svelte-ignore state_referenced_locally
  const remote = server ? peerStatus.of(server) : null
  // svelte-ignore state_referenced_locally
  const mv = modelView(server)

  let seen = $state<string[]>([])
  let error = $state('')
  onMount(() => {
    // Models from past requests, including ones no longer in llama-swap's config.
    listRequests({}, undefined, 1, server).then((r) => (seen = r.models ?? []), () => {})
  })

  const known = $derived((remote ? remote.current : status.current)?.llama_swap.known ?? [])
  const hidden = $derived(remote ? (remote.current?.privacy.hidden_models ?? []) : privacy.hidden)
  const isHidden = (m: string) => (remote ? remote.isHidden(m) : privacy.isHidden(m))
  // Models in llama-swap's config first, by the label shown, then models only seen in older requests.
  const all = $derived.by(() => {
    const inConfig = new Set(known.map((m) => m.model))
    const label = remote ? mv.label : (m: string) => modelLabel(m)
    return [...new Set([...known.filter((m) => !m.unlisted).map((m) => m.model), ...seen])].sort(
      (a, b) => Number(inConfig.has(b)) - Number(inConfig.has(a)) || label(a).localeCompare(label(b), undefined, { numeric: true }),
    )
  })
  const shownModels = $derived(all.filter((m) => !isHidden(m)))
  const hiddenModels = $derived(all.filter((m) => isHidden(m)).concat(hidden.filter((m) => !all.includes(m))))

  async function setHidden(m: string, hide: boolean) {
    error = ''
    try {
      if (remote) await remote.setHidden(m, hide)
      else await privacy.setHidden(m, hide)
    } catch (e) {
      error = `Couldn't save: ${e instanceof Error ? e.message : e}`
    }
  }

  // While hidden models are off, their names on this page stay covered until
  // asked for, so this page can be screenshotted too. Not saved anywhere.
  let reveal = $state(false)
  const namesVisible = $derived(privacy.showHidden || reveal)

  // The model whose colour and shape picker is open.
  let picking = $state<string | null>(null)
</script>

{#if error}<div class="mb-1.5 text-[12.5px] text-err">{error}</div>{/if}

<h3 class="mt-4 mb-1.5 text-xs font-semibold tracking-wider text-dim uppercase">Hidden ({hiddenModels.length})</h3>
{#if !hiddenModels.length}
  <div class="text-[12.5px] text-muted">No models are hidden.</div>
{:else if !namesVisible}
  <button class="rounded-md border border-dashed border-line px-3 py-2 text-left text-[12.5px] text-muted hover:border-primary hover:text-text" onclick={() => (reveal = true)}>
    Click to reveal {hiddenModels.length} hidden model name{hiddenModels.length === 1 ? '' : 's'} here.
  </button>
{:else}
  <ul class="divide-y divide-line-soft rounded-md border border-line">
    {#each hiddenModels as m (m)}
      <li class="px-3 py-1.5">
        <div class="flex items-center gap-2">
          {@render mark(m)}
          <span class="flex-1 truncate" title={mv.title(m)}>{mv.label(m)}</span>
          <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => setHidden(m, false)}>Unhide</button>
        </div>
        {#if picking === m && mv.visible(m)}<div class="mt-1.5 mb-0.5"><MarkPicker model={m} {server} /></div>{/if}
      </li>
    {/each}
  </ul>
  {#if reveal && !privacy.showHidden}
    <button class="mt-1 text-xs text-primary" onclick={() => (reveal = false)}>Hide names again</button>
  {/if}
{/if}

<h3 class="mt-4 mb-1.5 text-xs font-semibold tracking-wider text-dim uppercase">Shown ({shownModels.length})</h3>
<ul class="divide-y divide-line-soft rounded-md border border-line">
  {#each shownModels as m (m)}
    <li class="px-3 py-1.5">
      <div class="flex items-center gap-2">
        {@render mark(m)}
        <span class="flex-1 truncate" title={mv.title(m)}>{mv.label(m)}</span>
        <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => setHidden(m, true)}>Hide</button>
      </div>
      {#if picking === m}<div class="mt-1.5 mb-0.5"><MarkPicker model={m} {server} /></div>{/if}
    </li>
  {:else}
    <li class="px-3 py-2 text-[12.5px] text-muted">No visible models.</li>
  {/each}
</ul>

<!-- A model's dot, which opens its colour and shape picker. A hidden model's
     colour and shape aren't sent while hidden models are off, so it has no picker then. -->
{#snippet mark(m: string)}
  {#if mv.visible(m)}
    <button
      class="grid size-5 place-items-center rounded border {picking === m ? 'border-primary' : 'border-transparent hover:border-line'}"
      title="Choose colour and shape"
      aria-expanded={picking === m}
      onclick={() => (picking = picking === m ? null : m)}
    >
      <ModelMark model={m} color={mv.color(m)} shape={mv.shape(m)} />
    </button>
  {:else}
    <span class="grid size-5 place-items-center"><ModelMark model={m} color={mv.color(m)} shape={mv.shape(m)} /></span>
  {/if}
{/snippet}
