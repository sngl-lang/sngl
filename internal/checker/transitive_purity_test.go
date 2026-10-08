package checker

import (
	"testing"

	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// findCompFunc returns the named func from the named component.
func findCompFunc(t *testing.T, pkg *ir.Package, comp, fn string) *ir.Func {
	t.Helper()
	for _, c := range pkg.Components {
		if c.Name != comp {
			continue
		}
		for _, f := range c.Funcs {
			if f.Name == fn {
				return f
			}
		}
	}
	t.Fatalf("func %s.%s not found", comp, fn)
	return nil
}

func checkMainOK(t *testing.T, src string) *ir.Package {
	t.Helper()
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
	return pkg
}

// A function that only *calls* an impure function performs no direct mutation,
// so per-function analysis marks it PurityPure. That is unsound: the optimizer
// const-folds / inlines pure funcs and would discard the transitive side
// effect. Purity must propagate over the call graph.
func TestTransitivePurityWrapperOfImpure(t *testing.T) {
	pkg := checkMainOK(t, `
component main node {
    var count = 0
    func bump() { count = count + 1 }
    func wrap() { bump() }
    func wrap2() { wrap() }
    button(text="x", @click { wrap2() })
    text(value="{count}")
}
`)
	for _, name := range []string{"bump", "wrap", "wrap2"} {
		if fn := findCompFunc(t, pkg, "main", name); fn.Purity == ir.PurityPure {
			t.Errorf("%s transitively mutates state but was typed PurityPure (const-foldable)", name)
		}
	}
}

// Scope awareness: a local var that shadows a package/component var, written
// inside a function, is an internal mutation only — the function stays pure.
// The old name-keyed walker matched the shadowing local against the package
// var by name and wrongly marked the function PurityMutates.
func TestPurityLocalShadowingVarStaysPure(t *testing.T) {
	pkg := checkMainOK(t, `
component main node {
    var count = 0
    func compute() int {
        var count = 5
        count = count + 1
        return count
    }
    text(value="{compute()}")
}
`)
	fn := findCompFunc(t, pkg, "main", "compute")
	if fn.Purity != ir.PurityPure {
		t.Errorf("compute only writes a shadowing local; purity = %d, want PurityPure", fn.Purity)
	}
	if len(fn.Writes) != 0 {
		t.Errorf("compute writes only a local; Writes = %v, want empty", fn.Writes)
	}
}

// Transitive purity must see impure calls nested inside container statements,
// not just top-level call statements. Regression: highestCalledPurity's walker
// had no case for the container it was written with, so the call was never
// seen and the wrapper stayed PurityPure (const-foldable).
func TestTransitivePurityThroughIfBranch(t *testing.T) {
	pkg := checkMainOK(t, `
component main node {
    var count = 0
    func bump() { count = count + 1 }
    func gated() { if count >= 0 { bump() } }
    button(text="x", @click { gated() })
    text(value="{count}")
}
`)
	if fn := findCompFunc(t, pkg, "main", "gated"); fn.Purity == ir.PurityPure {
		t.Errorf("gated calls an impure func inside an if body but was typed PurityPure")
	}
}

// Control: a wrapper that only calls genuinely pure functions must stay pure,
// so propagation does not over-mark.
func TestTransitivePurityWrapperOfPureStaysPure(t *testing.T) {
	pkg := checkMainOK(t, `
component main node {
    func square(n int) => n * n
    func viaWrap(n int) => square(n)
    text(value="{viaWrap(3)}")
}
`)
	if fn := findCompFunc(t, pkg, "main", "viaWrap"); fn.Purity != ir.PurityPure {
		t.Errorf("viaWrap only calls a pure func; purity = %d, want PurityPure", fn.Purity)
	}
}
