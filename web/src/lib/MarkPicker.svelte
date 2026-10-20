<script lang="ts">
  import { customColor, modelColor, modelShape, palette, paletteIndex, saveMark, shapes, type Shape } from './modelColors.svelte'
  import ModelMark from './ModelMark.svelte'

  // Picks a model's colour (a palette colour or one of the user's own) and the
  // shape of its dot. Saved on the server for every browser.
  let { model }: { model: string } = $props()

  let error = $state('')
  const own = $derived(customColor(model))
  const index = $derived(paletteIndex(model))
  const shape = $derived(modelShape(model))

  async function save(change: { palette?: number; color?: string; shape: Shape }) {
    error = ''
    try {
      await saveMark(model, change)
    } catch (e) {
      error = `Couldn't save: ${e instanceof Error ? e.message : e}`
    }
  }
  // Keeps the current colour when only the shape changes.
  const keep = () => (own ? { color: own } : { palette: index })

  const box = (on: boolean) => `grid size-6 place-items-center rounded border ${on ? 'border-primary bg-panel2' : 'border-transparent hover:border-line'}`
</script>

<div class="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-muted">
  <span class="flex items-center gap-0.5" role="group" aria-label="Colour">
    {#each palette as c, i}
      <button class={box(!own && index === i)} title="Colour {i + 1}" aria-label="Colour {i + 1}" onclick={() => save({ palette: i, shape })}>
        <ModelMark {shape} color={c} size={16} />
      </button>
    {/each}
    <label class="{box(!!own)} cursor-pointer" title="Your own colour">
      <span class="sr-only">Your own colour</span>
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
        <path d="M0 0h8v8h-8z" fill="#ff5a36" /><path d="M8 0h8v8h-8z" fill="#f0b85a" />
        <path d="M0 8h8v8h-8z" fill="#5b8cff" /><path d="M8 8h8v8h-8z" fill={own ?? '#40d7a3'} />
      </svg>
      <input type="color" class="sr-only" value={modelColor(model)} onchange={(e) => save({ color: e.currentTarget.value, shape })} />
    </label>
  </span>
  <span class="flex items-center gap-0.5" role="group" aria-label="Shape">
    {#each Object.entries(shapes) as [k, s]}
      <button class={box(shape === k)} title={s.name} aria-label={s.name} onclick={() => save({ ...keep(), shape: k as Shape })}>
        <ModelMark {model} shape={k as Shape} size={16} />
      </button>
    {/each}
  </span>
  {#if error}<span class="text-err">{error}</span>{/if}
</div>
