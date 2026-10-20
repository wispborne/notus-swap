// Which requests count toward speeds, on the Models page and its Speed tab.
// Matches store.SpeedFilter; the server keeps it as the speed_filter setting.
export interface SpeedFilter {
  min_output: number
  min_new_prompt: number
  alone: boolean
  no_images: boolean
  exclude_clients: string[]
  min_context: number
  max_context: number
  structured: '' | 'exclude' | 'only'
}

export async function loadSpeedFilter(): Promise<{ filter: SpeedFilter; default: SpeedFilter }> {
  const res = await fetch('/notus/api/speed-filter')
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  return res.json()
}

export async function saveSpeedFilter(f: SpeedFilter) {
  const res = await fetch('/notus/api/speed-filter', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(f),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
}

export const sameFilter = (a: SpeedFilter, b: SpeedFilter) =>
  JSON.stringify({ ...a, exclude_clients: [...a.exclude_clients].sort() }) === JSON.stringify({ ...b, exclude_clients: [...b.exclude_clients].sort() })
