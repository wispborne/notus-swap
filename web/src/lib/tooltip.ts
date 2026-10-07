// One tooltip for the whole site, in place of the browser's own. Any element
// with a title gets it: it shows at once and follows the mouse.
//
// While the mouse is over an element, its title is taken off so the browser's
// tooltip doesn't show as well. It is put back when the mouse leaves, so
// screen readers still have it.

const OFFSET_X = 14
const OFFSET_Y = 18
const EDGE = 8

// Where to put a box of size w×h next to the cursor at (x, y): below and to
// the right, or flipped to the other side when it would leave the window.
export function nextToCursor(x: number, y: number, w: number, h: number) {
  let left = x + OFFSET_X
  let top = y + OFFSET_Y
  if (left + w > innerWidth - EDGE) left = x - OFFSET_X - w
  if (top + h > innerHeight - EDGE) top = y - 6 - h
  return { left: Math.max(EDGE, left), top: Math.max(EDGE, top) }
}

export function installTooltip(): () => void {
  const box = document.createElement('div')
  box.className = 'site-tooltip'
  // It repeats the title, which is put back for screen readers.
  box.setAttribute('aria-hidden', 'true')
  document.body.append(box)

  let owner: Element | null = null
  let text: string | null = null
  let x = 0
  let y = 0
  // Hidden by a click until the mouse moves to another element or the text
  // changes, like the browser's own tooltip.
  let clicked = false

  // Svelte sets a title again whenever its value changes, such as a copy
  // button's "Copied". Take the new text and remove the title again.
  const watch = new MutationObserver(() => {
    if (!owner) return
    text = owner.getAttribute('title')
    if (text !== null) {
      owner.removeAttribute('title')
      watch.takeRecords()
    }
    clicked = false
    show()
  })

  function take(el: Element) {
    owner = el
    text = el.getAttribute('title')
    el.removeAttribute('title')
    watch.observe(el, { attributes: true, attributeFilter: ['title'] })
    clicked = false
  }

  function release() {
    if (!owner) return
    watch.disconnect()
    if (text !== null && owner.isConnected) owner.setAttribute('title', text)
    owner = null
    text = null
    box.style.display = 'none'
  }

  function show() {
    if (!text || clicked) {
      box.style.display = 'none'
      return
    }
    if (box.textContent !== text) box.textContent = text
    box.style.display = 'block'
    const { left, top } = nextToCursor(x, y, box.offsetWidth, box.offsetHeight)
    box.style.transform = `translate(${left}px, ${top}px)`
  }

  function move(e: PointerEvent) {
    if (e.pointerType === 'touch') return release()
    x = e.clientX
    y = e.clientY
    const target = e.target as Element
    const found = target.closest?.('[title]') ?? null
    // The owner has no title while it is shown, so `closest` skips it. Keep
    // it unless the mouse is over a titled element inside it.
    const stay = owner?.isConnected && owner.contains(target) && (!found || found.contains(owner))
    if (!stay) {
      release()
      if (found) take(found)
    }
    show()
  }

  function out(e: PointerEvent) {
    // Left the window.
    if (!e.relatedTarget) release()
  }

  function down() {
    clicked = true
    show()
  }

  addEventListener('pointermove', move, { passive: true })
  addEventListener('pointerout', out, { passive: true })
  addEventListener('pointerdown', down, { passive: true })
  // Scrolling moves the page under a still mouse; the next move finds the
  // new element.
  addEventListener('scroll', release, { capture: true, passive: true })
  return () => {
    release()
    removeEventListener('pointermove', move)
    removeEventListener('pointerout', out)
    removeEventListener('pointerdown', down)
    removeEventListener('scroll', release, { capture: true })
    box.remove()
  }
}
