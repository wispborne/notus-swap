<script lang="ts">
  // A small "i" in a circle that shows longer notes as its tooltip, for
  // details that would otherwise sit under a section as small print. Put it
  // after a heading's text, in a flex row with items-center.
  let { text, class: cls = '' }: { text: string; class?: string } = $props()
</script>

<span class="inline-flex flex-none cursor-help text-dim hover:text-muted {cls}" title={text} role="img" aria-label={text}>
  <!-- A circle with the "i" cut out, on a 12-pixel grid. -->
  <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" fill-rule="evenodd" aria-hidden="true"
    ><path d="M6 0a6 6 0 1 1 0 12A6 6 0 1 1 6 0zM5 2v2h2V2zM5 5v5h2V5z" /></svg
  >
</span>
