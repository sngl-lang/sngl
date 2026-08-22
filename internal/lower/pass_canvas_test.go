package lower_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func buildCanvasPkg(t *testing.T) (*ir.Package, *ir.NodeInst) {
	t.Helper()
	styleTyp := &ir.Type{Kind: ir.TypeStruct}
	shapeListType := ir.ListOf(&ir.Type{Kind: ir.TypeShape})

	rectComp := &ir.Component{
		Name:         "rect",
		ChildrenType: shapeListType,
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
		ChildrenType: shapeListType,
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
		// A package holding a canvas is one that imported the package the
		// canvas is declared in; passCanvas skips a program that did not.
		Imports: []*ir.Import{{Path: "sngl://draw"}},
	}
	return pkg, canvasInst
}

// TestPassCanvas_SkipsProgramsWithoutDraw pins the gate: the canvas and its
// shapes are declared in sngl://draw, so a program that never imported it
// cannot hold one and is not searched. Without the gate the pass walks every
// component of every program a canvas-capable target builds.
func TestPassCanvas_SkipsProgramsWithoutDraw(t *testing.T) {
	pkg, canvasInst := buildCanvasPkg(t)
	pkg.Imports = nil

	if err := lower.Lower(pkg, lower.Caps{Canvas: true}, lower.Options{}); err != nil {
		t.Fatalf("lower error: %v", err)
	}
	if canvasInst.CanvasDraw != nil {
		t.Error("passCanvas ran on a program that does not import sngl://draw")
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
