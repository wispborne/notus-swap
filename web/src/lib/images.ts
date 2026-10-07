// Finds the images in a request's messages, in OpenAI, Responses API, and
// Anthropic form, and describes them for the image viewer.

import type { Msg, Part } from './responsesInput'

export type Image = { src: string; msg: number }

// The image's address: a data URL or a link. Anthropic images hold base64
// data and a media type instead of a URL.
export function imageSrc(p: Part): string | undefined {
  if (typeof p.image_url === 'string') return p.image_url
  if (p.image_url?.url) return p.image_url.url
  if (p.type === 'image' && p.source) {
    if (p.source.type === 'base64' && p.source.data) return `data:${p.source.media_type ?? 'image/png'};base64,${p.source.data}`
    if (p.source.url) return p.source.url
  }
  return undefined
}

export function requestImages(messages: Msg[]): Image[] {
  const out: Image[] = []
  messages.forEach((m, i) => {
    if (!Array.isArray(m.content)) return
    for (const p of m.content) {
      const src = imageSrc(p)
      if (src) out.push({ src, msg: i })
    }
  })
  return out
}

// "PNG" for a data URL holding a PNG, "link" for a web address.
export function imageFormat(src: string) {
  const m = /^data:([^;,]+)/.exec(src)
  if (!m) return 'link'
  return (m[1].split('/')[1] ?? 'image').replace(/^x-/, '').toUpperCase()
}

// The decoded size of a base64 data URL, in bytes. Null for a link.
export function imageBytes(src: string): number | null {
  const comma = src.indexOf(',')
  if (!src.startsWith('data:') || comma < 0 || !src.slice(0, comma).endsWith(';base64')) return null
  const data = src.slice(comma + 1).replace(/\s/g, '')
  const pad = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0
  return Math.floor((data.length * 3) / 4) - pad
}

export function formatBytes(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// Browsers block opening data URLs in a new tab, so the image becomes a blob first.
export async function openInTab(src: string) {
  if (!src.startsWith('data:')) {
    window.open(src, '_blank', 'noopener')
    return
  }
  const blob = await (await fetch(src)).blob()
  const url = URL.createObjectURL(blob)
  window.open(url, '_blank', 'noopener')
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}

export function fileName(src: string, n: number) {
  const format = imageFormat(src)
  const ext = format === 'link' ? (/\.(\w{3,4})(?:[?#]|$)/.exec(src)?.[1] ?? 'png') : format.toLowerCase().replace('jpeg', 'jpg').replace('svg+xml', 'svg')
  return `image-${n}.${ext}`
}
