// The Dashboard merges every server's data into one, and names another
// server's models "@<server id>:<model>" there, so every widget keeps working
// with one model name per series. The shared lookups (modelLabel, modelColor,
// modelShape, modelId and privacy) send such names here, to be looked up on
// their own server. servers.svelte.ts fills in the lookup; this module imports
// nothing, so those modules can use it without importing each other.
import type { Shape } from './modelColors.svelte'

export interface PeerModelView {
  label: (m: string) => string
  title: (m: string) => string | undefined
  color: (m: string) => string | undefined
  shape: (m: string) => Shape | undefined
  visible: (m: string) => boolean
  id: (m: string) => string
  /** The server's name, to tell its models from another server's. */
  serverName: string
}

const NAME = /^@([0-9a-f]{12}):/

/** The name a model on server goes by in merged data. '' is this server. */
export function qualify(server: string, model: string) {
  return server ? `@${server}:${model}` : model
}

let lookup: ((server: string) => PeerModelView) | null = null
let marks: () => string = () => ''

export function setPeerLookup(view: (server: string) => PeerModelView, marksKey: () => string) {
  lookup = view
  marks = marksKey
}

/** Another server's model, if m names one. */
export function peerModel(m: string): { server: string; model: string; view: PeerModelView } | null {
  if (!lookup || m[0] !== '@') return null
  const x = NAME.exec(m)
  if (!x) return null
  return { server: x[1], model: m.slice(x[0].length), view: lookup(x[1]) }
}

/** Changes whenever another server's colours or shapes change, for charts to rebuild. */
export const peerMarksKey = () => marks()
