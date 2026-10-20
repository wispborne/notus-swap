// The model detail's tabs, in the order they are shown.
export type DetailTab = 'log' | 'loads' | 'builds' | 'requests'

export const detailTabs: { tab: DetailTab; label: string }[] = [
  { tab: 'log', label: 'Log' },
  { tab: 'loads', label: 'Loads and unloads' },
  { tab: 'builds', label: 'Speed by build and settings' },
  { tab: 'requests', label: 'Recent requests' },
]
