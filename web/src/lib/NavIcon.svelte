<script lang="ts">
  // The icons for the sidebar and the phone's bottom bar: solid shapes,
  // drawn at 18 px on an 18-unit grid, so one unit is one pixel. Straight
  // edges sit on whole pixels so they stay sharp. That only holds when the
  // icon itself sits on a whole pixel, so the sidebar uses pixel sizes
  // around it.
  let { name }: { name: string } = $props()

  // The models icon's corners. Its mask needs an ID unique to each copy.
  const id = $props.id()
  const corners = [
    [9, 2],
    [16, 6],
    [16, 12],
    [9, 16],
    [2, 12],
    [2, 6],
    [9, 10],
  ]
</script>

<svg viewBox="0 0 18 18" width="18" height="18" class="block" fill="currentColor" aria-hidden="true">
  {#if name === 'dashboard'}
    <!-- Widgets of different sizes on a grid, 2 px apart. -->
    <rect x="1" y="1" width="7" height="9" rx="1" />
    <rect x="10" y="1" width="7" height="5" rx="1" />
    <rect x="10" y="8" width="7" height="9" rx="1" />
    <rect x="1" y="12" width="7" height="5" rx="1" />
  {:else if name === 'requests'}
    <!-- A request going out and an answer coming back. -->
    <path d="M1 4h11V1l5 4-5 4V6H1z" />
    <path d="M17 12H6V9l-5 4 5 4v-3h11z" />
  {:else if name === 'models'}
    <!-- A cube of lines with a dot at each corner. The lines stop short of
         each dot, so every dot stands apart. -->
    <mask id="cube-{id}" maskUnits="userSpaceOnUse" x="0" y="0" width="18" height="18">
      <rect width="18" height="18" fill="#fff" />
      {#each corners as [x, y]}<circle cx={x} cy={y} r="2.3" fill="#000" />{/each}
    </mask>
    <path
      d="M9 2 16 6V12L9 16 2 12V6zM2 6 9 10 16 6M9 10V16"
      mask="url(#cube-{id})"
      fill="none"
      stroke="currentColor"
      stroke-width="1.6"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    {#each corners as [x, y]}<circle cx={x} cy={y} r="1.6" />{/each}
  {:else if name === 'logs'}
    <!-- A terminal window with a prompt cut out of it. -->
    <path
      fill-rule="evenodd"
      d="M3 2h12a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM4 6.5 5.5 5l4 4-4 4L4 11.5 6.5 9zM10 11h5v2h-5z"
    />
  {:else if name === 'config'}
    <!-- Braces, for the config file. Drawn as thick lines, since braces are mostly curves. -->
    <path
      d="M6 2C4 2 4 3 4 5v1.5C4 8 3 9 1.5 9 3 9 4 10 4 11.5V13c0 2 0 3 2 3M12 2c2 0 2 1 2 3v1.5c0 1.5 1 2.5 2.5 2.5-1.5 0-2.5 1-2.5 2.5V13c0 2 0 3-2 3"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  {:else if name === 'collapse' || name === 'expand'}
    <!-- A bar for the sidebar's edge, and an arrow pointing the way it moves. -->
    <rect x="2" y="2" width="2" height="14" rx="1" />
    <path d={name === 'collapse' ? 'M12 4 7 9l5 5 1.5-1.5L10 9l3.5-3.5z' : 'M8 4l5 5-5 5-1.5-1.5L10 9 6.5 5.5z'} />
  {:else if name === 'system'}
    <!-- A gear with a hole in the middle. -->
    <path fill-rule="evenodd" d="M14.83 6.88 17 7.3 17 10.7 14.83 11.12 14.62 11.62 15.86 13.45 13.45 15.86 11.62 14.62 11.12 14.83 10.7 17 7.3 17 6.88 14.83 6.38 14.62 4.55 15.86 2.14 13.45 3.38 11.62 3.17 11.12 1 10.7 1 7.3 3.17 6.88 3.38 6.38 2.14 4.55 4.55 2.14 6.38 3.38 6.88 3.17 7.3 1 10.7 1 11.12 3.17 11.62 3.38 13.45 2.14 15.86 4.55 14.62 6.38zM9 6.5a2.5 2.5 0 1 0 0 5a2.5 2.5 0 1 0 0-5z" />
  {/if}
</svg>
