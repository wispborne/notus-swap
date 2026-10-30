<script lang="ts">
  // Picks the server a page acts on, for pages that show one server at a
  // time (Model Config, and the server sections of Settings). Shown only when
  // the Servers list has other servers. Picking one reloads the page.
  import { reachable, serverName, servers, showServer, stateWords } from './servers.svelte'

  /** The server shown now. '' is this server. */
  let { current }: { current: string } = $props()

  const isCurrent = (id: string, self: boolean) => (current ? id === current : self)
  const dot: Record<string, string> = {
    online: 'bg-primary',
    checking: 'bg-dim',
    needs_update: 'bg-warn',
    offline: 'bg-line',
    wrong_server: 'bg-err',
    not_notus: 'bg-err',
  }
</script>

{#if servers.many}
  <div class="flex flex-wrap items-center gap-1 text-[12.5px]" role="radiogroup" aria-label="Server">
    {#each servers.list as s (s.id)}
      {@const on = isCurrent(s.id, s.self)}
      <button
        role="radio"
        aria-checked={on}
        disabled={!on && !reachable(s)}
        title={s.self ? 'This server' : `${stateWords(s)}${s.url ? ` · ${s.url}` : ''}`}
        class="flex items-center gap-1.5 rounded-md border bg-panel2 px-2 py-0.5 disabled:opacity-50 {on ? 'border-primary text-text' : 'border-line text-muted hover:border-dim hover:text-text disabled:hover:border-line disabled:hover:text-muted'}"
        onclick={() => !on && showServer(s.id)}
      >
        <span class="size-2 flex-none rounded-full {dot[s.state] ?? 'bg-dim'}"></span>
        {serverName(s)}
      </button>
    {/each}
  </div>
{/if}
