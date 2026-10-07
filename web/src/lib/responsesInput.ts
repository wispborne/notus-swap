// Turns an OpenAI Responses API request (instructions plus a list of input
// items) into the chat messages that RequestMessages shows.

export type Part = {
  type?: string
  text?: string
  image_url?: { url?: string } | string
  // Anthropic images
  source?: { type?: string; media_type?: string; data?: string; url?: string }
}
export type Msg = {
  role?: string
  content?: string | Part[] | null
  reasoning_content?: string
  tool_calls?: { function?: { name?: string; arguments?: string } }[]
  tool_call_id?: string
  name?: string
}

type Item = {
  type?: string
  role?: string
  content?: unknown
  summary?: { text?: string }[]
  name?: string
  arguments?: string
  input?: string
  call_id?: string
  output?: unknown
}

function isItem(x: unknown): x is Item {
  return typeof x === 'object' && x !== null && !Array.isArray(x) && ('role' in x || 'type' in x)
}

function parts(content: unknown): string | Part[] | null {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return null
  return content.map((p): Part => {
    if (p?.type === 'input_text' || p?.type === 'output_text') return { type: 'text', text: p.text }
    if (p?.type === 'input_image') return { type: 'image_url', image_url: p.image_url }
    return p
  })
}

function itemText(parts: { text?: string }[] | undefined) {
  return (parts ?? []).map((p) => p.text ?? '').join('')
}

// Returns null for a request that isn't a Responses API call, such as an
// embeddings request, whose input is a string or a list of strings.
export function responsesMessages(req: Record<string, unknown>): Msg[] | null {
  const input = req.input
  const items = Array.isArray(input) && input.some(isItem) ? input.filter(isItem) : null
  if (!items && typeof req.instructions !== 'string') return null

  const out: Msg[] = []
  if (typeof req.instructions === 'string') out.push({ role: 'system', content: req.instructions })
  if (typeof input === 'string') out.push({ role: 'user', content: input })

  // Reasoning and tool calls are separate items. They join the assistant
  // message of the same turn, as in a chat request.
  let reasoning = ''
  const assistant = () => {
    const last = out[out.length - 1]
    if (last?.role === 'assistant' && !reasoning) return last
    const m: Msg = { role: 'assistant', content: '', reasoning_content: reasoning || undefined }
    reasoning = ''
    out.push(m)
    return m
  }
  const call = (name?: string, args?: string) => {
    const m = assistant()
    m.tool_calls = [...(m.tool_calls ?? []), { function: { name, arguments: args } }]
  }

  for (const it of items ?? []) {
    switch (it.type ?? 'message') {
      case 'message':
        if (it.role === 'assistant') {
          const m: Msg = { role: 'assistant', content: parts(it.content), reasoning_content: reasoning || undefined }
          reasoning = ''
          out.push(m)
        } else {
          out.push({ role: it.role, content: parts(it.content) })
        }
        break
      case 'reasoning':
        reasoning += itemText(Array.isArray(it.content) ? it.content : undefined) || itemText(it.summary)
        break
      case 'function_call':
        call(it.name, it.arguments)
        break
      case 'custom_tool_call':
        call(it.name, it.input)
        break
      case 'function_call_output':
      case 'custom_tool_call_output':
        out.push({ role: 'tool', tool_call_id: it.call_id, content: parts(it.output) })
        break
      default: {
        const { type, ...rest } = it
        out.push({ role: type, content: JSON.stringify(rest, null, 2) })
      }
    }
  }
  if (reasoning) out.push({ role: 'assistant', content: '', reasoning_content: reasoning })
  return out
}
