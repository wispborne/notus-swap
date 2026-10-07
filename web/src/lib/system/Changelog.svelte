<script lang="ts">
  import Markdown from '../Markdown.svelte'
  import { when } from '../format'

  export interface Entry {
    tag: string
    notes: string
    url?: string
    published: string
  }

  // markdown is false for notus-swap's own releases, whose notes are plain
  // commit messages.
  let {
    entries,
    isNew,
    markdown = true,
    error = '',
  }: { entries: Entry[] | null; isNew: (tag: string) => boolean; markdown?: boolean; error?: string } = $props()
</script>

<div class="mt-3 max-h-96 overflow-auto rounded-md border border-line bg-panel2 text-[12.5px]">
  {#if error}
    <p class="px-3 py-2 text-err">Couldn't load the changelog: {error}</p>
  {:else if !entries}
    <p class="px-3 py-2 text-muted">Loading…</p>
  {:else if !entries.length}
    <p class="px-3 py-2 text-muted">No releases found.</p>
  {:else}
    <ul class="divide-y divide-line-soft">
      {#each entries as e (e.tag)}
        <li class="px-3 py-1.5">
          <div class="flex items-baseline gap-2">
            <span class="font-mono {isNew(e.tag) ? 'text-primary' : 'text-text'}">{e.tag}</span>
            {#if isNew(e.tag)}<span class="rounded bg-primary/15 px-1.5 text-[11px] text-primary">not installed</span>{/if}
            <span class="flex-1"></span>
            <span class="text-dim">{when(Date.parse(e.published))}</span>
            {#if e.url}<a href={e.url} target="_blank" rel="noreferrer" class="text-primary">GitHub ↗</a>{/if}
          </div>
          {#if e.notes}
            {#if markdown}
              <div class="mt-0.5 text-muted"><Markdown text={e.notes} /></div>
            {:else}
              <div class="mt-0.5 whitespace-pre-wrap text-muted">{e.notes}</div>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>
