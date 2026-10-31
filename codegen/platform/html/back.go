package html

import (
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// backTag is the custom element a window's way back is in on html, which the
// page defines with backHelper. A page has no back of its own to hear but the
// browser's, so the element keeps a history entry while it is connected --
// while the window can go back -- and the browser's back pops it: the element
// hears the popstate and dispatches `back`, which html.element declares as an
// event. The window's up arrow goes back through the history as well, so one
// press runs `@back` once whichever way it came.
const backTag = "sngl-back"

// backHelper defines backTag. An entry already there -- the page reloaded on
// it -- is held rather than pushed again. One the element still holds as it
// leaves stays: going back through history from a disconnect left the page
// whenever a harness tore it down, and a document's way back is read once, so
// only the page going takes the element away.
const backHelper = `if (!customElements.get("sngl-back")) customElements.define("sngl-back", class extends HTMLElement {
  connectedCallback() {
    this.style.display = "contents";
    if (!(history.state && history.state.snglBack)) history.pushState({ snglBack: true }, "");
    this._held = true;
    this._pop = () => {
      if (!this._held) return;
      this._held = false;
      this.dispatchEvent(new CustomEvent("back"));
    };
    addEventListener("popstate", this._pop);
  }
  disconnectedCallback() {
    removeEventListener("popstate", this._pop);
  }
});
`

// rendersBack reports whether the page writes backTag anywhere, which is when
// it needs the element defined: as a node, or as the tag a render slot's body
// creates it with -- a window's way back is under an `if`.
func rendersBack(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			if t, ok := rawElementTag(x); ok && t == backTag {
				found = true
			}
		case *ir.Literal:
			if s, ok := codegen.IRLiteralString(x); ok && s == backTag {
				found = true
			}
		}
		if found {
			return ir.SkipAll
		}
		return nil
	})
	return found
}
