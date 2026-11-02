<script lang="ts">
  import { serverIcon, type Server } from './servers.svelte'
  import { serverIcons, type ServerIconName } from './serverIcons'

  // A server's icon, in the text colour. `icon` overrides the server's own,
  // for the picker.
  let { server, icon, class: cls = '', title }: { server?: Server; icon?: ServerIconName; class?: string; title?: string } = $props()

  const i = $derived(serverIcons[icon ?? serverIcon(server)])
</script>

<span class="inline-flex flex-none {cls}" {title}>
  <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" fill-rule="evenodd" shape-rendering={'crisp' in i ? 'crispEdges' : undefined} aria-hidden="true"
    ><path d={i.d} /></svg
  >
</span>
