// Shorten legend names within groups that share their first hyphenated segment.
// Remove the common prefix, keeping at least one segment in every name.
// A model alone in its group keeps its full name.
export function shortNames(models: string[]): Map<string, string> {
  const out = new Map<string, string>()
  const groups = new Map<string, string[]>()
  for (const m of models) {
    const first = m.split('-')[0]
    groups.set(first, [...(groups.get(first) ?? []), m])
  }
  for (const group of groups.values()) {
    const parts = group.map((m) => m.split('-'))
    let prefixLength = 0
    if (group.length > 1) {
      const prefixLimit = Math.min(...parts.map((p) => p.length)) - 1
      while (prefixLength < prefixLimit && parts.every((p) => p[prefixLength] === parts[0][prefixLength])) prefixLength++
    }
    group.forEach((m, i) => out.set(m, parts[i].slice(prefixLength).join('-')))
  }
  return out
}

/**
 * Splits each label into the words it shares with other labels that start
 * with the same word, and the rest, so a narrow space can show the part that
 * tells them apart. Words are split at spaces, hyphens and colons, which stay
 * in place. "Glistening Gem 31B Q5_K_M" and "Glistening Gem 31B Q5_K_M
 * (lowthink)" give ["Glistening Gem 31B ", "Q5_K_M"] and ["Glistening Gem
 * 31B ", "Q5_K_M (lowthink)"]. A label alone in its group is all rest.
 */
export function splitShared(labels: string[]): Map<string, [string, string]> {
  const out = new Map<string, [string, string]>()
  // Words at even places, separators at odd places.
  const tokens = new Map(labels.map((l) => [l, l.split(/([-:\s]+)/)]))
  for (const l of labels) {
    const t = tokens.get(l)!
    const group = labels.filter((o) => tokens.get(o)![0] === t[0])
    if (group.length === 1) {
      out.set(l, ['', l])
      continue
    }
    const words = (x: string) => tokens.get(x)!.filter((_, i) => i % 2 === 0)
    // Every label in the group keeps at least one word in the rest, so a
    // label that adds words to another keeps the other's last word too.
    const limit = Math.min(...group.map((o) => words(o).length)) - 1
    let k = 0
    while (k < limit && group.every((o) => words(o)[k] === words(l)[k])) k++
    out.set(l, [t.slice(0, 2 * k).join(''), t.slice(2 * k).join('')])
  }
  return out
}
