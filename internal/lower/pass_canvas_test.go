package lower_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/internal/lower"
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
	}
	return pkg, canvasInst
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
