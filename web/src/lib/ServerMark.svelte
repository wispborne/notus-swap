<script lang="ts" module>
  /**
   * What a server's mark says, by colour: up, up with a problem, down,
   * asleep or off, and not checked yet (which pulses).
   */
  export type Tone = 'up' | 'warn' | 'down' | 'off' | 'checking'
</script>

<script lang="ts">
  import ServerIcon from './ServerIcon.svelte'
  import type { Server } from './servers.svelte'

  // A server's icon in place of a state dot: the same colours a dot had, on
  // the server's own picture, so one mark says both which server and how it
  // is. The tooltip should still give the state in words.
  let { server, tone, class: cls = '', title }: { server?: Server; tone: Tone; class?: string; title?: string } = $props()

  const color: Record<Tone, string> = {
    up: 'text-primary',
    warn: 'text-warn',
    down: 'text-err',
    off: 'text-dim',
    checking: 'pulse text-dim',
  }
</script>

<ServerIcon {server} class="{color[tone]} {cls}" {title} />
