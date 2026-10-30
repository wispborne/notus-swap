<script lang="ts">
  // Shown in place of a server's sections while it doesn't answer. A
  // computer that sleeps shows this; it is not an error.
  import { lastUsed } from './format'
  import { serverName, stateWords, type Server } from './servers.svelte'

  let { server }: { server: Server | undefined } = $props()
</script>

<div class="mb-4 rounded-lg border border-line bg-panel p-4 text-[12.5px] text-muted">
  {#if server?.state === 'offline'}
    {serverName(server)} is asleep or off{server.since ? ` since ${lastUsed(server.since)}` : ''}. Its settings show here once it answers again.
  {:else if server}
    {serverName(server)}: {stateWords(server)}. {server.error ?? ''} Change its address in the Servers section if it moved.
  {:else}
    That server is no longer in the Servers list.
  {/if}
</div>
