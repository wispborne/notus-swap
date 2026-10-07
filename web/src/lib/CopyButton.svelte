<script lang="ts">
  // A small copy icon. `text` is a function so large bodies are only turned
  // into text when clicked. It shows a check mark for a moment after copying.
  let { text, title = 'Copy', class: cls = '' }: { text: () => string; title?: string; class?: string } = $props()

  let copied = $state(false)
  let timer: ReturnType<typeof setTimeout> | undefined

  async function copy(e: MouseEvent) {
    // Don't toggle a <details> or row this button sits in.
    e.preventDefault()
    e.stopPropagation()
    const t = text()
    try {
      await navigator.clipboard.writeText(t)
    } catch {
      // navigator.clipboard only exists over HTTPS or on localhost. Over
      // plain HTTP (such as the server's LAN address), fall back to execCommand.
      const area = document.createElement('textarea')
      area.value = t
      area.style.position = 'fixed'
      area.style.opacity = '0'
      document.body.append(area)
      area.select()
      const ok = document.execCommand('copy')
      area.remove()
      if (!ok) return
    }
    copied = true
    clearTimeout(timer)
    timer = setTimeout(() => (copied = false), 1500)
  }
</script>

<button
  type="button"
  class="inline-flex shrink-0 items-center justify-center rounded p-0.5 normal-case {copied ? 'text-primary' : 'text-dim hover:text-text'} {cls}"
  title={copied ? 'Copied' : title}
  aria-label={title}
  onclick={copy}
>
  <svg viewBox="0 0 16 16" class="size-[13px]" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if copied}
      <path d="M3 8.5l3 3 7-7" />
    {:else}
      <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
      <path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
    {/if}
  </svg>
</button>
