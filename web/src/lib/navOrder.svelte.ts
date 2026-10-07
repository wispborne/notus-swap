// The order of the pages in the sidebar and the phone's bottom bar. Saved
// per browser, like the status bar's items.

export const pageIds = ['dashboard', 'requests', 'models', 'logs', 'config', 'system'] as const
export type PageId = (typeof pageIds)[number]

const key = 'sidebar_order'

function saved(): PageId[] {
  try {
    const list = JSON.parse(localStorage.getItem(key) ?? 'null')
    if (!Array.isArray(list)) return [...pageIds]
    const known = list.filter((id, n): id is PageId => pageIds.includes(id) && list.indexOf(id) === n)
    // Pages added in a later version go at the end.
    return [...known, ...pageIds.filter((id) => !known.includes(id))]
  } catch {
    return [...pageIds]
  }
}

class NavOrder {
  list = $state<PageId[]>(saved())

  save() {
    try {
      localStorage.setItem(key, JSON.stringify(this.list))
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }

  // Moves a page to sit just before or after another. Saving is left to the
  // caller, so a drag saves once at the end.
  place(id: PageId, target: PageId, after: boolean) {
    if (id === target) return
    const rest = this.list.filter((p) => p !== id)
    const at = rest.indexOf(target) + (after ? 1 : 0)
    this.list = [...rest.slice(0, at), id, ...rest.slice(at)]
  }

  reset() {
    this.list = [...pageIds]
    try {
      localStorage.removeItem(key)
    } catch {
      // Nothing saved to remove.
    }
  }

  get isDefault() {
    return this.list.every((id, i) => id === pageIds[i])
  }
}

export const navOrder = new NavOrder()
