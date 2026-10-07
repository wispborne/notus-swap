<script lang="ts">
  import { onMount } from 'svelte'
  import LogPanel from '../lib/logs/LogPanel.svelte'
  import type { LogSource } from '../lib/logs/stream.svelte'
  import { privacy } from '../lib/privacy.svelte'
  import { status } from '../lib/status.svelte'

  // llama-swap's own log ("proxy"), all model output ("upstream"), one
  // model's output, or notus-swap's log. One panel, or two side by side like
  // llama-swap's Logs page.

  onMount(() => status.start())

  function saved<T>(key: string, fallback: T): T {
    try {
      const v = localStorage.getItem(key)
      return v == null ? fallback : JSON.parse(v)
    } catch {
      return fallback
    }
  }
  function save(key: string, v: unknown) {
    try {
      localStorage.setItem(key, JSON.stringify(v))
    } catch {}
  }

  let split = $state(saved('logs-split', true))
  let sources = $state<LogSource[]>(saved('logs-sources', ['proxy', 'upstream']))
  $effect(() => save('logs-sources', sources))

  const models = $derived((status.current?.llama_swap.known ?? []).map((m) => m.model).filter(privacy.visible))
  const fixed: [LogSource, string][] = [
    ['proxy', 'llama-swap'],
    ['upstream', 'All models'],
    ['notus', 'notus-swap'],
  ]
</script>

{#snippet picker(i: number)}
  <select
    bind:value={sources[i]}
    class="max-w-[260px] rounded border border-line bg-panel2 px-1 text-xs leading-5 font-semibold outline-none focus:border-primary"
    aria-label="Log source"
  >
    {#each fixed as [v, label]}
      <option value={v}>{label}{v === 'upstream' && !privacy.logSource(v) ? ' (unavailable while models are hidden)' : ''}</option>
    {/each}
    {#if models.length}
      <optgroup label="One model">
        {#each models as m}<option value="model:{m}">{m}</option>{/each}
      </optgroup>
    {/if}
    {#if sources[i].startsWith('model:') && !models.includes(sources[i].slice(6))}
      <option value={sources[i]}>{privacy.logSource(sources[i]) ? sources[i].slice(6) : 'Hidden model'}</option>
    {/if}
  </select>
{/snippet}

<div class="flex h-[calc(100vh-34px-3.5rem)] min-h-[420px] flex-col gap-2">
  <div class="flex items-center gap-3">
    <h1 class="text-[17px] font-semibold">Logs</h1>
    <div class="flex overflow-hidden rounded-md border border-line text-xs">
      <button class="px-2 py-0.5 {!split ? 'bg-primary/15 text-primary' : 'text-muted hover:text-text'}" onclick={() => save('logs-split', (split = false))}>One panel</button>
      <button class="border-l border-line px-2 py-0.5 {split ? 'bg-primary/15 text-primary' : 'text-muted hover:text-text'}" onclick={() => save('logs-split', (split = true))}>Side by side</button>
    </div>
    <span class="hidden text-xs text-dim md:inline">
      llama-swap keeps the newest 100 KB of each log, so older lines may be missing. notus-swap's full log is in <code>journalctl -u notus-swap</code>.
    </span>
  </div>

  <div class="flex min-h-0 flex-1 flex-col gap-2 md:flex-row">
    <div class="min-h-[300px] min-w-0 flex-1">
      <LogPanel source={sources[0]} id="logs-0">
        {#snippet title()}{@render picker(0)}{/snippet}
      </LogPanel>
    </div>
    {#if split}
      <div class="min-h-[300px] min-w-0 flex-1">
        <LogPanel source={sources[1]} id="logs-1">
          {#snippet title()}{@render picker(1)}{/snippet}
        </LogPanel>
      </div>
    {/if}
  </div>
</div>
