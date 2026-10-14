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
const palette = ['#40d7a3', '#b69cff', '#f0b85a', '#5b8cff', '#ff7a9a', '#18ffff', '#e95cf0', '#c4e36b', '#ff5a36']

let stored = $state<Record<string, number>>({})

/** Sets the stored colours. Called with every status poll. */
export function setModelColors(colors: Record<string, number>) {
  const keys = Object.keys(colors)
  if (keys.length === Object.keys(stored).length && keys.every((k) => stored[k] === colors[k])) return
  stored = colors
}

export function modelColor(model: string) {
  const i = stored[model]
  if (i !== undefined) return palette[i % palette.length]
  let h = 0
  for (const c of model) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return palette[h % palette.length]
}
