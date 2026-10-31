// The Servers list: this notus-swap and the other notus-swap servers it
// shows, in order. The first is the primary. The browser only talks to this
// server, which passes calls on to the others (internal/peers). The list
// arrives with every status poll.
import { SvelteMap } from 'svelte/reactivity'
import { appTitle } from './appTitle.svelte'
import { palette, type Shape } from './modelColors.svelte'
import { modelShow } from './modelNames.svelte'
import { privacy } from './privacy.svelte'
import type { RunningModel, Status } from './status.svelte'

export interface Server {
  id: string
  name?: string
  url?: string
  self: boolean
  state: 'checking' | 'online' | 'offline' | 'wrong_server' | 'not_notus' | 'needs_update'
  since?: number
  last_seen?: number
  version?: string
  clock_gap_ms?: number
  error?: string
}

class Servers {
  list = $state<Server[]>([])
  /** False until the list has been read once. */
  ready = $state(false)
  private loading?: Promise<void>

  apply(list: Server[] | undefined) {
    if (!list) return
    if (JSON.stringify(list) !== JSON.stringify(this.list)) this.list = list
    this.ready = true
    peerStatus.sync(list)
  }

  /** Reads the list now, for pages that don't wait for a status poll. */
  load() {
    if (this.ready) return Promise.resolve()
    this.loading ??= fetch('/notus/api/peers')
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then((b: { servers: Server[] }) => this.apply(b.servers))
      .catch(() => {
        this.loading = undefined
      })
    return this.loading
  }

  get = (id: string) => this.list.find((s) => s.id === id)
  /** Whether there are servers other than this one. */
  many = $derived(this.list.length > 1)
}

export const servers = new Servers()

/** A server's name: this server's app title, or the name given to another. */
export function serverName(s: Server | undefined) {
  if (!s) return 'unknown server'
  if (s.self) return appTitle.text
  return s.name || s.url || s.id
}

/** Whether calls reach the server now. */
export const reachable = (s: Server | undefined) => !!s && ['online', 'checking', 'needs_update'].includes(s.state)

/** A few words on the server's state, for its row or the picker. */
export function stateWords(s: Server) {
  switch (s.state) {
    case 'online':
      return 'Online'
    case 'checking':
      return 'Checking…'
    case 'offline':
      return 'Asleep or off'
    case 'wrong_server':
      return 'Address answers as another server'
    case 'not_notus':
      return "Address doesn't answer as notus-swap"
    case 'needs_update':
      return 'Needs an update'
  }
}

/**
 * The path for a call to a server. '' is this server. Calls to other servers
 * go through this one, at /notus/peer/<id>/.
 */
export function api(server: string, path: string) {
  return server ? `/notus/peer/${server}${path}` : path
}

/**
 * The server a page with a server picker shows: ?server=<id>, or else the
 * first server in the list that answers, which is the primary unless it is
 * asleep. '' is this server. Needs servers.ready. Pages read it once, so a
 * server that wakes up doesn't change the page under the user.
 */
export function pageServer(): string {
  const want = new URLSearchParams(location.search).get('server')
  const s = (want && servers.get(want)) || servers.list.find((s) => s.self || reachable(s))
  return !s || s.self ? '' : s.id
}

/** Opens the page again on another server. A reload starts its data afresh. */
export function showServer(id: string) {
  const u = new URL(location.href)
  u.searchParams.set('server', id)
  location.assign(u)
}

/**
 * Another server's status, polled every 2 seconds through this server, for
 * the status bar and for its models and hidden models. This server's "Show
 * hidden models" switch applies to them.
 */
export class RemoteStatus {
  current = $state<Status | null>(null)
  private timer?: ReturnType<typeof setInterval>

  constructor(private server: string) {}

  start() {
    const poll = async () => {
      // While the server is off, this server would only answer 503. The list
      // says when it is back.
      const s = servers.get(this.server)
      if (s && !reachable(s)) return
      try {
        const res = await fetch(api(this.server, '/notus/api/status'))
        if (res.ok) this.current = await res.json()
      } catch {
        // Unreachable: keep the last answer. The server's state says it is off.
      }
    }
    poll()
    this.timer = setInterval(poll, 2000)
  }

  stop() {
    clearInterval(this.timer)
  }

  private hidden = $derived(new Set(this.current?.privacy.hidden_models ?? []))

  /** The model's ID, if m is one of its aliases. */
  private id(m: string) {
    return this.current?.llama_swap.known.find((k) => k.model === m || k.aliases?.includes(m))?.model ?? m
  }
  private known(m: string): RunningModel | undefined {
    const id = this.id(m)
    return this.current?.llama_swap.known.find((x) => x.model === id)
  }

  visible = (m: string) => privacy.showHidden || !this.hidden.has(this.id(m))
  /** Whether the server hides any model. */
  hides = $derived(this.hidden.size > 0)

  label = (m: string) => {
    const k = this.known(m)
    return modelShow.show === 'name' && k?.name ? k.name : m
  }
  /** Both the name and the ID, for a tooltip. */
  title = (m: string) => {
    const k = this.known(m)
    return k?.name && k.name !== k.model ? `${k.name}\n${k.model}` : this.id(m)
  }
  /** The model's colour and shape on its own server. */
  color = (m: string) => {
    const id = this.id(m)
    const own = this.current?.model_marks?.[id]?.color
    const i = this.current?.model_colors?.[id]
    return own ?? (i != null ? palette[i % palette.length] : undefined)
  }
  shape = (m: string): Shape | undefined => this.current?.model_marks?.[this.id(m)]?.shape
}

/** One RemoteStatus per other server in the list, shared by every page. */
class PeerStatuses {
  private map = new SvelteMap<string, RemoteStatus>()

  /** Starts polling servers new to the list, and stops removed ones. */
  sync(list: Server[]) {
    const ids = new Set(list.filter((s) => !s.self).map((s) => s.id))
    for (const id of ids) this.of(id)
    for (const [id, r] of this.map)
      if (!ids.has(id)) {
        r.stop()
        this.map.delete(id)
      }
  }

  /** The server's status poller, if it has one. Unlike of(), never starts one. */
  get(id: string) {
    return this.map.get(id)
  }

  of(id: string) {
    let r = this.map.get(id)
    if (!r) {
      r = new RemoteStatus(id)
      this.map.set(id, r)
      r.start()
    }
    return r
  }
}

export const peerStatus = new PeerStatuses()
