// Which status bar items show, and in what order. Saved per browser, so a
// phone and a desktop can differ.

export const itemIds = ['llama-swap', 'requests', 'vram', 'ram', 'power', 'system', 'models'] as const
export type ItemId = (typeof itemIds)[number]

export const itemLabels: Record<ItemId, string> = {
  'llama-swap': 'llama-swap up or down',
  requests: 'Active requests',
  vram: 'VRAM',
  ram: 'RAM',
  power: 'GPU power',
  system: 'System power',
  models: 'Loaded models',
}

export type Item = { id: ItemId; on: boolean }

const key = 'statusbar_items'
const defaults = (): Item[] => itemIds.map((id) => ({ id, on: true }))

function saved(): Item[] {
  try {
    const list = JSON.parse(localStorage.getItem(key) ?? 'null')
    if (!Array.isArray(list)) return defaults()
    const known = list.filter((i): i is Item => itemIds.includes(i?.id) && typeof i.on === 'boolean')
    const ids = new Set(known.map((i) => i.id))
    // Items added in a later version go at the end, shown.
    return [...known.filter((i, n) => known.findIndex((j) => j.id === i.id) === n), ...defaults().filter((i) => !ids.has(i.id))]
  } catch {
    return defaults()
  }
}

class StatusItems {
  list = $state<Item[]>(saved())

  save() {
    try {
      localStorage.setItem(key, JSON.stringify(this.list))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  toggle(id: ItemId) {
    this.list = this.list.map((i) => (i.id === id ? { ...i, on: !i.on } : i))
    this.save()
  }

  // Moves an item to sit just before or after another. Saving is left to the
  // caller, so a drag saves once at the end.
  place(id: ItemId, target: ItemId, after: boolean) {
    if (id === target) return
    const item = this.list.find((i) => i.id === id)!
    const rest = this.list.filter((i) => i.id !== id)
    const at = rest.findIndex((i) => i.id === target) + (after ? 1 : 0)
    this.list = [...rest.slice(0, at), item, ...rest.slice(at)]
  }

  shift(id: ItemId, by: number) {
    const from = this.list.findIndex((i) => i.id === id)
    const to = from + by
    if (to < 0 || to >= this.list.length) return
    const list = [...this.list]
    ;[list[from], list[to]] = [list[to], list[from]]
    this.list = list
    this.save()
  }

  reset() {
    this.list = defaults()
    try {
      localStorage.removeItem(key)
    } catch {
      // Nothing saved to remove.
    }
  }

  get isDefault() {
    return this.list.every((i, n) => i.on && i.id === itemIds[n])
  }
}

export const statusItems = new StatusItems()
