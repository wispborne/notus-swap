<script lang="ts">
  import { popover } from './popover.svelte'
  import { nextToCursor } from './tooltip'

  const tones = { warn: 'text-warn' }
  let box = $state<HTMLDivElement>()
  let w = $state(0)
  let h = $state(0)
  const at = $derived(nextToCursor(popover.x, popover.y, w, h))

  // A click anywhere else closes a pinned popover. Chips stop their own
  // clicks from reaching here.
  function outside(e: MouseEvent) {
    if (popover.pinned && !box?.contains(e.target as Node)) popover.close()
  }
</script>

<svelte:window onclick={outside} onscrollcapture={() => popover.close()} onkeydown={(e) => e.key === 'Escape' && popover.close()} />

{#if popover.chips.length}
  <div
    bind:this={box}
    bind:offsetWidth={w}
    bind:offsetHeight={h}
    role="tooltip"
    class="fixed z-50 max-w-[340px] min-w-[200px] rounded-md border border-line bg-sunken px-2 py-1.5 text-xs whitespace-normal shadow-lg"
    style="left:{at.left}px; top:{at.top}px"
  >
    {#each popover.chips as c, i (c.kind)}
      <div class={i ? 'mt-1.5' : ''}>
        <div class="font-semibold {c.tone ? tones[c.tone] : c.bright ? 'text-primary' : 'text-text'}" style={c.color ? `color:${c.color}` : undefined}>{c.title}</div>
        {#each c.lines as line}
          <div class="text-muted {c.mono ? 'font-mono text-[11px] break-all' : ''}">{line}</div>
        {/each}
      </div>
    {/each}
  </div>
{/if}
