package ir

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
)

// Walk is a second implementation of Rewrite's traversal rather than a
// wrapper over it, so nothing but these tests keeps the two reaching the same
// nodes. Each fixture below is walked by both, under several control patterns,
// and the visit sequences must be identical -- node for node, by pointer.

// traversals are the two walks every coverage test in this package holds to
// one answer.
var traversals = []struct {
	name string
	walk func(root any, visit func(Node) error) error
}{
	{"Walk", Walk},
	{"Rewrite", func(root any, visit func(Node) error) error {
		return Rewrite(root, func(n Node) (Node, error) { return n, visit(n) })
	}},
}

// filledTypes are the structs the reflective fixture descends into and fills.
// They are the ones that hold IR a walk might reach; anything else a field
// points at (a Type, a Symbol, a declaration) is left nil.
var filledTypes = map[reflect.Type]bool{
	reflect.TypeFor[Package]():      true,
	reflect.TypeFor[Component]():    true,
	reflect.TypeFor[Func]():         true,
	reflect.TypeFor[Var]():          true,
	reflect.TypeFor[Param]():        true,
	reflect.TypeFor[Prop]():         true,
	reflect.TypeFor[StructDef]():    true,
	reflect.TypeFor[StructField]():  true,
	reflect.TypeFor[EnumDef]():      true,
	reflect.TypeFor[EnumMember]():   true,
	reflect.TypeFor[Context]():      true,
	reflect.TypeFor[Output]():       true,
	reflect.TypeFor[EventHandler](): true,
	reflect.TypeFor[SlotContent]():  true,
	reflect.TypeFor[CallArg]():      true,
	reflect.TypeFor[FieldInit]():    true,
	reflect.TypeFor[MapEntry]():     true,
	reflect.TypeFor[Arg]():          true,
	reflect.TypeFor[StructLit]():    true,
	reflect.TypeFor[Call]():         true,
	reflect.TypeFor[NodeInst]():     true,
}

const maxFillDepth = 5

// filler populates every field of every IR-holding struct it reaches, so a
// slot either traversal forgets shows up as a divergence without anyone having
// to have written a marker for it. Reference slots are filled too: both walks
// must skip them, and a walk that descended would diverge.
type filler struct{ n int }

func (f *filler) marker(path string) *Literal {
	f.n++
	return &Literal{Type: TypString, Value: fmt.Sprintf("%s#%d", path, f.n)}
}

func (f *filler) value(t reflect.Type, path string, depth int) (reflect.Value, bool) {
	switch {
	case t == reflect.TypeFor[Expr]():
		return reflect.ValueOf(Expr(f.marker(path))), true
	case t == reflect.TypeFor[Stmt]():
		return reflect.ValueOf(Stmt(&Return{Value: f.marker(path)})), true
	}
	switch t.Kind() {
	case reflect.Pointer:
		if !filledTypes[t.Elem()] || depth >= maxFillDepth {
			return reflect.Value{}, false
		}
		p := reflect.New(t.Elem())
		f.fill(p.Elem(), path, depth+1)
		return p, true
	case reflect.Struct:
		if !filledTypes[t] || depth >= maxFillDepth {
			return reflect.Value{}, false
		}
		v := reflect.New(t).Elem()
		f.fill(v, path, depth+1)
		return v, true
	case reflect.Slice:
		elem, ok := f.value(t.Elem(), path+"[0]", depth)
		if !ok {
			return reflect.Value{}, false
		}
		s := reflect.MakeSlice(t, 0, 2)
		return reflect.Append(s, elem), true
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return reflect.Value{}, false
		}
		m := reflect.MakeMap(t)
		// Two keys, written out of order, so the by-name order of a slot
		// walk is part of what is compared.
		for _, k := range []string{"b", "a"} {
			v, ok := f.value(t.Elem(), path+"["+k+"]", depth)
			if !ok {
				return reflect.Value{}, false
			}
			m.SetMapIndex(reflect.ValueOf(k), v)
		}
		return m, true
	}
	return reflect.Value{}, false
}

func (f *filler) fill(v reflect.Value, path string, depth int) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		if val, ok := f.value(sf.Type, path+"."+sf.Name, depth); ok {
			v.Field(i).Set(val)
		}
	}
}

// everyKindPackage is a package with every field of its declarations filled,
// and one filled instance of every IR node kind in irNodeTypes -- which
// TestNodeRegistryIsComplete keeps complete -- in the body of its first func.
func everyKindPackage() *Package {
	f := &filler{}
	pkg := &Package{}
	f.fill(reflect.ValueOf(pkg).Elem(), "Package", 0)
	var block []Stmt
	for _, name := range slices.Sorted(maps.Keys(irNodeTypes)) {
		p := reflect.New(irNodeTypes[name])
		f.fill(p.Elem(), name, 1)
		switch n := p.Interface().(type) {
		case Stmt:
			block = append(block, n)
		case Expr:
			block = append(block, &Return{Value: n})
		default:
			panic(fmt.Sprintf("%s is neither an Expr nor a Stmt", name))
		}
	}
	pkg.Funcs[0].Block = append(pkg.Funcs[0].Block, block...)
	return pkg
}

// lockstepControls steer both traversals the same way, so pruning and early
// exit are compared as well as the full visit.
var lockstepControls = []struct {
	name string
	ctl  func(i int, n Node) error
}{
	{"all", func(int, Node) error { return nil }},
	{"skipdir", func(i int, _ Node) error {
		if i%3 == 1 {
			return SkipDir
		}
		return nil
	}},
	{"skipall", func(i int, _ Node) error {
		if i == 40 {
			return SkipAll
		}
		return nil
	}},
}

func traceWith(walk func(any, func(Node) error) error, root any, ctl func(int, Node) error) []Node {
	var out []Node
	_ = walk(root, func(n Node) error {
		out = append(out, n)
		return ctl(len(out)-1, n)
	})
	return out
}

// assertWalkMatchesRewrite fails t if Walk and a no-op Rewrite visit root
// differently under any control pattern, or if a typed walk disagrees with
// Rewrite filtered to the same kind.
func assertWalkMatchesRewrite(t *testing.T, label string, root any) {
	t.Helper()
	for _, c := range lockstepControls {
		got := traceWith(traversals[0].walk, root, c.ctl)
		want := traceWith(traversals[1].walk, root, c.ctl)
		if i, ok := firstDivergence(got, want); !ok {
			t.Errorf("%s/%s: Walk and Rewrite diverge at visit %d: Walk %s, Rewrite %s (Walk %d visits, Rewrite %d)",
				label, c.name, i, describe(got, i), describe(want, i), len(got), len(want))
		}
		var exprs, rexprs []Expr
		_ = WalkExprs(root, func(e Expr) error {
			exprs = append(exprs, e)
			return c.ctl(len(exprs)-1, e)
		})
		_ = RewriteExprs(root, func(e Expr) (Expr, error) {
			rexprs = append(rexprs, e)
			return e, c.ctl(len(rexprs)-1, e)
		})
		if !slices.Equal(exprs, rexprs) {
			t.Errorf("%s/%s: WalkExprs visited %d expressions, Rewrite %d, or in another order", label, c.name, len(exprs), len(rexprs))
		}
		var stmts, rstmts []Stmt
		_ = WalkStmts(root, func(s Stmt) error {
			stmts = append(stmts, s)
			return c.ctl(len(stmts)-1, s)
		})
		_ = Rewrite(root, func(n Node) (Node, error) {
			s, ok := n.(Stmt)
			if !ok {
				return n, nil
			}
			rstmts = append(rstmts, s)
			return n, c.ctl(len(rstmts)-1, s)
		})
		if !slices.Equal(stmts, rstmts) {
			t.Errorf("%s/%s: WalkStmts visited %d statements, Rewrite %d, or in another order", label, c.name, len(stmts), len(rstmts))
		}
	}
}

// TestWalkReadsSlotsAfterTheVisit pins the one ordering a mutating callback
// depends on: a node's slots are read after its own visit, so what the
// callback put there is walked and what it replaced is not.
func TestWalkReadsSlotsAfterTheVisit(t *testing.T) {
	for _, tr := range traversals {
		oldChild, newChild := mark("old child"), mark("new child")
		oldProp, newProp := mark("old prop"), mark("new prop")
		oldBody, newBody := mark("old body"), mark("new body")
		node := &NodeInst{
			Props:    []Arg{{Value: oldProp}},
			Children: []Stmt{&Return{Value: oldChild}},
		}
		cond := &If{Body: []Stmt{&Return{Value: oldBody}}}
		seen := map[Node]bool{}
		_ = tr.walk([]Stmt{node, cond}, func(n Node) error {
			seen[n] = true
			switch n {
			case node:
				node.Props = []Arg{{Value: newProp}}
				node.Children = []Stmt{&Return{Value: newChild}}
			case cond:
				cond.Body = []Stmt{&Return{Value: newBody}}
			}
			return nil
		})
		for _, m := range []Expr{newChild, newProp, newBody} {
			if !seen[m] {
				t.Errorf("%s: never visited %q, which the callback put in place", tr.name, m.(*Literal).Value)
			}
		}
		for _, m := range []Expr{oldChild, oldProp, oldBody} {
			if seen[m] {
				t.Errorf("%s: visited %q, which the callback had replaced", tr.name, m.(*Literal).Value)
			}
		}
	}
}

func firstDivergence(a, b []Node) (int, bool) {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i, false
		}
	}
	return min(len(a), len(b)), len(a) == len(b)
}

func describe(ns []Node, i int) string {
	if i >= len(ns) {
		return "<end>"
	}
	if lit, ok := ns[i].(*Literal); ok {
		return fmt.Sprintf("%T %q", lit, lit.Value)
	}
	return fmt.Sprintf("%T", ns[i])
}

func TestWalkMatchesRewrite(t *testing.T) {
	fixtures := map[string]*Package{
		"marked":    markedPackage(),
		"bodied":    bodiedPackage(),
		"everyKind": everyKindPackage(),
	}
	for name, pkg := range fixtures {
		assertWalkMatchesRewrite(t, name, pkg)
		for i, c := range pkg.Components {
			assertWalkMatchesRewrite(t, fmt.Sprintf("%s/component%d", name, i), c)
		}
		for i, fn := range pkg.Funcs {
			assertWalkMatchesRewrite(t, fmt.Sprintf("%s/func%d", name, i), fn)
			assertWalkMatchesRewrite(t, fmt.Sprintf("%s/func%d/block", name, i), fn.Block)
			for j, s := range fn.Block {
				assertWalkMatchesRewrite(t, fmt.Sprintf("%s/func%d/stmt%d(%T)", name, i, j, s), s)
				if r, ok := s.(*Return); ok && r.Value != nil {
					assertWalkMatchesRewrite(t, fmt.Sprintf("%s/func%d/expr%d(%T)", name, i, j, r.Value), r.Value)
				}
			}
		}
	}
}

// TestEveryKindPackageReachesEveryKind guards the fixture itself: a kind the
// filler failed to place would be a kind the lockstep test never compares.
func TestEveryKindPackageReachesEveryKind(t *testing.T) {
	seen := map[reflect.Type]bool{}
	_ = Walk(everyKindPackage(), func(n Node) error {
		seen[reflect.TypeOf(n).Elem()] = true
		return nil
	})
	for name, rt := range irNodeTypes {
		if !seen[rt] {
			t.Errorf("everyKindPackage never reaches a %s", name)
		}
	}
}
