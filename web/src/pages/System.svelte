<script lang="ts">
  import { onMount } from 'svelte'
  import { appTitle, defaultTitle } from '../lib/appTitle.svelte'
  import { modelShow } from '../lib/modelNames.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status } from '../lib/status.svelte'
  import DefaultModel from '../lib/system/DefaultModel.svelte'
  import Manage from '../lib/system/Manage.svelte'
  import ServerModels from '../lib/system/ServerModels.svelte'
  import Servers from '../lib/system/Servers.svelte'
  import ServerPicker from '../lib/ServerPicker.svelte'
  import ServerAsleep from '../lib/ServerAsleep.svelte'
  import { pageServer, reachable, serverName, servers, stateWords } from '../lib/servers.svelte'
  import { theme, themes } from '../lib/theme.svelte'

  onMount(() => {
    status.start()
    servers.load()
  })

  // The server that Updates, Services, Request text and Default model act
  // on. '' is this server. Choosing another server reloads the page.
  let server = $state<string | null>(null)
  $effect(() => {
    if (server === null && servers.ready) server = pageServer()
  })

  // Links to each section, since the page is long. The first perServer
  // belong to the server the picker chose.
  const perServer = 4
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
  <!-- With other servers, the links to the sections the server picker acts
       on share the frame's tint. -->
  <nav class="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]" aria-label="Sections">
    {#if servers.many}
      <span class="-ml-[9px] flex flex-wrap gap-x-3 rounded-md border border-primary/35 bg-primary/8 px-[8px] py-[2px]">
        {#each sections.slice(0, perServer) as [id, label] (id)}<a href="#{id}" class="text-muted hover:text-primary">{label}</a>{/each}
      </span>
      {#each sections.slice(perServer) as [id, label] (id)}<a href="#{id}" class="text-muted hover:text-primary">{label}</a>{/each}
    {:else}
      {#each sections as [id, label] (id)}<a href="#{id}" class="text-muted hover:text-primary">{label}</a>{/each}
    {/if}
  </nav>

  <!-- The sections that belong to one server. With other servers, they sit in
       one tinted frame whose header is the server picker, kept in view while
       they scroll past. -->
  {#snippet perServerSections()}
    {#if server && !reachable(servers.get(server))}
      <ServerAsleep server={servers.get(server)} />
    {:else if server !== null}
      {#key server}
        <Manage {server} />

        <DefaultModel {server} />
      {/key}
    {/if}
  {/snippet}
  {#if servers.many}
    <div class="-mx-[9px] mb-4 rounded-xl border border-primary/35 bg-primary/4">
      <div class="sticky top-(--bar-h) z-10 rounded-t-xl border-b border-primary/35 bg-[color-mix(in_srgb,var(--color-primary)_10%,var(--color-surface))] px-[8px] py-[8px]">
        <ServerPicker current={server ?? ''} />
      </div>
      <!-- A section jumped to from the links lands 8 px under the sticky header (41 px). -->
      <div class="px-[8px] pt-[8px] [&>:last-child]:mb-[8px] [&_section]:scroll-mt-[calc(var(--bar-h)_+_49px)]">
        {@render perServerSections()}
      </div>
    </div>
  {:else}
    {@render perServerSections()}
  {/if}

  <Servers />

  <section id="app-title" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
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

  <section id="theme" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
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

  <section id="model-labels" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
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

  <section id="models" class="scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
    <h2 class="text-sm font-semibold">Models</h2>
    <p class="mt-1 text-[12.5px] text-muted">
      Click a model's dot to choose its colour and shape. They are used on every page, and saved for every browser on the server that runs the model.
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

    {#if servers.many}
      {#each servers.list as sv (sv.id)}
        <h3 class="mt-5 -mb-2 text-[13px] font-semibold">{serverName(sv)}</h3>
        {#if sv.self || reachable(sv)}
          <ServerModels server={sv.self ? '' : sv.id} />
        {:else}
          <div class="mt-3 text-[12.5px] text-muted">{stateWords(sv)}. Its models show here once it answers.</div>
        {/if}
      {/each}
    {:else}
      <ServerModels />
    {/if}
    <p class="mt-3 text-xs text-dim">Hidden model names remain visible in llama-swap's UI at /ui.</p>
  </section>
</div>
