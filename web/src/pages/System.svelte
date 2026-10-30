<script lang="ts">
  import { onMount } from 'svelte'
  import { appTitle, defaultTitle } from '../lib/appTitle.svelte'
  import { listRequests } from '../lib/api'
  import MarkPicker from '../lib/MarkPicker.svelte'
  import ModelMark from '../lib/ModelMark.svelte'
  import { modelLabel, modelShow, modelTitle } from '../lib/modelNames.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status } from '../lib/status.svelte'
  import DefaultModel from '../lib/system/DefaultModel.svelte'
  import Manage from '../lib/system/Manage.svelte'
  import Servers from '../lib/system/Servers.svelte'
  import ServerPicker from '../lib/ServerPicker.svelte'
  import ServerAsleep from '../lib/ServerAsleep.svelte'
  import { pageServer, reachable, serverName, servers } from '../lib/servers.svelte'
  import { theme, themes } from '../lib/theme.svelte'

  let seen = $state<string[]>([])
  onMount(() => {
    status.start()
    // Models from past requests, including ones no longer in llama-swap's config.
    listRequests({}, undefined, 1).then((r) => (seen = r.models ?? []), () => {})
    servers.load()
  })

  // The server that Updates, Services, Request text and Default model act
  // on. '' is this server. Choosing another server reloads the page.
  let server = $state<string | null>(null)
  $effect(() => {
    if (server === null && servers.ready) server = pageServer()
  })

  const known = $derived(status.current?.llama_swap.known ?? [])
  // Models in llama-swap's config first, by the label shown, then models only seen in older requests.
  const all = $derived.by(() => {
    const inConfig = new Set(known.map((m) => m.model))
    return [...new Set([...known.filter((m) => !m.unlisted).map((m) => m.model), ...seen])].sort(
      (a, b) => Number(inConfig.has(b)) - Number(inConfig.has(a)) || modelLabel(a).localeCompare(modelLabel(b), undefined, { numeric: true }),
    )
  })

  // Links to each section, since the page is long.
  const sections = [
    ['updates', 'Updates'],
    ['services', 'Services'],
    ['storage', 'Request text'],
    ['default-model', 'Default model'],
    ['servers', 'Servers'],
    ['app-title', 'App title'],
    ['theme', 'Theme'],
    ['model-labels', 'Model labels'],
    ['models', 'Models'],
  ] as const
  const shownModels = $derived(all.filter((m) => !privacy.isHidden(m)))
  const hiddenModels = $derived(all.filter((m) => privacy.isHidden(m)).concat(privacy.hidden.filter((m) => !all.includes(m))))

  // While hidden models are off, their names on this page stay covered until
  // asked for, so this page can be screenshotted too. Not saved anywhere.
  let reveal = $state(false)
  const namesVisible = $derived(privacy.showHidden || reveal)

  // The model whose colour and shape picker is open.
  let picking = $state<string | null>(null)

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
  <h1 class="mb-1.5 text-[17px] font-semibold">Settings</h1>
  <nav class="mb-3 flex flex-wrap gap-x-3 gap-y-1 text-[12.5px]" aria-label="Sections">
    {#each sections as [id, label] (id)}<a href="#{id}" class="text-muted hover:text-primary">{label}</a>{/each}
  </nav>

  {#if servers.many}
    <div class="mb-3 flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
      <ServerPicker current={server ?? ''} />
      <span>Updates, services, request text and the default model below are for {serverName(server ? servers.get(server) : servers.list.find((s) => s.self))}.</span>
    </div>
  {/if}

  {#if server && !reachable(servers.get(server))}
    <ServerAsleep server={servers.get(server)} />
  {:else if server !== null}
    {#key server}
      <Manage {server} />

      <DefaultModel {server} />
    {/key}
  {/if}

  <Servers />

  <section id="app-title" class="mb-4 scroll-mt-12 rounded-lg border border-line bg-panel p-4">
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

  <section id="theme" class="mb-4 scroll-mt-12 rounded-lg border border-line bg-panel p-4">
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

  <section id="model-labels" class="mb-4 scroll-mt-12 rounded-lg border border-line bg-panel p-4">
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

  <section id="models" class="scroll-mt-12 rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">Models</h2>
    <p class="mt-1 text-[12.5px] text-muted">
      Click a model's dot to choose its colour and shape. They are used on every page, and saved on the server for every browser.
    </p>
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
          <li class="px-3 py-1.5">
            <div class="flex items-center gap-2">
              {@render mark(m)}
              <span class="flex-1 truncate" title={modelTitle(m)}>{modelLabel(m)}</span>
              <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => privacy.setHidden(m, false)}>Unhide</button>
            </div>
            {#if picking === m && privacy.visible(m)}<div class="mt-1.5 mb-0.5"><MarkPicker model={m} /></div>{/if}
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
            <span class="flex-1 truncate" title={modelTitle(m)}>{modelLabel(m)}</span>
            <button class="rounded border border-line bg-panel2 px-2 text-xs hover:border-primary" onclick={() => privacy.setHidden(m, true)}>Hide</button>
          </div>
          {#if picking === m}<div class="mt-1.5 mb-0.5"><MarkPicker model={m} /></div>{/if}
        </li>
      {:else}
        <li class="px-3 py-2 text-[12.5px] text-muted">No visible models.</li>
      {/each}
    </ul>
    <p class="mt-3 text-xs text-dim">Hidden model names remain visible in llama-swap's UI at /ui.</p>
  </section>
</div>

<!-- A model's dot, which opens its colour and shape picker. A hidden model's
     colour and shape aren't sent while hidden models are off, so it has no picker then. -->
{#snippet mark(m: string)}
  {#if privacy.visible(m)}
    <button
      class="grid size-5 place-items-center rounded border {picking === m ? 'border-primary' : 'border-transparent hover:border-line'}"
      title="Choose colour and shape"
      aria-expanded={picking === m}
      onclick={() => (picking = picking === m ? null : m)}
    >
      <ModelMark model={m} />
    </button>
  {:else}
    <span class="grid size-5 place-items-center"><ModelMark model={m} /></span>
  {/if}
{/snippet}
