package lower_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

func buildCanvasPkg(t *testing.T) (*ir.Package, *ir.NodeInst) {
	t.Helper()
	styleTyp := &ir.Type{Kind: ir.TypeStruct}
	childListType := ir.ListOf(&ir.Type{Kind: ir.TypeComponent})

	// The family is a declaration, so both the member and its host name this
	// one. It is a family by being a member of the family of families, which
	// is a member of itself.
	families := &ir.Component{Name: "family", Pkg: "sngl:build", Builtin: ir.BuiltinTreeFamily}
	families.Tree = families
	shapeTree := &ir.Component{Name: "shape", Pkg: "sngl:ui/draw", Builtin: ir.BuiltinTreeShape, Tree: families}
	shapeSlot := func() []*ir.SlotDecl {
		return []*ir.SlotDecl{{
			Name:    "shapes",
			Rest:    true,
			Content: shapeTree.SymType(),
		}}
	}

	// A platform primitive, because that is the only kind of shape that draws:
	// the painting is a `@draw` handler its call site supplies, which
	// primitiveDrawBody splices with the handler's payload rebound to the draw
	// function's ctx. A bodyless non-primitive -- what this used to build --
	// paints nothing, and the only statements it produced were the bracket the
	// lowering no longer writes.
	rectComp := &ir.Component{
		Name:         "rect",
		Tree:         shapeTree,
		Intrinsic:    "test:draw",
		Slots:        shapeSlot(),
		ChildrenType: childListType,
		Props: []*ir.Prop{
			{Name: "x", Type: ir.TypFloat},
			{Name: "y", Type: ir.TypFloat},
			{Name: "w", Type: ir.TypFloat},
			{Name: "h", Type: ir.TypFloat},
			{Name: "style", Type: styleTyp},
		},
	}
	canvasComp := &ir.Component{
		Name:         "canvas",
		Slots:        shapeSlot(),
		ChildrenType: childListType,
		Props: []*ir.Prop{
			{Name: "width", Type: ir.TypFloat},
			{Name: "height", Type: ir.TypFloat},
		},
	}

	rectInst := &ir.NodeInst{
		Name:      "rect",
		Component: rectComp,
		Props: []ir.Arg{
			{Name: "x", Value: &ir.Literal{Type: ir.TypFloat, Value: "10"}},
			{Name: "y", Value: &ir.Literal{Type: ir.TypFloat, Value: "10"}},
			{Name: "w", Value: &ir.Literal{Type: ir.TypFloat, Value: "100"}},
			{Name: "h", Value: &ir.Literal{Type: ir.TypFloat, Value: "50"}},
		},
		// What the target paints, as an override's `@draw` would have written
		// it: one call against the context the handler binds.
		Handlers: []ir.EventHandler{{
			Name: "draw",
			Func: &ir.Func{
				Params: []*ir.Param{{Name: "e", Type: ir.TypDyn}},
				Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
					Type: ir.TypVoid,
					Func: &ir.Func{Name: "paintRect"},
				}}},
			},
		}},
	}
	canvasInst := &ir.NodeInst{
		Name:      "canvas",
		Component: canvasComp,
		Props: []ir.Arg{
			{Name: "width", Value: &ir.Literal{Type: ir.TypFloat, Value: "400"}},
			{Name: "height", Value: &ir.Literal{Type: ir.TypFloat, Value: "300"}},
		},
		Children: []ir.Stmt{rectInst},
	}

	mainComp := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{canvasInst},
	}
	pkg := &ir.Package{
		Components: []*ir.Component{mainComp},
		TreeKinds:  map[*ir.Component]bool{shapeTree: true},
	}
	return pkg, canvasInst
}

// A program with no shape node anywhere is not searched. Without the gate the
// pass walks every component of every program a canvas-capable target builds.
func TestShapeDraw_SkipsProgramsWithoutShapes(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)
	pkg.TreeKinds = nil

	if err := lower.Lower(pkg, lower.Features{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if !onlyShapeNodes(canvasInst.Children) {
		t.Error("passShapeDraw ran on a program that declares no shapes")
	}
}

// The gate is the declaration, not the import. A package may declare its own
// shapes, and inlining flattens a canvas out of the package that imported it,
// so gating on the import list dropped canvases on the floor: the shapes
// survived as ordinary widget nodes and the generated program failed to build.
func TestShapeDraw_RunsWithoutADrawImport(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)
	pkg.Imports = nil

	if err := lower.Lower(pkg, lower.Features{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if onlyShapeNodes(canvasInst.Children) {
		t.Error("passShapeDraw skipped a canvas because the package had no draw import")
	}
}

// The shapes become the statements that paint them, where they stood. In place
// is the whole of the design: they stay ordinary IR, so the optimize pass that
// runs after lowering folds them and keeps what they call. A version that built
// a function instead put the body somewhere nothing walked, and the helpers its
// calls named were shaken away as unreferenced.
func TestShapeDraw_SplicesTheDrawingInPlace(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)

	if err := lower.Lower(pkg, lower.Features{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if len(canvasInst.Children) == 0 {
		t.Fatal("expected the canvas to keep the statements that paint it")
	}
	for i, st := range canvasInst.Children {
		if n, ok := st.(*ir.NodeInst); ok && ir.IsDrawShapeTree(n.Component.Tree) {
			t.Errorf("children[%d] is still the shape %q, not the drawing", i, n.Name)
		}
	}
	// Nothing is synthesized into the IR: no func list anywhere holds a
	// drawing, which is what let the pass stop owning one.
	for _, fn := range pkg.Funcs {
		if fn != nil && fn.Synthesized && strings.HasPrefix(fn.Name, "_canvasDraw") {
			t.Errorf("a drawing reached pkg.Funcs as %q", fn.Name)
		}
	}
}

// onlyShapeNodes reports whether stmts are all still un-spliced shapes, which
// is what an unsearched canvas keeps.
func onlyShapeNodes(stmts []ir.Stmt) bool {
	for _, st := range stmts {
		n, ok := st.(*ir.NodeInst)
		if !ok || !ir.IsDrawShapeTree(n.Component.Tree) {
			return false
		}
	}
	return len(stmts) > 0
}
