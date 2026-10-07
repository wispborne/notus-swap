// Shorter model names for a chart's legend. Models whose names start with the
// same word (the text before the first "-") are grouped, and each group drops
// the start its names share, cut at a "-". So `qwen3.8-27b-dev-swift` and
// `qwen3.8-27b-swift-1.0-radiance` become `dev-swift` and `swift-1.0-radiance`.
// A model alone in its group keeps its full name, and no name is cut to nothing.
export function shortNames(models: string[]): Map<string, string> {
  const out = new Map<string, string>()
  const groups = new Map<string, string[]>()
  for (const m of models) {
    const first = m.split('-')[0]
    groups.set(first, [...(groups.get(first) ?? []), m])
  }
  for (const group of groups.values()) {
    const parts = group.map((m) => m.split('-'))
    // How many leading words every name shares, leaving at least one word in each.
    let n = 0
    if (group.length > 1) {
      const most = Math.min(...parts.map((p) => p.length)) - 1
      while (n < most && parts.every((p) => p[n] === parts[0][n])) n++
    }
    group.forEach((m, i) => out.set(m, parts[i].slice(n).join('-')))
  }
  return out
}
