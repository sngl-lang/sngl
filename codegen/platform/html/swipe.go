package html

import (
	"duckfam.us/sngl/ir"
)

// swipeTag is the custom element ui.tabs' html override puts its panels in,
// which the page defines with swipeHelper. The DOM has no swipe event, so the
// element hears the pointer and dispatches `swipe`, which html.element
// declares as an event.
const swipeTag = "sngl-swipe"

// swipeHelper defines swipeTag. A touch or a pen that moves at least 48px
// along x, and further along x than half again its move along y, is a swipe:
// `detail` is 1 where it moved left, bringing in what follows, and -1 where
// it moved right. A mouse drags to select text, so it swipes nothing. A
// gesture starting in something that scrolls sideways is that scroll's: the
// Sprites, Evolution and Moves panels of the Pokédex do. `touch-action:
// pan-y` leaves the browser the vertical pan and the element the horizontal,
// and stops at the nearest scroll container, so a sideways scroll inside
// still pans itself.
const swipeHelper = `if (!customElements.get("sngl-swipe")) customElements.define("sngl-swipe", class extends HTMLElement {
  connectedCallback() {
    this.style.display = "block";
    this.style.touchAction = "pan-y";
    let from = null;
    const scrollsAcross = (el) => {
      for (; el && el !== this; el = el.parentElement) {
        const ox = getComputedStyle(el).overflowX;
        if ((ox === "auto" || ox === "scroll") && el.scrollWidth > el.clientWidth) return true;
      }
      return false;
    };
    this._down = (e) => {
      from = e.pointerType === "mouse" || scrollsAcross(e.target) ? null : { id: e.pointerId, x: e.clientX, y: e.clientY };
    };
    this._up = (e) => {
      if (!from || e.pointerId !== from.id) return;
      const dx = e.clientX - from.x, dy = e.clientY - from.y;
      from = null;
      if (Math.abs(dx) >= 48 && Math.abs(dx) > 1.5 * Math.abs(dy)) {
        this.dispatchEvent(new CustomEvent("swipe", { detail: dx < 0 ? 1 : -1 }));
      }
    };
    this._cancel = () => { from = null; };
    this.addEventListener("pointerdown", this._down);
    this.addEventListener("pointerup", this._up);
    this.addEventListener("pointercancel", this._cancel);
  }
  disconnectedCallback() {
    this.removeEventListener("pointerdown", this._down);
    this.removeEventListener("pointerup", this._up);
    this.removeEventListener("pointercancel", this._cancel);
  }
});
`

// rendersSwipe reports whether the page writes swipeTag anywhere, which is
// when it needs the element defined.
func rendersSwipe(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && !found {
			if t, ok := rawElementTag(ni); ok && t == swipeTag {
				found = true
			}
		}
		return nil
	})
	return found
}
