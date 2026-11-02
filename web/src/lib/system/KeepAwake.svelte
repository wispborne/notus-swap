<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import InfoTip from '../InfoTip.svelte'
  import { api } from '../servers.svelte'

  /** The server this section acts on. '' is this server. */
  let { server = '' }: { server?: string } = $props()

  interface State {
    supported: boolean
    enabled: boolean
    held: boolean
    in_flight: number
  }

  let saved = $state<State | null>(null)
  let error = $state('')
  let busy = $state(false)

  async function load() {
    try {
      const res = await fetch(api(server, '/notus/api/keep-awake'))
      if (!res.ok) throw new Error(`${res.status} ${await res.text()}`)
      saved = await res.json()
      error = ''
    } catch (e) {
      error = String(e)
    }
  }

  let timer: ReturnType<typeof setInterval>
  onMount(() => {
    load()
    timer = setInterval(load, 5000)
  })
  onDestroy(() => clearInterval(timer))

  async function toggle() {
    if (!saved) return
    busy = true
    try {
      const res = await fetch(api(server, '/notus/api/keep-awake'), {
        method: 'PUT',
        body: JSON.stringify({ enabled: !saved.enabled }),
      })
      if (!res.ok) throw new Error(await res.text())
      saved = await res.json()
      error = ''
    } catch (e) {
      error = String(e)
    } finally {
      busy = false
    }
  }

  const on = $derived(!!saved?.supported && !!saved?.enabled)
</script>

<section id="keep-awake" class="mb-4 scroll-mt-[calc(var(--bar-h)_+_14px)] rounded-lg border border-line bg-panel p-4">
  <h2 class="flex items-center gap-1.5 text-sm font-semibold">Keep awake<InfoTip
      text="Windows doesn't count requests from other computers as activity, so it can sleep in the middle of an answer."
    /></h2>

  {#if error}<div class="mt-2 text-[12.5px] text-err">{error}</div>{/if}

  {#if saved}
    <div class="mt-3 flex flex-wrap items-center gap-3 text-[12.5px]">
      <label class="flex items-center gap-2 {saved.supported ? 'cursor-pointer' : 'opacity-50'}">
        <button
          role="switch"
          aria-checked={on}
          aria-label="Keep the computer awake during requests"
          class="relative h-5 w-9 flex-none rounded-full transition-colors {on ? 'bg-primary' : 'bg-line'}"
          disabled={!saved.supported || busy}
          onclick={toggle}
        >
          <span class="absolute top-0.5 left-0.5 size-4 rounded-full bg-text transition-transform {on ? 'translate-x-4' : ''}"></span>
        </button>
        Keep awake during requests
      </label>
    </div>
    <div class="mt-2 text-[12.5px] text-muted">
      {#if !saved.supported}
        Windows only.
      {:else if !saved.enabled}
        Off.
      {:else if saved.held && saved.in_flight === 0}
        Letting the computer sleep in a few seconds.
      {:else if saved.held}
        <span class="pulse text-secondary">●</span> Keeping the computer awake for {saved.in_flight}
        active {saved.in_flight === 1 ? 'request' : 'requests'}.
      {:else}
        No active requests.
      {/if}
    </div>
  {/if}
</section>
