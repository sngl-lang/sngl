// Package canvasutil holds the platform-neutral Canvas2D helpers shared by
// every Go-emitting platform (fyne, gtk4, and future Go canvas backends).
//
// codegen.Canvases finds the drawings and builds the `_canvasDrawN(ctx)` func
// each one paints with; passDeclarative has by then flattened the canvas
// NodeInst to a `lower.CreateNode("canvas")` LocalVar carrying a back-pointer
// to it.
//
// Two pieces generalise across Go platforms and live here:
//
//  1. the flattened canvas metadata in the shape these platforms read it
//     (Collect / Meta), and
//  2. emitting the Go stdlib struct decls for Color/CanvasStyle/PathCmd,
//     which the draw funcs reference but which aren't carried on pkg.Structs
//     for the Go path.
//
// The native 2D translation (gg for fyne, cairo for gtk4) stays in each
// platform package: it differs per backend and must not be shared.
package canvasutil

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Meta records a flattened canvas element discovered via a
// `LocalVar id = lower.CreateNode("canvas")` carrying its canvas node.
type Meta struct {
	ID string // synthesized node id (e.g. "__n0") → platform widget field
	// Node is the canvas instantiation, which is what a CanvasRedrawStmt names
	// -- there being no draw function until codegen builds one.
	Node   *ir.NodeInst
	Draw   *ir.Func
	Width  int
	Height int
	// Scaling is the `scalingMode` prop: what to do with the picture when the
	// room the canvas is laid out in is not the size its shapes were placed
	// at. Empty is the declaration's default, which is Center.
	Scaling string
}

// The scaling modes, spelled as lib/ui/draw declares them.
const (
	ScaleCenter  = "center"
	ScaleFit     = "fit"
	ScaleFill    = "fill"
	ScaleStretch = "stretch"
)

// Collect is the drawings codegen found, in the three shapes these platforms
// look them up by: the node id they address the widget with, the draw func
// they emit as a method, and the canvas node a repaint names.
func Collect(draws *codegen.CanvasDraws) (byID map[string]*Meta, byFunc map[*ir.Func]*Meta, byNode map[*ir.NodeInst]*Meta) {
	byID = map[string]*Meta{}
	byFunc = map[*ir.Func]*Meta{}
	byNode = map[*ir.NodeInst]*Meta{}
	for _, c := range draws.All() {
		m := &Meta{ID: c.ID, Node: c.Node, Draw: c.Draw, Width: c.Width, Height: c.Height, Scaling: c.Scaling}
		byID[m.ID] = m
		byFunc[c.Draw] = m
		byNode[c.Node] = m
	}
	return byID, byFunc, byNode
}

// ByIDFor rebuilds the id→Meta map from the func→Meta map (both share the
// same *Meta pointers).
func ByIDFor(byFunc map[*ir.Func]*Meta) map[string]*Meta {
	out := make(map[string]*Meta, len(byFunc))
	for _, m := range byFunc {
		out[m.ID] = m
	}
	return out
}

// StructDecls holds the hardcoded Go decl per stdlib struct name, keyed by
// struct name so a collision with a user-declared struct can omit just that
// one decl. Field names/types mirror the stdlib definitions in
// lib/canvas.sngl (CanvasStyle, PathCmd); their color fields reference the
// shared ColorGoType. The per-platform drift-guard tests assert every stdlib
// field is present.
var StructDecls = map[string]string{
	"CanvasStyle": "type CanvasStyle struct {\n" +
		"\tFill        " + ColorGoType + "\n" +
		"\tStroke      " + ColorGoType + "\n" +
		"\tStrokeWidth float64\n" +
		"\tLineCap     string\n" +
		"\tLineJoin    string\n" +
		"\tFontSize    float64\n" +
		"\tFontFamily  string\n" +
		"}\n",
	"PathCmd": "type PathCmd struct {\n" +
		"\tOp  string\n" +
		"\tX   float64\n\tY   float64\n" +
		"\tCx1 float64\n\tCy1 float64\n\tCx2 float64\n\tCy2 float64\n\tR float64\n" +
		"}\n",
}

// Color is the SNGL `color` type. On Go it maps to the shared
// pkg/go/snglcolor.Color struct (int RGBA channels), so a color value is the
// same type whether it flows into a CanvasStyle field or is coerced to a CSS
// string elsewhere. Canvas emitters must register ColorImportPath when they
// emit CanvasStyle. Kept in sync with lib/types.sngl by the golang lang layer.
const (
	ColorImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/snglcolor"
	ColorGoType     = "snglcolor.Color"
)

// StructOrder fixes the emission order of StructDecls.
var StructOrder = []string{"CanvasStyle", "PathCmd"}

// StructDeclsExcluding returns the canvas stdlib struct decls, omitting any
// whose name is in declared (a set of user-declared struct names that already
// emit their own Go type decl). The canvas structs are stdlib-only today; if a
// program declares one (or more) itself, only the colliding name(s) are
// dropped — the others stay so the draw funcs still reference valid types.
func StructDeclsExcluding(declared map[string]struct{}) string {
	var b strings.Builder
	for _, name := range StructOrder {
		if _, collides := declared[name]; collides {
			continue
		}
		b.WriteString(StructDecls[name])
		b.WriteString("\n")
	}
	return b.String()
}
