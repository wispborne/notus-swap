<script lang="ts">
  import { fileName, formatBytes, imageBytes, imageFormat, openInTab, type Image } from './images'

  // Shows one of a request's images over the page, fitted to the window.
  // Clicking the image switches to its real size. The arrow keys move between
  // images, and Escape or a click outside closes it.
  let { images, index = $bindable(), onclose }: { images: Image[]; index: number; onclose: () => void } = $props()

  const img = $derived(images[index])
  const bytes = $derived(img ? imageBytes(img.src) : null)
  let full = $state(false)
  let size = $state<{ w: number; h: number } | null>(null)

  function go(d: number) {
    if (images.length < 2) return
    index = (index + d + images.length) % images.length
    full = false
    size = null
  }

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose()
    else if (e.key === 'ArrowLeft') go(-1)
    else if (e.key === 'ArrowRight') go(1)
    else return
    e.preventDefault()
  }
</script>

<svelte:window onkeydown={key} />

{#if img}
  <div class="fixed inset-0 z-50 flex flex-col bg-black/85" role="dialog" aria-modal="true" aria-label="Image viewer">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface px-3 py-1.5 text-xs">
      <span class="text-text">Image {index + 1} of {images.length}</span>
      <span class="text-dim">
        {imageFormat(img.src)}{size ? ` · ${size.w} × ${size.h}` : ''}{bytes !== null ? ` · ${formatBytes(bytes)}` : ''} · message {img.msg + 1}
      </span>
      <span class="ml-auto flex items-center gap-3">
        <button class="text-primary" onclick={() => (full = !full)}>{full ? 'Fit to window' : 'Actual size'}</button>
        <a class="text-primary" href={img.src} download={fileName(img.src, index + 1)}>Download</a>
        <button class="text-primary" onclick={() => openInTab(img.src)}>Open in new tab</button>
        <button class="px-1 text-base leading-none text-muted hover:text-text" title="Close (Esc)" aria-label="Close" onclick={onclose}>✕</button>
      </span>
    </div>
    <div
      class="relative flex min-h-0 flex-1 overflow-auto p-4 {full ? '' : 'items-center justify-center'}"
      role="presentation"
      onclick={(e) => e.target === e.currentTarget && onclose()}
    >
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
      <img
        src={img.src}
        alt="Image {index + 1} from the request"
        class="m-auto {full ? 'max-w-none cursor-zoom-out' : 'max-h-full max-w-full cursor-zoom-in object-contain'}"
        onload={(e) => {
          const el = e.currentTarget as HTMLImageElement
          size = { w: el.naturalWidth, h: el.naturalHeight }
        }}
        onclick={() => (full = !full)}
      />
    </div>
    {#if images.length > 1}
      <button
        class="fixed top-1/2 left-2 -translate-y-1/2 rounded-full bg-surface/80 px-3 py-2 text-lg text-muted hover:text-text"
        title="Previous (←)"
        aria-label="Previous image"
        onclick={() => go(-1)}>‹</button
      >
      <button
        class="fixed top-1/2 right-2 -translate-y-1/2 rounded-full bg-surface/80 px-3 py-2 text-lg text-muted hover:text-text"
        title="Next (→)"
        aria-label="Next image"
        onclick={() => go(1)}>›</button
      >
    {/if}
  </div>
{/if}
