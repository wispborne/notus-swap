// One colour per model, the same on every page.
//
// Models take colours in the order llama-swap's config lists them, so models
// next to each other in the config (often variants of one model) always get
// different colours. With more models than colours, a model shares its colour
// with the one 9 places before it. Models seen in requests but no longer in
// the config come after the config's models, in name order. A model not seen
// in either falls back to a colour picked from its name.
//
// The palette was checked on the surface colour #21242B: every pair differs by
// at least ΔE 10 (OKLab ×100) for normal vision, and neighbours pass the
// colour-blindness checks too.
const palette = ['#40d7a3', '#b69cff', '#f0b85a', '#5b8cff', '#ff7a9a', '#18ffff', '#e95cf0', '#c4e36b', '#ff5a36']

let order = $state<string[]>([])
let extra = $state<string[]>([])

/** Sets the config's model order. Called with every status poll. */
export function setModelOrder(names: string[]) {
  if (names.length === order.length && names.every((n, i) => n === order[i])) return
  order = names
}

/** Adds models seen in requests, so ones missing from the config get their own colours too. */
export function noteModels(names: Iterable<string>) {
  const add = [...new Set(names)].filter((n) => n && !extra.includes(n))
  if (add.length) extra = [...extra, ...add].sort()
}

export function modelColor(model: string) {
  let i = order.indexOf(model)
  if (i < 0 && extra.includes(model)) i = order.length + extra.filter((m) => !order.includes(m)).indexOf(model)
  if (i >= 0) return palette[i % palette.length]
  let h = 0
  for (const c of model) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return palette[h % palette.length]
}
