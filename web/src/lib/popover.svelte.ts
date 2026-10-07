// One popover for the whole page, so a table of chips needs no listener per
// chip. Hovering shows it next to the mouse, and it follows the mouse like
// the site's tooltip. A click or tap keeps it where it is until the next
// click elsewhere.
import type { Chip } from './notable'

class Popover {
  chips = $state<Chip[]>([])
  // The mouse position it sits next to.
  x = $state(0)
  y = $state(0)
  pinned = $state(false)
  private owner: Element | null = null

  show(chips: Chip[], el: Element, e: MouseEvent) {
    if (this.pinned && this.owner !== el) return
    this.chips = chips
    this.owner = el
    this.move(e)
  }

  move(e: MouseEvent) {
    if (this.pinned) return
    this.x = e.clientX
    this.y = e.clientY
  }

  hide(el: Element) {
    if (!this.pinned && this.owner === el) this.close()
  }

  toggle(chips: Chip[], el: Element, e: MouseEvent) {
    if (this.pinned && this.owner === el) return this.close()
    this.pinned = false
    this.show(chips, el, e)
    this.pinned = true
  }

  close() {
    this.chips = []
    this.pinned = false
    this.owner = null
  }
}

export const popover = new Popover()
