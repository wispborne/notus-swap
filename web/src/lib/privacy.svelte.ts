// Hidden models: models the UI keeps off screen unless "show hidden models"
// is on. Everything is still captured and measured; only the UI filters.
//
// Filter through `visible()` (or the helpers below) wherever a model name
// could appear. The settings arrive with every status poll, so a change made
// on one device reaches every open page within 2 seconds.
import type { DashboardData } from './dashboard/data'

class Privacy {
  hidden = $state<string[]>([])
  showHidden = $state(false)
  /** False until the settings have been read once; pages wait for it. */
  ready = $state(false)
  private hiddenSet = $derived(new Set(this.hidden))

  /** Whether a model may appear on screen right now. */
  visible = (model: string) => this.showHidden || !this.hiddenSet.has(model)

  isHidden = (model: string) => this.hiddenSet.has(model)

  /** Whether a log line may appear: it names no hidden model. */
  logLine = (line: string) => this.showHidden || !this.hidden.some((m) => line.includes(m))

  /**
   * Whether a log source may appear. The combined output of all models can't
   * be split by model, and a hidden model's output rarely names the model, so
   * that log is off while any model is hidden.
   */
  logSource = (source: string) => {
    if (this.showHidden) return true
    if (source === 'upstream') return !this.hidden.length
    return !source.startsWith('model:') || this.visible(source.slice(6))
  }

  async load() {
    try {
      const [h, s] = await Promise.all(
        ['hidden_models', 'show_hidden'].map((k) => fetch(`/notus/api/settings/${k}`).then((r) => r.json())),
      )
      this.apply({ hidden_models: h ?? [], show_hidden: !!s })
    } catch {
      // Stay not-ready, so pages keep waiting; the next status poll
      // carries the settings too.
    }
  }

  apply(p: { hidden_models: string[]; show_hidden: boolean }) {
    if (JSON.stringify(p.hidden_models) !== JSON.stringify(this.hidden)) this.hidden = p.hidden_models
    this.showHidden = p.show_hidden
    this.ready = true
  }

  async setHidden(model: string, hide: boolean) {
    const next = hide ? [...new Set([...this.hidden, model])].sort() : this.hidden.filter((m) => m !== model)
    this.hidden = next
    await put('hidden_models', next)
  }

  async setShowHidden(show: boolean) {
    this.showHidden = show
    await put('show_hidden', show)
  }

  /** The Dashboard's data without hidden models' requests and events. */
  dashboard(d: DashboardData): DashboardData {
    if (this.showHidden || !this.hidden.length) return d
    return {
      ...d,
      requests: d.requests.filter((r) => this.visible(r.model)),
      model_events: d.model_events.filter((e) => this.visible(e.model)),
    }
  }
}

function put(key: string, value: unknown) {
  return fetch(`/notus/api/settings/${key}`, { method: 'PUT', body: JSON.stringify(value) })
}

export const privacy = new Privacy()
