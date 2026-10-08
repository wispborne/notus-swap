// The app's name, shown in the status bar's corner and the browser tab.
// It is the server-side app_title setting, which arrives with every status
// poll, so a change reaches every open page within 2 s. A copy is kept in
// localStorage so index.html can set the tab title before the page draws.

export const defaultTitle = 'notus-swap'

function cached(): string {
  try {
    return localStorage.getItem('app_title') ?? ''
  } catch {
    return ''
  }
}

class AppTitle {
  /** The saved title, or '' for the default. */
  saved = $state(cached())
  /** The title to show. */
  readonly text = $derived(this.saved.trim() || defaultTitle)

  apply(t: string | undefined) {
    this.saved = t ?? ''
    document.title = this.text
    try {
      if (this.saved) localStorage.setItem('app_title', this.saved)
      else localStorage.removeItem('app_title')
    } catch {
      // The tab shows the default until the first status poll.
    }
  }

  async set(t: string) {
    const v = t.trim()
    const res = await fetch('/notus/api/settings/app_title', { method: 'PUT', body: JSON.stringify(v) })
    if (!res.ok) throw new Error(await res.text())
    this.apply(v)
  }
}

export const appTitle = new AppTitle()
