// Turns ANSI colour codes in log text into HTML. Copied from llama-swap's
// ui/src/lib/ansi.ts and cut down to the dark palette. llama-swap's licence:
//
// MIT License
//
// Copyright (c) 2024 Benson Wong
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
// FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS
// IN THE SOFTWARE.

interface Style {
  bold: boolean
  italic: boolean
  underline: boolean
  strikethrough: boolean
  inverse: boolean
  fg: string | null
  bg: string | null
}

const DEFAULT: Style = { bold: false, italic: false, underline: false, strikethrough: false, inverse: false, fg: null, bg: null }

// Terminal colours brightened for a dark background.
const colors = ['#767676', '#ff5555', '#50fa7b', '#f1fa8c', '#61afef', '#ff79c1', '#8be9fd', '#e5e5e5']
const brightColors = ['#969696', '#ff6e6e', '#69ff94', '#f5fc9e', '#7cc0ff', '#ff92d0', '#a4f5ff', '#ffffff']

function color256(n: number): string {
  if (n < 16) return n < 8 ? colors[n] : brightColors[n - 8]
  if (n < 232) {
    const i = n - 16
    const to255 = (v: number) => (v === 0 ? 0 : v * 40 + 55)
    return `rgb(${to255(Math.floor(i / 36))}, ${to255(Math.floor((i % 36) / 6))}, ${to255(i % 6)})`
  }
  const v = (n - 232) * 10 + 8
  return `rgb(${v}, ${v}, ${v})`
}

function applySgr(prev: Style, params: number[]): Style {
  let style = prev
  const update = (patch: Partial<Style>) => {
    if (style === prev) style = { ...prev }
    Object.assign(style, patch)
  }
  for (let i = 0; i < params.length; i++) {
    const p = params[i]
    if (p === 0) update({ ...DEFAULT })
    else if (p === 1) update({ bold: true })
    else if (p === 3) update({ italic: true })
    else if (p === 4) update({ underline: true })
    else if (p === 7) update({ inverse: true })
    else if (p === 9) update({ strikethrough: true })
    else if (p === 22) update({ bold: false })
    else if (p === 23) update({ italic: false })
    else if (p === 24) update({ underline: false })
    else if (p === 27) update({ inverse: false })
    else if (p === 29) update({ strikethrough: false })
    else if (p >= 30 && p <= 37) update({ fg: colors[p - 30] })
    else if (p === 39) update({ fg: null })
    else if (p >= 40 && p <= 47) update({ bg: colors[p - 40] })
    else if (p === 49) update({ bg: null })
    else if (p >= 90 && p <= 97) update({ fg: brightColors[p - 90] })
    else if (p >= 100 && p <= 107) update({ bg: brightColors[p - 100] })
    else if (p === 38 || p === 48) {
      const isFg = p === 38
      if (params[i + 1] === 5) {
        const c = color256(params[i + 2] ?? 0)
        update(isFg ? { fg: c } : { bg: c })
        i += 2
      } else if (params[i + 1] === 2) {
        const c = `rgb(${params[i + 2] ?? 0}, ${params[i + 3] ?? 0}, ${params[i + 4] ?? 0})`
        update(isFg ? { fg: c } : { bg: c })
        i += 4
      }
    }
  }
  return style
}

const isDefault = (s: Style) =>
  !s.bold && !s.italic && !s.underline && !s.strikethrough && !s.inverse && s.fg === null && s.bg === null

function css(s: Style): string {
  const parts: string[] = []
  if (s.bold) parts.push('font-weight:600')
  if (s.italic) parts.push('font-style:italic')
  const deco = [s.underline && 'underline', s.strikethrough && 'line-through'].filter(Boolean)
  if (deco.length) parts.push(`text-decoration:${deco.join(' ')}`)
  // Reverse video swaps the text and background colours.
  const fg = s.inverse ? (s.bg ?? 'var(--color-sunken)') : s.fg
  const bg = s.inverse ? (s.fg ?? 'var(--color-text)') : s.bg
  if (fg) parts.push(`color:${fg}`)
  if (bg) parts.push(`background-color:${bg}`)
  return parts.join(';')
}

const entities: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }
export const escapeHtml = (text: string) => text.replace(/[&<>"']/g, (c) => entities[c])

const CSI = /\x1b\[([0-9;?]*)([a-zA-Z])/g

/** The text with ANSI escape codes removed. */
export const stripAnsi = (text: string) => (text.includes('\x1b[') ? text.replace(CSI, '') : text)

/**
 * HTML for text that may hold ANSI escape codes. Colour and style codes
 * become styled spans; other codes are dropped. The text is HTML-escaped.
 */
export function ansiToHtml(text: string): string {
  if (!text.includes('\x1b[')) return escapeHtml(text)

  let html = ''
  let last = 0
  let style = DEFAULT
  let open: Style | null = null
  const close = () => {
    if (open) html += '</span>'
    open = null
  }
  const flush = (end: number) => {
    if (end <= last) return
    const chunk = escapeHtml(text.slice(last, end))
    last = end
    if (isDefault(style)) {
      close()
      html += chunk
    } else if (open === style) {
      html += chunk
    } else {
      close()
      html += `<span style="${css(style)}">${chunk}`
      open = style
    }
  }

  const re = new RegExp(CSI.source, 'g')
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    flush(m.index)
    last = m.index + m[0].length
    if (m[2] === 'm') {
      const params = m[1] === '' ? [0] : m[1].split(';').map((p) => (p === '' ? 0 : Number.parseInt(p, 10)))
      style = applySgr(style, params)
    }
  }
  flush(text.length)
  close()
  return html
}
