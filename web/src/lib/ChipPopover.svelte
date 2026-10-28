<script lang="ts">
  import { popover } from './popover.svelte'
  import { nextToCursor } from './tooltip'

  const tones = { warn: 'text-warn' }
  let box = $state<HTMLDivElement>()
  let w = $state(0)
  let h = $state(0)
  const at = $derived(nextToCursor(popover.x, popover.y, w, h))
  // Tool calls' arguments get more room.
  const wide = $derived(popover.chips.some((c) => c.calls?.length))

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
    class="fixed z-50 min-w-[200px] rounded-md border border-line bg-sunken px-2 py-1.5 text-xs whitespace-normal shadow-lg {wide ? 'max-w-[420px]' : 'max-w-[340px]'}"
    style="left:{at.left}px; top:{at.top}px"
  >
    {#each popover.chips as c, i (c.kind)}
      <div class={i ? 'mt-1.5' : ''}>
        <div class="font-semibold {c.tone ? tones[c.tone] : c.bright ? 'text-primary' : 'text-text'}" style={c.color ? `color:${c.color}` : undefined}>{c.title}</div>
        {#each c.lines as line}
          <div class="text-muted {c.mono ? 'font-mono text-[11px] break-all' : ''}">{line}</div>
        {/each}
        <!-- Each call's name, with its arguments under it, one to a line. -->
        {#each c.calls ?? [] as call, j}
          <div class="font-mono text-[11px] {j ? 'mt-1.5' : 'mt-1'}">
            <div class="font-semibold text-text">{call.name}</div>
            {#if call.args.length}
              <div class="mt-0.5 ml-[3px] border-l border-line pl-2">
                {#each call.args as a}
                  <div class="line-clamp-3 [overflow-wrap:anywhere] text-muted">{#if a.key}<span class="text-dim">{a.key}:</span>{' '}{/if}{a.value}</div>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
        {#if c.calls && c.total && c.total > c.calls.length}
          <div class="mt-1.5 text-dim">and {(c.total - c.calls.length).toLocaleString('en-US')} more</div>
        {/if}
        {#if c.sample}<div class="mt-1.5 break-words text-muted italic">{c.sample}</div>{/if}
      </div>
    {/each}
  </div>
{/if}
