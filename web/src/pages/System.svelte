<script lang="ts">
  import { onMount } from 'svelte'
  import { appTitle, defaultTitle } from '../lib/appTitle.svelte'
  import { listRequests } from '../lib/api'
  import { modelColor } from '../lib/format'
  import { modelLabel, modelShow, modelTitle } from '../lib/modelNames.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status } from '../lib/status.svelte'
  import DefaultModel from '../lib/system/DefaultModel.svelte'
  import Manage from '../lib/system/Manage.svelte'
  import { theme, themes } from '../lib/theme.svelte'

  let seen = $state<string[]>([])
  onMount(() => {
    status.start()
    // Models from past requests, including ones no longer in llama-swap's config.
    listRequests({}, undefined, 1).then((r) => (seen = r.models ?? []), () => {})
  })

  const known = $derived(status.current?.llama_swap.known ?? [])
  const all = $derived([...new Set([...known.filter((m) => !m.unlisted).map((m) => m.model), ...seen])].sort())
  const shownModels = $derived(all.filter((m) => !privacy.isHidden(m)))
  const hiddenModels = $derived(all.filter((m) => privacy.isHidden(m)).concat(privacy.hidden.filter((m) => !all.includes(m))))

  // While hidden models are off, their names on this page stay covered until
  // asked for, so this page can be screenshotted too. Not saved anywhere.
  let reveal = $state(false)
  const namesVisible = $derived(privacy.showHidden || reveal)

  // The title field starts from the saved title, and follows it until edited.
  let titleDraft = $state<string | null>(null)
  const titleValue = $derived(titleDraft ?? appTitle.saved)
  let titleError = $state('')
  async function saveTitle(t: string) {
    titleError = ''
    try {
      await appTitle.set(t)
      titleDraft = null
    } catch (e) {
      titleError = `Couldn't save: ${e instanceof Error ? e.message : e}`
    }
  }
</script>

<div class="max-w-[760px]">
  <h1 class="mb-3 text-[17px] font-semibold">Settings</h1>

  <Manage />

  <DefaultModel />

  <section class="mb-4 rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">App title</h2>
    <p class="mt-1 text-[12.5px] text-muted">Shown in the top left corner and on the browser tab. Leave it empty for "{defaultTitle}". Saved on the server, for every browser.</p>
    <form
      class="mt-3 flex flex-wrap items-center gap-2"
      onsubmit={(e) => {
        e.preventDefault()
        saveTitle(titleValue)
      }}
    >
      <input
        type="text"
        maxlength="60"
        placeholder={defaultTitle}
        class="w-64 rounded-md border border-line bg-panel2 px-2 py-1 text-[12.5px] text-text outline-none focus:border-primary"
        value={titleValue}
        oninput={(e) => (titleDraft = e.currentTarget.value)}
      />
      <button
        type="submit"
        class="rounded-md border border-line bg-panel2 px-2.5 py-1 text-[12.5px] text-muted hover:border-dim hover:text-text disabled:opacity-50"
        disabled={titleValue.trim() === appTitle.saved}
      >
        Save
      </button>
      {#if appTitle.saved}
        <button type="button" class="rounded-md px-2 py-1 text-[12.5px] text-muted hover:text-text" onclick={() => saveTitle('')}>Reset to default</button>
      {/if}
    </form>
    {#if titleError}<p class="mt-2 text-[12.5px] text-err">{titleError}</p>{/if}
  </section>

  <section class="mb-4 rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">Theme</h2>
    <p class="mt-1 text-[12.5px] text-muted">Colour themes from TriOS. Saved in this browser only.</p>
    <div class="mt-3 flex flex-wrap gap-1.5" role="radiogroup" aria-label="Theme">
      {#each themes as t (t.id)}
        <button
          role="radio"
          aria-checked={theme.id === t.id}
          class="flex items-center gap-2 rounded-md border bg-panel2 py-1 pr-2.5 pl-1.5 text-[12.5px] {theme.id === t.id ? 'border-primary text-text' : 'border-line text-muted hover:border-dim hover:text-text'}"
          onclick={() => theme.set(t.id)}
        >
          <span class="flex overflow-hidden rounded-sm border border-black/40">
            {#each t.swatch as c}<span class="h-4 w-2" style:background={c}></span>{/each}
          </span>
          {t.name}
        </button>
      {/each}
    </div>
  </section>

  <section class="mb-4 rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">Model labels</h2>
    <p class="mt-1 text-[12.5px] text-muted">
      Show models by the name in llama-swap's config, or by their ID. A model without a name shows its ID either way. Hovering a model shows both. Saved in this browser only.
    </p>
    <div class="mt-3 flex flex-wrap gap-1.5" role="radiogroup" aria-label="Model labels">
      {#each [['name', 'Name'], ['id', 'ID']] as const as [v, text] (v)}
        <button
          role="radio"
          aria-checked={modelShow.show === v}
          class="rounded-md border bg-panel2 px-2.5 py-1 text-[12.5px] {modelShow.show === v ? 'border-primary text-text' : 'border-line text-muted hover:border-dim hover:text-text'}"
          onclick={() => modelShow.set(v)}
        >
          {text}
        </button>
      {/each}
    </div>
  </section>

  <section class="rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">Hidden models</h2>
    <p class="mt-1 text-[12.5px] text-muted">
      Hidden models are still recorded. When hidden, they're omitted from requests and charts; totals such as power still include them.
    </p>

    <label class="mt-3 flex cursor-pointer items-center gap-3 rounded-md border border-line bg-panel2 px-3 py-2">
      <button
        role="switch"
        aria-checked={privacy.showHidden}
        aria-label="Show hidden models"
        class="relative h-5 w-9 flex-none rounded-full transition-colors {privacy.showHidden ? 'bg-primary' : 'bg-line'}"
        onclick={() => privacy.setShowHidden(!privacy.showHidden)}
      >
        <span class="absolute top-0.5 left-0.5 size-4 rounded-full bg-text transition-transform {privacy.showHidden ? 'translate-x-4' : ''}"></span>
      </button>
      <span>
        <b class="font-semibold">Show hidden models</b>
        <span class="block text-xs text-muted">
          {privacy.showHidden ? 'Shown in requests, charts, and logs.' : 'Hidden from requests, charts, and logs.'}
          This applies to every device.
        </span>
      </span>
    </label>

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
          <li class="flex items-center gap-2 px-3 py-1.5">
            <span class="inline-block size-2 rounded-full" style="background:{modelColor(m)}"></span>
            <span class="flex-1 truncate" title={modelTitle(m)}>{modelLabel(m)}</span>
            <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => privacy.setHidden(m, false)}>Unhide</button>
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
        <li class="flex items-center gap-2 px-3 py-1.5">
          <span class="inline-block size-2 rounded-full" style="background:{modelColor(m)}"></span>
          <span class="flex-1 truncate" title={modelTitle(m)}>{modelLabel(m)}</span>
          <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => privacy.setHidden(m, true)}>Hide</button>
        </li>
      {:else}
        <li class="px-3 py-2 text-[12.5px] text-muted">No visible models.</li>
      {/each}
    </ul>
    <p class="mt-3 text-xs text-dim">Hidden model names remain visible in llama-swap's UI at /ui.</p>
  </section>
</div>
