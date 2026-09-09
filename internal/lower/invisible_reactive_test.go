package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// lowerOne checks src and runs the inliner over it, answering the package.
func lowerOne(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, errs := checker.Check(doc, &checker.Config{IsMain: true})
	if len(errs) > 0 {
		t.Fatalf("check: %v", errs)
	}
	if err := lowerInlineComponents(pkg, Caps{NoInlineComponents: true}, Options{Platform: "html"}); err != nil {
		t.Fatalf("inline: %v", err)
	}
	return pkg
}

func componentNamed(pkg *ir.Package, name string) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// A component holding state and rendering nothing is inlined where it stands
// even inside a reactive `if`, because there is no node at that position for a
// reconcile to patch and the branch is already the effect's lifetime.
//
// Elected a runtime instance instead, the reactive `if` demands a setter for
// every prop it would push and passNoReactivity stops the build -- which is
// what a `timer` written under a branch did once html began implementing it as
// an effect over setInterval rather than as a primitive of its own.
func TestAnInvisibleComponentUnderAReactiveIfIsInlined(t *testing.T) {
	pkg := lowerOne(t, `
component held(period int) ui {
    var handle = 0

    effect(
        on = period,
        @mount(p) { handle = p },
        @unmount(p) { handle = 0 },
    )
}

component main ui {
    var shown = false

    button(text="toggle", @click { shown = !shown })
    if shown {
        held(period=20)
    }
}
`)
	if held := componentNamed(pkg, "held"); held != nil && held.RuntimeInstance {
		t.Errorf("held was elected a runtime instance; it renders nothing")
	}
	main := componentNamed(pkg, "main")
	if main == nil {
		t.Fatal("no main")
	}
	if instancesOf(main.Body, "held") != 0 {
		t.Errorf("the held node survived the inliner: %#v", main.Body)
	}
	// And its state came along, renamed per instance, rather than being
	// dropped with the declaration.
	if len(main.Vars) < 2 {
		t.Errorf("held's var was not hoisted onto main; main has %v", varSliceNames(main.Vars))
	}
}

// Under a `for` the exemption does not apply: the body is spliced once and the
// hoisted var would be shared by every copy of it, so the instance election
// stands.
func TestAnInvisibleComponentUnderAReactiveForStaysAnInstance(t *testing.T) {
	pkg := lowerOne(t, `
component held(period int) ui {
    var handle = 0

    effect(
        on = period,
        @mount(p) { handle = p },
        @unmount(p) { handle = 0 },
    )
}

component main ui {
    var periods = [10, 20]

    button(text="add", @click { periods.push(30) })
    for var p = periods {
        held(period=p)
    }
}
`)
	held := componentNamed(pkg, "held")
	if held == nil {
		t.Fatal("held was dropped")
	}
	if !held.RuntimeInstance {
		t.Errorf("held was inlined into a reactive loop; each copy needs its own state")
	}
}

// The control: a component that renders something is elected an instance under
// a reactive `if` exactly as before. Without it the test above would pass for a
// rule far wider than the one written.
func TestAVisibleComponentUnderAReactiveIfStaysAnInstance(t *testing.T) {
	pkg := lowerOne(t, `
import . "sngl:ui"

component held(label string) ui {
    var n = 0

    text(value=label + string(n))
}

component main ui {
    var shown = false

    button(text="toggle", @click { shown = !shown })
    if shown {
        held(label="x")
    }
}
`)
	held := componentNamed(pkg, "held")
	if held == nil {
		t.Fatal("held was dropped")
	}
	if !held.RuntimeInstance {
		t.Errorf("a component with a node in it was inlined into a reactive branch")
	}
}

// instancesOf counts the nodes naming a component, at any depth.
func instancesOf(stmts []ir.Stmt, name string) int {
	n := 0
	_ = ir.Walk(stmts, func(node ir.Node) error {
		if inst, ok := node.(*ir.NodeInst); ok && inst.Component != nil && inst.Component.Name == name {
			n++
		}
		return nil
	})
	return n
}
