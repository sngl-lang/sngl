package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The drawings in a package, for the platforms that paint one.
//
// A canvas's children *are* the statements that paint it by the time codegen
// runs: passShapeDraw replaced its shapes with them, in place. Nothing here
// builds a function -- each platform emits those statements into whatever it
// paints through, which is a closure on three targets and a lambda on a
// fourth. There used to be a synthesized `_canvasDrawN` for them to call, and
// every platform needed a name match to keep it out of its generic func loop.
//
// What a canvas is remains a fact about declarations: ir.IsShapeContainer.

// Canvas is one drawing in the program: the node the shapes were written
// under, and the statements that paint them.
type Canvas struct {
	// Node is the canvas instantiation. Non-nil always -- on a target whose
	// lowering flattened the tree it is reached through LocalVar.CanvasNode.
	Node *ir.NodeInst
	// Local is the flattened createNode statement, or nil on a target that
	// kept its tree. Platforms key their widget off whichever they hold.
	Local *ir.LocalVar
	// Draw is the statements that paint it, in order. They are the node's own
	// children: passShapeDraw put them there in place of the shapes, so the
	// optimizer folds them and the shaker keeps what they call. Empty for a
	// canvas whose shapes all folded away.
	Draw []ir.Stmt
	// Owner is the component whose body holds this drawing, or nil when a
	// window's body or the package's does. A platform that gives a component
	// a record of its own puts the drawing's routine on that record: the
	// canvas's widget and its backing context are fields of the instance, not
	// of the Model, so a method on the Model would address the wrong one --
	// and there is one per instance rather than one per program.
	Owner *ir.Component
	// Name is what a platform calls the routine it emits for Draw. Three
	// places invoke a drawing -- the first paint, a repaint, and the host's
	// own generator callback -- so every backend wraps the statements once and
	// calls the wrapper, and the name is given here so the wrapper and the
	// three calls cannot disagree. It names no declaration: there is no
	// ir.Func for a drawing, and no func list holds one.
	Name string
	// ID is the node id a platform addresses the widget by -- the synthesized
	// `__nN` on a flattened target.
	ID string
	// Width and Height are the coordinate space the shapes were placed in, and
	// Scaling what the platform does when the room it lays the canvas out in
	// is not that size. Empty Scaling means the declaration's default.
	Width, Height int
	Scaling       string
}

// Canvases is every drawing the package describes, in the order the tree holds
// them: each component's body then its funcs, each window's body then its
// funcs, then the package's own.
//
// An owner's funcs come after its body because that is where a flattened
// canvas ends up: passDeclarative moves a canvas local out of the body and
// into that same owner's render func.
func Canvases(pkg *ir.Package) []Canvas {
	if pkg == nil {
		return nil
	}
	roots := [][]ir.Stmt{pkg.Body}
	for _, w := range pkg.Windows {
		if w != nil {
			roots = append(roots, w.Children)
		}
	}
	return canvasesOf(pkg, roots)
}

// CanvasesIn is Canvases for a target writing one document at a time: the
// components' drawings and the package's funcs', and of the windows only the
// one body it is writing.
func CanvasesIn(pkg *ir.Package, body []ir.Stmt) []Canvas {
	if pkg == nil {
		return nil
	}
	return canvasesOf(pkg, [][]ir.Stmt{body})
}

func canvasesOf(pkg *ir.Package, roots [][]ir.Stmt) []Canvas {
	// Not gated on pkg.UsesDrawShapes(): by now passShapeDraw has replaced the
	// shapes with the statements that paint them, so the program uses none and
	// the gate answers no for every drawing there is.
	var out []Canvas
	owner := func(comp *ir.Component, body []ir.Stmt, funcs []*ir.Func) {
		first := len(out)
		collectCanvases(body, &out)
		for _, fn := range funcs {
			if fn != nil {
				collectCanvases(fn.Block, &out)
			}
		}
		for i := first; i < len(out); i++ {
			out[i].Owner = comp
		}
	}
	for _, comp := range pkg.Components {
		if comp != nil {
			owner(comp, comp.Body, comp.Funcs)
		}
	}
	for _, body := range roots {
		owner(nil, body, nil)
	}
	owner(nil, nil, pkg.Funcs)
	for i := range out {
		c := &out[i]
		c.Draw = c.Node.Children
		c.Name = fmt.Sprintf("_canvasDraw%d", i)
		c.Width = nodeIntProp(c.Node, "width")
		c.Height = nodeIntProp(c.Node, "height")
		c.Scaling = nodeEnumProp(c.Node, "scalingMode")
		if c.Local != nil {
			c.ID = c.Local.Name
		} else {
			c.ID = c.Node.ID
		}
	}
	return out
}

// CanvasDraws is Canvases keyed by node, which is what a platform emitting one
// node at a time wants. A flattened target reaches the node through
// LocalVar.CanvasNode.
type CanvasDraws struct {
	byNode map[*ir.NodeInst]*Canvas
	all    []Canvas
}

func NewCanvasDraws(pkg *ir.Package) *CanvasDraws {
	return newCanvasDraws(Canvases(pkg))
}

// NewCanvasDrawsIn is NewCanvasDraws over CanvasesIn.
func NewCanvasDrawsIn(pkg *ir.Package, body []ir.Stmt) *CanvasDraws {
	return newCanvasDraws(CanvasesIn(pkg, body))
}

func newCanvasDraws(all []Canvas) *CanvasDraws {
	cs := &CanvasDraws{all: all, byNode: make(map[*ir.NodeInst]*Canvas, len(all))}
	for i := range cs.all {
		cs.byNode[cs.all[i].Node] = &cs.all[i]
	}
	return cs
}

// All is every drawing, in tree order.
func (cs *CanvasDraws) All() []Canvas {
	if cs == nil {
		return nil
	}
	return cs.all
}

// ForNode answers nil for a node that is not a canvas, so a caller
// may ask of every node it meets.
func (cs *CanvasDraws) ForNode(n *ir.NodeInst) *Canvas {
	if cs == nil {
		return nil
	}
	return cs.byNode[n]
}

// collectCanvases finds the drawings in one statement list, reaching through
// everything that says when or how many rather than what.
//
// A canvas is often one arm of a target test -- a drawing where there are
// pixels, something else where there are not -- and a boundary or a context
// override is how the nodes under it got there rather than a node. Stopping at
// NodeInsts left such a canvas unlowered and its shapes rendered as widgets.
func collectCanvases(stmts []ir.Stmt, out *[]Canvas) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(v) {
				*out = append(*out, Canvas{Node: v})
				continue
			}
			collectCanvases(v.Children, out)
		case *ir.LocalVar:
			// The tree was flattened; the canvas node rode across on the
			// createNode statement. Its shapes were never flattened, so this
			// is the same drawing reached by the other route.
			if v.CanvasNode != nil {
				*out = append(*out, Canvas{Node: v.CanvasNode, Local: v})
			}
		case *ir.If:
			collectCanvases(v.Body, out)
			collectCanvases(v.Else, out)
		case *ir.For:
			collectCanvases(v.Body, out)
			collectCanvases(v.Else, out)
		case *ir.ErrorBoundary:
			// Children alone: passBoundaryFailed has already rewritten the
			// pair into a reactive `if` over its flag, so walking Failed would
			// find the fallback a second time.
			collectCanvases(v.Children, out)
		}
	}
}

// nodeEnumProp is an enum-valued prop as the member name, or "" when absent.
func nodeEnumProp(n *ir.NodeInst, name string) string {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if id, ok := p.Value.(*ir.Ident); ok && id.Member != "" {
			return id.Member
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			return lit.Value
		}
	}
	return ""
}

// nodeIntProp is a numeric or measurement prop as an int, or 0 when absent.
func nodeIntProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		if lit, ok := p.Value.(*ir.Literal); ok {
			raw := strings.TrimSuffix(lit.Value, lit.Suffix)
			if v, err := strconv.Atoi(raw); err == nil {
				return v
			}
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				return int(f)
			}
		}
	}
	return 0
}
