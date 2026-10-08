<script lang="ts">
  import { tick } from 'svelte'
  import CopyButton from './CopyButton.svelte'
  import ImageViewer from './ImageViewer.svelte'
  import { formatBytes, imageBytes, imageFormat, imageSrc, requestImages } from './images'
  import Markdown from './Markdown.svelte'
  import { ansiToHtml, stripAnsi } from './logs/ansi'
  import { responsesMessages, type Msg } from './responsesInput'

  // Shows a request body: chat messages, a Responses API input, a completion
  // prompt, or embedding input, plus the tools offered and the other settings sent.
  let {
    request,
    cached = null,
    promptTokens = null,
  }: { request: unknown; cached?: { tokens: number; total: number } | null; promptTokens?: number | null } = $props()

  const req = $derived(typeof request === 'object' && request !== null ? (request as Record<string, unknown>) : null)
  const fromResponses = $derived(req ? responsesMessages(req) : null)
  const messages = $derived((fromResponses ?? (Array.isArray(req?.messages) ? req.messages : [])) as Msg[])
  // Chat tools nest under "function"; Responses API tools don't.
  type Tool = { type?: string; name?: string; description?: string; function?: { name?: string; description?: string } }
  const tools = $derived((Array.isArray(req?.tools) ? req.tools : []) as Tool[])
  const settings = $derived(
    req
      ? Object.entries(req).filter(
          ([k, v]) => !['messages', 'tools', 'prompt', 'input', 'instructions', 'model'].includes(k) && (typeof v !== 'object' || v === null),
        )
      : [],
  )
  const prompt = $derived(fromResponses ? undefined : (req?.prompt ?? req?.input))

  const roleStyle: Record<string, string> = {
    system: 'border-l-dim',
    user: 'border-l-secondary',
    assistant: 'border-l-primary',
    tool: 'border-l-warn',
  }

  // Every image in the request, in order. Clicking any of them opens the viewer.
  const images = $derived(requestImages(messages))
  let viewing = $state<number | null>(null)
  const view = (src: string) => (viewing = Math.max(0, images.findIndex((im) => im.src === src)))
  const describe = (src: string) => {
    const bytes = imageBytes(src)
    return `${imageFormat(src)}${bytes !== null ? ` · ${formatBytes(bytes)}` : ''}`
  }

  // Long conversations start with most messages collapsed to two lines.
  // Only the newest message, and the newest user or tool message, start open,
  // because those are what the response answers.
  const FEW = 4
  let open = $state(new Set<number>())
  let openedFor = -1
  $effect(() => {
    const n = messages.length
    if (n === openedFor) return
    openedFor = n
    if (n <= FEW) {
      open = new Set(messages.keys())
      return
    }
    const start = new Set([n - 1])
    for (let i = n - 1; i >= 0; i--) {
      if (messages[i].role === 'user' || messages[i].role === 'tool') {
        start.add(i)
        break
      }
    }
    open = start
    tick().then(() => scrollToOpened(Math.min(...start)))
  })

  function toggle(i: number) {
    const next = new Set(open)
    if (next.has(i)) next.delete(i)
    else next.add(i)
    open = next
  }

  // The list scrolls on its own, so the response stays in view beside it.
  // It opens scrolled to the first message that starts open.
  let list = $state<HTMLDivElement>()
  function scrollToOpened(i: number) {
    const el = list?.querySelector<HTMLElement>(`[data-msg="${i}"]`)
    if (list && el) list.scrollTop = el.offsetTop - list.offsetTop
  }

  const roleCounts = $derived.by(() => {
    const counts = new Map<string, number>()
    for (const m of messages) counts.set(m.role ?? '?', (counts.get(m.role ?? '?') ?? 0) + 1)
    return [...counts].map(([r, c]) => `${c} ${r}`).join(', ')
  })

  // Plain text of a message, for the collapsed preview and its length.
  function plain(m: Msg) {
    const parts: string[] = []
    if (typeof m.content === 'string') parts.push(m.content)
    else if (Array.isArray(m.content))
      for (const p of m.content) parts.push(p.text ?? (imageSrc(p) ? '[image]' : `[${p.type ?? 'part'}]`))
    for (const tc of m.tool_calls ?? []) parts.push(`${tc.function?.name}(${tc.function?.arguments ?? ''})`)
    return parts.join('\n')
  }

  // Tool output often holds terminal colour codes. They are drawn as colours
  // in the open message and removed from previews, counts and copies.
  const shown = (m: Msg) => stripAnsi(plain(m))
  // The start of a long message, without a colour code cut in half at the end.
  const clipped = (text: string) => text.slice(0, LONG).replace(/\x1b\[[0-9;?]*$/, '') + '…'

  let expanded = $state(new Set<number>())
  const LONG = 1200

  // llama.cpp reports a cached token count, not its position. Estimate the
  // cached prefix from message lengths, with tools before messages as in most
  // chat templates. Allow for template markers that plain() does not include.
  const PER_MESSAGE = 16
  const cacheShare = $derived.by(() => {
    if (!cached || cached.total <= 0 || !messages.length) return null
    const toolSize = tools.length ? JSON.stringify(tools).length : 0
    const sizes = messages.map((m) => plain(m).length + PER_MESSAGE)
    const all = toolSize + sizes.reduce((a, b) => a + b, 0)
    let left = Math.min(1, cached.tokens / cached.total) * all - toolSize
    return sizes.map((n) => {
      const part = Math.max(0, Math.min(1, left / n))
      left -= n
      // Hide small estimation errors at the cache boundary.
      return part >= 0.9 ? 1 : part <= 0.1 ? 0 : part
    })
  })
  // The server only counts tokens for the whole prompt, so each message's
  // count is its share of the prompt's characters. Images take tokens that
  // have no characters, so requests with images get no estimate.
  const perChar = $derived.by(() => {
    if (!promptTokens || !messages.length || images.length) return null
    const toolSize = tools.length ? JSON.stringify(tools).length : 0
    const all = toolSize + messages.reduce((a, m) => a + plain(m).length + PER_MESSAGE, 0)
    return all > 0 ? promptTokens / all : null
  })
  const msgTokens = (m: Msg) => Math.round((plain(m).length + PER_MESSAGE) * (perChar ?? 0))
  const cachedCount =$derived(cacheShare ? cacheShare.filter((p) => p === 1).length : 0)
  const cacheEnd = $derived(cacheShare ? cacheShare.findIndex((p) => p < 1) : -1)
</script>

{#if !req}
  <pre class="text-xs whitespace-pre-wrap text-muted">{String(request ?? '(no request body stored)')}</pre>
{:else}
  {#if settings.length}
    <div class="mb-2 flex flex-wrap gap-1.5">
      {#each settings as [k, v]}
        <span class="rounded border border-line bg-panel2 px-1.5 text-[11px]"><span class="text-dim">{k}</span> {String(v)}</span>
      {/each}
    </div>
  {/if}

  {#if tools.length}
    <details class="mb-2 rounded border border-dashed border-line px-2 py-1 text-muted">
      <summary class="cursor-pointer text-xs">
        {tools.length} tool{tools.length === 1 ? '' : 's'} available to the model
        <CopyButton class="align-middle" title="Copy tool definitions" text={() => JSON.stringify(tools, null, 2)} />
      </summary>
      <ul class="mt-1 text-xs">
        {#each tools as t}
          <li>
            <span class="font-mono text-text">{t.function?.name ?? t.name ?? t.type}</span>
            <span class="text-dim">{t.function?.description ?? t.description ?? ''}</span>
          </li>
        {/each}
      </ul>
    </details>
  {/if}

  {#if messages.length > FEW}
    <div class="mb-1.5 flex flex-wrap items-center gap-x-2 text-xs text-dim">
      <span>{messages.length} messages · {roleCounts}</span>
      <button class="text-primary" onclick={() => (open = new Set(messages.keys()))}>Expand all</button>
      <button class="text-primary" onclick={() => (open = new Set())}>Collapse all</button>
    </div>
  {/if}

  {#if cached && cacheShare}
    <div class="mb-1.5 text-xs text-dim" title="The server only reports how many tokens were cached, so where the cache ends is estimated from message lengths.">
      {#if cached.tokens === 0}
        Nothing was reused from the cache.
      {:else}
        Estimated: {cachedCount} of {messages.length} messages fully cached
        ({cached.tokens.toLocaleString('en-US')} of {cached.total.toLocaleString('en-US')} prompt tokens cached).
      {/if}
    </div>
  {/if}

  {#if images.length}
    <div class="mb-2 flex flex-wrap items-center gap-1.5">
      <span class="text-xs text-dim">{images.length} image{images.length === 1 ? '' : 's'}:</span>
      {#each images as im, n}
        <button
          class="size-10 overflow-hidden rounded border border-line hover:border-primary"
          title="Image {n + 1}, in message {im.msg + 1} · {describe(im.src)}"
          onclick={() => (viewing = n)}
        >
          <img src={im.src} alt="Image {n + 1}" class="size-full object-cover" />
        </button>
      {/each}
    </div>
  {/if}

  <div bind:this={list} class="max-h-[70vh] overflow-auto pr-1">
    {#each messages as m, i}
      {#if cacheShare && i === cacheEnd && i > 0 && cached?.tokens}
        <div class="mb-2 flex items-center gap-2 text-[10px] tracking-wider text-secondary/80 uppercase">
          <span class="h-px flex-1 border-t border-dashed border-secondary/50"></span>
          <span>≈ cached above · processed below</span>
          <span class="h-px flex-1 border-t border-dashed border-secondary/50"></span>
        </div>
      {/if}
      {@const text = typeof m.content === 'string' ? m.content : ''}
      {@const long = text.length > LONG && !expanded.has(i)}
      <div data-msg={i} class="mb-2 rounded-lg border border-l-3 border-line bg-panel2 px-2.5 py-2 {roleStyle[m.role ?? ''] ?? 'border-l-dim'}">
        <div class="mb-0.5 flex items-center gap-1">
          <button
            class="flex min-w-0 flex-1 items-baseline gap-2 text-left text-[10px] tracking-wider text-dim uppercase hover:text-muted"
            onclick={() => toggle(i)}
          >
            <span>{open.has(i) ? '▾' : '▸'}</span>
            <span class="min-w-0 truncate">{m.role}{m.name ? ` · ${m.name}` : ''}{m.tool_call_id ? ` · ${m.tool_call_id}` : ''}</span>
            {#if cacheShare && cacheShare[i] > 0}
              <span class="ml-auto shrink-0 rounded border border-secondary/40 px-1 text-secondary/80" title="Estimated from token counts">
                {cacheShare[i] === 1 ? 'cached' : `≈${Math.round(cacheShare[i] * 100)}% cached`}
              </span>
            {/if}
            <span class="{cacheShare && cacheShare[i] > 0 ? '' : 'ml-auto'} shrink-0 normal-case tracking-normal"
              title={perChar ? 'Tokens are estimated from this message’s share of the prompt’s characters' : undefined}
              >{shown(m).length.toLocaleString()} chars{#if perChar}, ≈{msgTokens(m).toLocaleString()}t{/if}</span
            >
          </button>
          <CopyButton title="Copy this message" text={() => shown(m)} />
        </div>
        {#if !open.has(i)}
          <button class="line-clamp-2 w-full text-left whitespace-pre-wrap text-muted" onclick={() => toggle(i)}>{shown(m).slice(0, 400)}</button>
          {@const thumbs = images.filter((im) => im.msg === i)}
          {#if thumbs.length}
            <div class="mt-1 flex flex-wrap gap-1">
              {#each thumbs as im}
                <button class="size-10 overflow-hidden rounded border border-line hover:border-primary" title="View image · {describe(im.src)}" onclick={() => view(im.src)}>
                  <img src={im.src} alt="attached" class="size-full object-cover" />
                </button>
              {/each}
            </div>
          {/if}
        {:else}
          {#if m.reasoning_content}
            <details class="mb-1 text-muted">
              <summary class="cursor-pointer text-xs"
                >Reasoning ({m.reasoning_content.length.toLocaleString()} chars{#if perChar}, <span
                    title="Estimated from the prompt's tokens per character. Many chat templates drop earlier reasoning from the prompt."
                    >≈{Math.round(m.reasoning_content.length * perChar).toLocaleString()}t</span
                  >{/if}) <CopyButton class="align-middle" title="Copy reasoning" text={() => m.reasoning_content ?? ''} /></summary>
              <div class="text-xs whitespace-pre-wrap">{m.reasoning_content}</div>
            </details>
          {/if}
          {#if Array.isArray(m.content)}
            {#each m.content as p}
              {#if p.type === 'text' || p.text}
                <div class="whitespace-pre-wrap">{@html ansiToHtml(p.text ?? '')}</div>
              {:else if imageSrc(p)}
                {@const src = imageSrc(p) ?? ''}
                <button class="group my-1 block text-left" title="View full size" onclick={() => view(src)}>
                  <img src={src} alt="attached" class="max-h-64 max-w-full cursor-zoom-in rounded border border-line group-hover:border-primary" />
                  <span class="text-[10px] text-dim">{describe(src)} · click to enlarge</span>
                </button>
              {:else}
                <div class="text-xs text-dim">[{p.type ?? 'part'}]</div>
              {/if}
            {/each}
          {:else if m.role === 'assistant'}
            <Markdown text={text} />
          {:else}
            <div class="whitespace-pre-wrap">{@html ansiToHtml(long ? clipped(text) : text)}</div>
            {#if long}
              <button class="mt-1 text-xs text-primary" onclick={() => (expanded = new Set([...expanded, i]))}>Show full message ({stripAnsi(text).length.toLocaleString()} characters)</button>
            {/if}
          {/if}
          {#each m.tool_calls ?? [] as tc}
            <div class="mt-1 font-mono text-xs"><span class="text-warn">{tc.function?.name}</span>({tc.function?.arguments})</div>
          {/each}
        {/if}
      </div>
    {/each}
  </div>

  {#if prompt !== undefined}
    <div class="mb-2 rounded-lg border border-l-3 border-line border-l-secondary bg-panel2 px-2.5 py-2">
      <div class="mb-0.5 flex items-center justify-between text-[10px] tracking-wider text-dim uppercase">
        {req.prompt !== undefined ? 'prompt' : 'input'}
        <CopyButton
          title="Copy {req.prompt !== undefined ? 'prompt' : 'input'}"
          text={() => (Array.isArray(prompt) ? prompt.map((p) => (typeof p === 'string' ? p : JSON.stringify(p))).join('\n') : String(prompt))}
        />
      </div>
      {#if Array.isArray(prompt)}
        {#each prompt as p}<div class="whitespace-pre-wrap">• {typeof p === 'string' ? p : JSON.stringify(p)}</div>{/each}
      {:else}
        <div class="whitespace-pre-wrap">{String(prompt)}</div>
      {/if}
    </div>
  {/if}
{/if}

{#if viewing !== null}
  <ImageViewer {images} bind:index={viewing} onclose={() => (viewing = null)} />
{/if}
