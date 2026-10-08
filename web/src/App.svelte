<script lang="ts">
  import { onMount } from 'svelte'
  import { live } from './lib/live.svelte'
  import StatusBar from './lib/StatusBar.svelte'
  import Requests from './pages/Requests.svelte'
  import System from './pages/System.svelte'
  import NavIcon from './lib/NavIcon.svelte'
  import { navOrder, type PageId } from './lib/navOrder.svelte'
  import { privacy } from './lib/privacy.svelte'
  import { status } from './lib/status.svelte'
  import { installTooltip } from './lib/tooltip'
  import { prefetchDashboard } from './lib/dashboard/data'
  import { prefetchRequests } from './lib/api'

  // Pages other than Requests and System load their code only when opened,
  // which keeps the first load small.
  const loadDashboard = () => import('./pages/Dashboard.svelte')
  const loadConfig = () => import('./pages/Config.svelte')
  const loadModels = () => import('./pages/Models.svelte')
  const loadLogs = () => import('./pages/Logs.svelte')

  const pages = [
    { path: 'dashboard', label: 'Dashboard', ready: true },
    { path: 'requests', label: 'Requests', ready: true },
    { path: 'models', label: 'Models', ready: true },
    { path: 'logs', label: 'Logs', ready: true },
    { path: 'config', label: 'Model Config', ready: true },
    { path: 'system', label: 'Settings', ready: true },
  ]

  function current() {
    const p = location.pathname.replace(/^\/notus\/?/, '').split('/')[0]
    return pages.find((x) => x.path === p && x.ready)?.path ?? 'dashboard'
  }
  const first = current()
  let page = $state(first)
  // The first page starts loading its code, and the Dashboard and Requests
  // their data, at once. Otherwise they would wait for the privacy settings
  // to load first.
  const loaders: Record<string, (() => Promise<unknown>) | undefined> = { dashboard: loadDashboard, config: loadConfig, models: loadModels, logs: loadLogs }
  loaders[first]?.()
  if (first === 'dashboard') prefetchDashboard()
  if (first === 'requests') prefetchRequests({ model: new URLSearchParams(location.search).get('model') ?? '' })

  // The pages in the order the user arranged them.
  const ordered = $derived(navOrder.list.map((id) => pages.find((p) => p.path === id)!))

  // Dragging a page moves it as the pointer passes the middle of another.
  // The order is saved when the drag ends.
  let dragging = $state<PageId | null>(null)
  function dragStart(e: DragEvent, id: PageId) {
    dragging = id
    e.dataTransfer!.effectAllowed = 'move'
    e.dataTransfer!.setData('text/plain', id)
  }
  function dragOver(e: DragEvent, id: PageId, axis: 'x' | 'y') {
    if (!dragging) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    navOrder.place(dragging, id, axis === 'x' ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2)
  }
  function dragEnd() {
    dragging = null
    navOrder.save()
  }
  let collapsed = $state(localStorageGet('sidebar') === 'collapsed')

  function go(path: string) {
    history.pushState(null, '', `/notus/${path}`)
    page = path
  }
  function toggle() {
    collapsed = !collapsed
    try {
      localStorage.setItem('sidebar', collapsed ? 'collapsed' : 'open')
    } catch {}
  }
  function localStorageGet(k: string) {
    try {
      return localStorage.getItem(k)
    } catch {
      return null
    }
  }

  onMount(() => {
    privacy.load()
    live.connect()
    const removeTooltip = installTooltip()
    const pop = () => (page = current())
    addEventListener('popstate', pop)
    // Links marked data-nav change page without reloading.
    const click = (e: MouseEvent) => {
      const a = (e.target as Element).closest?.('a[data-nav]') as HTMLAnchorElement | null
      if (!a || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey) return
      e.preventDefault()
      history.pushState(null, '', a.href)
      page = current()
      scrollTo(0, 0)
    }
    addEventListener('click', click)
    return () => {
      removeEventListener('popstate', pop)
      removeEventListener('click', click)
      removeTooltip()
    }
  })
</script>

<StatusBar />

<div class="flex min-h-[calc(100vh-34px)]">
  <!-- Spacing here is in whole pixels, so the icons land on whole pixels and
       stay sharp. It stays in view while the page scrolls. -->
  <nav
    class="sticky top-[34px] hidden h-[calc(100vh-34px)] flex-none flex-col border-r border-line bg-[#1e2127] px-[6px] py-[8px] transition-[width] md:flex {collapsed
      ? 'w-[46px]'
      : 'w-[176px]'}"
  >
    <div class="flex flex-col gap-[2px]">
      {#each ordered as p (p.path)}
        <!-- The current page gets a bar along the sidebar's left edge. Pages can be dragged into another order. -->
        <button
          draggable="true"
          ondragstart={(e) => dragStart(e, p.path as PageId)}
          ondragover={(e) => dragOver(e, p.path as PageId, 'y')}
          ondrop={(e) => e.preventDefault()}
          ondragend={dragEnd}
          class="relative flex h-[30px] w-full items-center gap-[10px] rounded-md px-[8px] whitespace-nowrap {dragging === p.path ? 'opacity-40' : ''}
            {page === p.path
            ? 'bg-primary/12 text-primary before:absolute before:top-[7px] before:-left-[6px] before:h-[16px] before:w-[3px] before:rounded-r-sm before:bg-primary'
            : 'text-muted hover:bg-hover hover:text-text'}"
          title={collapsed ? p.label : undefined}
          onclick={() => go(p.path)}
        >
          {@render icon(p, collapsed)}
          {#if !collapsed}
            <span class="overflow-hidden">{p.label}</span>
            {@render badge(p)}
          {/if}
        </button>
      {/each}
    </div>
    {#if !collapsed && !navOrder.isDefault}
      <button class="mt-auto h-[24px] rounded-md px-[8px] text-left text-xs text-dim hover:bg-hover hover:text-text" onclick={() => navOrder.reset()}>Reset order</button>
    {/if}
    <button
      class="{collapsed || navOrder.isDefault ? 'mt-auto' : ''} flex h-[30px] w-full items-center gap-[10px] overflow-hidden rounded-md px-[8px] whitespace-nowrap text-dim hover:bg-hover hover:text-text"
      title={collapsed ? 'Expand the sidebar' : undefined}
      onclick={toggle}
    >
      <span class="flex size-[18px] flex-none items-center justify-center"><NavIcon name={collapsed ? 'expand' : 'collapse'} /></span>
      {#if !collapsed}<span>Collapse</span>{/if}
    </button>
  </nav>

  <main class="min-w-0 flex-1 px-4 pt-3.5 pb-16">
    {#if !privacy.ready}
      <div class="text-muted">Loading…</div>
    {:else if page === 'dashboard'}
      {#await loadDashboard() then m}<m.default />{/await}
    {:else if page === 'requests'}<Requests />
    {:else if page === 'models'}
      {#await loadModels() then m}<m.default />{/await}
    {:else if page === 'logs'}
      {#await loadLogs() then m}<m.default />{/await}
    {:else if page === 'config'}
      {#await loadConfig() then m}<m.default />{/await}
    {:else if page === 'system'}<System />{/if}
  </main>
</div>

<!-- The side menu is hidden below 768 px wide, so small windows get this bar along the bottom instead. -->
<nav class="fixed inset-x-0 bottom-0 z-30 flex border-t border-line bg-[#1e2127] pb-[env(safe-area-inset-bottom)] md:hidden">
  {#each ordered as p (p.path)}
    <button
      draggable="true"
      ondragstart={(e) => dragStart(e, p.path as PageId)}
      ondragover={(e) => dragOver(e, p.path as PageId, 'x')}
      ondrop={(e) => e.preventDefault()}
      ondragend={dragEnd}
      class="{dragging === p.path ? 'opacity-40' : ''} flex min-w-0 flex-1 flex-col items-center gap-[2px] py-[6px] text-[11px] leading-[14px] {page === p.path ? 'text-primary' : 'text-muted hover:text-text'}"
      aria-label={p.label}
      onclick={() => go(p.path)}
    >
      {@render icon(p, true)}<span class="max-w-full truncate">{p.label}</span>
    </button>
  {/each}
</nav>

<!-- dots: show requests in flight and an available update as a dot on the
     icon. The open sidebar shows them as a badge after the label instead. -->
{#snippet icon(p: (typeof pages)[number], dots: boolean)}
  <span class="relative flex size-[18px] flex-none items-center justify-center">
    <NavIcon name={p.path} />
    {#if dots && p.path === 'system' && status.current?.update_available}
      <span class="absolute -top-[2px] -right-[2px] size-[7px] rounded-full bg-secondary" title="An update is available"></span>
    {:else if dots && p.path === 'requests' && live.flights.size}
      <span class="pulse absolute -top-[2px] -right-[2px] size-[7px] rounded-full bg-secondary" title="{live.flights.size} in progress"></span>
    {/if}
  </span>
{/snippet}

{#snippet badge(p: (typeof pages)[number])}
  {#if p.path === 'requests' && live.flights.size}
    <span class="num ml-auto rounded-full bg-secondary/15 px-[6px] text-[11px] leading-[16px] text-secondary" title="{live.flights.size} in progress">{live.flights.size}</span>
  {:else if p.path === 'system' && status.current?.update_available}
    <span class="ml-auto rounded-full bg-secondary/15 px-[6px] text-[11px] leading-[16px] text-secondary">Update</span>
  {/if}
{/snippet}
