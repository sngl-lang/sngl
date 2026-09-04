package lower

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// constructInstanceSrc is one component under a dynamic `for` -- the shape that
// cannot be inlined -- with a #[construct] prop its own `var` reads.
const constructInstanceSrc = `
import . "sngl:ui"
import . "sngl:macro"

component card(#[construct] seed int, name = "") {
    var clicks = seed
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

component main {
    var (
        names list<string> = ["a", "b"]
        base = 0
    )
    for var nm = names {
        card(seed=base, name=nm)
    }
}
`

func checkForLower(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}

// constructInstancePkg checks constructInstanceSrc and stamps the mark the
// inliner would have left on `card` in a real build: these tests drive
// lowerComponentProps directly, and that pass only gives cells to a
// declaration something instantiates at run time.
func constructInstancePkg(t *testing.T) *ir.Package {
	t.Helper()
	pkg := checkForLower(t, constructInstanceSrc)
	cardComponent(t, pkg).RuntimeInstance = true
	return pkg
}

func cardComponent(t *testing.T, pkg *ir.Package) *ir.Component {
	t.Helper()
	for _, c := range pkg.Components {
		if c != nil && c.Name == "card" {
			return c
		}
	}
	t.Fatal("no card component")
	return nil
}

// A #[construct] prop gets no setter, which is what makes componentAbsorbs
// report it as unwritable. An ordinary prop beside it still gets one, so the
// mark is what decides and not the pass giving up on the component.
func TestConstructPropGetsNoSetter(t *testing.T) {
	pkg := constructInstancePkg(t)
	if err := lowerComponentProps(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatalf("lower props: %v", err)
	}
	card := cardComponent(t, pkg)
	if componentAbsorbs(card, "seed") {
		t.Error("a construct prop should carry no setter")
	}
	if !componentAbsorbs(card, "name") {
		t.Error("an ordinary prop beside it should still carry one")
	}
}

// A prop that can neither be absorbed nor rebuilt for is reported rather than
// dropped.
//
// Nothing in the language reaches this today -- every unmarked prop is
// promoted, and every marked one rebuilds -- so the state is staged here: the
// mark is honoured while the setters are built and then cleared, which is
// exactly a prop with no setter and no mark. Before this guard that program
// generated cleanly and ignored the write, which is how the class stayed
// invisible.
func TestUnwritableUnmarkedPropIsReported(t *testing.T) {
	pkg := constructInstancePkg(t)
	if err := lowerComponentProps(pkg, Caps{NoReactivity: true}, Options{}); err != nil {
		t.Fatalf("lower props: %v", err)
	}
	for _, p := range cardComponent(t, pkg).Props {
		p.Construct = false
	}
	err := lowerReactivity(pkg, Caps{NoReactivity: true}, Options{})
	if err == nil {
		t.Fatal("want a diagnostic for a prop that can be neither written nor rebuilt")
	}
	if !strings.Contains(err.Error(), `prop "seed" of component card`) {
		t.Errorf("the report should name the prop and its component; got %v", err)
	}
}

// constructStaticSrc puts a #[construct] prop, written from state, at a
// position no reactive slot governs. The component is in a recursive cycle, so
// the inliner keeps the instantiation and the record is real -- and the
// position is a plain statement of main's body, which nothing re-renders.
const constructStaticSrc = `
import . "sngl:ui"
import . "sngl:macro"

component seeded(#[construct] start int, tail = "") {
    var n = start
    text(value="{tail}:{n}")
    if n > 3 {
        seeded(start=start, tail=tail)
    }
}

component main {
    var k = 0
    button(text="bump", @click { k = k + 1 })
    seeded(start=k, tail="solo")
}
`

// A prop a static instance cannot absorb is reported, not emitted.
//
// The reactive-slot path has asked this since #[construct] existed: a prop
// with no setter either rebuilds the instance or is a routing nothing answers.
// A static position never asked, so every prop of a static instance became an
// UpdateComponent -- and a #[construct] prop has no setter to call. fyne
// emitted `m.__n1.SetStart(m.k)` against a record declaring only SetTail and
// did not compile; html emitted `__n1.__set_start(...)` on an object exporting
// only __set_tail and threw on the click.
func TestConstructPropAtAStaticPositionIsReported(t *testing.T) {
	pkg := checkForLower(t, constructStaticSrc)
	// Both, as every instance-runtime platform declares them: the mark that
	// makes a component an instance is the inliner's, and passComponentProps
	// gives cells only to what carries it.
	err := Lower(pkg, Caps{NoReactivity: true, NoInlineComponents: true}, Options{})
	if err == nil {
		t.Fatal("want a diagnostic for a construct prop nothing at this position can rebuild")
	}
	for _, want := range []string{`prop "start" of component seeded`, "rebuilds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report should mention %q; got %v", want, err)
		}
	}
}
