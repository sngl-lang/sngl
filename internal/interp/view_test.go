package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestMountAgreesWithThePreTreeWalk is the evidence that the retained tree
// replaced the query walk rather than changing what it answered.
//
// The oracle in view_oracle_test.go is that walk, frozen. ResolveElementRef
// now reads the tree, so comparing against it would compare the tree with
// itself.
//
// For every fixture in testdata that checks cleanly with no platform
// registered, every component is mounted and every #id in the package is looked
// up both ways. The results must match in count, element name and every
// evaluated prop.
//
// Fixtures that need a platform, or that assert a check error, do not check
// here and are skipped -- so this is a lower bound on agreement, not a proof
// over the whole corpus. The count assertion below is what keeps the skip list
// from quietly swallowing everything.
func TestMountAgreesWithThePreTreeWalk(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures found")
	}

	compared, withIDs, swept, supplied := 0, 0, 0, 0
	for _, file := range files {
		pkg := checkFixture(t, file)
		if pkg == nil {
			continue
		}
		swept++
		ids := allIDs(pkg)
		for _, comp := range pkg.Components {
			env, err := BuildEnv(pkg, comp.Name)
			if err != nil {
				continue
			}
			view, err := Mount(env)
			if err != nil {
				t.Errorf("%s: Mount(%s): %v", filepath.Base(file), comp.Name, err)
				continue
			}
			for _, id := range ids {
				old, oerr := oracleResolve(env, id)
				if oerr != nil {
					continue
				}
				oldMaps := normalizeRefs(old)
				// The tree finds strictly more than the walk did: content a
				// call site supplies to a user component is now mounted, and
				// the walk never reached it. That is the one intentional
				// divergence, so it is subtracted here rather than the
				// comparison being loosened -- everything else must still
				// match exactly.
				visible, fromSlot := splitSupplied(view.Find(id))
				supplied += fromSlot
				newMaps := nodeMaps(visible)
				compared++
				if len(oldMaps) > 0 {
					withIDs++
				}
				if diff := diffRefs(oldMaps, newMaps); diff != "" {
					t.Errorf("%s: %s #%s: %s", filepath.Base(file), comp.Name, id, diff)
				}
			}
		}
	}

	// Floors, not targets: what they guard against is the sweep going vacuous
	// because fixtures stopped checking here. Set just under what the corpus
	// currently yields, so adding or removing a fixture does not trip them.
	if swept < 60 {
		t.Errorf("only %d of %d fixtures checked cleanly; the sweep has collapsed", swept, len(files))
	}
	if compared < 80 {
		t.Errorf("only %d lookups compared; the sweep is not exercising the tree", compared)
	}
	if withIDs < 50 {
		t.Errorf("only %d lookups resolved to a node; ids are not being found", withIDs)
	}
	// The subtraction above must be doing something, or it is a silent escape
	// hatch that would hide a real divergence.
	if supplied == 0 {
		t.Error("no slot-supplied nodes were found; the exclusion is unexercised, so it hides rather than documents")
	}
	t.Logf("%d/%d fixtures swept, %d lookups compared, %d resolved to at least one node, %d found only by the tree (slot-supplied)",
		swept, len(files), compared, withIDs, supplied)
}

// TestMountKeysAreUniqueAndStable: a mounted path is identity, so two nodes
// sharing one would make the reconciler overwrite state. Mounting the same env
// twice must also produce the same keys.
func TestMountKeysAreUniqueAndStable(t *testing.T) {
	src := `import . "sngl:ui"

component row(label string) node {
    text(value=label)
    text(value="fixed")
}

component main node {
    var items = ["a", "b", "c"]
    vbox {
        row(label="one")
        row(label="two")
        for var it = items {
            text #each(value=it)
        }
    }
}
`
	env, _ := envFor(t, src, "main")
	first, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if first.Len() == 0 {
		t.Fatal("mounted nothing")
	}
	// Uniqueness is byKey's business: a collision would silently drop a node,
	// so the index size and the walked count must agree.
	walked := 0
	first.Walk(func(*Node) bool { walked++; return true })
	if walked != first.Len() {
		t.Errorf("walked %d nodes but the key index holds %d; keys collide", walked, first.Len())
	}

	env2, _ := envFor(t, src, "main")
	second, err := Mount(env2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !KeyPathsEqual(sortedKeys(first), sortedKeys(second)) {
		t.Errorf("keys differ across two mounts of the same source:\n%s\n---\n%s",
			KeyList(sortedKeys(first)), KeyList(sortedKeys(second)))
	}

	// The loop renders three nodes under one #id, each at its own path.
	if got := len(first.Find("each")); got != 3 {
		t.Errorf("#each resolved to %d nodes, want 3", got)
	}
}

// TestAComponentInstantiationIsANodeWithItsExpansionBeneath is the shape that
// lets one tree serve readers wanting opposite things.
//
// An element ref and a snapshot want the rendered tree, so they descend through
// a component. `c.children` wants the authored tree, so it stops at one and
// hands back the component itself. Expanding at mount time served the first and
// made the second impossible.
func TestAComponentInstantiationIsANodeWithItsExpansionBeneath(t *testing.T) {
	src := `import . "sngl:ui"

component leaf() node {
    text #inner(value="hi")
}

component main node {
    leaf #outer()
}
`
	env, _ := envFor(t, src, "main")
	v, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	if len(v.Roots) != 1 {
		t.Fatalf("mounted %d roots, want 1", len(v.Roots))
	}
	root := v.Roots[0]
	if !root.IsComponent() {
		t.Fatalf("root is %q, which is not a component node", root.Name)
	}
	if len(root.Children) != 1 || root.Children[0].Name != "text" {
		t.Errorf("component's children are %v, want one text", root.Children)
	}

	// The rendered reading: descend through the component.
	if got := len(v.Find("inner")); got != 1 {
		t.Errorf("#inner resolved to %d element nodes, want 1", got)
	}
	// A component instantiation is not an element ref, which is what
	// ResolveElementRef has always done -- so Find skips it...
	if got := len(v.Find("outer")); got != 0 {
		t.Errorf("#outer resolved to %d element nodes; a component is not one", got)
	}
	// ...while the tree still holds it, for the authored reading.
	if got := len(v.FindAny("outer")); got != 1 {
		t.Errorf("FindAny(#outer) found %d nodes, want 1", got)
	}
}

// TestComponentNodeCarriesItsArguments: the instantiation's props are the
// arguments, evaluated in the caller's scope.
func TestComponentNodeCarriesItsArguments(t *testing.T) {
	src := `import . "sngl:ui"

component leaf(label string) node {
    text(value=label)
}

component main node {
    var greeting = "hello"
    leaf #outer(label=greeting)
}
`
	env, _ := envFor(t, src, "main")
	v, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	nodes := v.FindAny("outer")
	if len(nodes) != 1 {
		t.Fatalf("FindAny(#outer) found %d nodes, want 1", len(nodes))
	}
	if got := nodes[0].Props["label"]; got != "hello" {
		t.Errorf("label = %v, want the caller's greeting %q", got, "hello")
	}
}

// --- helpers

func checkFixture(t *testing.T, file string) *ir.Package {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	doc, perr := parser.Parse(file, src)
	if perr != nil || doc == nil {
		return nil
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil
		}
	}
	return pkg
}

func allIDs(pkg *ir.Package) []string {
	seen := map[string]bool{}
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if n.ID != "" {
					seen[n.ID] = true
				}
				walk(n.Children)
				// Named-slot content, which is where the gap was: an id here
				// was invisible to the walk, to the checker, and to this
				// collector, so nothing noticed.
				for _, sc := range n.Slots {
					if sc != nil {
						walk(sc.Body)
					}
				}
			case *ir.CallStmt:
				if _, id := elemCallInfo(n); id != "" {
					seen[id] = true
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.SlotInst:
				walk(n.Children)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.ContextProvider:
				walk(n.Children)
			}
		}
	}
	for _, c := range pkg.Components {
		walk(c.Body)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func normalizeRefs(v any) []map[string]any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return []map[string]any{x}
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, el := range x {
			if m, ok := el.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// splitSupplied separates nodes the pre-tree walk could reach from those it
// could not: content a call site supplied to a user component, which the
// mounter marks with a `supplied:` path segment.
func splitSupplied(nodes []*Node) (visible []*Node, fromSlot int) {
	for _, n := range nodes {
		if contains(n.Key.Path, "/supplied:") {
			fromSlot++
			continue
		}
		visible = append(visible, n)
	}
	return visible, fromSlot
}

func nodeMaps(nodes []*Node) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Map())
	}
	return out
}

// diffRefs compares two element-map lists. Handler and owner-env entries are
// compared by presence: both sides hold pointers into IR or scopes, and a
// pointer is not what agreement means here.
func diffRefs(old, got []map[string]any) string {
	if len(old) != len(got) {
		return fmt.Sprintf("walk found %d nodes, tree found %d", len(old), len(got))
	}
	for i := range old {
		for k, ov := range old[i] {
			gv, ok := got[i][k]
			if !ok {
				return fmt.Sprintf("node %d: tree is missing %q", i, k)
			}
			if k == "__ownerEnv" || (len(k) > 0 && k[0] == '@') {
				continue
			}
			if fmt.Sprint(ov) != fmt.Sprint(gv) {
				return fmt.Sprintf("node %d: %q is %v in the walk, %v in the tree", i, k, ov, gv)
			}
		}
		for k := range got[i] {
			if _, ok := old[i][k]; !ok {
				return fmt.Sprintf("node %d: tree has extra %q", i, k)
			}
		}
	}
	return ""
}

func sortedKeys(v *View) []Key {
	out := make([]Key, 0, v.Len())
	v.Walk(func(n *Node) bool { out = append(out, n.Key); return true })
	sort.Slice(out, func(i, j int) bool {
		if out[i].Comp != out[j].Comp {
			return out[i].Comp < out[j].Comp
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// TestAnEmptyBodiedComponentIsBothReadings is the rule a fixture caught rather
// than a test: `component main node { var x = 5 }` renders nothing of its own, so in
// the rendered tree it *is* the element and Find must return it -- while
// `c.m.double()` still needs its scope, so it must also be a component node.
//
// Skipping every component node broke test_reactivity_extension_method with
// "non-numeric operands <nil>, int", which is what `len(Component.Body) > 0`
// had been deciding all along.
func TestAnEmptyBodiedComponentIsBothReadings(t *testing.T) {
	src := `import . "sngl:ui"

component holder node {
    var x = 5
}

component main node {
    holder #h()
}
`
	env, _ := envFor(t, src, "main")
	v, err := Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// The rendered reading: it is the element, so Find returns it.
	found := v.Find("h")
	if len(found) != 1 {
		t.Fatalf("Find(#h) returned %d nodes, want 1 -- an empty-bodied component is the element", len(found))
	}
	if found[0].Expanded {
		t.Error("#h is marked Expanded, but its component declares no visual body")
	}

	// The authored reading: it is still a component, and carries its scope.
	if !found[0].IsComponent() {
		t.Error("#h is not a component node, so c.h.<member> has no scope to resolve against")
	}
	if found[0].CompEnv == nil {
		t.Error("#h has no CompEnv")
	}

	// And a component that DOES render is skipped by Find, as before.
	src2 := `import . "sngl:ui"

component leaf() node {
    text(value="hi")
}

component main node {
    leaf #l()
}
`
	env2, _ := envFor(t, src2, "main")
	v2, err := Mount(env2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if got := len(v2.Find("l")); got != 0 {
		t.Errorf("Find(#l) returned %d nodes; an expanded component is not an element ref", got)
	}
	if got := len(v2.FindAny("l")); got != 1 {
		t.Errorf("FindAny(#l) returned %d nodes, want 1", got)
	}
}
