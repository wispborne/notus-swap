<script lang="ts">
  let {
    value,
    max,
    unit,
    label,
    color,
    detail = '',
    warnAt,
    dangerAt,
    warnNote = 'high',
    segments = [],
    layers = [],
  }: {
    value: number | null
    max: number
    unit: string
    label: string
    color: string
    detail?: string
    /** At or above this value the arc turns amber and the label says `warnNote`. */
    warnAt?: number
    /** At or above this value the arc turns red. */
    dangerAt?: number
    warnNote?: string
    /** Splits the arc into one piece per entry, each as wide as its `max`, filled by its own `value`. Used when there is more than one. */
    segments?: { value: number; max: number }[]
    /** Draws every entry on the one arc, each filled by its `value` out of its own `max` in its own colour, the emptiest on top. Used when there is more than one. */
    layers?: { label: string; value: number; max: number; color: string }[]
  } = $props()

  const levelOf = (v: number | null) => (v == null ? 0 : dangerAt != null && v >= dangerAt ? 2 : warnAt != null && v >= warnAt ? 1 : 0)
  const colorOf = (l: number) => (l === 2 ? 'var(--color-err)' : l === 1 ? 'var(--color-warn)' : color)
  const level = $derived(levelOf(value))
  const arcColor = $derived(colorOf(level))

  const r = 54, cx = 70, cy = 66
  const frac = $derived(value == null || max <= 0 ? 0 : Math.max(0, Math.min(1, value / max)))
  const at = (a: number) => [cx + r * Math.cos(a), cy + r * Math.sin(a)]
  const arc = $derived.by(() => {
    const [x0, y0] = at(Math.PI)
    const [x1, y1] = at(Math.PI + frac * Math.PI)
    return `M${x0},${y0}A${r},${r} 0 0 1 ${x1},${y1}`
  })
  const track = (() => {
    const [x0, y0] = at(Math.PI)
    const [x1, y1] = at(2 * Math.PI)
    return `M${x0},${y0}A${r},${r} 0 0 1 ${x1},${y1}`
  })()
  // One track and one fill per segment. The round end caps stick out by half the stroke, so each
  // line stops that far short of its piece and the caps fill the rest. This keeps the outer ends
  // where the single arc has them, and leaves a small visible gap between neighbours. A fill's
  // line is shortened by both caps too, so its drawn length is its share of the piece. The
  // smallest fill that can be drawn is a round dot, one stroke wide. Each piece takes its own
  // warning colour, so only the one that is high turns amber or red.
  const cap = 5 / r
  const gap = 0.05
  const pieces = $derived.by(() => {
    const total = segments.reduce((s, g) => s + g.max, 0)
    if (segments.length < 2 || total <= 0) return []
    const span = Math.PI + 2 * cap - gap * (segments.length - 1)
    const part = (from: number, to: number) => {
      const [x0, y0] = at(from)
      const [x1, y1] = at(to)
      return `M${x0},${y0}A${r},${r} 0 0 1 ${x1},${y1}`
    }
    let start = Math.PI - cap
    return segments.map((g) => {
      const len = (g.max / total) * span
      const f = g.max > 0 ? Math.max(0, Math.min(1, g.value / g.max)) : 0
      const from = start + cap
      const line = len - 2 * cap
      const piece = { track: part(from, from + line), fill: f > 0 ? part(from, from + Math.max(0, len * f - 2 * cap)) : '', color: colorOf(levelOf(g.value)) }
      start += len + gap
      return piece
    })
  })
  // Overlaid layers: every entry is filled from the start of the one track, the fullest drawn first
  // and the emptiest on top, so each one's end stays visible. A layer that is high takes the
  // warning colour in place of its own.
  const layerArcs = $derived.by(() => {
    if (layers.length < 2) return []
    const fracOf = (g: { value: number; max: number }) => (g.max > 0 ? Math.max(0, Math.min(1, g.value / g.max)) : 0)
    return [...layers]
      .sort((a, b) => fracOf(b) - fracOf(a))
      .map((g) => {
        const f = fracOf(g)
        const [x0, y0] = at(Math.PI)
        const [x1, y1] = at(Math.PI + f * Math.PI)
        const l = levelOf(g.value)
        return { fill: f > 0 ? `M${x0},${y0}A${r},${r} 0 0 1 ${x1},${y1}` : '', color: l ? colorOf(l) : g.color }
      })
  })
  const shown = $derived(value == null ? '–' : value >= 100 ? Math.round(value).toString() : value.toFixed(1))
</script>

<div class="flex h-full flex-col items-center justify-center" title={detail}>
  <svg viewBox="0 0 140 82" class="max-h-[calc(100%-18px)] w-full max-w-[180px]">
    {#if layerArcs.length}
      <path d={track} style:stroke="var(--color-grid)" stroke-width="10" fill="none" stroke-linecap="round" />
      {#each layerArcs as a}
        {#if a.fill}<path d={a.fill} stroke={a.color} stroke-width="10" fill="none" stroke-linecap="round" />{/if}
      {/each}
    {:else if pieces.length}
      {#each pieces as p}
        <path d={p.track} style:stroke="var(--color-grid)" stroke-width="10" fill="none" stroke-linecap="round" />
        {#if p.fill}<path d={p.fill} stroke={p.color} stroke-width="10" fill="none" stroke-linecap="round" />{/if}
      {/each}
    {:else}
      <path d={track} style:stroke="var(--color-grid)" stroke-width="10" fill="none" stroke-linecap="round" />
      {#if frac > 0}<path d={arc} stroke={arcColor} stroke-width="10" fill="none" stroke-linecap="round" />{/if}
    {/if}
    <text x="70" y="62" text-anchor="middle" style:fill="var(--color-text)" font-size="22" font-weight="700">{shown}</text>
    <text x="70" y="79" text-anchor="middle" style:fill="color-mix(in srgb, var(--color-text) 25%, var(--color-muted))" font-size="11">{unit} of {Math.round(max)}</text>
  </svg>
  <div class="text-xs text-muted">
    {#if layers.length > 1}
      <!-- Layers name each card by its colour in place of the label. -->
      {#each layers as g}<span class="mr-1.5 whitespace-nowrap"><span class="mr-0.5" style="color:{g.color}">●</span>{g.label}</span>{/each}
    {:else}{label}{/if}{#if level}<span class="font-semibold {level === 2 ? 'text-err' : 'text-warn'}"> · {warnNote}</span>{/if}
  </div>
</div>
