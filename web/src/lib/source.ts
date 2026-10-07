// A short name for the client that sent a request, from its User-Agent
// header, for the Requests page's Source column.

import type { Summary } from './api'

// Checked in order; the first match names the client. Each pattern is
// matched against the whole User-Agent, ignoring case.
const known: [RegExp, string][] = [
  [/codex/i, 'Codex'],
  [/claude-cli|claude-code/i, 'Claude Code'],
  [/opencode/i, 'opencode'],
  [/open-?webui/i, 'Open WebUI'],
  [/^OpenAI\/Python/i, 'OpenAI Python'],
  [/^OpenAI\/JS/i, 'OpenAI JS'],
  [/^Anthropic\/Python/i, 'Anthropic Python'],
  [/^Anthropic\/JS/i, 'Anthropic JS'],
  [/aiohttp/i, 'Python aiohttp'],
  [/python-httpx/i, 'Python httpx'],
  [/python-requests/i, 'Python requests'],
  [/^curl\//i, 'curl'],
  [/^Wget/i, 'Wget'],
  [/Go-http-client/i, 'Go'],
  [/node-fetch|undici|^node/i, 'Node'],
  [/^Mozilla\//i, 'Browser'],
]

/** A short label for where r came from, or '' when nothing is known. */
export function sourceLabel(r: Pick<Summary, 'user_agent' | 'retry_of'>): string {
  if (r.retry_of) return 'Retry'
  const ua = r.user_agent?.trim()
  if (!ua) return ''
  for (const [re, label] of known) if (re.test(ua)) return label
  // Otherwise the first product name, such as "my-tool" from "my-tool/1.2 (Linux)".
  return ua.split(/[\s/;(]/)[0] || ua
}

/** The tooltip: the full User-Agent and the address. */
export function sourceTitle(r: Pick<Summary, 'user_agent' | 'client_ip' | 'retry_of'>): string {
  const lines = []
  if (r.retry_of) lines.push(`Sent again from the web UI, from request #${r.retry_of}`)
  if (r.user_agent) lines.push(`User-Agent: ${r.user_agent}`)
  if (r.client_ip) lines.push(`Address: ${r.client_ip}`)
  return lines.join('\n') || 'Not recorded. Requests from before this column existed have no source.'
}
