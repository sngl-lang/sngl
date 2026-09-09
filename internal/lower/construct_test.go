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

component card(#[construct] seed int, name = "") ui {
    var clicks = seed
    button(text="{name} ({clicks})", @click { clicks += 1 })
}

component main ui {
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

// lowerDump is the lowered package read back as source, which is how a
// statement sequence is asserted here: the shape is the claim, and a walk of
// the IR asserting node by node says less about it than the text does.
func lowerDump(t *testing.T, pkg *ir.Package) string {
	t.Helper()
	return parser.Format(ir.Convert(pkg))
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

component seeded(#[construct] start int, tail = "") ui {
    var n = start
    text(value="{tail}:{n}")
    if n > 3 {
        seeded(start=start, tail=tail)
    }
}

component main ui {
    var k = 0
    button(text="bump", @click { k = k + 1 })
    seeded(start=k, tail="solo")
}
`

// staticConstructCaps are the caps every platform that builds instances
// declares: the mark that makes a component an instance is the inliner's, and
// passComponentProps gives cells only to what carries it.
var staticConstructCaps = Caps{NoReactivity: true, NoInlineComponents: true}

// A #[construct] prop written from state rebuilds the instance where it
// stands, on a platform that can put a child back at a position.
//
// The reactive-slot path has answered this since #[construct] existed: an
// instance that cannot be handed the new value is destroyed and one built from
// it takes its place. A static position has no render to rebuild from, so the
// rebuild is the update's own business -- and putting the new root back among
// the old one's siblings is InsertBefore, which is why the answer is available
// only where that is.
func TestConstructPropAtAStaticPositionRebuildsTheInstance(t *testing.T) {
	pkg := checkForLower(t, constructStaticSrc)
	caps := staticConstructCaps
	caps.InsertBefore = true
	if err := Lower(pkg, caps, Options{Platform: "html"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	// The click handler is what the rebuild was spliced into.
	body := lowerDump(t, pkg)
	for _, want := range []string{
		"lower.CreateComponent(seeded",
		"lower.InsertBefore(__n1__pos",
		"lower.RemoveChild(__n1__pos",
		"lower.DestroyComponent(__n1)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the rebuild should emit %q; got:\n%s", want, body)
		}
	}
	// The one thing that must NOT be there: a setter call on this instance,
	// whose absence is the whole reason the prop is marked. The recursive
	// child inside the component's own slot still gets its ordinary props
	// written, which is a different instance and a different question.
	if strings.Contains(body, "UpdateComponent(__n1") {
		t.Errorf("a construct prop has no setter to call; got:\n%s", body)
	}
}

// The same prop is a positioned refusal where the platform cannot place a
// child, because the operation the rebuild is made of does not exist there.
//
// Reported rather than compiled into a program that drops the write: before
// there was any check, fyne emitted `m.__n1.SetStart(m.k)` against a record
// declaring only SetTail and did not compile, and html emitted
// `__n1.__set_start(...)` on an object exporting only __set_tail and threw on
// the click.
func TestConstructPropAtAStaticPositionNeedsInsertBefore(t *testing.T) {
	pkg := checkForLower(t, constructStaticSrc)
	err := Lower(pkg, staticConstructCaps, Options{Platform: "fyne"})
	if err == nil {
		t.Fatal("want a diagnostic for a rebuild the platform cannot place")
	}
	for _, want := range []string{`prop "start" of component seeded`, "fyne", "position"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report should mention %q; got %v", want, err)
		}
	}
}
