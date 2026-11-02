<script lang="ts">
  import { autoColors, customColor, modelColor, modelShape, palette, paletteIndex, saveMark, shapes, type Shape } from './modelColors.svelte'
  import ModelMark from './ModelMark.svelte'
  import { peerStatus } from './servers.svelte'

  // Picks a model's colour (a palette colour or one of the user's own) and the
  // shape of its dot. Saved on the server that runs the model ('' for this
  // one), for every browser.
  let { model, server = '' }: { model: string; server?: string } = $props()

  // svelte-ignore state_referenced_locally
  const remote = server ? peerStatus.of(server) : null
  let error = $state('')
  const own = $derived(remote ? remote.customColor(model) : customColor(model))
  const index = $derived(remote ? remote.paletteIndex(model) : paletteIndex(model))
  const shape = $derived((remote ? remote.shape(model) : modelShape(model)) ?? 'circle')
  const color = $derived((remote ? remote.color(model) : undefined) ?? modelColor(model))

  async function save(change: { palette?: number; color?: string; shape: Shape }) {
    error = ''
    try {
      if (remote) await remote.saveMark(model, change)
      else await saveMark(model, change)
    } catch (e) {
      error = `Couldn't save: ${e instanceof Error ? e.message : e}`
    }
  }
  // Keeps the current colour when only the shape changes.
  const keep = () => (own ? { color: own } : { palette: index })

  const box = (on: boolean) => `grid size-6 place-items-center rounded border ${on ? 'border-primary bg-panel2' : 'border-transparent hover:border-line'}`
</script>

<div class="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-muted">
  <span class="grid grid-cols-[repeat(10,auto)] gap-0.5" role="group" aria-label="Colour">
    {#snippet swatch(c: string, i: number)}
      <button class={box(!own && index === i)} title="Colour {i + 1}" aria-label="Colour {i + 1}" onclick={() => save({ palette: i, shape })}>
        <ModelMark {shape} color={c} size={16} />
      </button>
    {/snippet}
    <!-- The colours given out automatically, then your own, then the rest in a second row. -->
    {#each palette.slice(0, autoColors) as c, i}{@render swatch(c, i)}{/each}
    <label class="{box(!!own)} cursor-pointer" title="Your own colour">
      <span class="sr-only">Your own colour</span>
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
        <path d="M0 0h8v8h-8z" fill="#ff5a36" /><path d="M8 0h8v8h-8z" fill="#f0b85a" />
        <path d="M0 8h8v8h-8z" fill="#5b8cff" /><path d="M8 8h8v8h-8z" fill={own ?? '#40d7a3'} />
      </svg>
      <input type="color" class="sr-only" value={color} onchange={(e) => save({ color: e.currentTarget.value, shape })} />
    </label>
    {#each palette.slice(autoColors) as c, i}{@render swatch(c, i + autoColors)}{/each}
  </span>
  <span class="grid grid-cols-[repeat(14,auto)] gap-0.5" role="group" aria-label="Shape">
    {#each Object.entries(shapes) as [k, s]}
      <button class={box(shape === k)} title={s.name} aria-label={s.name} onclick={() => save({ ...keep(), shape: k as Shape })}>
        <ModelMark {model} shape={k as Shape} {color} size={16} />
      </button>
    {/each}
  </span>
  {#if error}<span class="text-err">{error}</span>{/if}
</div>
