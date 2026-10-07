// A model's ID and display name, from llama-swap's config. Requests store the
// model as the client sent it, which may be an alias.

import { status, type RunningModel } from './status.svelte'

/** The config entry for a model ID or alias, if llama-swap knows it. */
export function knownModel(model: string): RunningModel | undefined {
  const known = status.current?.llama_swap.known ?? []
  return known.find((m) => m.model === model) ?? known.find((m) => m.aliases?.includes(model))
}

export type ModelShow = 'name' | 'id'

/** What to show for a model: its name (or its ID when it has none), or its ID. */
export function modelLabel(model: string, show: ModelShow): string {
  if (!model) return ''
  const k = knownModel(model)
  if (!k) return model
  return show === 'name' ? k.name || k.model : k.model
}

/** A tooltip with the model's ID, name, and the alias the request used. */
export function modelTitle(model: string): string | undefined {
  const k = knownModel(model)
  if (!k) return undefined
  const lines = [`ID: ${k.model}`]
  if (k.name) lines.push(`Name: ${k.name}`)
  if (model !== k.model) lines.push(`Requested as: ${model}`)
  return lines.join('\n')
}
