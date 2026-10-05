package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `#id` binds the same *ir.Var every other node id binds, and the
// window is an ordinary node of the package body: what its id names is its
// handle and nothing else.
func TestWindowIDBindsANodeHandle(t *testing.T) {
	src := `
window #home(title="Home") {
    text(value="{home.title}")
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Msg)
		}
	}
	if len(pkg.Body) != 1 {
		t.Fatalf("got %d root statements; want the window", len(pkg.Body))
	}
	w, ok := pkg.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("root statement is %T; want the window node", pkg.Body[0])
	}

	if w.Handle == nil {
		t.Fatal("window #home declared no handle")
	}
	if !w.Handle.NodeHandle {
		t.Errorf("window handle %q is not marked NodeHandle", w.Handle.Name)
	}
	if w.Handle.Name != "home" {
		t.Errorf("handle name = %q; want home", w.Handle.Name)
	}

	// The read in the body resolves to the handle and to nothing else: a
	// window reached through a second kind of symbol is the split being closed.
	var reads int
	_ = ir.Walk(pkg, func(n ir.Node) error {
		id, ok := n.(*ir.Ident)
		if !ok || id.Name != "home" {
			return nil
		}
		reads++
		if id.Sym != w.Handle {
			t.Errorf("`home` resolved to %T, not to the window's handle", id.Sym)
		}
		return nil
	})
	if reads == 0 {
		t.Error("no reference to `home` was found; the fixture no longer tests the read")
	}
}

// Inside a `for`, the id names the one window this iteration renders, and the
// body's `page.title` reads that window's handle.
func TestLoopWindowIDNamesItsIteration(t *testing.T) {
	src := `
const items list<string> = ["a", "b"]

for var it = items {
    window #page(title=it) {
        text(value=page.title)
    }
}
`
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected diag: %s", d.Msg)
		}
	}

	var found int
	_ = ir.Walk(pkg, func(n ir.Node) error {
		id, ok := n.(*ir.Ident)
		if !ok || id.Name != "page" {
			return nil
		}
		found++
		v, isVar := id.Sym.(*ir.Var)
		switch {
		case !isVar:
			t.Errorf("`page` in the window body resolved to %T", id.Sym)
		case !v.NodeHandle:
			t.Error("`page` did not resolve to this iteration's handle")
		}
		return nil
	})
	if found == 0 {
		t.Error("no reference to `page` was found; the fixture no longer tests the read")
	}
}
