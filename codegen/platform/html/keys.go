package html

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// keysTag is the custom element a window's `@key` is heard by on html, which
// the page defines with keysHelper. The window it is in is the `<dialog>` it
// is in, or the document where it is in none, and it listens for a keydown
// there while it is connected, so a test's remount, which takes the page's
// elements away, takes its listener with them. A key nothing in the window
// took is dispatched as `key`, which html.element declares as an event.
const keysTag = "sngl-keys"

// keysHelper defines keysTag. What a window does not hear is what lib/ui's
// window says something in it takes: a key a handler prevented, one with
// Control, Alt or Meta held, a Tab, one typed into a field, an Enter or a
// space pressing a focused button or link, and an Escape while the window
// can go back, which is while it holds a `sngl-back`. The document does not
// hear a key pressed in a `<dialog>`, which is a window of its own. A key is
// reported as KeyboardEvent.key names it, where that is one character or one
// of the names lib/ui lists: a Shift alone, a function key or a dead key is
// not.
const keysHelper = `if (!customElements.get("sngl-keys")) customElements.define("sngl-keys", class extends HTMLElement {
  connectedCallback() {
    this.style.display = "none";
    const dialog = this.closest("dialog");
    const named = ["Enter", "Escape", "Backspace", "Delete", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown"];
    this._on = dialog || document;
    this._key = (e) => {
      if (e.defaultPrevented || e.ctrlKey || e.altKey || e.metaKey || e.isComposing) return;
      if ([...e.key].length !== 1 && !named.includes(e.key)) return;
      const t = e.target instanceof Element ? e.target : null;
      if (t) {
        if (t.closest("dialog") !== dialog) return;
        if (t.isContentEditable || t.closest("input, textarea, select")) return;
        if ((e.key === "Enter" || e.key === " ") && t.closest("button, a[href], summary, [role=button], [role=link], [role=tab]")) return;
      }
      if (e.key === "Escape" && [...this._on.querySelectorAll("sngl-back")].some((b) => b.closest("dialog") === dialog)) return;
      this.dispatchEvent(new KeyboardEvent("key", { key: e.key }));
    };
    this._on.addEventListener("keydown", this._key);
  }
  disconnectedCallback() {
    this._on.removeEventListener("keydown", this._key);
  }
});
`

// rendersKeys reports whether the page writes keysTag anywhere, which is when
// it needs the element defined: as a node, or as the tag a render slot's body
// creates it with -- a window under an `if`.
func rendersKeys(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			if t, ok := rawElementTag(x); ok && t == keysTag {
				found = true
			}
		case *ir.Literal:
			if s, ok := codegen.IRLiteralString(x); ok && s == keysTag {
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

// dropUnheardKeys takes the keysTag out of each Window in pkg whose call site
// does not handle `@key`: its handler is then empty, and a page would listen
// for every key to dispatch it to nothing. A Window the markup writes holds it
// as a node; one a script builds -- under an `if`, in an instance -- as the
// ops creating it, each of which goes.
func dropUnheardKeys(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		w, ok := n.(*ir.NodeInst)
		if !ok || !isWindowPrimitive(w) || !slices.ContainsFunc(w.Children, unheardKeys) {
			return nil
		}
		w.Children = slices.DeleteFunc(slices.Clone(w.Children), unheardKeys)
		return nil
	})
	created, heard := map[string]bool{}, map[string]bool{}
	_ = walkThroughCatch(pkg, func(n ir.Node) error {
		switch v := n.(type) {
		case *ir.Assign:
			if sel, ok := v.Target.(*ir.Select); ok && sel.Field == tagProp {
				if tag, ok := codegen.IRLiteralString(v.Value); ok && tag == keysTag && identName(sel.Operand) != "" {
					created[identName(sel.Operand)] = true
				}
			}
		case *ir.CallStmt:
			c := v.Call
			if c == nil || c.Func == nil || c.Func.Intrinsic != ir.NodeOpAttachHandler || len(c.Args) == 0 {
				return nil
			}
			for _, a := range c.Args[1:] {
				if l, ok := a.Value.(*ir.Lambda); ok && l.Func != nil && len(l.Func.Block) > 0 {
					heard[identName(c.Args[0].Value)] = true
				}
			}
		}
		return nil
	})
	for name := range heard {
		delete(created, name)
	}
	if len(created) == 0 {
		return
	}
	ir.RewriteBlocks(pkg, func(block []ir.Stmt) []ir.Stmt {
		if !slices.ContainsFunc(block, func(s ir.Stmt) bool { return opOnUnheard(s, created) }) {
			return block
		}
		return slices.DeleteFunc(slices.Clone(block), func(s ir.Stmt) bool { return opOnUnheard(s, created) })
	})
}

// opOnUnheard reports whether s creates one of the keysTag elements unheard
// names, writes one of its props, or is a node op naming one.
func opOnUnheard(s ir.Stmt, unheard map[string]bool) bool {
	switch v := s.(type) {
	case *ir.LocalVar:
		return unheard[v.Name]
	case *ir.Assign:
		if sel, ok := v.Target.(*ir.Select); ok {
			return unheard[identName(sel.Operand)]
		}
	case *ir.CallStmt:
		if v.Call == nil || v.Call.Func == nil || v.Call.Func.Intrinsic == "" {
			return false
		}
		for _, a := range v.Call.Args {
			if unheard[identName(a.Value)] {
				return true
			}
		}
	}
	return false
}

// unheardKeys reports whether s is a keysTag whose `key` handler does nothing.
func unheardKeys(s ir.Stmt) bool {
	n, ok := s.(*ir.NodeInst)
	if !ok {
		return false
	}
	if t, ok := rawElementTag(n); !ok || t != keysTag {
		return false
	}
	for _, h := range n.Handlers {
		if h.Name == "key" && h.Func != nil && len(h.Func.Block) > 0 {
			return false
		}
	}
	return true
}

// keyEvent is the event a test fires through a window's `#id` for a key.
const keyEvent = "key"

// emitWindowKeyInvokers attaches `<id>Key` to the state a test holds for each
// window a test fires `key` through: `c.main.key("7")` dispatches a keydown
// for "7" where the focus is, if it is in the window, and on the window
// itself where it is not, so the key goes through what the user's would --
// the field it is typed into, the button it presses, and the window's
// `sngl-keys`. The document's window is its body, and a dialog is found by
// the element it was written as or, built at run time, by its mark.
func (g *htmlGen) emitWindowKeyInvokers(b *strings.Builder, seen map[string]bool) {
	if g.triggered == nil {
		g.triggered = codegen.TriggeredEvents(g.pkg)
	}
	var ids []string
	for k := range g.triggered {
		if id, ok := strings.CutSuffix(k, "."+keyEvent); ok && isProgramID(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		name := id + capitalizeFirst(keyEvent)
		var root string
		switch {
		case g.irWindow != nil && g.irWindow.ID == id:
			root = "document.body"
		case g.dialogElems[id] != "":
			root = fmt.Sprintf("document.getElementById(%q)", g.dialogElems[id])
		case g.programWindows()[id]:
			root = fmt.Sprintf("document.querySelector('dialog[data-sngl-id=%q]')", id)
		default:
			continue
		}
		seen[name] = true
		fmt.Fprintf(b, "state.%s = (p) => { const w = %s; const a = document.activeElement; (a && w.contains(a) ? a : w).dispatchEvent(new KeyboardEvent(\"keydown\", { key: p.key, bubbles: true, cancelable: true })); };\n", name, root)
	}
}

// programWindows is the `#id` of every Window in the package and the document.
func (g *htmlGen) programWindows() map[string]bool {
	ids := map[string]bool{}
	_ = ir.Walk(g.pkg, func(n ir.Node) error {
		if w, ok := n.(*ir.NodeInst); ok && isWindowPrimitive(w) && w.ID != "" {
			ids[w.ID] = true
		}
		return nil
	})
	for _, s := range g.irBodyStmts {
		_ = ir.Walk(s, func(n ir.Node) error {
			if w, ok := n.(*ir.NodeInst); ok && isWindowPrimitive(w) && w.ID != "" {
				ids[w.ID] = true
			}
			return nil
		})
	}
	return ids
}

// dropUnpreventedPresses takes out each `mousedown` listener a script attaches
// that does nothing: what every control's `focusable` writes where it is true
// and the control is built at run time, where the markup's own listeners
// leave an empty handler unattached. One a test fires stays, as the markup's
// does.
func dropUnpreventedPresses(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	for k := range codegen.TriggeredEvents(pkg) {
		if strings.HasSuffix(k, "."+pressEvent) {
			return
		}
	}
	empty := func(s ir.Stmt) bool {
		v, ok := s.(*ir.CallStmt)
		if !ok || v.Call == nil || v.Call.Func == nil || v.Call.Func.Intrinsic != ir.NodeOpAttachHandler || len(v.Call.Args) < 3 {
			return false
		}
		event, ok := codegen.IRLiteralString(v.Call.Args[1].Value)
		l, isLambda := v.Call.Args[2].Value.(*ir.Lambda)
		return ok && event == pressEvent && isLambda && l.Func != nil && len(l.Func.Block) == 0
	}
	ir.RewriteBlocks(pkg, func(block []ir.Stmt) []ir.Stmt {
		if !slices.ContainsFunc(block, empty) {
			return block
		}
		return slices.DeleteFunc(slices.Clone(block), empty)
	})
}

// pressEvent is the DOM event a control's `focusable=false` prevents.
const pressEvent = "mousedown"
