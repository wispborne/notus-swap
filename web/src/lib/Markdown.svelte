<script lang="ts">
  import DOMPurify from 'dompurify'
  import { marked } from 'marked'

  // Model output is untrusted, so the HTML is sanitised before it is shown.
  let { text, cursor = false }: { text: string; cursor?: boolean } = $props()
  const html = $derived(DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true, breaks: true })))
</script>

<div class="md">
  {@html html}{#if cursor}<span class="cursor"></span>{/if}
</div>
