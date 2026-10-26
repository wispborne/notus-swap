// The model detail's tabs, in the order they are shown.
export type DetailTab = 'log' | 'loads' | 'builds' | 'requests'

// short is the name on the Models page's row buttons.
export const detailTabs: { tab: DetailTab; label: string; short: string }[] = [
  { tab: 'log', label: 'Log', short: 'Log' },
  { tab: 'loads', label: 'Loads and unloads', short: 'Loads' },
  { tab: 'builds', label: 'Speed by build and settings', short: 'Speed' },
  { tab: 'requests', label: 'Recent requests', short: 'Requests' },
]
