// One colour per model, the same on every page.
//
// The server gives each model a colour once and keeps it (model_colors in
// internal/api/colors.go), so colours stay the same across restarts, config
// edits and browsers. They arrive with every status poll. A new model gets a
// colour its neighbours in llama-swap's config don't have, and that the fewest
// config models use. A model without a stored colour yet falls back to one
// picked from its name.
//
// The palette was checked on the surface colour #21242B: every pair differs by
// at least ΔE 10 (OKLab ×100) for normal vision, and neighbours pass the
// colour-blindness checks too. Its length must match paletteSize in colors.go.
//
// The user can pick another palette colour, a colour of their own, and a
// shape for any model, on the Settings page or in the Models page's detail.
// The shape and own colour are kept in the model_marks setting, and arrive
// with the status poll too.
export const palette = ['#40d7a3', '#b69cff', '#f0b85a', '#5b8cff', '#ff7a9a', '#18ffff', '#e95cf0', '#c4e36b', '#ff5a36']

let stored = $state<Record<string, number>>({})

/** The colour outside the palette (`#rrggbb`) and the shape a model was given, if any. */
export interface Mark {
  color?: string
  shape?: Shape
}
let marks = $state<Record<string, Mark>>({})

/**
 * The shapes a model's dot can take, drawn on an 8 by 8 pixel grid. Straight
 * edges sit on whole pixels. Shapes must match `shapes` in colors.go.
 */
export const shapes = {
  circle: { name: 'Circle', d: 'M4 0a4 4 0 1 1 0 8a4 4 0 1 1 0-8z' },
  square: { name: 'Square', d: 'M1 1h6v6h-6z' },
  diamond: { name: 'Diamond', d: 'M4 0l4 4-4 4-4-4z' },
  triangle: { name: 'Triangle', d: 'M4 0l4 7h-8z' },
  triangle_down: { name: 'Triangle, pointing down', d: 'M0 1h8l-4 7z' },
  plus: { name: 'Plus', d: 'M3 0h2v3h3v2h-3v3h-2v-3h-3v-2h3z' },
}
export type Shape = keyof typeof shapes

/** Sets the chosen colours and shapes. Called with every status poll. */
export function setModelMarks(next: Record<string, Mark>) {
  if (JSON.stringify(next) !== JSON.stringify(marks)) marks = next
}

/** A model's shape. Circle unless one was chosen. */
export function modelShape(model: string): Shape {
  const s = marks[model]?.shape
  return s && s in shapes ? s : 'circle'
}

/** The palette index the server gave a model, if any. */
export const paletteIndex = (model: string): number | undefined => stored[model]

/** A model's colour outside the palette, if one was chosen. */
export const customColor = (model: string) => marks[model]?.color

/**
 * Changes for every change to any colour or shape. Charts read it with `void`
 * so they are rebuilt with the new look.
 */
export const marksKey = () => JSON.stringify(stored) + JSON.stringify(marks)

/**
 * Saves a model's colour and shape on the server. `palette` picks a palette
 * colour and drops an own colour; `color` is an own colour.
 */
export async function saveMark(model: string, change: { palette?: number; color?: string; shape: Shape }) {
  const res = await fetch(`/notus/api/models/${encodeURIComponent(model)}/mark`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ palette: change.palette, color: change.color ?? '', shape: change.shape }),
  })
  if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`)
  // Show it at once rather than at the next status poll.
  if (change.palette !== undefined) stored = { ...stored, [model]: change.palette }
  const m: Mark = {}
  if (change.color && change.palette === undefined) m.color = change.color
  if (change.shape !== 'circle') m.shape = change.shape
  const next = { ...marks }
  if (m.color || m.shape) next[model] = m
  else delete next[model]
  marks = next
}

/** Sets the stored colours. Called with every status poll. */
export function setModelColors(colors: Record<string, number>) {
  const keys = Object.keys(colors)
  if (keys.length === Object.keys(stored).length && keys.every((k) => stored[k] === colors[k])) return
  stored = colors
}

export function modelColor(model: string) {
  const own = marks[model]?.color
  if (own) return own
  const i = stored[model]
  if (i !== undefined) return palette[i % palette.length]
  let h = 0
  for (const c of model) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return palette[h % palette.length]
}
