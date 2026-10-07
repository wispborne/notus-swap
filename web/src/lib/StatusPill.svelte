<script lang="ts">
  import type { Summary } from './api'

  let { row, waiting = false }: { row: Summary; waiting?: boolean } = $props()

  const pill = $derived.by(() => {
    switch (row.state) {
      case 'in_flight':
        return { text: waiting ? 'waiting' : 'streaming', cls: 'bg-secondary/12 text-secondary pulse' }
      case 'done':
        return { text: String(row.status_code ?? 'done'), cls: row.status_code && row.status_code >= 400 ? 'bg-warn/15 text-warn' : 'bg-primary/15 text-primary' }
      case 'failed':
        return { text: row.status_code ? String(row.status_code) : 'failed', cls: 'bg-err/15 text-err' }
      case 'client_gone':
        return { text: 'client disconnected', cls: 'bg-warn/15 text-warn' }
      case 'interrupted':
        return { text: 'interrupted', cls: 'bg-dim/25 text-muted' }
      case 'cancelled':
        return { text: 'cancelled', cls: 'bg-dim/25 text-muted' }
    }
  })
</script>

<span class="inline-block rounded-full px-1.5 text-[11px] leading-[17px] font-semibold whitespace-nowrap {pill.cls}">{pill.text}</span>
