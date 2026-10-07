// The colour theme, picked on the Settings page and saved per browser. The
// colours are in themes.css; this only sets `data-theme` on <html>. An inline
// script in index.html sets it too, before the page draws, so a reload
// doesn't flash Sigma first.

export interface Theme {
  id: string
  name: string
  /** surface, panel, primary, secondary: for the picker's swatches. */
  swatch: [string, string, string, string]
}

export const themes: Theme[] = [
  { id: 'sigma', name: 'Sigma', swatch: ['#21242b', '#282c34', '#40d7a3', '#18ffff'] },
  { id: 'redacted', name: '[REDACTED]', swatch: ['#071412', '#0d1f1c', '#46ffeb', '#8fa9a6'] },
  { id: 'player', name: 'Player', swatch: ['#04141c', '#0a2029', '#8bc3e8', '#4fb3c8'] },
  { id: 'one-dark', name: 'One Dark', swatch: ['#21242b', '#282c34', '#6a93e0', '#98c379'] },
  { id: 'independents', name: 'Independents', swatch: ['#101216', '#1a1d23', '#b8bcc4', '#ffd737'] },
  { id: 'lavender', name: 'Lavender', swatch: ['#1a1524', '#241d31', '#e3b7d8', '#9ad8e8'] },
  { id: 'knights-of-ludd', name: 'Knights of Ludd', swatch: ['#14170a', '#1d2110', '#b4d93c', '#e4dcc4'] },
  { id: 'sindrian-diktat', name: 'Sindrian Diktat', swatch: ['#170a12', '#22101b', '#e762ce', '#f2a6e0'] },
]

const key = 'theme'
const fallback = 'sigma'

function saved(): string {
  try {
    const id = localStorage.getItem(key)
    return themes.some((t) => t.id === id) ? id! : fallback
  } catch {
    return fallback
  }
}

function apply(id: string) {
  if (id === fallback) delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = id
}

class ThemeChoice {
  id = $state(saved())

  set(id: string) {
    this.id = id
    apply(id)
    try {
      if (id === fallback) localStorage.removeItem(key)
      else localStorage.setItem(key, id)
    } catch {
      // Not saved; it still applies until the page reloads.
    }
  }
}

export const theme = new ThemeChoice()
apply(theme.id)

/**
 * A theme colour's current value, such as cssColor('grid') for --color-grid.
 * For canvas charts, which can't use var(). Charts read it when they are
 * built, so a theme change shows on them the next time the page opens.
 */
export function cssColor(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(`--color-${name}`).trim()
}
