<script lang="ts">
  import { modelColor, modelShape, shapes, type Shape } from './modelColors.svelte'

  // A model's dot: its colour and shape. `shape` and `color` override the
  // model's own, for previews. `size` should be a multiple of 8, so straight
  // edges stay on whole pixels.
  let {
    model = '',
    shape,
    color,
    size = 8,
    class: cls = '',
    title,
  }: { model?: string; shape?: Shape; color?: string; size?: number; class?: string; title?: string } = $props()

  const s = $derived(shape ?? modelShape(model))
</script>

<span class="inline-flex flex-none {cls}" {title}>
  <svg
    width={size}
    height={size}
    viewBox="0 0 8 8"
    fill={color ?? modelColor(model)}
    shape-rendering={'crisp' in shapes[s] ? 'crispEdges' : undefined}
    aria-hidden="true"><path d={shapes[s].d} /></svg
  >
</span>
