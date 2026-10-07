<script lang="ts">
  // A small bar for the status bar: one segment per entry, as wide as its
  // total, filled by its use. Only the outer ends of the whole bar are rounded.
  let { segments, color }: { segments: { used: number; total: number; color?: string }[]; color: string } = $props()
</script>

<span class="flex h-1.5 w-16 gap-0.5">
  {#each segments as g, i}
    <span
      class="h-full min-w-0 overflow-hidden bg-grid {i === 0 ? 'rounded-l-full' : ''} {i === segments.length - 1 ? 'rounded-r-full' : ''}"
      style="flex:{g.total} 1 0"
    >
      <span class="block h-full" style="width:{g.total > 0 ? Math.min(100, (g.used / g.total) * 100) : 0}%;background:{g.color ?? color}"></span>
    </span>
  {/each}
</span>
