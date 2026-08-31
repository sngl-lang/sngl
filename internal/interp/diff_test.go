package interp

import (
	"strings"
	"testing"
)

// mountTwice mounts src, runs mutate against the live env, and mounts again --
// the shape a state change has: same program, new values.
func mountTwice(t *testing.T, src, comp string, mutate func(env *Env)) (*View, *View) {
	t.Helper()
	env, _ := envFor(t, src, comp)
	before, err := Mount(env)
	if err != nil {
		t.Fatalf("first Mount: %v", err)
	}
	if mutate != nil {
		mutate(env)
	}
	after, err := Mount(env)
	if err != nil {
		t.Fatalf("second Mount: %v", err)
	}
	return before, after
}

func setVar(env *Env, name string, v any) {
	for _, sym := range env.Comp.Vars {
		if sym.SymName() == name {
			env.Set(sym, v)
			return
		}
	}
}

func patchLines(patches []Patch) string {
	var b strings.Builder
	for _, p := range patches {
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	return b.String()
}

const diffSrc = `import . "sngl:ui"

component main {
    var (
        label = "before"
        shown = false
        items = ["a", "b"]
    )
    vbox {
        text #lbl(value=label)
        if shown {
            text #extra(value="now here")
        }
        for it = items {
            text(value=it)
        }
    }
}
`

// TestDiffOfAnUnchangedTreeIsEmpty: re-mounting without touching state must
// produce nothing, or every idle tick would repaint the window.
func TestDiffOfAnUnchangedTreeIsEmpty(t *testing.T) {
	before, after := mountTwice(t, diffSrc, "main", nil)
	if got := Diff(before, after); len(got) != 0 {
		t.Errorf("unchanged tree produced %d patches:\n%s", len(got), patchLines(got))
	}
}

// TestAChangedPropIsOneAssignment: the point of a retained tree. A value
// changing must not re-create the node holding it.
func TestAChangedPropIsOneAssignment(t *testing.T) {
	before, after := mountTwice(t, diffSrc, "main", func(env *Env) {
		setVar(env, "label", "after")
	})
	got := Diff(before, after)
	if len(got) != 1 {
		t.Fatalf("want 1 patch, got %d:\n%s", len(got), patchLines(got))
	}
	if got[0].Kind != PatchSetProp || got[0].Prop != "value" || got[0].Value != "after" {
		t.Errorf("got %s, want a setprop of value=after", got[0])
	}
	if got[0].Key.Path != "vbox@0/#lbl" {
		t.Errorf("patch addressed %s, want the #lbl node", got[0].Key)
	}
}

// TestAnIfTurningOnCreates: the branch that was not mounted has no key in the
// old tree, so it is a creation rather than an assignment.
func TestAnIfTurningOnCreates(t *testing.T) {
	before, after := mountTwice(t, diffSrc, "main", func(env *Env) {
		setVar(env, "shown", true)
	})
	got := Diff(before, after)
	if len(got) != 1 || got[0].Kind != PatchCreate {
		t.Fatalf("want one create, got:\n%s", patchLines(got))
	}
	if got[0].Node.ID != "extra" {
		t.Errorf("created %q, want #extra", got[0].Node.ID)
	}
	if got[0].Parent.Path != "vbox@0" {
		t.Errorf("created under %s, want vbox@0", got[0].Parent)
	}
}

// TestAnIfTurningOffRemoves is the reverse.
func TestAnIfTurningOffRemoves(t *testing.T) {
	env, _ := envFor(t, diffSrc, "main")
	setVar(env, "shown", true)
	before, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	setVar(env, "shown", false)
	after, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	got := Diff(before, after)
	if len(got) != 1 || got[0].Kind != PatchRemove {
		t.Fatalf("want one remove, got:\n%s", patchLines(got))
	}
}

// TestAShorterListRemovesTheTail: iterations are keyed by index, so dropping
// the last element removes the last node and leaves the rest alone.
func TestAShorterListRemovesTheTail(t *testing.T) {
	before, after := mountTwice(t, diffSrc, "main", func(env *Env) {
		setVar(env, "items", []any{"a"})
	})
	got := Diff(before, after)
	if len(got) != 1 || got[0].Kind != PatchRemove {
		t.Fatalf("want one remove, got:\n%s", patchLines(got))
	}
	if !strings.Contains(got[0].Key.Path, "[1]") {
		t.Errorf("removed %s, want the second iteration", got[0].Key)
	}
}

// TestReorderingAnUnkeyedListAssignsRatherThanMoves records a real limitation
// rather than a behaviour worth having. Iterations are addressed by index, so
// swapping two elements looks like both of them changing value. A node's `key=`
// is what would fix this, and the mounter does not read it yet.
func TestReorderingAnUnkeyedListAssignsRatherThanMoves(t *testing.T) {
	before, after := mountTwice(t, diffSrc, "main", func(env *Env) {
		setVar(env, "items", []any{"b", "a"})
	})
	got := Diff(before, after)
	if len(got) != 2 {
		t.Fatalf("want 2 patches, got %d:\n%s", len(got), patchLines(got))
	}
	for _, p := range got {
		if p.Kind != PatchSetProp {
			t.Errorf("got %s; a reorder is currently seen as assignment, not movement", p)
		}
	}
}

// TestAParentAndItsChildAppearParentFirst: a host must be able to apply the
// list in order without buffering.
func TestAParentAndItsChildAppearParentFirst(t *testing.T) {
	src := `import . "sngl:ui"

component main {
    var shown = false
    vbox {
        if shown {
            hbox {
                text(value="inner")
            }
        }
    }
}
`
	before, after := mountTwice(t, src, "main", func(env *Env) {
		setVar(env, "shown", true)
	})
	got := Diff(before, after)
	if len(got) != 2 {
		t.Fatalf("want 2 creates, got %d:\n%s", len(got), patchLines(got))
	}
	if got[0].Node.Name != "hbox" || got[1].Node.Name != "text" {
		t.Errorf("order is %s then %s; want the parent first", got[0].Node.Name, got[1].Node.Name)
	}
	if got[1].Parent != got[0].Key {
		t.Errorf("child's parent is %s, want the hbox at %s", got[1].Parent, got[0].Key)
	}
}

const reloadA = `import . "sngl:ui"

component main {
    var n = 0
    vbox {
        text #lbl(value="one")
        button #go(text="go", @click { n += 1 })
    }
}
`

// TestARecheckOfIdenticalSourceProducesNoPatches is what Key is for. The two
// trees share no IR at all -- every *ir.NodeInst, every ir.Symbol is new -- so
// under pointer identity this would be a full teardown and rebuild.
func TestARecheckOfIdenticalSourceProducesNoPatches(t *testing.T) {
	envA, _ := envFor(t, reloadA, "main")
	before, err := Mount(envA)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	envB, _ := envFor(t, reloadA, "main")
	after, err := Mount(envB)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Guard against the test passing for the wrong reason.
	if before.Roots[0] == after.Roots[0] {
		t.Fatal("both mounts returned the same *Node; the trees are not independent")
	}
	if got := Diff(before, after); len(got) != 0 {
		t.Errorf("recompiling identical source produced %d patches:\n%s", len(got), patchLines(got))
	}
}

// TestAnEditedLiteralAcrossAReloadIsOneAssignment: the edit a hot reload is
// for. Changing one string must patch one prop, not rebuild the window.
func TestAnEditedLiteralAcrossAReloadIsOneAssignment(t *testing.T) {
	edited := `import . "sngl:ui"

component main {
    var n = 0
    vbox {
        text #lbl(value="two")
        button #go(text="go", @click { n += 1 })
    }
}
`
	envA, _ := envFor(t, reloadA, "main")
	before, _ := Mount(envA)
	envB, _ := envFor(t, edited, "main")
	after, _ := Mount(envB)

	got := Diff(before, after)
	if len(got) != 1 {
		t.Fatalf("want 1 patch, got %d:\n%s", len(got), patchLines(got))
	}
	if got[0].Kind != PatchSetProp || got[0].Value != "two" {
		t.Errorf("got %s, want value=two assigned", got[0])
	}
}

// TestChangingANodesElementReplacesIt: no host turns a button into a text
// field by assignment, so a changed element at one key is a remove and a
// create rather than a prop patch.
func TestChangingANodesElementReplacesIt(t *testing.T) {
	edited := `import . "sngl:ui"

component main {
    var n = 0
    vbox {
        text #lbl(value="one")
        text #go(value="go")
    }
}
`
	envA, _ := envFor(t, reloadA, "main")
	before, _ := Mount(envA)
	envB, _ := envFor(t, edited, "main")
	after, _ := Mount(envB)

	got := Diff(before, after)
	var removed, created bool
	for _, p := range got {
		if p.Key.Path != "vbox@0/#go" {
			t.Errorf("unexpected patch %s", p)
		}
		switch p.Kind {
		case PatchRemove:
			removed = true
		case PatchCreate:
			created = true
			if p.Node.Name != "text" {
				t.Errorf("created a %q, want text", p.Node.Name)
			}
		}
	}
	if !removed || !created {
		t.Errorf("want the button removed and a text created, got:\n%s", patchLines(got))
	}
	// And the remove must come first, or the host briefly holds two nodes at
	// one key.
	if len(got) >= 2 && got[0].Kind != PatchRemove {
		t.Errorf("first patch is %s; a replacement removes before it creates", got[0])
	}
}

// TestAddingAHandlerRebinds: handler bodies are IR that every reload replaces,
// so they are never compared by value -- only the set of names is.
func TestAddingAHandlerRebinds(t *testing.T) {
	edited := `import . "sngl:ui"

component main {
    var n = 0
    vbox {
        text #lbl(value="one", @click { n += 1 })
        button #go(text="go", @click { n += 1 })
    }
}
`
	envA, _ := envFor(t, reloadA, "main")
	before, _ := Mount(envA)
	envB, _ := envFor(t, edited, "main")
	after, _ := Mount(envB)

	got := Diff(before, after)
	if len(got) != 1 || got[0].Kind != PatchRebind {
		t.Fatalf("want one rebind, got:\n%s", patchLines(got))
	}
	if got[0].Key.Path != "vbox@0/#lbl" {
		t.Errorf("rebound %s, want #lbl", got[0].Key)
	}
}

// TestAUserStructPropSurvivesAReload is why values are compared by rendered
// form rather than by == or DeepEqual.
//
// A *Struct carries the declaration and type it was checked against. A stdlib
// one like Style is memoized across checks and keeps its pointer, but a
// program's own struct does not: reloading replaces the StructDef, so pointer
// comparison would report every user-struct prop as changed on every save.
func TestAUserStructPropSurvivesAReload(t *testing.T) {
	src := `import . "sngl:ui"

struct Point {
    x int = 0
    y int = 0
}

component dot(at Point) {
    text(value="{at.x}")
}

component main {
    dot #d(at=Point{x=1, y=2})
}
`
	envA, _ := envFor(t, src, "main")
	before, err := Mount(envA)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	envB, _ := envFor(t, src, "main")
	after, err := Mount(envB)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	nodesA, nodesB := before.FindAny("d"), after.FindAny("d")
	if len(nodesA) != 1 || len(nodesB) != 1 {
		t.Fatalf("#d resolved to %d and %d nodes, want 1 each", len(nodesA), len(nodesB))
	}
	atA, _ := nodesA[0].Props["at"].(*Struct)
	atB, _ := nodesB[0].Props["at"].(*Struct)
	if atA == nil || atB == nil {
		t.Fatalf("the `at` prop is %T, not a *Struct; this test is checking nothing", nodesA[0].Props["at"])
	}
	// The guard: without this the test could pass because nothing was
	// replaced, which is the opposite of the case being covered.
	if atA.Def == atB.Def {
		t.Fatal("both mounts share the Point StructDef; the reload is not independent")
	}

	if got := Diff(before, after); len(got) != 0 {
		t.Errorf("an unchanged user-struct prop produced %d patches:\n%s", len(got), patchLines(got))
	}
}
