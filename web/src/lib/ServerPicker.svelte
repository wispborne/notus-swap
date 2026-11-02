<script lang="ts">
  // Picks the server a page acts on, for pages that show one server at a
  // time (Model Config, and the server sections of Settings). Shown only when
  // the Servers list has other servers. Picking one reloads the page.
  import ServerMark from './ServerMark.svelte'
  import { reachable, serverName, servers, serverTone, showServer, stateWords } from './servers.svelte'

  /** The server shown now. '' is this server. */
  let { current }: { current: string } = $props()

  const isCurrent = (id: string, self: boolean) => (current ? id === current : self)
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
        class="flex items-center gap-1.5 rounded-md border bg-panel2 px-2 py-[2px] leading-[18px] disabled:opacity-50 {on ? 'border-primary text-text' : 'border-line text-muted hover:border-dim hover:text-text disabled:hover:border-line disabled:hover:text-muted'}"
        onclick={() => !on && showServer(s.id)}
      >
        <!-- 3 px of the icon stick out past the capitals, which whole pixels
             can't split evenly. 1 px lower puts the extra pixel under the
             baseline. -->
        <ServerMark server={s} tone={serverTone(s)} class="translate-y-px" />
        {serverName(s)}
      </button>
    {/each}
  </div>
{/if}
