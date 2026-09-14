package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `#id` binds the same *ir.Var every other node id binds.
//
// It used to bind the *ir.Window itself, which made ir.Window an ir.Symbol and
// left "what does a node id name" with two answers. One of them has to go
// before a window can be an ir.NodeInst, because a NodeInst is not a symbol --
// its id binds through NodeInst.Handle, which is what this now mirrors.
func TestWindowIDBindsANodeHandle(t *testing.T) {
	src := `
output(entry=home) {
    none {
        html
    }
}

window #home(title="Home", href="/index.html") {
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
	if len(pkg.Windows) != 1 {
		t.Fatalf("got %d windows; want 1", len(pkg.Windows))
	}
	w := pkg.Windows[0]

	if w.Handle == nil {
		t.Fatal("window #home declared no handle")
	}
	if !w.Handle.NodeHandle {
		t.Errorf("window handle %q is not marked NodeHandle", w.Handle.Name)
	}
	if w.Handle.Name != "home" {
		t.Errorf("handle name = %q; want home", w.Handle.Name)
	}
	if got := ir.WindowForHandle(pkg, w.Handle); got != w {
		t.Errorf("WindowForHandle did not find the window back from its handle")
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

	// The entry reference is the other half: it is resolved by handle, and
	// resolving it at all is what a program depends on.
	if pkg.EntryWindow != "home" {
		t.Errorf("EntryWindow = %q; want home", pkg.EntryWindow)
	}
}
