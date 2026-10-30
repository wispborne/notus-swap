// Compares two llama-server commands flag by flag, for the Models page.

const isFlag = (a: string) => /^-{1,2}[A-Za-z]/.test(a)

/**
 * Reads a command's arguments as flags and their values. A flag takes the
 * next argument as its value unless that is another flag. Arguments that
 * aren't flags or values (the program, for one) are keyed by position.
 * A flag given more than once is keyed with its count, such as "-ot (2)".
 */
function flags(args: string[]): Map<string, string> {
  const out = new Map<string, string>()
  const seen = new Map<string, number>()
  let pos = 0
  for (let i = 0; i < args.length; i++) {
    const a = args[i]
    if (!isFlag(a)) {
      out.set(`#${pos++}`, a)
      continue
    }
    let name = a
    let value = ''
    const eq = a.indexOf('=')
    if (a.startsWith('--') && eq > 0) {
      name = a.slice(0, eq)
      value = a.slice(eq + 1)
    } else if (i + 1 < args.length && !isFlag(args[i + 1])) {
      value = args[++i]
    }
    const n = (seen.get(name) ?? 0) + 1
    seen.set(name, n)
    out.set(n > 1 ? `${name} (${n})` : name, value)
  }
  return out
}

const show = (key: string, value: string) => (key.startsWith('#') ? value : `${key} ${value}`.trim())

// Strata's engine settings, which notus-swap adds to a Strata model's
// command as --strata.<name>=<value> (llamaswap.Strata.SettingArgs).
const strata = (args: string[]) => args.some((a) => a.startsWith('--strata.'))

/** What changed from `before` to `after`, as short phrases like "-c 4096 → 8192". */
export function cmdDiff(before: string[], after: string[]): string[] {
  // Commands stored before notus-swap read Strata's settings have none of
  // them. That isn't a change of settings, so the first record is one phrase.
  const recorded = strata(after) && !strata(before)
  if (recorded) after = after.filter((x) => !x.startsWith('--strata.'))
  const a = flags(before)
  const b = flags(after)
  const out: string[] = recorded ? ['Strata engine settings recorded'] : []
  for (const [k, v] of b) {
    if (!a.has(k)) out.push(`added ${show(k, v)}`)
    else if (a.get(k) !== v) out.push(k.startsWith('#') ? `${a.get(k)} → ${v}` : `${k} ${a.get(k)} → ${v}`)
  }
  for (const [k, v] of a) if (!b.has(k)) out.push(`removed ${show(k, v)}`)
  return out
}

/** Joins arguments back into one line, quoting any that hold spaces. */
export function cmdLine(args: string[]) {
  return args.map((a) => (/[\s"']/.test(a) ? `'${a.replaceAll("'", `'\\''`)}'` : a)).join(' ')
}
