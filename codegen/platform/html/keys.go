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
// elements away, takes its listener with them. Each key pressed in the window
// is dispatched as `key`, which html.element declares as an event.
const keysTag = "sngl-keys"

// keysHelper defines keysTag. It listens in the capture phase, so the window
// hears a key before the element with the focus does: before a field types
// it, a button's Enter or space presses it, or a Tab moves the focus. A
// `key` the window's handler prevented -- its `stop` -- is a keydown
// prevented and stopped, which is all of those, and the keyup after it is
// too, since a button pressed by the space bar is clicked as the key comes
// up. A key pressed in a `<dialog>` is that dialog's window's, and the
// document's does not hear it: the dialogs are shown without a backdrop, as
// desktop windows, so the window with the focus is the one that hears the
// keyboard, and none of them is modal. A key composing text in an input
// method passes untouched, as do a modifier alone and a key lib/ui has no name
// for. With Control, Alt or Meta held the key is its own character, which is
// what KeyboardEvent.key says but where Alt on a Mac types another: there the
// key's position names it, `KeyS` "s". `report` is what a test's
// `c.<id>.key(press, stop)` calls, handing the press it wrote, and `pass`
// presses on one the window did not stop, as a keydown and a keyup the
// window's element lets by.
const keysHelper = `if (!customElements.get("sngl-keys")) customElements.define("sngl-keys", class extends HTMLElement {
  static named = new Set(["Enter", "Tab", "Escape", "Backspace", "Delete", "Insert", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "PageUp", "PageDown", ...Array.from({ length: 12 }, (_, i) => "F" + (i + 1))]);
  static legacy = { Esc: "Escape", Spacebar: " ", Left: "ArrowLeft", Right: "ArrowRight", Up: "ArrowUp", Down: "ArrowDown", Del: "Delete" };
  static untyped = ["button", "checkbox", "radio", "range", "color", "file", "submit", "reset", "image", "hidden"];
  static press(e) {
    let key = this.legacy[e.key] || e.key;
    if ((e.ctrlKey || e.altKey || e.metaKey) && /^(Key[A-Z]|Digit[0-9])$/.test(e.code) && !/^[ -~]$/.test(key)) {
      key = e.shiftKey ? e.code.slice(-1) : e.code.slice(-1).toLowerCase();
    }
    if ([...key].length !== 1 && !this.named.has(key)) return null;
    const t = e.target instanceof Element ? e.target : null;
    const editing = !!t && (t.isContentEditable || ((t instanceof HTMLTextAreaElement || (t instanceof HTMLInputElement && !this.untyped.includes(t.type))) && !t.readOnly));
    return { key, ctrl: e.ctrlKey, alt: e.altKey, shift: e.shiftKey, meta: e.metaKey, editing };
  }
  connectedCallback() {
    this.style.display = "none";
    const dialog = this.closest("dialog");
    const held = new Set();
    const swallow = (e) => { e.preventDefault(); e.stopImmediatePropagation(); };
    this._on = dialog || document;
    this._down = (e) => {
      if (e.isComposing || e.keyCode === 229 || this.constructor.passing.has(e)) return;
      if (e.target instanceof Element && e.target.closest("dialog") !== dialog) return;
      const press = this.constructor.press(e);
      if (press && !this.report(press)) {
        swallow(e);
        held.add(e.code || e.key);
      }
    };
    this._up = (e) => {
      if (held.delete(e.code || e.key)) swallow(e);
    };
    this._on.addEventListener("keydown", this._down, true);
    this._on.addEventListener("keyup", this._up, true);
  }
  disconnectedCallback() {
    this._on.removeEventListener("keydown", this._down, true);
    this._on.removeEventListener("keyup", this._up, true);
  }
  static passing = new WeakSet();
  static pass(target, press) {
    for (const type of ["keydown", "keyup"]) {
      const e = new KeyboardEvent(type, { key: press.key, ctrlKey: press.ctrl, altKey: press.alt, shiftKey: press.shift, metaKey: press.meta, bubbles: true, cancelable: true });
      this.passing.add(e);
      target.dispatchEvent(e);
    }
  }
  report(press) {
    const e = new Event("key", { cancelable: true });
    for (const f of ["key", "ctrl", "alt", "shift", "meta", "editing"]) e[f] = press[f];
    return this.dispatchEvent(e);
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
// window a test fires `key` through: `c.main.key(press, stop)` hands the
// press, as the test wrote it, to the `report` of the window's `sngl-keys`,
// which dispatches it as it would a keydown it heard, and calls `stop` where
// the window's handler stopped it. A key it did not stop is pressed where the
// focus is, if it is in the window, and on the window itself where it is not,
// as a keydown and a keyup the window's own element lets by, so it goes on
// to what the user's would without the window hearing it twice. The
// document's window is its body, and a dialog is found by the element it was
// written as or, built at run time, by its mark; the window's own element is
// the one there in no other dialog.
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
		fmt.Fprintf(b, "state.%s = (p) => { const w = %s; const d = w.tagName === \"DIALOG\" ? w : null; const k = [...w.querySelectorAll(%q)].find((k) => k.closest(\"dialog\") === d); if (k && !k.report(p.e)) { p.stop(); return; } const a = document.activeElement; customElements.get(%q)?.pass(a && w.contains(a) ? a : w, p.e); };\n", name, root, keysTag, keysTag)
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
