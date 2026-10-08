// A model's ID and display name, from llama-swap's config. Requests store the
// model as the client sent it, which may be an alias.
//
// Every page shows models by name or by ID, as picked on the Settings page
// and saved per browser (`model_labels`). Tooltips show both.

import { status, type RunningModel } from './status.svelte'

/** The config entry for a model ID or alias, if llama-swap knows it. */
export function knownModel(model: string): RunningModel | undefined {
  const known = status.current?.llama_swap.known ?? []
  return known.find((m) => m.model === model) ?? known.find((m) => m.aliases?.includes(model))
}

export type ModelShow = 'name' | 'id'

const key = 'model_labels'
// Before this setting, the Requests page's Model column had its own choice.
const oldKey = 'requests_column_settings'

function saved(): ModelShow {
  try {
    const v = localStorage.getItem(key)
    if (v === 'name' || v === 'id') return v
    const old = JSON.parse(localStorage.getItem(oldKey) ?? 'null')?.model?.show
    if (old === 'id') {
      localStorage.setItem(key, 'id')
      return 'id'
    }
  } catch {
    // Use the default.
  }
  return 'name'
}

class ModelShowChoice {
  show = $state<ModelShow>(saved())

  set(v: ModelShow) {
    this.show = v
    try {
      localStorage.setItem(key, v)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
}

export const modelShow = new ModelShowChoice()

/** What to show for a model: its name (or its ID when it has none), or its ID. */
export function modelLabel(model: string, show: ModelShow = modelShow.show): string {
  if (!model) return ''
  const k = knownModel(model)
  if (!k) return model
  return show === 'name' ? k.name || k.model : k.model
}

/** A tooltip with the model's name, ID, and the alias the request used. */
export function modelTitle(model: string): string | undefined {
  if (!model) return undefined
  const k = knownModel(model)
  if (!k) return `ID: ${model}`
  const lines: string[] = []
  if (k.name) lines.push(`Name: ${k.name}`)
  lines.push(`ID: ${k.model}`)
  if (model !== k.model) lines.push(`Requested as: ${model}`)
  return lines.join('\n')
}
