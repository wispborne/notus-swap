// Requests store the model name the client sent, which may be an alias from
// llama-swap's config. Colours, shapes, hidden models and anything grouped
// per model go by the model's ID, so an alias counts as its model.
//
// The aliases arrive with every status poll.
import { peerModel, qualify } from './peerModels'

let ids = $state<Record<string, string>>({})

/** Sets each alias's model ID, from llama-swap's config. Called with every status poll. */
export function setModelAliases(known: { model: string; aliases?: string[] }[]) {
  const next: Record<string, string> = {}
  for (const m of known) for (const a of m.aliases ?? []) next[a] ??= m.model
  if (JSON.stringify(next) !== JSON.stringify(ids)) ids = next
}

/**
 * The model ID for a name that may be an alias. Other names are returned as
 * they are. Another server's model (peerModels.ts) is looked up on that server.
 */
export const modelId = (name: string): string => {
  const p = peerModel(name)
  if (p) return qualify(p.server, p.view.id(p.model))
  return ids[name] ?? name
}

/** A model's aliases. */
export const modelAliases = (id: string): string[] => Object.keys(ids).filter((a) => ids[a] === id)
