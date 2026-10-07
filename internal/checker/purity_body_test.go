package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A bodyless library function says nothing about what its host does, so its
// purity is what the declaration says and the fixpoint leaves it there. Read as
// a body, the empty block lifted error.raise from unknown to pure, and the
// optimizer folded calls through it.
func TestBodylessLibFuncKeepsUnknownPurity(t *testing.T) {
	pkg := LibPackage("builtin")
	for _, fn := range pkg.Funcs {
		if fn.Receiver == "error" && fn.Name == "raise" {
			if fn.Purity != ir.PurityUnknown {
				t.Fatalf("error.raise purity = %v, want Unknown", fn.Purity)
			}
			return
		}
	}
	t.Fatal("error.raise not found in sngl:builtin")
}

// A write into a value the function built itself is no effect; a write that may
// reach a value someone else holds still is.
func TestLocalFieldWritePurity(t *testing.T) {
	src := `
struct P {
    x int
}

struct Outer {
    inner P
}

func built(a int) P {
    var r = P{x=0}
    r.x = a
    return r
}

func element(a int) list<int> {
    var r = [0, 0]
    r[0] = a
    return r
}

func aliased(xs list<int>) list<int> {
    var r = xs
    r[0] = 1
    return r
}

func reassigned(p P) P {
    var r = P{x=0}
    r = p
    r.x = 1
    return r
}

func nested(p P) Outer {
    var r = Outer{inner=p}
    r.inner.x = 1
    return r
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
	want := map[string]bool{
		"built":      true,
		"element":    true,
		"aliased":    false,
		"reassigned": false,
		"nested":     false,
	}
	for _, fn := range pkg.Funcs {
		pure, ok := want[fn.Name]
		if !ok {
			continue
		}
		delete(want, fn.Name)
		if got := fn.Purity == ir.PurityPure; got != pure {
			t.Errorf("%s: purity %v, want pure=%v", fn.Name, fn.Purity, pure)
		}
	}
	for name := range want {
		t.Errorf("%s not found", name)
	}
}
