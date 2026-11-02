<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import { listRequests, getRequest, summaryFromStart, type ListFilter, type Summary } from '../lib/api'
  import { fixed, shortBuild, when } from '../lib/format'
  import ModelMark from '../lib/ModelMark.svelte'
  import { issueKinds, issueMutes } from '../lib/issues.svelte'
  import { live, liveInTitle, liveOutSpeed } from '../lib/live.svelte'
  import ChipPopover from '../lib/ChipPopover.svelte'
  import NotableChips from '../lib/NotableChips.svelte'
  import { heatColor, heatMap, type HeatColumn } from '../lib/heat'
  import { expertShares } from '../lib/experts'
  import { rowChips } from '../lib/notable'
  import { modelId } from '../lib/modelIds.svelte'
  import { knownModel } from '../lib/modelNames.svelte'
  import RequestDetail from '../lib/RequestDetail.svelte'
  import { modelView, peerStatus, reachable, serverName, servers, type Server } from '../lib/servers.svelte'
  import ServerIcon from '../lib/ServerIcon.svelte'
  import { sourceLabel, sourceTitle } from '../lib/source'
  import StatusPill from '../lib/StatusPill.svelte'

  const PAGE = 100
  const states = [
    ['', 'All states'],
    ['in_flight', 'Streaming'],
    ['done', 'Completed'],
    ['failed', 'Errors'],
  ] as const

  // How far back the list goes, as [value, label, milliseconds].
  const ranges = [
    ['', 'Any time', 0],
    ['1h', 'Last hour', 3_600_000],
    ['6h', 'Last 6 hours', 6 * 3_600_000],
    ['24h', 'Last 24 hours', 24 * 3_600_000],
    ['7d', 'Last 7 days', 7 * 86_400_000],
    ['30d', 'Last 30 days', 30 * 86_400_000],
  ] as const

  // The filters and the open request are kept in the URL, so a reload, a
  // bookmark or a shared link comes back to the same place. Other pages link
  // here with ?model=M (Models) and ?open=12 (Dashboard). Another server's
  // request opens as ?open=<server ID>:12.
  const params = new URLSearchParams(location.search)
  let rows = $state<Summary[]>([])
  // Each model ID seen, with the servers it was seen on.
  let models = $state<{ id: string; servers: string[] }[]>([])
  let q = $state(params.get('q') ?? '')
  let model = $state(params.get('model') ?? '')
  let stateFilter = $state(states.some(([v]) => v === params.get('state')) ? params.get('state')! : '')
  let issue = $state(params.get('issue') ?? '')
  let range = $state(ranges.some(([v]) => v === params.get('range')) ? params.get('range')! : '')
  // The open request, by its key (rk).
  let open = $state<string | null>(openKey(params.get('open')))
  // One server's ID, or '' for every server.
  let serverFilter = $state(params.get('server') ?? '')
  let loading = $state(false)
  let error = $state('')
  // Other servers that didn't answer the last load.
  let silent = $state<string[]>([])
  let now = $state(Date.now())

  const filter = $derived.by<ListFilter>(() => {
    const ms = ranges.find(([v]) => v === range)?.[2]
    return { q: q.trim(), model, state: stateFilter, issue, since: ms ? Date.now() - ms : undefined }
  })

  $effect(() => {
    const p = new URLSearchParams()
    if (q.trim()) p.set('q', q.trim())
    if (model) p.set('model', model)
    if (stateFilter) p.set('state', stateFilter)
    if (issue) p.set('issue', issue)
    if (range) p.set('range', range)
    if (serverFilter) p.set('server', serverFilter)
    if (open) p.set('open', open.startsWith(':') ? open.slice(1) : open)
    const url = location.pathname + (p.size ? `?${p}` : '')
    if (url !== location.pathname + location.search) history.replaceState(history.state, '', url)
  })

  /** A row's key: IDs are only unique on their own server. */
  function rk(r: { server?: string; id: number }) {
    return `${r.server ?? ''}:${r.id}`
  }
  function openKey(v: string | null) {
    if (!v) return null
    return v.includes(':') ? v : Number(v) ? `:${v}` : null
  }
  /** The server a row is on, as a Servers list entry. */
  const serverOf = (r: Summary): Server | undefined => (r.server ? servers.get(r.server) : servers.list.find((s) => s.self))

  // The servers to list: this one ('') and every other server that answers,
  // or only the one the filter names.
  const targets = $derived.by(() => {
    if (!servers.many) return ['']
    return servers.list
      .filter((s) => (!serverFilter || s.id === serverFilter) && (s.self || reachable(s)))
      .map((s) => (s.self ? '' : s.id))
  })
  const targetsKey = $derived(targets.join(','))

  // Each server's requests load a page at a time. A cursor says where its
  // next page starts, whether it has one, and how far back its list reaches.
  // Rows older than the newest such reach are held back, so loading older
  // requests only ever adds rows at the end.
  type Cursor = { before?: number; more: boolean; oldest: number }
  let cursors = $state<Record<string, Cursor>>({})
  const more = $derived(Object.values(cursors).some((c) => c.more))
  const cutoff = $derived(Math.max(-Infinity, ...Object.values(cursors).filter((c) => c.more).map((c) => c.oldest)))

  // A filter dropdown is as wide as the value it shows, up to a limit, and
  // has a coloured border and brighter text while it filters ('' is no filter).
  const filterCls = (v: string) =>
    `field-sizing-content max-w-[16rem] truncate rounded-md border bg-panel2 px-2 py-1 ${v ? 'border-primary/60 text-text' : 'border-line text-muted hover:text-text'}`

  // In-flight requests first, then newest first.
  const visibleRows = $derived(rows.filter((r) => (r.state === 'in_flight' || r.started_at >= cutoff) && modelView(r.server).visible(r.model)))
  const shown = $derived([...visibleRows.filter((r) => r.state === 'in_flight'), ...visibleRows.filter((r) => r.state !== 'in_flight')])
  // Models in llama-swap's config come first, by the label shown. Models
  // only seen in older requests are listed after them.
  // A model counts as in a config when any server it was seen on has it,
  // and is hidden when any of them hides it.
  type SeenModel = { id: string; servers: string[] }
  const visibleModels = $derived.by(() => {
    const label = (m: SeenModel) => modelView(m.servers[0]).label(m.id)
    const known = (m: SeenModel) => m.servers.some((s) => (s ? peerStatus.get(s)?.current?.llama_swap.known.some((k) => k.model === m.id) : knownModel(m.id)))
    const list = models.filter((m) => m.servers.every((s) => modelView(s).visible(m.id))).map((m) => ({ ...m, label: label(m) }))
    const byLabel = (a: { label: string }, b: { label: string }) => a.label.localeCompare(b.label, undefined, { numeric: true })
    return {
      config: list.filter(known).sort(byLabel),
      old: list.filter((m) => !known(m)).sort(byLabel),
    }
  })
  function addModels(server: string, ids: string[]) {
    const next = models.map((m) => ({ ...m, servers: [...m.servers] }))
    for (const id of ids) {
      const m = next.find((x) => x.id === id)
      if (!m) next.push({ id, servers: [server] })
      else if (!m.servers.includes(server)) m.servers.push(server)
    }
    models = next
  }
  const byNewest = (a: Summary, b: Summary) => b.started_at - a.started_at

  // Only the newest load's answer is used, so a slower, older one can't
  // replace it.
  let loadSeq = 0
  async function load(reset: boolean) {
    const seq = ++loadSeq
    loading = true
    error = ''
    const ask = reset ? targets : targets.filter((s) => cursors[s]?.more)
    const got = await Promise.all(
      ask.map((s) =>
        listRequests(filter, reset ? undefined : cursors[s]?.before, PAGE, s).then(
          (res) => ({ s, res, err: null as unknown }),
          (err: unknown) => ({ s, res: null, err }),
        ),
      ),
    )
    if (seq !== loadSeq) return
    const next: Record<string, Cursor> = reset ? {} : { ...cursors }
    const added: Summary[] = []
    if (reset) {
      models = []
      silent = []
    }
    for (const { s, res, err } of got) {
      if (!res) {
        if (s) silent = [...silent, s]
        else error = String(err)
        next[s] = { more: false, oldest: -Infinity }
        continue
      }
      const list = res.requests.map((r) => ({ ...r, server: s }))
      added.push(...list)
      next[s] = { before: list.at(-1)?.id ?? next[s]?.before, more: list.length === PAGE, oldest: list.at(-1)?.started_at ?? -Infinity }
      if (res.models) addModels(s, res.models)
    }
    rows = (reset ? added : [...rows, ...added]).sort(byNewest)
    cursors = next
    loading = false
  }

  // Reload when the filters change: at once, or after a moment when the
  // search text changed, so typing doesn't send a request per key.
  let timer: ReturnType<typeof setTimeout>
  let lastQ: string | undefined
  $effect(() => {
    const f = filter
    void issueMutes.version
    void targetsKey
    clearTimeout(timer)
    const typing = lastQ !== undefined && f.q !== lastQ
    lastQ = f.q
    if (typing) timer = setTimeout(() => load(true), 200)
    else untrack(() => load(true))
  })

  function matches(r: Summary) {
    const f = filter
    if (!targets.includes(r.server ?? '')) return false
    if (f.model && modelView(r.server).id(r.model) !== (r.server ? f.model : modelId(f.model))) return false
    if (f.state && r.state !== f.state) return false
    if (f.since && r.started_at < f.since) return false
    // A request still in flight may yet have an issue, so it stays listed.
    if (f.issue && r.state !== 'in_flight' && !(f.issue === 'any' ? r.issues.length : r.issues.includes(f.issue))) return false
    if (f.q) {
      const s = f.q.toLowerCase()
      if (![r.preview, r.preview_end, r.last_preview, r.model].some((t) => t?.toLowerCase().includes(s))) return false
    }
    return true
  }

  onMount(() => {
    live.connect()
    const stop = live.onAll(async (e) => {
      if (e.type === 'peer_down') return
      const server = e.server ?? ''
      if (e.type === 'start') {
        const r = { ...summaryFromStart(e.start), server }
        if (matches(r) && !rows.some((x) => rk(x) === rk(r))) rows = [r, ...rows]
        const id = modelView(server).id(r.model)
        if (id && !models.some((m) => m.id === id && m.servers.includes(server))) addModels(server, [id])
      } else if (e.type === 'finish' || e.type === 'settled') {
        const key = rk({ server, id: e.id })
        if (!rows.some((x) => rk(x) === key)) return
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { request, response_raw, response, truncated, live: _, chunks, issue_details, ...done } = await getRequest(e.id, server)
        rows = rows.map((x) => (rk(x) === key ? { ...done, server } : x)).filter((x) => rk(x) === open || matches(x))
      }
    })
    const tick = setInterval(() => (now = Date.now()), 1000)
    return () => {
      stop()
      clearInterval(tick)
    }
  })

  function ttft(r: Summary) {
    const first = r.first_token_at ?? live.of(r.server ?? '')?.get(r.id)?.firstTokenAt
    return first ? first - r.started_at : null
  }

  // Each column uses one unit and one number of decimals for every row, so
  // big and small values can be told apart at a glance.
  const elapsed = (r: Summary) => Math.max(0, (r.finished_at ?? now) - r.started_at)

  // Compare model-dependent metrics within each model. Exclude mostly cached
  // prompts from prompt speeds: too few processed tokens make them unrepresentative.
  const heatCols: Record<string, HeatColumn<Summary>> = {
    in: { value: (r) => r.prompt_tokens, high: true, full: 4, perModel: false },
    out: { value: (r) => r.completion_tokens, high: true, full: 4, perModel: false },
    in_tps: {
      value: (r) => ((r.cached_tokens ?? 0) * 2 > (r.prompt_tokens ?? 0) ? null : r.prompt_per_second),
      high: false,
      full: 2,
      perModel: true,
    },
    out_tps: { value: (r) => r.predicted_per_second, high: false, full: 1.5, perModel: true },
    ttft: { value: (r) => ttft(r), high: true, full: 4, perModel: true },
    duration: { value: (r) => elapsed(r), high: true, full: 4, perModel: false },
    energy: { value: (r) => r.energy_j, high: true, full: 4, perModel: false },
    watts: { value: (r) => (r.energy_j != null && elapsed(r) > 0 ? r.energy_j / elapsed(r) : null), high: true, full: 1.5, perModel: true },
    jtok: { value: (r) => (r.energy_j != null && r.completion_tokens ? r.energy_j / r.completion_tokens : null), high: true, full: 2, perModel: true },
    experts: { value: (r) => expertShares(r.server_stats)?.vram, high: false, full: 2, perModel: true },
    drafts: { value: (r) => (r.draft_n ? (r.draft_accepted ?? 0) / r.draft_n : null), high: false, full: 2, perModel: true },
  }
  // Each server's models compare only with themselves: the same model runs
  // at another speed on another machine.
  const heat = $derived(heatMap(shown, heatCols, (r) => `${r.server ?? ''}:${modelView(r.server).id(r.model)}`))
  const heatFg = (k: string, r: Summary) => heatColor(heat(k, r), heatStrength)

  // `group` puts a shared header over neighbouring columns. Only the token
  // and speed columns get one, from `groupings` below.
  // `menu` names the column in the Columns menu, when its header says less.
  type Column = { key: string; label: string; menu?: string; group?: string; num?: boolean; title?: string }
  const columns = [
    { key: 'time', label: 'Time' },
    { key: 'server', label: 'Server', title: 'The server that answered. Shown when the Servers list on the Settings page has other servers.' },
    { key: 'id', label: 'ID' },
    { key: 'model', label: 'Model' },
    { key: 'status', label: 'Status' },
    { key: 'source', label: 'Source', title: 'Client and address. The client name comes from its User-Agent; hover a value to see the full header.' },
    { key: 'build', label: 'Build', title: 'The server build that answered, such as the llama.cpp build number' },
    { key: 'prompt', label: 'Prompt' },
    { key: 'notable', label: 'Notable', title: 'Issues, tool calls, thinking, images, and other request details. Hover or tap a chip for details.' },
    { key: 'cache_pct', label: 'Cache %', num: true, title: 'Share of the prompt tokens reused from the cache' },
    { key: 'cache', label: 'Cache', num: true, title: 'Prompt tokens reused from the cache' },
    { key: 'in', label: 'In', num: true, title: 'Prompt tokens, including cached tokens' },
    { key: 'out', label: 'Out', num: true, title: 'Generated tokens' },
    { key: 'in_tps', label: 'In t/s', num: true, title: 'Prompt processing speed, tokens per second' },
    { key: 'out_tps', label: 'Out t/s', num: true, title: 'Generation speed, tokens per second' },
    {
      key: 'drafts',
      label: 'Drafts %',
      num: true,
      title: "Speculative decoding: the share of drafted tokens that the model kept. The bar under it is the share of the output that came from kept drafts. Hover a value for the numbers and the model's usual share.",
    },
    { key: 'experts', label: 'Experts %', num: true, title: "Strata only: the share of the model's expert work while generating that was done from the VRAM cache. The bar under it splits the work into VRAM, PCIe and cache misses, and the tick marks the model's usual share. Hover a value for the numbers." },
    { key: 'ttft', label: 'TTFT s', num: true, title: 'Time to first token, in seconds' },
    { key: 'duration', label: 'Duration s', num: true, title: 'Total request time, in seconds' },
    { key: 'timing', label: 'Duration s', menu: 'Duration and TTFT', num: true, title: 'Total request time, with the time to first token under it, in seconds' },
    { key: 'energy', label: 'Energy Wh', num: true, title: 'GPU energy used, in watt-hours' },
    { key: 'watts', label: 'Avg W', num: true, title: 'Average GPU power during this request: energy divided by duration' },
    { key: 'jtok', label: 'J/tok', num: true, title: 'GPU energy per generated token, in joules' },
  ] as const satisfies readonly Column[]
  type Col = (typeof columns)[number]['key']
  const defaultCols: Col[] = ['time', 'server', 'model', 'status', 'source', 'prompt', 'notable', 'in', 'out', 'in_tps', 'out_tps', 'drafts', 'experts', 'timing', 'energy']

  // The column choice is kept per browser, since a phone may want fewer.
  // COLS_VERSION goes up when new columns should appear in saved choices.
  const COLS_VERSION = 6
  function savedCols(): Col[] {
    try {
      let v = JSON.parse(localStorage.getItem('requests_columns') ?? 'null')
      if (Array.isArray(v)) {
        const version = Number(localStorage.getItem('requests_columns_version') ?? 1)
        if (version < 2) {
          // Version 2: Cache % takes the place of Cache, and Notable is added.
          v = v.map((k: string) => (k === 'cache' ? 'cache_pct' : k))
          v.push('notable')
        }
        // Version 3: Status distinguishes failed and cancelled requests, which have no chip.
        if (version < 3 && !v.includes('status')) v.push('status')
        if (version < 4 && !v.includes('source')) v.push('source')
        if (version < 5 && !v.includes('server')) v.push('server')
        if (version < 6) {
          // Version 6: the cache share is a bar under the prompt tokens, and
          // TTFT and Duration share one column.
          v = v.filter((k: string) => k !== 'cache_pct')
          if (v.includes('ttft') && v.includes('duration')) v = [...v.filter((k: string) => k !== 'ttft' && k !== 'duration'), 'timing']
        }
        if (version < COLS_VERSION) {
          localStorage.setItem('requests_columns', JSON.stringify(v))
          localStorage.setItem('requests_columns_version', String(COLS_VERSION))
        }
        return columns.map((c) => c.key).filter((k) => v.includes(k))
      }
    } catch {
      // No storage, or a bad value: use the defaults.
    }
    return defaultCols
  }
  let chosen = $state<Col[]>(savedCols())
  // The token and speed columns can be grouped by phase (the prompt's
  // tokens and speed, then the output's) or by kind (both token counts,
  // then both speeds). Each entry gives the order, and each column's group
  // and its short label under that group.
  type Grouping = 'phase' | 'kind'
  const groupings: Record<Grouping, [Col, string, string][]> = {
    phase: [
      ['in', 'Prompt', 'Tokens'],
      ['in_tps', 'Prompt', 't/s'],
      ['out', 'Output', 'Tokens'],
      ['out_tps', 'Output', 't/s'],
    ],
    kind: [
      ['in', 'Tokens', 'In'],
      ['out', 'Tokens', 'Out'],
      ['in_tps', 'Speed t/s', 'In'],
      ['out_tps', 'Speed t/s', 'Out'],
    ],
  }
  let grouping = $state<Grouping>(savedGrouping())
  function savedGrouping(): Grouping {
    try {
      return localStorage.getItem('requests_grouping') === 'kind' ? 'kind' : 'phase'
    } catch {
      return 'phase'
    }
  }
  function setGrouping(g: Grouping) {
    grouping = g
    order = grouped(order, g)
    saveOrder()
    try {
      localStorage.setItem('requests_grouping', g)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  // The column order, with every column, shown or not. It can be changed by
  // dragging a header or an entry in the Columns menu, and is saved per
  // browser (requests_column_order).
  const allKeys = columns.map((c) => c.key) as Col[]
  // Puts the token and speed columns side by side in the grouping's order,
  // where the first of them sits.
  function grouped(list: Col[], g: Grouping): Col[] {
    const block = groupings[g].map(([k]) => k)
    const at = list.findIndex((k) => block.includes(k))
    const rest = list.filter((k) => !block.includes(k))
    const restAt = list.slice(0, at).filter((k) => !block.includes(k)).length
    return [...rest.slice(0, restAt), ...block, ...rest.slice(restAt)]
  }
  function savedOrder(): Col[] {
    try {
      const v = JSON.parse(localStorage.getItem('requests_column_order') ?? 'null')
      if (Array.isArray(v)) {
        const list = v.filter((k, n): k is Col => allKeys.includes(k) && v.indexOf(k) === n)
        // A column added in a later version goes after the column it
        // follows in the default order.
        for (const [n, k] of allKeys.entries()) {
          if (list.includes(k)) continue
          const prev = allKeys.slice(0, n).findLast((p) => list.includes(p))
          list.splice(prev ? list.indexOf(prev) + 1 : 0, 0, k)
        }
        return list
      }
    } catch {
      // No storage, or a bad value: use the default order.
    }
    return grouped(allKeys, grouping)
  }
  let order = $state<Col[]>(savedOrder())
  function saveOrder() {
    try {
      localStorage.setItem('requests_column_order', JSON.stringify(order))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  // Moves a column to sit just before or after another. Saving is left to
  // the caller, so a drag saves once at the end.
  function place(k: Col, target: Col, after: boolean) {
    if (k === target) return
    const rest = order.filter((c) => c !== k)
    const at = rest.indexOf(target) + (after ? 1 : 0)
    order = [...rest.slice(0, at), k, ...rest.slice(at)]
  }
  // Dragging a header, or an entry in the Columns menu, moves the column as
  // the pointer passes the middle of another. The order is saved when the
  // drag ends.
  let dragging = $state<Col | null>(null)

  // Per-column display choices, saved per browser.
  type ColumnSetting = { key: string; label: string; options: [string, string][]; default: string; title?: string }
  // Token counts are short ("126k", "0.4k") unless a column's setting
  // shows them in full.
  const tokenSetting: ColumnSetting = {
    key: 'tokens',
    label: 'Numbers',
    options: [
      ['short', 'Short, such as 126k'],
      ['full', 'In full, such as 126,227'],
    ],
    default: 'short',
  }
  const columnSettings: Partial<Record<Col, ColumnSetting[]>> = {
    out: [tokenSetting],
    cache: [tokenSetting],
    time: [
      {
        key: 'show',
        label: 'Show',
        options: [
          ['seconds', 'With seconds'],
          ['minutes', 'Hours and minutes'],
        ],
        default: 'seconds',
      },
    ],
    server: [
      {
        key: 'show',
        label: 'Show',
        options: [
          ['both', 'Icon and name'],
          ['icon', 'Icon only'],
          ['name', 'Name only'],
        ],
        default: 'both',
        title: "Pick a server's icon on the Settings page, in Servers.",
      },
    ],
    model: [
      {
        key: 'show',
        label: 'Show',
        options: [
          ['both', 'Icon and name'],
          ['icon', 'Icon only'],
        ],
        default: 'both',
        title: "With the icon only, hover it for the model's name.",
      },
    ],
    in: [
      {
        key: 'bar',
        label: 'Bar under the count',
        options: [
          ['cache', 'Share from the cache'],
          ['none', 'None'],
        ],
        default: 'cache',
        title: 'The same bar as the Cache % column, so that column can be left off.',
      },
      tokenSetting,
    ],
    source: [
      {
        key: 'show',
        label: 'Show',
        options: [
          ['both', 'Client and address'],
          ['client', 'Client'],
          ['address', 'Address'],
          ['agent', 'Full User-Agent'],
        ],
        default: 'both',
        title: 'The client is a short name worked out from the User-Agent, such as Codex or curl.',
      },
    ],
    prompt: [
      {
        key: 'show',
        label: 'Show',
        options: [
          ['last', 'Last message'],
          ['user', 'Start of last user message'],
          ['user_end', 'End of last user message'],
        ],
        default: 'last',
        title:
          'Clients often start every user message with the same text, and agents usually end with a tool result. Pick the part that tells requests apart.',
      },
    ],
  }
  let colValues = $state<Record<string, Record<string, string>>>(savedColValues())
  function savedColValues() {
    try {
      let v = JSON.parse(localStorage.getItem('requests_column_settings') ?? 'null')
      v = v && typeof v === 'object' && !Array.isArray(v) ? v : {}
      // Before token counts were set per column, one switch set them all.
      if (localStorage.getItem('requests_full_tokens') === 'true') {
        for (const k of ['in', 'out', 'cache']) v[k] = { tokens: 'full', ...v[k] }
        localStorage.setItem('requests_column_settings', JSON.stringify(v))
      }
      localStorage.removeItem('requests_full_tokens')
      return v
    } catch {
      return {}
    }
  }
  function colSetting(k: Col, key: string): string {
    const setting = columnSettings[k]?.find((x) => x.key === key)
    const savedValue = colValues[k]?.[key]
    return setting && setting.options.some(([o]) => o === savedValue) ? savedValue! : (setting?.default ?? '')
  }
  function setColSetting(k: Col, key: string, v: string) {
    colValues = { ...colValues, [k]: { ...colValues[k], [key]: v } }
    try {
      localStorage.setItem('requests_column_settings', JSON.stringify(colValues))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
    customize()
  }
  const tokensFull = (k: Col) => colSetting(k, 'tokens') === 'full'
  let settingsFor = $state<Col | null>(null)
  const sourceShow = $derived(colSetting('source', 'show'))
  const timeSeconds = $derived(colSetting('time', 'show') === 'seconds')
  const serverShow = $derived(colSetting('server', 'show'))
  const modelShow = $derived(colSetting('model', 'show'))
  const inBar = $derived(colSetting('in', 'bar'))
  // A column showing only icons drops its header text, so it is as narrow as
  // the icons. Its name moves to the header's tooltip.
  const iconOnly = (k: string) => (k === 'server' && serverShow === 'icon') || (k === 'model' && modelShow === 'icon')
  const promptShow = $derived(colSetting('prompt', 'show'))
  // The full text for the tooltip. Older requests without their bodies only
  // have the start of the last user message.
  function promptText(r: Summary) {
    return (promptShow === 'last' ? r.last_preview : promptShow === 'user_end' ? r.preview_end : r.preview) || r.preview
  }
  // The end of the last user message shows its last characters, since the
  // column is only about 20 characters wide.
  function promptCell(r: Summary) {
    const t = promptText(r)
    if (promptShow !== 'user_end' || !r.preview_end) return t
    const chars = Array.from(t)
    return chars.length > 24 ? '…' + chars.slice(-24).join('') : t
  }
  function dragStart(e: DragEvent, k: Col) {
    dragging = k
    e.dataTransfer!.effectAllowed = 'move'
    e.dataTransfer!.setData('text/plain', k)
  }
  function dragOver(e: DragEvent, k: Col, axis: 'x' | 'y') {
    if (!dragging) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    place(dragging, k, axis === 'x' ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2)
  }
  function dragEnd() {
    dragging = null
    saveOrder()
  }

  const orderedCols = $derived.by((): Column[] => {
    const groupOf = new Map(groupings[grouping].map(([key, group, label]) => [key, { group, label }]))
    return order.map((k) => ({ ...columns.find((c) => c.key === k)!, ...groupOf.get(k) }))
  })
  const visibleCols = $derived(orderedCols.filter((c) => chosen.includes(c.key as Col) && (c.key !== 'server' || servers.many)))
  // The top header row: one cell per run of neighbouring columns with the
  // same group, and an empty cell per column without one.
  const groupRow = $derived.by(() => {
    const cells: { label: string; span: number }[] = []
    for (const c of visibleCols) {
      const last = cells.at(-1)
      if (c.group && last?.label === c.group) last.span++
      else cells.push({ label: c.group ?? '', span: 1 })
    }
    return cells
  })
  // A thin line where each group starts and ends sets the groups apart.
  const edges = $derived(
    new Set(
      visibleCols
        .filter((c, i) => i > 0 && c.group !== visibleCols[i - 1].group && (c.group || visibleCols[i - 1].group))
        .map((c) => c.key),
    ),
  )
  function setCols(next: Col[]) {
    chosen = next
    try {
      localStorage.setItem('requests_columns', JSON.stringify(next))
      localStorage.setItem('requests_columns_version', String(COLS_VERSION))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
    customize()
  }
  // Puts the columns back in their first order and picks the Default view.
  function resetCols() {
    order = grouped(allKeys, grouping)
    try {
      localStorage.removeItem('requests_column_order')
    } catch {
      // Nothing saved to remove.
    }
    pickView('default')
  }
  function toggleCol(k: Col) {
    setCols(chosen.includes(k) ? chosen.filter((c) => c !== k) : [...chosen, k])
  }

  // The same size as the cache_miss tag's limit in internal/capture/notable.go.
  const cacheMissTokens = 8000

  // Saved per browser, like the column choices. On unless turned off.
  let colorful = $state(savedColorful())
  function savedColorful() {
    try {
      return localStorage.getItem('requests_colorful') !== 'false'
    } catch {
      return true
    }
  }
  function toggleColorful() {
    colorful = !colorful
    try {
      localStorage.setItem('requests_colorful', String(colorful))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  // Dimming percentage for typical values; 0 disables it. Saved per browser.
  let heatStrength = $state(savedHeat())
  function savedHeat() {
    try {
      const v = Number(localStorage.getItem('requests_heat') ?? 30)
      return isFinite(v) ? Math.min(60, Math.max(0, v)) : 30
    } catch {
      return 30
    }
  }
  function setHeat(v: number) {
    heatStrength = v
    try {
      localStorage.setItem('requests_heat', String(v))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  // Shows "t" after token counts and "t/s" after speeds. Saved per browser.
  // On unless turned off.
  let units = $state(savedUnits())
  function savedUnits() {
    try {
      return localStorage.getItem('requests_units') !== 'false'
    } catch {
      return true
    }
  }
  function toggleUnits() {
    units = !units
    try {
      localStorage.setItem('requests_units', String(units))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
    customize()
  }

  // A token count in thousands: one decimal under 10k, so small counts
  // still differ, and whole thousands above. Millions get an M.
  function shortTokens(v: number) {
    if (v >= 999_500) return { n: (v / 1e6).toFixed(1), unit: 'M' }
    if (v >= 9_950) return { n: String(Math.round(v / 1000)), unit: 'k' }
    if (v >= 50) return { n: (v / 1000).toFixed(1), unit: 'k' }
    return { n: '<0.1', unit: 'k' }
  }
  // The full count for a cell's tooltip, when the cell shows it short.
  const tokenTitle = (v: number | null | undefined, full: boolean) => (full || v == null ? undefined : `${fixed(v)} tokens`)

  // The space on each side of a cell. More is the spacing from before this
  // was a choice. Saved per browser.
  const spacings = [
    ['minimal', 'Minimal', '3px'],
    ['default', 'Default', '5px'],
    ['more', 'More', '0.5rem'],
  ] as const
  type Spacing = (typeof spacings)[number][0]
  let spacing = $state<Spacing>(savedSpacing())
  function savedSpacing(): Spacing {
    try {
      const v = localStorage.getItem('requests_spacing')
      return spacings.some(([k]) => k === v) ? (v as Spacing) : 'default'
    } catch {
      return 'default'
    }
  }
  function setSpacing(v: Spacing) {
    spacing = v
    try {
      localStorage.setItem('requests_spacing', v)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
    customize()
  }
  const cellPx = $derived(spacings.find(([k]) => k === spacing)![2])

  // ---- Column views ----
  // A view is the settings that decide how wide the table is: the columns
  // shown, their settings, spacing and units. Narrow, Default
  // and Wide are fixed. Changing any of these settings switches to Custom and
  // saves them there, so Custom is always the user's last own setup. Column
  // order, colours and the heatmap stay the same in every view.
  type ViewName = 'narrow' | 'default' | 'wide' | 'custom'
  type View = { chosen: Col[]; colValues: Record<string, Record<string, string>>; spacing: Spacing; units: boolean }
  const presets: Record<Exclude<ViewName, 'custom'>, View> = {
    // Fits beside the side panel.
    narrow: {
      chosen: ['time', 'server', 'model', 'status', 'source', 'prompt', 'notable', 'in', 'in_tps', 'out', 'out_tps', 'timing'],
      colValues: { time: { show: 'minutes' }, server: { show: 'icon' }, model: { show: 'icon' }, source: { show: 'client' } },
      spacing: 'minimal',
      units: false,
    },
    default: {
      chosen: defaultCols,
      colValues: { server: { show: 'icon' }, source: { show: 'client' } },
      spacing: 'default',
      units: false,
    },
    wide: {
      chosen: ['time', 'server', 'id', 'model', 'status', 'source', 'build', 'prompt', 'notable', 'cache_pct', 'in', 'out', 'in_tps', 'out_tps', 'drafts', 'experts', 'ttft', 'duration', 'energy', 'watts', 'jtok'],
      colValues: { in: { tokens: 'full' }, out: { tokens: 'full' }, cache: { tokens: 'full' } },
      spacing: 'more',
      units: true,
    },
  }
  const viewNames = [
    ['narrow', 'Narrow', 'Fewer columns, icons for server and model, and tight spacing. Fits beside the side panel.'],
    ['default', 'Default', 'The usual columns.'],
    ['wide', 'Wide', 'Every column, with full token counts.'],
    ['custom', 'Custom', 'Your own columns and settings. Changing any column setting switches to this view.'],
  ] as const
  const firstView = savedViewName()
  let viewName = $state<ViewName>(firstView)
  function savedViewName(): ViewName {
    const v = stored('requests_view')
    if (viewNames.some(([k]) => k === v)) return v as ViewName
    // Settings saved before views existed are the user's own.
    const keys = ['requests_columns', 'requests_column_settings', 'requests_spacing', 'requests_units']
    return keys.some((k) => stored(k) != null) ? 'custom' : 'default'
  }
  const viewNow = (): View => ({ chosen, colValues, spacing, units })
  // Settings saved before views existed become Custom, so picking a preset
  // doesn't lose them.
  if (firstView === 'custom' && stored('requests_view_custom') == null) store('requests_view_custom', JSON.stringify(viewNow()))
  function customize() {
    viewName = 'custom'
    store('requests_view', 'custom')
    store('requests_view_custom', JSON.stringify(viewNow()))
  }
  // Puts a view's settings in place and saves them as the current ones.
  function applyView(v: View) {
    const copy: View = JSON.parse(JSON.stringify(v))
    chosen = columns.map((c) => c.key).filter((k) => copy.chosen.includes(k))
    colValues = copy.colValues
    spacing = copy.spacing
    units = copy.units
    store('requests_columns', JSON.stringify(chosen))
    store('requests_columns_version', String(COLS_VERSION))
    store('requests_column_settings', JSON.stringify(colValues))
    store('requests_spacing', spacing)
    store('requests_units', String(units))
  }
  function pickView(name: ViewName) {
    if (name === 'custom') {
      try {
        const saved = JSON.parse(stored('requests_view_custom') ?? 'null')
        if (saved && Array.isArray(saved.chosen)) {
          // Saved before token counts were set per column.
          if (saved.fullTokens) for (const k of ['in', 'out', 'cache']) saved.colValues = { ...saved.colValues, [k]: { tokens: 'full', ...saved.colValues?.[k] } }
          applyView({ ...presets.default, ...saved })
        }
      } catch {
        // A bad saved view: keep the settings in place.
      }
    } else applyView(presets[name])
    viewName = name
    store('requests_view', name)
  }
  // A browser with nothing saved starts with the Default view's settings.
  if (firstView === 'default' && stored('requests_view') == null) pickView('default')

  // ---- Side panel ----
  // On a wide screen, a panel right of the table can show a request in place
  // of expanding its row: the one clicked (Selected), or the newest one in
  // flight (Live). Whether it is open, its mode and its width are saved per
  // browser.
  type PanelMode = 'selected' | 'live'
  function stored(key: string) {
    try {
      return localStorage.getItem(key)
    } catch {
      return null
    }
  }
  function store(key: string, v: string) {
    try {
      localStorage.setItem(key, v)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
  let panelOn = $state(stored('requests_panel') === 'true')
  let panelMode = $state<PanelMode>(stored('requests_panel_mode') === 'live' ? 'live' : 'selected')
  const minPanel = 320
  let panelWidth = $state(Math.max(minPanel, Number(stored('requests_panel_width')) || 560))
  const wideQuery = matchMedia('(min-width: 1024px)')
  let wide = $state(wideQuery.matches)
  $effect(() => {
    const changed = () => (wide = wideQuery.matches)
    wideQuery.addEventListener('change', changed)
    return () => wideQuery.removeEventListener('change', changed)
  })
  const usePanel = $derived(panelOn && wide)
  function togglePanel() {
    panelOn = !panelOn
    store('requests_panel', String(panelOn))
  }
  function setPanelMode(m: PanelMode) {
    panelMode = m
    store('requests_panel_mode', m)
  }

  // Live mode follows the newest request in flight. It stays on a request
  // until that one finishes, so requests that run side by side don't make
  // it jump. With nothing in flight, it keeps the last one.
  let followed = $state<string | null>(null)
  $effect(() => {
    if (!usePanel || panelMode !== 'live') return
    const cur = followed ? shown.find((r) => rk(r) === followed) : undefined
    if (cur?.state === 'in_flight') return
    const next = shown.find((r) => r.state === 'in_flight')
    if (next) followed = rk(next)
  })
  const panelKey = $derived(usePanel ? (panelMode === 'live' ? followed : open) : null)
  const panelRow = $derived(panelKey ? (shown.find((r) => rk(r) === panelKey) ?? rows.find((r) => rk(r) === panelKey)) : undefined)
  // The row the panel or the expanded row shows.
  const current = $derived(usePanel ? panelKey : open)

  function rowClick(r: Summary) {
    if (!usePanel) {
      open = open === rk(r) ? null : rk(r)
      return
    }
    open = rk(r)
    if (panelMode === 'live') setPanelMode('selected')
  }

  // Dragging the panel's left edge sets its width.
  function resizeStart(e: PointerEvent) {
    const handle = e.currentTarget as HTMLElement
    const x0 = e.clientX
    const w0 = panelWidth
    handle.setPointerCapture(e.pointerId)
    const move = (ev: PointerEvent) => (panelWidth = Math.round(Math.min(innerWidth * 0.7, Math.max(minPanel, w0 + x0 - ev.clientX))))
    const up = () => {
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', up)
      store('requests_panel_width', String(panelWidth))
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', up)
  }

  // While the request in the panel streams, the end of its answer stays in
  // view, until the user scrolls it out of view. Coming back to it, or
  // another request, starts following again.
  let panelBody = $state<HTMLElement>()
  let panelContent = $state<HTMLElement>()
  let stick = true
  let ours = false
  const responseEnd = () => panelBody?.querySelector('[data-response-end]')
  function panelScroll() {
    if (ours) {
      ours = false
      return
    }
    const end = responseEnd()
    if (!end || !panelBody) return
    const box = panelBody.getBoundingClientRect()
    const at = end.getBoundingClientRect().bottom
    stick = at >= box.top && at <= box.bottom + 24
  }
  $effect(() => {
    void panelKey
    stick = true
    if (panelBody) panelBody.scrollTop = 0
  })
  $effect(() => {
    if (!panelContent || panelRow?.state !== 'in_flight') return
    const ro = new ResizeObserver(() => {
      const end = responseEnd()
      if (!stick || !end || !panelBody) return
      const over = end.getBoundingClientRect().bottom - panelBody.getBoundingClientRect().bottom + 12
      if (over > 0) {
        ours = true
        panelBody.scrollTop += over
      }
    })
    ro.observe(panelContent)
    return () => ro.disconnect()
  })

  // How a row's cache share looks: its bar, and its text colour in the
  // Cache % column. With Colorful on, a big prompt that mostly missed the
  // cache turns amber: fully under 10%, softer under 50%.
  function cacheLook(r: Summary) {
    if (r.cached_tokens == null || !r.prompt_tokens) return null
    const pct = (r.cached_tokens / r.prompt_tokens) * 100
    const big = r.prompt_tokens >= cacheMissTokens
    const miss = colorful && big && pct < 10
    const low = colorful && big && !miss && pct < 50
    return {
      pct,
      fg: miss ? 'var(--color-warn)' : low ? 'color-mix(in srgb, var(--color-warn) 55%, var(--color-text))' : undefined,
      bar: !colorful ? 'var(--color-dim)' : miss ? 'var(--color-warn)' : low ? 'color-mix(in srgb, var(--color-warn) 60%, var(--color-dim))' : 'var(--color-primary)',
      title: `${fixed(r.cached_tokens)} of ${fixed(r.prompt_tokens)} prompt tokens from the cache (${fixed(pct)}%)`,
    }
  }

  let picker = $state<HTMLDetailsElement>()
  let overflow = $state<HTMLDetailsElement>()
  function closePicker(e: MouseEvent) {
    for (const d of [picker, overflow]) if (d?.open && !d.contains(e.target as Node)) d.open = false
  }
</script>

<svelte:window onclick={closePicker} />

<!-- Live estimates are dimmed until stored values arrive. -->
{#snippet maybeLive(stored: number | null, estimate: number | null | undefined, digits: number, title: string, unit?: string, fg?: string)}
  {#if stored == null && estimate != null}
    <td class="num text-right text-muted" {title}>{@render withUnit(estimate, digits, unit)}</td>
  {:else}
    <td class="num text-right" style:color={fg}>{@render withUnit(stored, digits, unit)}</td>
  {/if}
{/snippet}

<!-- The unit is dimmed, and left off when there is no number. -->
{#snippet seconds(ms: number | null)}
  {#if ms == null}–{:else}{@render withUnit(ms / 1000, 2, 's')}{/if}
{/snippet}

<!-- A token count, short unless the column's setting shows it in full. The "k" shows even
     with units off, since the number is wrong without it. -->
{#snippet tokenCount(v: number | null | undefined, full: boolean)}
  {#if full || v == null || !isFinite(v) || v === 0}{@render withUnit(v, 0, 't')}
  {:else}
    {@const t = shortTokens(v)}{t.n}<span class="ml-0.5 text-dim">{t.unit}{units ? 't' : ''}</span>
  {/if}
{/snippet}

{#snippet withUnit(v: number | null | undefined, digits: number, unit?: string)}
  {fixed(v, digits)}{#if units && unit && v != null && isFinite(v)}<span class="ml-0.5 text-dim">{unit}</span>{/if}
{/snippet}

<!-- A thin bar along the bottom of a cell, inside its padding. -->
{#snippet bar(share: number, color: string)}
  <span class="absolute inset-x-(--cell-px) bottom-[2px] h-[2px] rounded-sm bg-line-soft"><span class="block h-full rounded-sm" style:width="{share * 100}%" style:background={color}></span></span>
{/snippet}

<!-- Cache %: the share with a bar under it. See cacheLook. -->
{#snippet cachePct(r: Summary)}
  {@const look = cacheLook(r)}
  {#if look}
    <td class="num relative text-right" style:color={look.fg} title={look.title}>
      {@render withUnit(look.pct, 0, '%')}
      {@render bar(look.pct / 100, look.bar)}
    </td>
  {:else}
    <td class="num text-right">–</td>
  {/if}
{/snippet}

<!-- Prompt tokens, with the cache share as a bar under them unless the
     column's setting turns it off. -->
{#snippet inTokens(r: Summary, estimate: number | undefined, title: string)}
  {#if r.prompt_tokens == null && estimate != null}
    <td class="num text-right text-muted" {title}>{@render tokenCount(estimate, tokensFull('in'))}</td>
  {:else}
    {@const look = inBar === 'cache' ? cacheLook(r) : null}
    <td class="num relative text-right" style:color={heatFg('in', r)} title={[tokenTitle(r.prompt_tokens, tokensFull('in')), look?.title].filter(Boolean).join('\n') || undefined}>
      {@render tokenCount(r.prompt_tokens, tokensFull('in'))}
      {#if look}{@render bar(look.pct / 100, look.bar)}{/if}
    </td>
  {/if}
{/snippet}

<!-- Drafts %: the share of drafted tokens kept, with a bar under it for the
     share of the output that came from kept drafts. -->
{#snippet draftsPct(r: Summary)}
  {#if r.draft_n}
    {@const accepted = r.draft_accepted ?? 0}
    {@const share = r.completion_tokens ? Math.min(1, accepted / r.completion_tokens) : null}
    {@const usual = heat.median('drafts', r)}
    <td
      class="num relative text-right"
      style:color={heatFg('drafts', r)}
      title="{fixed(accepted)} of {fixed(r.draft_n)} drafted tokens kept ({fixed((accepted / r.draft_n) * 100)}%).{share != null
        ? ` Kept drafts made up ${fixed(share * 100)}% of the ${fixed(r.completion_tokens)} output tokens.`
        : ''}{usual != null ? ` This model usually keeps ${fixed(usual * 100)}% (the median over the requests listed).` : ''}"
    >
      {@render withUnit((accepted / r.draft_n) * 100, 0, '%')}
      {#if share != null}{@render bar(share, colorful ? 'var(--color-secondary)' : 'var(--color-dim)')}{/if}
    </td>
  {:else}
    <td class="num text-right">–</td>
  {/if}
{/snippet}

<!-- Duration, with TTFT under it in smaller text. The cell has less padding
     than the others, so the two lines fit in a row's usual height. -->
{#snippet timing(r: Summary)}
  <td class="num py-0! text-right">
    <div class="leading-[14px]" style:color={heatFg('duration', r)}>{@render seconds(elapsed(r))}</div>
    <div class="text-[10px] leading-[11px] text-muted" style:color={heatFg('ttft', r)} style:opacity={heatStrength ? 0.8 : undefined}>{@render seconds(ttft(r))}</div>
  </td>
{/snippet}

<!-- Experts %: the VRAM share, with the same split as the expanded row's chip
     as a thin bar under it, and a tick on the bar at the model's usual share. -->
{#snippet expertsPct(r: Summary)}
  {@const ex = expertShares(r.server_stats)}
  {#if ex}
    {@const usual = heat.median('experts', r)}
    {@const parts = [
      [ex.vram, colorful ? 'var(--color-experts)' : 'var(--color-dim)'],
      [ex.pcie, colorful ? 'color-mix(in srgb, var(--color-secondary) 70%, transparent)' : 'color-mix(in srgb, var(--color-dim) 50%, transparent)'],
      [ex.missed, colorful ? 'color-mix(in srgb, var(--color-warn) 70%, transparent)' : 'transparent'],
    ] as const}
    <td
      class="num relative text-right"
      style:color={heatFg('experts', r)}
      title="{fixed(ex.vram * 100)}% from the VRAM cache, {fixed(ex.pcie * 100)}% read over PCIe, {fixed(ex.missed * 100)}% missed the cache.{usual != null ? ` This model usually gets ${fixed(usual * 100)}% from VRAM (the median over the requests listed).` : ''}"
    >
      {@render withUnit(ex.vram * 100, 0, '%')}
      <span class="absolute inset-x-(--cell-px) bottom-[2px] flex h-[2px] rounded-sm bg-line-soft">
        {#each parts as [share, color]}<span class="block h-full" style:width="{share * 100}%" style:background={color}></span>{/each}
        {#if usual != null}<span class="absolute -top-px h-[4px] w-px bg-text" style:left="round(down, {usual * 100}%, 1px)"></span>{/if}
      </span>
    </td>
  {:else}
    <td class="num text-right">–</td>
  {/if}
{/snippet}

{#snippet cell(k: string, r: Summary, flight: ReturnType<typeof live.flights.get>)}
  {@const progress = flight?.progress}
  {#if k === 'time'}<td class="num" title={timeSeconds ? undefined : when(r.started_at)}>{when(r.started_at, timeSeconds)}</td>
  {:else if k === 'id'}<td class="font-mono text-dim">{r.id}</td>
  {:else if k === 'server'}
    {@const sv = serverOf(r)}
    <td class="text-muted" title={serverShow === 'icon' ? serverName(sv) : undefined}>
      <span class="flex items-center gap-1.5 {serverShow === 'icon' ? 'justify-center' : ''}">{#if serverShow !== 'name'}<ServerIcon server={sv} />{/if}{#if serverShow !== 'icon'}{serverName(sv)}{/if}</span>
    </td>
  {:else if k === 'model'}
    {@const mv = modelView(r.server)}
    {@const state = r.state === 'in_flight' ? (flight && !flight.firstTokenAt ? 'Waiting for the first token' : 'Streaming') : ''}
    {#if modelShow === 'icon'}
      {@const name = mv.requested(r.model)}
      <td class="text-center" title={[name || 'No model', mv.title(r.model) !== name ? mv.title(r.model) : '', state].filter(Boolean).join('\n')}
        ><ModelMark model={r.model} color={mv.color(r.model)} shape={mv.shape(r.model)} class={r.state === 'in_flight' ? 'pulse' : ''} /></td
      >
    {:else}
      <td><ModelMark model={r.model} color={mv.color(r.model)} shape={mv.shape(r.model)} class="mr-1.5 {r.state === 'in_flight' ? 'pulse' : ''}" title={state || undefined} /><span title={mv.title(r.model)}>{mv.requested(r.model) || '–'}</span></td>
    {/if}
  {:else if k === 'status'}<td><StatusPill row={r} waiting={!!flight && !flight.firstTokenAt} /></td>
  {:else if k === 'source'}
    <td class="overflow-hidden text-ellipsis text-muted {sourceShow === 'agent' ? 'max-w-[260px]' : 'max-w-[180px]'}" title={sourceTitle(r)}>
      {#if sourceShow === 'address'}<span class="font-mono">{r.client_ip || '–'}</span>
      {:else if sourceShow === 'agent'}{r.retry_of ? sourceLabel(r) : r.user_agent || '–'}
      {:else}{sourceLabel(r) || '–'}{#if sourceShow === 'both' && r.client_ip}<span class="ml-1.5 font-mono text-dim">{r.client_ip}</span>{/if}
      {/if}
    </td>
  {:else if k === 'build'}<td class="font-mono text-muted" title={r.build ?? undefined}>{r.build ? shortBuild(r.build) : '–'}</td>
  {:else if k === 'prompt'}<td class="max-w-[140px] overflow-hidden text-ellipsis text-muted" title={promptText(r)}>{promptCell(r)}</td>
  {:else if k === 'notable'}<td><NotableChips chips={rowChips(r, flight)} {colorful} /></td>
  {:else if k === 'cache_pct'}{@render cachePct(r)}
  {:else if k === 'cache'}
    {#if r.cached_tokens == null && progress?.cache != null}<td class="num text-right text-muted" title="Reused from the cache so far">{@render tokenCount(progress.cache, tokensFull('cache'))}</td>
    {:else}<td class="num text-right" title={tokenTitle(r.cached_tokens, tokensFull('cache'))}>{@render tokenCount(r.cached_tokens, tokensFull('cache'))}</td>{/if}
  {:else if k === 'in'}{@render inTokens(r, progress?.total ?? progress?.processed, progress ? liveInTitle(progress) : '')}
  {:else if k === 'out'}
    {@const n = flight ? flight.chunks : r.completion_tokens}
    <td class="num text-right" style:color={heatFg(k, r)} title={tokenTitle(n, tokensFull('out'))}>{@render tokenCount(n, tokensFull('out'))}</td>
  {:else if k === 'in_tps'}{@render maybeLive(r.prompt_per_second, progress?.per_second, 0, 'Prompt speed so far', 't/s', heatFg(k, r))}
  {:else if k === 'out_tps'}{@render maybeLive(r.predicted_per_second, liveOutSpeed(flight), 1, 'Generation speed so far', 't/s', heatFg(k, r))}
  {:else if k === 'experts'}{@render expertsPct(r)}
  {:else if k === 'drafts'}{@render draftsPct(r)}
  {:else if k === 'ttft'}<td class="num text-right" style:color={heatFg(k, r)}>{@render seconds(ttft(r))}</td>
  {:else if k === 'duration'}<td class="num text-right" style:color={heatFg(k, r)}>{@render seconds(elapsed(r))}</td>
  {:else if k === 'timing'}{@render timing(r)}
  {:else if k === 'energy'}<td class="num text-right" style:color={heatFg(k, r)}>{@render withUnit(r.energy_j != null ? r.energy_j / 3600 : null, 2, 'Wh')}</td>
  {:else if k === 'watts'}<td class="num text-right" style:color={heatFg(k, r)}>{@render withUnit(r.energy_j != null && elapsed(r) > 0 ? r.energy_j / (elapsed(r) / 1000) : null, 0, 'W')}</td>
  {:else if k === 'jtok'}<td class="num text-right" style:color={heatFg(k, r)}>{@render withUnit(r.energy_j != null && r.completion_tokens ? r.energy_j / r.completion_tokens : null, 2, 'J/t')}</td>
  {/if}
{/snippet}

<!-- Each filter is as wide as the value it shows, not its longest choice, and
     stands out while it filters. -->
<div class="mb-3 flex flex-wrap items-center gap-2">
  <h1 class="mr-1 text-[17px] font-semibold">Requests</h1>
  <input
    class="w-48 rounded-md border bg-panel2 px-2 py-1 outline-none focus:border-primary {q.trim() ? 'border-primary/60' : 'border-line'}"
    placeholder="Search"
    title="Search prompts and models"
    bind:value={q}
  />
  <select class={filterCls(model)} bind:value={model} title="Which model's requests to list">
    <option value="">All models</option>
    {#each visibleModels.config as m}<option value={m.id}>{m.label}</option>{/each}
    {#if visibleModels.old.length}
      <optgroup label="No longer in llama-swap's config">
        {#each visibleModels.old as m}<option value={m.id}>{m.label}</option>{/each}
      </optgroup>
    {/if}
  </select>
  {#if servers.many}
    <select class={filterCls(serverFilter)} bind:value={serverFilter} title="Which server's requests to list">
      <option value="">All servers</option>
      {#each servers.list as s (s.id)}<option value={s.id}>{serverName(s)}</option>{/each}
    </select>
  {/if}
  <select class={filterCls(range)} bind:value={range} title="How far back to list requests">
    {#each ranges as [v, label]}<option value={v}>{label}</option>{/each}
  </select>
  <select class={filterCls(issue)} bind:value={issue} title="Problems found in the output">
    <option value="">All requests</option>
    <option value="any">Any issue</option>
    {#each issueKinds as [k, label]}<option value={k}>{label}</option>{/each}
  </select>
  <select class={filterCls(stateFilter)} bind:value={stateFilter} title="Which requests to list by state">
    {#each states as [v, label]}<option value={v}>{label}</option>{/each}
  </select>
  <span class="flex-1"></span>
  {#if silent.length}<span class="text-xs text-muted" title="Their requests aren't listed. They're asked again when the list next loads.">{silent.map((s) => serverName(servers.get(s))).join(', ')} didn't answer</span>{/if}
  {#if !live.connected}<span class="text-xs text-warn">Live updates reconnecting…</span>{/if}
  <div class="inline-flex overflow-hidden rounded-md border border-line" role="radiogroup" aria-label="Column view">
    {#each viewNames as [v, label, title] (v)}
      <button role="radio" aria-checked={viewName === v} class="px-2.5 py-1 {viewName === v ? 'bg-panel2 text-text' : 'text-muted hover:text-text'}" {title} onclick={() => pickView(v)}>{label}</button>
    {/each}
  </div>
  {#if wide}
    <button
      class="rounded-md border px-2 py-1 {panelOn ? 'border-primary/60 bg-panel2 text-text' : 'border-line bg-panel2 text-muted hover:text-text'}"
      title={panelOn ? 'Hide the side panel' : 'Show a request in a panel beside the table: the one clicked, or the newest one streaming'}
      aria-label="Side panel"
      aria-pressed={panelOn}
      onclick={togglePanel}
    >
      <!-- A window with its right part filled, on a 15-pixel grid. -->
      <svg viewBox="0 0 15 15" class="block size-[15px]" fill="currentColor" fill-rule="evenodd" shape-rendering="crispEdges" aria-hidden="true"
        ><path d="M1 2h13v11H1zM2 3v9h7V3z" /></svg
      >
    </button>
  {/if}
  <details bind:this={picker} class="relative">
    <summary class="cursor-pointer list-none rounded-md border border-line bg-panel2 px-2.5 py-1 text-muted hover:text-text">Columns</summary>
    <div class="absolute right-0 z-20 mt-1 w-44 rounded-md border border-line bg-panel2 p-1.5 shadow-lg">
      <div class="px-1.5 pt-0.5 pb-1 text-xs text-dim">Drag columns here or by their headers to move them.</div>
      {#each orderedCols as c (c.key)}
        {@const k = c.key as Col}
        <div
          role="listitem"
          draggable="true"
          class="flex cursor-grab items-center gap-1.5 rounded px-1.5 py-0.5 hover:bg-hover {dragging === k ? 'opacity-40' : ''}"
          title={c.title}
          ondragstart={(e) => dragStart(e, k)}
          ondragover={(e) => dragOver(e, k, 'y')}
          ondrop={(e) => e.preventDefault()}
          ondragend={dragEnd}
        >
          <span class="text-dim" aria-hidden="true">⠿</span>
          <label class="flex flex-1 cursor-pointer items-center gap-2">
            <input type="checkbox" class="accent-primary" checked={chosen.includes(k)} onchange={() => toggleCol(k)} />
            {c.menu ?? c.label}
          </label>
          {#if columnSettings[k]}
            <button
              class="rounded p-0.5 {settingsFor === k ? 'text-primary' : 'text-dim hover:text-text'}"
              title="Settings for this column"
              aria-label="{columns.find((x) => x.key === k)!.label} column settings"
              aria-expanded={settingsFor === k}
              onclick={() => (settingsFor = settingsFor === k ? null : k)}
            >
              <!-- A gear: a ring with eight teeth, on a 12-pixel grid. -->
              <svg viewBox="0 0 12 12" class="block size-3" fill="currentColor" aria-hidden="true">
                <path fill-rule="evenodd" d="M5 0h2v2.1l1.4.6 1.5-1.5 1.4 1.4-1.5 1.5.6 1.4H12v2H9.9l-.6 1.4 1.5 1.5-1.4 1.4-1.5-1.5-1.4.6V12H5V9.9l-1.4-.6-1.5 1.5-1.4-1.4 1.5-1.5L1.6 7H0V5h1.6l.6-1.4L.7 2.1 2.1.7l1.5 1.5L5 1.6zM6 4a2 2 0 1 0 0 4a2 2 0 1 0 0-4z" />
              </svg>
            </button>
          {/if}
        </div>
        {#if settingsFor === k}
          {#each columnSettings[k] ?? [] as set (set.key)}
            <div class="mb-1 ml-5 rounded bg-panel px-1.5 py-1 text-xs" title={set.title}>
              <div class="mb-0.5 text-dim">{set.label}</div>
              {#each set.options as [v, label]}
                <label class="flex cursor-pointer items-center gap-1.5 rounded px-1 py-px hover:bg-hover">
                  <input
                    type="radio"
                    name="requests-col-{k}-{set.key}"
                    class="accent-primary"
                    checked={colSetting(k, set.key) === v}
                    onchange={() => setColSetting(k, set.key, v)}
                  />
                  {label}
                </label>
              {/each}
            </div>
          {/each}
        {/if}
      {/each}
      <button class="mt-1 w-full rounded px-1.5 py-0.5 text-left text-xs text-primary hover:bg-hover" onclick={resetCols}>Reset to default</button>
    </div>
  </details>
  <details bind:this={overflow} class="relative">
    <summary class="cursor-pointer list-none rounded-md border border-line bg-panel2 px-2 py-1 text-muted hover:text-text" title="More options" aria-label="More options">
      <svg viewBox="0 0 16 16" class="size-[15px]" fill="currentColor" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg>
    </summary>
    <div class="absolute right-0 z-20 mt-1 w-44 rounded-md border border-line bg-panel2 p-1.5 shadow-lg">
      <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover" title="Colour Notable chips by type and highlight large prompts with low cache reuse in amber">
        <input type="checkbox" class="accent-primary" checked={colorful} onchange={toggleColorful} />
        Colorful
      </label>
      <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover" title="Show t after token counts and t/s after speeds">
        <input type="checkbox" class="accent-primary" checked={units} onchange={toggleUnits} />
        Show units
      </label>
      <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5 pb-0.5 text-xs text-dim">Highlight worse numbers</div>
      <label class="flex items-center gap-2 px-1.5 py-0.5" title="Dim typical values so worse-than-usual values stand out. Move the slider right to increase dimming.">
        <input type="range" min="0" max="60" step="5" class="min-w-0 flex-1 accent-primary" value={heatStrength} oninput={(e) => setHeat(Number(e.currentTarget.value))} />
        <span class="num w-7 text-right text-xs text-muted">{heatStrength ? `${heatStrength}%` : 'Off'}</span>
      </label>
      <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5 pb-0.5 text-xs text-dim">Column spacing</div>
      {#each spacings as [v, label]}
        <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover">
          <input type="radio" name="requests-spacing" class="accent-primary" checked={spacing === v} onchange={() => setSpacing(v)} />
          {label}
        </label>
      {/each}
      <div class="mt-1 border-t border-line-soft px-1.5 pt-1.5 pb-0.5 text-xs text-dim">Group token columns</div>
      {#each [['phase', 'Prompt / Output'], ['kind', 'Tokens / Speed']] as const as [g, label]}
        <label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-hover">
          <input type="radio" name="requests-grouping" class="accent-primary" checked={grouping === g} onchange={() => setGrouping(g)} />
          {label}
        </label>
      {/each}
    </div>
  </details>
</div>

{#if error}<div class="mb-2 text-err">{error}</div>{/if}

<!-- The table scrolls inside its box so the header can stay pinned. A box that
     scrolls sideways can't also let the header stick to the page. The height
     leaves room for the status bar (--bar-h) and the page's bottom padding (4rem),
     so the whole box fits on screen. -->
<div class="flex items-start gap-2" style:--cell-px={cellPx}>
<div class="max-h-[calc(100dvh_-_var(--bar-h)_-_4.5rem)] min-w-0 flex-1 overflow-auto rounded-lg border border-line bg-panel">
  <table class="w-full border-collapse">
    <colgroup>
      {#each visibleCols as c}<col class={edges.has(c.key) ? 'border-l border-line-soft' : ''} />{/each}
    </colgroup>
    <thead class="sticky top-0 z-10 bg-panel shadow-[inset_0_-1px_0_var(--color-line)]">
      {#if groupRow.some((g) => g.label)}
        <tr class="text-[11px] text-dim [&>th]:px-(--cell-px) [&>th]:pt-1.5 [&>th]:font-medium [&>th]:whitespace-nowrap">
          {#each groupRow as g}
            <th colspan={g.span} class="text-center">
              {#if g.label}<span class="block border-b border-line pb-0.5">{g.label}</span>{/if}
            </th>
          {/each}
        </tr>
      {/if}
      <tr class="text-left text-[11px] text-dim [&>th]:border-b [&>th]:border-line [&>th]:px-(--cell-px) [&>th]:py-1.5 [&>th]:font-medium [&>th]:whitespace-nowrap">
        {#each visibleCols as c (c.key)}
          {@const k = c.key as Col}
          <th
            draggable="true"
            class="cursor-grab {c.num ? 'text-right' : ''} {dragging === k ? 'opacity-40' : ''}"
            title={iconOnly(k) ? [c.label, c.title].filter(Boolean).join('\n') : c.title}
            aria-label={iconOnly(k) ? c.label : undefined}
            ondragstart={(e) => dragStart(e, k)}
            ondragover={(e) => dragOver(e, k, 'x')}
            ondrop={(e) => e.preventDefault()}
            ondragend={dragEnd}
          >{#if k === 'timing'}<span class="block">Duration s</span><span class="block text-[10px] leading-[11px]">TTFT</span>{:else if !iconOnly(k)}{c.label}{/if}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each shown as r (rk(r))}
        {@const flight = r.state === 'in_flight' ? live.of(r.server ?? '')?.get(r.id) : undefined}
        <tr
          class="cursor-pointer [&>td]:border-b [&>td]:border-line-soft [&>td]:px-(--cell-px) [&>td]:py-1.5 [&>td]:whitespace-nowrap {current === rk(r) ? '[&>td]:bg-primary/10' : 'hover:[&>td]:bg-hover'}"
          onclick={() => rowClick(r)}
        >
          {#each visibleCols as c}{@render cell(c.key, r, flight)}{/each}
        </tr>
        {#if !usePanel && open === rk(r)}
          <tr><td colspan={visibleCols.length} class="border-b border-line-soft bg-[#252930] px-3.5 py-3"><RequestDetail row={r} usualExperts={heat.median('experts', r)} onopen={(id) => (open = rk({ server: r.server, id }))} /></td></tr>
        {/if}
      {:else}
        <tr><td colspan={visibleCols.length} class="px-3 py-6 text-center text-muted">{loading ? 'Loading…' : 'No matching requests.'}</td></tr>
      {/each}
    </tbody>
  </table>
  {#if more && rows.length}
    <div class="my-3 text-center">
      <button class="rounded-md border border-line bg-panel2 px-3 py-1 hover:border-primary" disabled={loading} onclick={() => load(false)}>
        {loading ? 'Loading…' : 'Load older'}
      </button>
    </div>
  {/if}
</div>
{#if usePanel}
  <aside
    class="relative flex h-[calc(100dvh_-_var(--bar-h)_-_4.5rem)] flex-none flex-col rounded-lg border border-line bg-panel"
    style:width="{panelWidth}px"
    aria-label="Request panel"
  >
    <!-- Dragging the left edge sets the width. -->
    <div
      role="separator"
      aria-orientation="vertical"
      class="absolute inset-y-0 -left-[6px] z-10 w-[8px] cursor-col-resize hover:bg-primary/20"
      title="Drag to resize"
      onpointerdown={resizeStart}
    ></div>
    <div class="flex items-center gap-2 border-b border-line px-2.5 py-1.5">
      <div class="inline-flex overflow-hidden rounded-md border border-line text-xs">
        {#each [['selected', 'Selected', 'The request last clicked in the table'], ['live', 'Live', 'The newest request in flight. It stays on one request until that one finishes.']] as const as [m, label, title]}
          <button class="px-2 py-0.5 {panelMode === m ? 'bg-panel2 text-text' : 'text-muted hover:text-text'}" {title} onclick={() => setPanelMode(m)}>{label}</button>
        {/each}
      </div>
      {#if panelMode === 'live' && panelRow && panelRow.state !== 'in_flight'}<span class="text-xs text-dim">Finished. The next request shows here when it starts.</span>{/if}
      <span class="flex-1"></span>
      <button class="flex size-6 items-center justify-center rounded-md text-dim hover:bg-hover hover:text-text" title="Hide the side panel" aria-label="Hide the side panel" onclick={togglePanel}>
        <svg viewBox="0 0 10 10" class="block size-[10px]" fill="currentColor" aria-hidden="true"><path d="M1.4 0 5 3.6 8.6 0 10 1.4 6.4 5 10 8.6 8.6 10 5 6.4 1.4 10 0 8.6 3.6 5 0 1.4z" /></svg>
      </button>
    </div>
    <div bind:this={panelBody} class="min-h-0 flex-1 overflow-auto px-3 py-2.5" onscroll={panelScroll}>
      <div bind:this={panelContent}>
        {#if panelRow}
          {#key panelKey}
            <RequestDetail
              row={panelRow}
              usualExperts={heat.median('experts', panelRow)}
              onopen={(id) => {
                open = rk({ server: panelRow?.server, id })
                setPanelMode('selected')
              }}
            />
          {/key}
        {:else}
          <div class="py-6 text-center text-muted">
            {panelMode === 'live' ? 'Nothing is streaming. The next request shows here when it starts.' : 'Click a request in the table to show it here.'}
          </div>
        {/if}
      </div>
    </div>
  </aside>
{/if}
</div>

<ChipPopover />

