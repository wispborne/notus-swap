<script lang="ts">
  import type { Chip } from './notable'
  import { popover } from './popover.svelte'

  let { chips: given, colorful = false }: { chips: Chip[]; colorful?: boolean } = $props()

  // Without Colorful, chips drop their own colour, so the popover drops it too.
  const chips = $derived(colorful ? given : given.map((c) => ({ ...c, color: undefined })))

  // Four slots fit in the column: four chips, or three and "+N" for the rest.
  const MAX = 4
  const shown = $derived(chips.length > MAX ? chips.slice(0, MAX - 1) : chips)
  const rest = $derived(chips.length > MAX ? chips.slice(MAX - 1) : [])
  const tint = (color: string, s: { text: number; fill: number }) =>
    `color:color-mix(in srgb, ${color} ${s.text}%, transparent); background:color-mix(in srgb, ${color} ${s.fill}%, transparent)`
  const tones = { warn: 'bg-warn/15 text-warn' }
  const more = $derived<Chip>({ kind: 'more', label: `+${rest.length}`, bright: false, title: '', lines: [] })
</script>

{#snippet icon(name: Chip['icon'])}
  <svg viewBox="0 0 16 16" class="size-[11px]" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if name === 'tool'}
      <!-- Filled, so it reads better at 11 px than the outline alone. -->
      <path d="M10.5 2.5a3 3 0 0 0-3.2 4.1L2.8 11.1a1.4 1.4 0 0 0 2 2l4.5-4.5a3 3 0 0 0 4.1-3.2l-1.9 1.9-1.8-.4-.4-1.8z" fill="currentColor" stroke-width="1" />
    {:else if name === 'think' || name === 'nothink'}
      <path d="M4 9.5a3 3 0 0 1 .6-5.9 3.3 3.3 0 0 1 6.2-.4A2.9 2.9 0 0 1 12 9.5z" />
      <circle cx="5" cy="12" r="1.1" fill="currentColor" stroke="none" />
      <circle cx="3" cy="14.3" r=".7" fill="currentColor" stroke="none" />
      {#if name === 'nothink'}<path d="M2 14 14 2" />{/if}
    {:else if name === 'warn'}
      <!-- A solid triangle with the "!" cut out (even-odd fill), which reads better at 11 px than an outline. -->
      <path d="M8 1.4 15.3 14.3H.7zM6.9 5.4h2.2v4.8H6.9zM6.9 11.3h2.2v1.9H6.9z" fill="currentColor" fill-rule="evenodd" stroke="none" />
    {:else if name === 'image'}
      <!-- A frame with a filled hill and sun: solid shapes read better at 11 px than thin lines. -->
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
      <path d="M3 12.5 6.5 7.5l3 3.5 1.8-1.8 2.2 3.3z" fill="currentColor" stroke-width="1" />
      <circle cx="11" cy="5.8" r="1.4" fill="currentColor" stroke="none" />
    {:else if name === 'nostream'}
      <!-- Lines of text arriving one by one, crossed out like "nothink". The first is solid and the rest dim, so they read as a stream. -->
      <rect x="1" y="2" width="9" height="3" fill="currentColor" stroke="none" />
      <rect x="1" y="7" width="13" height="3" fill="currentColor" stroke="none" opacity=".35" />
      <rect x="1" y="12" width="6" height="2" fill="currentColor" stroke="none" opacity=".35" />
      <path d="M2 15 15 2" />
    {/if}
  </svg>
{/snippet}

<!-- Four pips stacked in a column, the bottom `level` of them lit. Whole
     pixels with crisp edges, so they don't blur. For "max", above xhigh,
     the top pip glows: pink with Colorful on, near-white in the chip's own colour without. -->
{#snippet pips(level: number, peak = false)}
  {@const glow = colorful ? '#ff7a9a' : 'currentColor'}
  <svg viewBox="0 0 5 11" width="5" height="11" class="mr-0.5 overflow-visible" fill="currentColor" shape-rendering="crispEdges" aria-hidden="true">
    {#each [0, 1, 2, 3] as i}
      {#if peak && i === 3}
        <!-- Three stacked shadows, because one is too faint around a pip this small. Without Colorful the pip is whitened, so it still stands out from the other pips. -->
        <rect
          x="0"
          y={9 - i * 3}
          width="5"
          height="2"
          style="fill: {colorful ? glow : 'color-mix(in srgb, currentColor 35%, white)'}; filter: drop-shadow(0 0 1px {glow}) drop-shadow(0 0 3px {glow}) drop-shadow(0 0 6px {glow})"
        />
      {:else}
        <rect x="0" y={9 - i * 3} width="5" height="2" opacity={i < level ? 1 : 0.3} />
      {/if}
    {/each}
  </svg>
{/snippet}

{#snippet one(c: Chip, list: Chip[])}
  <button
    type="button"
    class="inline-flex h-4 shrink-0 cursor-default items-center gap-0.5 rounded px-1 font-mono text-[10.5px] leading-4 font-semibold {c.tone
      ? tones[c.tone]
      : c.color
        ? ''
        : c.bright
          ? 'bg-primary/15 text-primary'
          : 'bg-[#353a44] text-muted'}"
    style={c.color && !c.tone ? tint(c.color, c.strength ?? { text: 100, fill: 15 }) : undefined}
    aria-label={list.map((x) => x.title).join(', ')}
    onmouseenter={(e) => popover.show(list, e.currentTarget, e)}
    onmousemove={(e) => popover.move(e)}
    onmouseleave={(e) => popover.hide(e.currentTarget)}
    onclick={(e) => {
      // Don't open the row.
      e.stopPropagation()
      popover.toggle(list, e.currentTarget, e)
    }}
  >
    {#if c.icon}{@render icon(c.icon)}{/if}{#if c.level}{@render pips(c.level, c.peak)}{/if}{#if c.unit}<span>{c.label}<span class="ml-px text-[9px] leading-none font-normal opacity-90">{c.unit}</span></span>{:else}{c.label}{/if}
  </button>
{/snippet}

{#if chips.length}
  <!-- As wide as its chips, up to 110 px, so the table gives the column room for them. -->
  <div class="flex w-max max-w-[110px] gap-[3px] overflow-hidden">
    {#each shown as c (c.kind)}{@render one(c, [c])}{/each}
    {#if rest.length}{@render one(more, rest)}{/if}
  </div>
{:else}
  <span class="text-dim">–</span>
{/if}
