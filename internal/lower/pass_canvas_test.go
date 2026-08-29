package lower_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func buildCanvasPkg(t *testing.T) (*ir.Package, *ir.NodeInst) {
	t.Helper()
	styleTyp := &ir.Type{Kind: ir.TypeStruct}
	childListType := ir.ListOf(&ir.Type{Kind: ir.TypeComponent})

	// The tree is a declaration, so both the member and its host name this one.
	shapeTree := &ir.StructDef{Name: "shape", IsTree: true}
	shapeSlot := func() []*ir.SlotDecl {
		return []*ir.SlotDecl{{
			Name:    ir.DefaultSlot,
			Content: &ir.Type{Kind: ir.TypeStruct, Decl: shapeTree},
		}}
	}

	rectComp := &ir.Component{
		Name:         "rect",
		Tree:         shapeTree,
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
			{Name: "x", Value: &ir.Literal{Type: ir.TypFloat, Raw: "10"}},
			{Name: "y", Value: &ir.Literal{Type: ir.TypFloat, Raw: "10"}},
			{Name: "w", Value: &ir.Literal{Type: ir.TypFloat, Raw: "100"}},
			{Name: "h", Value: &ir.Literal{Type: ir.TypFloat, Raw: "50"}},
		},
	}
	canvasInst := &ir.NodeInst{
		Name:      "canvas",
		Component: canvasComp,
		Props: []ir.Arg{
			{Name: "width", Value: &ir.Literal{Type: ir.TypFloat, Raw: "400"}},
			{Name: "height", Value: &ir.Literal{Type: ir.TypFloat, Raw: "300"}},
		},
		Children: []ir.Stmt{rectInst},
	}

	mainComp := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{canvasInst},
	}
	pkg := &ir.Package{
		Components: []*ir.Component{mainComp},
		TreeKinds:  map[string]bool{"shape": true},
	}
	return pkg, canvasInst
}

// A program with no shape node anywhere is not searched. Without the gate the
// pass walks every component of every program a canvas-capable target builds.
func TestPassCanvas_SkipsProgramsWithoutShapes(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)
	pkg.TreeKinds = nil

	if err := lower.Lower(pkg, lower.Caps{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if canvasInst.CanvasDraw != nil {
		t.Error("passCanvas ran on a program that declares no shapes")
	}
}

// The gate is the declaration, not the import. A package may declare its own
// shapes, and inlining flattens a canvas out of
// the package that imported it, so gating on the import list dropped canvases
// on the floor: the shapes survived as ordinary widget nodes and the generated
// program failed to build.
func TestPassCanvas_RunsWithoutADrawImport(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)
	pkg.Imports = nil

	if err := lower.Lower(pkg, lower.Caps{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if canvasInst.CanvasDraw == nil {
		t.Error("passCanvas skipped a canvas because the package had no draw import")
	}
}

func TestPassCanvas_GeneratesDrawFunc(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)

	caps := lower.Caps{Canvas: true}
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}

	if canvasInst.CanvasDraw == nil {
		t.Fatal("expected CanvasDraw to be set after passCanvas")
	}
	if canvasInst.CanvasDraw.Name != "_canvasDraw0" {
		t.Errorf("expected draw func name %q, got %q", "_canvasDraw0", canvasInst.CanvasDraw.Name)
	}
	if len(canvasInst.Children) != 0 {
		t.Errorf("expected Children to be cleared, got %d", len(canvasInst.Children))
	}
	if len(canvasInst.CanvasDraw.Block) == 0 {
		t.Error("expected draw function Block to have statements")
	}
}
