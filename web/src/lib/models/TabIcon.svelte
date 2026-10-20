<script lang="ts">
  // The icons for the Models page's row buttons, one per tab of the model
  // detail: solid shapes drawn at 14 px on a 14-unit grid, so one unit is
  // one pixel.
  let { name }: { name: 'log' | 'loads' | 'builds' | 'requests' } = $props()
</script>

<svg viewBox="0 0 14 14" width="14" height="14" class="block" fill="currentColor" aria-hidden="true">
  {#if name === 'log'}
    <!-- A terminal window with a prompt cut out of it, like the sidebar's Logs icon. -->
    <path fill-rule="evenodd" d="M2 1h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V2a1 1 0 0 1 1-1zM3 5.5 4.5 4l3 3-3 3L3 8.5 4.5 7zM8 8h3v2H8z" />
  {:else if name === 'loads'}
    <!-- An arrow going up onto a base: a model loading. -->
    <path d="M7 1l5 5H9v4H5V6H2z" />
    <rect x="2" y="11" width="10" height="2" />
  {:else if name === 'builds'}
    <!-- A lightning bolt, for speed. -->
    <path d="M8.5 1 2.5 8h4l-1 5 6-7h-4z" />
  {:else}
    <!-- A request going out and an answer coming back, like the sidebar's Requests icon. -->
    <path d="M1 3h8V1l4 3-4 3V5H1z" />
    <path d="M13 9H5V7l-4 3 4 3v-2h8z" />
  {/if}
</svg>
