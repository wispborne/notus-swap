// What a model can take in and do, as its llama-server last reported it, and
// the chips that show it in the Models page's Capabilities column.
import { shortTokens, type Chip } from '../notable'

/** store.ModelProps, from GET /notus/api/models. Saved each time the model becomes ready. */
export interface ModelProps {
  /** The context size of one slot: the most one request can use. */
  n_ctx: number
  /** The server's default output limit; 0 or less for none. */
  n_predict: number
  slots: number
  build?: string
  /** Missing for props saved before notus-swap read capabilities. */
  caps?: Caps
}

/** llamaswap.Caps. */
export interface Caps {
  vision: boolean
  audio: boolean
  /** The chat template handles thinking text. */
  thinking: boolean
  /** llama-server's chat_template_caps, such as supports_tool_calls. Older llama-server versions don't send it. */
  template?: Record<string, boolean>
}

/**
 * The context size as a chip, such as 250k: the size in llama-swap's config,
 * or else what llama-server gives one request.
 */
export function contextChip(configured: number | undefined, props: ModelProps | undefined): Chip | null {
  const size = configured || props?.n_ctx
  if (!size) return null
  const lines = []
  if (configured) lines.push(`${configured.toLocaleString('en-US')} tokens in llama-swap's config.`)
  if (props) {
    const slots = `${props.slots.toLocaleString('en-US')} ${props.slots === 1 ? 'slot' : 'slots'}`
    lines.push(`llama-server gives each request up to ${props.n_ctx.toLocaleString('en-US')} tokens, with ${slots}.`)
  }
  return { kind: 'context', label: shortTokens(size), bright: false, title: 'Context size', lines }
}

/** One chip for each thing the model can do, in a fixed order. */
export function capChips(caps: Caps | undefined): Chip[] {
  if (!caps) return []
  const t = caps.template ?? {}
  const out: Chip[] = []
  if (caps.vision) out.push({ kind: 'vision', icon: 'image', label: '', bright: false, color: '#40d7a3', title: 'Images', lines: ['Reads images sent in requests.'] })
  if (caps.audio) out.push({ kind: 'audio', icon: 'audio', label: '', bright: false, color: '#ff7a9a', title: 'Audio', lines: ['Reads audio sent in requests.'] })
  if (t.supports_tool_calls) {
    const lines = ['Its chat template supports tool calls.']
    if (t.supports_parallel_tool_calls) lines.push('It can make several calls in one answer.')
    out.push({ kind: 'tools', icon: 'tool', label: '', bright: false, color: '#18ffff', title: 'Tool calls', lines })
  }
  if (caps.thinking) {
    const lines = ['Its chat template handles thinking text.']
    if (t.supports_reasoning_effort) lines.push('Requests can set how much it thinks (reasoning_effort).')
    if (t.supports_preserve_reasoning) lines.push('Earlier thinking is kept in the conversation.')
    out.push({ kind: 'thinking', icon: 'think', label: '', bright: false, color: '#b69cff', title: 'Thinking', lines })
  }
  return out
}

/** The capabilities in words, for the model detail. */
export function capWords(caps: Caps): { yes: string[]; no: string[] } {
  const t = caps.template
  const all: [string, boolean | undefined][] = [
    ['images', caps.vision],
    ['audio', caps.audio],
    // Unknown without chat_template_caps, so left out of both lists.
    ['tool calls', t ? !!t.supports_tool_calls : undefined],
    ['thinking', caps.thinking],
  ]
  return { yes: all.filter(([, v]) => v === true).map(([w]) => w), no: all.filter(([, v]) => v === false).map(([w]) => w) }
}
