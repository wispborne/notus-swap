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
