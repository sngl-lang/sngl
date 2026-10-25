package html

import (
	"duckfam.us/sngl/ir"
)

// reachEndTag is the custom element ui.scroll's html override writes after
// its content, which the page defines with reachEndHelper. The DOM has no
// event for the end of a box's content coming into view, so the element is
// the sentinel that asks: it watches itself against its scrolling parent and
// dispatches `reachEnd`, which html.element declares as an event.
const reachEndTag = "sngl-reach-end"

// reachEndHelper defines reachEndTag. An IntersectionObserver rooted at the
// parent fires as the element comes within half the parent's height of
// showing -- including at once, where the content is too short to scroll --
// and a ResizeObserver on the parent's content asks again whenever the
// content changes size while it is there, so a handler that adds a page is
// asked for the next while the end still shows. The content grew where the
// element moved: the parent's scroll height stands still while the content
// is shorter than the parent, so a first page too short to scroll asked for
// one page more and no further. That second check waits for
// the next frame: a handler adding rows from inside the observer's callback
// resizes what it observes again in the same delivery, which the browser
// reports as an error the page raised. It measures then rather than reading
// the intersection observer's last answer, which would be a frame stale. A
// child added to the parent is watched too, and so is anything added below
// it: content a slot renders into a `display:contents` span has no box of
// its own for the resize observer to see grow. Everything disconnects when
// the element leaves the page.
const reachEndHelper = `if (!customElements.get("sngl-reach-end")) customElements.define("sngl-reach-end", class extends HTMLElement {
  connectedCallback() {
    const box = this.parentElement;
    if (!box) return;
    this.style.display = "block";
    const fire = () => this.dispatchEvent(new Event("reachEnd"));
    const near = () => this.getBoundingClientRect().top - box.getBoundingClientRect().bottom <= box.clientHeight / 2;
    this._io = new IntersectionObserver((es) => {
      if (es[es.length - 1].isIntersecting) fire();
    }, { root: box, rootMargin: "0px 0px 50% 0px" });
    this._io.observe(this);
    let last = this.offsetTop, asked = false;
    const grew = () => {
      const h = this.offsetTop;
      if (h !== last && !asked) {
        asked = true;
        requestAnimationFrame(() => {
          asked = false;
          if (this.isConnected && near()) fire();
        });
      }
      last = h;
    };
    this._ro = new ResizeObserver(grew);
    const watch = (c) => { if (c !== this && c.nodeType === 1) this._ro.observe(c); };
    for (const c of box.children) watch(c);
    this._mo = new MutationObserver((ms) => {
      for (const m of ms) if (m.target === box) m.addedNodes.forEach(watch);
      grew();
    });
    this._mo.observe(box, { childList: true, subtree: true });
  }
  disconnectedCallback() {
    if (this._io) this._io.disconnect();
    if (this._ro) this._ro.disconnect();
    if (this._mo) this._mo.disconnect();
  }
});
`

// scrollRules scroll the box ui.scroll's override writes, which renders
// reachEndTag in every one, so a page defining the element needs them.
var scrollRules = []string{
	".sngl-scroll { overflow: auto; }",
}

// rendersReachEnd reports whether the page writes reachEndTag anywhere, which
// is when it needs the element defined.
func rendersReachEnd(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && !found {
			if t, ok := rawElementTag(ni); ok && t == reachEndTag {
				found = true
			}
		}
		return nil
	})
	return found
}
