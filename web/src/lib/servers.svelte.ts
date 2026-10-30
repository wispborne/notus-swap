// The Servers list: this notus-swap and the other notus-swap servers it
// shows, in order. The first is the primary. The browser only talks to this
// server, which passes calls on to the others (internal/peers). The list
// arrives with every status poll.
import { appTitle } from './appTitle.svelte'
import { modelShow } from './modelNames.svelte'
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
 * Another server's status, polled while a page shows that server, for its
 * models and hidden models. The "Show hidden models" switch is this server's.
 */
export class RemoteStatus {
  current = $state<Status | null>(null)
  private timer?: ReturnType<typeof setInterval>

  constructor(
    private server: string,
    private showHidden: () => boolean,
  ) {}

  start() {
    const poll = async () => {
      try {
        const res = await fetch(api(this.server, '/notus/api/status'))
        if (res.ok) this.current = await res.json()
      } catch {
        // Offline: keep the last answer.
      }
    }
    poll()
    this.timer = setInterval(poll, 5000)
    return () => clearInterval(this.timer)
  }

  private hidden = $derived(new Set(this.current?.privacy.hidden_models ?? []))

  /** The model's ID, if m is one of its aliases. */
  private id(m: string) {
    return this.current?.llama_swap.known.find((k) => k.model === m || k.aliases?.includes(m))?.model ?? m
  }

  visible = (m: string) => this.showHidden() || !this.hidden.has(this.id(m))
  /** Whether the server hides any model. */
  hides = $derived(this.hidden.size > 0)

  label = (m: string) => {
    const k: RunningModel | undefined = this.current?.llama_swap.known.find((x) => x.model === this.id(m))
    return modelShow.show === 'name' && k?.name ? k.name : m
  }
}
