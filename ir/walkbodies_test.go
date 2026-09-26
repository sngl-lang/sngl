package ir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
)

// TestRewriteVisitsEveryExprSlot guards the Expr-typed fields of the IR. The
// other half of a node is the IR it *owns* -- a statement list, a Func, an
// event handler -- and that half had no guard at all: the walk simply never
// descended into an owned body it had no line for, and every pass built on
// Rewrite stopped seeing it. That is how a context read inside an
// errorBoundary's @error handler survived the pass whose whole job is to
// remove it, and reached codegen as a bare *ir.ContextRead.
//
// The expected set here is derived from the IR types by reflection, so a new
// owned-body field joins it automatically and fails by name until the walk
// reaches it. A field that names IR owned somewhere else is not a body to
// descend into; those are listed in referenceSlots, which mirrors the opt-out
// list on Rewrite's doc comment and is the only hand-written part.

// irNodeTypes is every concrete IR node, by name. Go reflection cannot
// enumerate a package's types, so this registry is written out --
// TestNodeRegistryIsComplete parses node.go and fails naming anything the IR
// gained that is not here, which is what keeps it from falling behind.
var irNodeTypes = map[string]reflect.Type{
	"Literal":     reflect.TypeFor[Literal](),
	"Ident":       reflect.TypeFor[Ident](),
	"Binary":      reflect.TypeFor[Binary](),
	"Unary":       reflect.TypeFor[Unary](),
	"Ternary":     reflect.TypeFor[Ternary](),
	"Call":        reflect.TypeFor[Call](),
	"Conversion":  reflect.TypeFor[Conversion](),
	"Select":      reflect.TypeFor[Select](),
	"Index":       reflect.TypeFor[Index](),
	"StructLit":   reflect.TypeFor[StructLit](),
	"ListLit":     reflect.TypeFor[ListLit](),
	"MapLitIR":    reflect.TypeFor[MapLitIR](),
	"Spread":      reflect.TypeFor[Spread](),
	"Lambda":      reflect.TypeFor[Lambda](),
	"Closure":     reflect.TypeFor[Closure](),
	"ContextRead": reflect.TypeFor[ContextRead](),

	"NodeInst":         reflect.TypeFor[NodeInst](),
	"CallStmt":         reflect.TypeFor[CallStmt](),
	"SlotInst":         reflect.TypeFor[SlotInst](),
	"ErrorBoundary":    reflect.TypeFor[ErrorBoundary](),
	"Assign":           reflect.TypeFor[Assign](),
	"Toggle":           reflect.TypeFor[Toggle](),
	"Emit":             reflect.TypeFor[Emit](),
	"LocalVar":         reflect.TypeFor[LocalVar](),
	"Return":           reflect.TypeFor[Return](),
	"Break":            reflect.TypeFor[Break](),
	"Continue":         reflect.TypeFor[Continue](),
	"If":               reflect.TypeFor[If](),
	"For":              reflect.TypeFor[For](),
	"ContextProvider":  reflect.TypeFor[ContextProvider](),
	"CanvasRedrawStmt": reflect.TypeFor[CanvasRedrawStmt](),
}

// carrierTypes are the structs that are not Nodes but hold IR the walk has to
// reach: the package and its declarations, and the small records a node holds.
var carrierTypes = map[string]reflect.Type{
	"Package":      reflect.TypeFor[Package](),
	"Component":    reflect.TypeFor[Component](),
	"Func":         reflect.TypeFor[Func](),
	"Var":          reflect.TypeFor[Var](),
	"EventHandler": reflect.TypeFor[EventHandler](),
	"SlotContent":  reflect.TypeFor[SlotContent](),
}

// referenceSlots name IR owned somewhere else. Descending into one would walk
// another node's body once per reference to it, which is why Rewrite's doc
// comment calls them out; this list is that one, in the form the check reads.
var referenceSlots = map[string]bool{
	// The callee, owned by pkg.Funcs -- a recursive call closes a cycle here.
	"Call.Func": true,
	// Aliases Call.ErrorHandler or a handler owned by an enclosing boundary
	// or window, each of which is walked where it is owned.
	"Call.ResolvedHandler": true,
	// The lifted body, appended to pkg.Funcs by passLambda; this field stores
	// the pointer so the closure's construction site can name it.
	"Closure.Func": true,
	// The same *Funcs already reached through their owning slice.
	"Component.Methods": true,
	// A macro is a declaration, not code: it is never called and no backend
	// emits it, so no pass has anything to do to its body. Excluded here
	// deliberately rather than by omission -- walking it would subject a
	// description of the language to the passes that lower a program.
	"Package.Macros": true,
	// The settle handler, owned by the component or window whose props it
	// updates and walked there. The package stores the pointer so an entry
	// point can subscribe it without looking a name up.
	"Package.RemoteSettle": true,
	// The exit handler, owned by the component, window or package whose
	// effects it releases and walked there. Stored on the package for the same
	// reason the settle handler is: an entry point calls it without having to
	// look a name up.
	"Package.Teardown": true,
	// The settle handlers, each owned by the component or window whose
	// brackets it moves and walked there. Same reason as Teardown, at the
	// other end of the lifetime.
	"Package.Mounts": true,
}

// bodySlots returns "Type.Field" for every field holding IR a node owns and
// the walk must descend into: a statement list, a Func, or an event handler.
func bodySlots() []string {
	owned := map[reflect.Type]bool{
		reflect.TypeFor[Stmt]():          true,
		reflect.TypeFor[*Func]():         true,
		reflect.TypeFor[*EventHandler](): true,
	}
	var out []string
	for _, set := range []map[string]reflect.Type{irNodeTypes, carrierTypes} {
		for name, rt := range set {
			for f := range rt.Fields() {
				ft := f.Type
				for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map {
					ft = ft.Elem()
				}
				slot := name + "." + f.Name
				if owned[ft] && !referenceSlots[slot] {
					out = append(out, slot)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// bodiedPackage puts a marker statement in every body slot bodySlots reports.
// Keep it in step with that list: an unpopulated slot fails here exactly as an
// unvisited one does, which is the point.
func bodiedPackage() *Package {
	// fn returns a Func whose body is a marker naming the slot it fills.
	fn := func(slot string) *Func { return &Func{Block: []Stmt{&Return{Value: mark(slot)}}} }
	h := func(slot string) *EventHandler { return &EventHandler{Func: fn(slot)} }
	body := func(slot string) []Stmt { return []Stmt{&Return{Value: mark(slot)}} }

	return &Package{
		Body: body("Package.Body"),
		Vars: []*Var{{Handlers: []*EventHandler{h("Var.Handlers")}}},
		Funcs: []*Func{{
			Block: []Stmt{
				&If{Body: body("If.Body"), Else: body("If.Else")},
				&For{Body: body("For.Body"), Else: body("For.Else")},
				&ErrorBoundary{
					Handler:  h("ErrorBoundary.Handler"),
					Children: body("ErrorBoundary.Children"),
					Failed:   body("ErrorBoundary.Failed"),
				},
				&ContextProvider{Children: body("ContextProvider.Children")},
				&SlotInst{Children: body("SlotInst.Children")},
				&NodeInst{
					Children:     body("NodeInst.Children"),
					Handlers:     []EventHandler{*h("NodeInst.Handlers")},
					ErrorHandler: h("NodeInst.ErrorHandler"),
					Slots:        map[string]*SlotContent{"s": {Body: body("SlotContent.Body")}},
				},
				&Return{Value: &Call{ErrorHandler: h("Call.ErrorHandler")}},
				&Return{Value: &Lambda{Func: fn("Lambda.Func")}},
			},
		}},
		Components: []*Component{{
			Body:  body("Component.Body"),
			Funcs: []*Func{fn("Component.Funcs")},
			Vars:  []*Var{{Handlers: []*EventHandler{h("Var.Handlers")}}},
		}},
		// A window is a NodeInst, so its own slots are the ones marked above.
		// It is here so that the walk has one to reach through pkg.Windows,
		// which bodySlots does not list -- the field holds nodes rather than
		// bodies.
		Windows: []*Window{{Children: body("Package.Windows")}},
	}
}

func TestRewriteVisitsEveryOwnedBody(t *testing.T) {
	for _, tr := range traversals {
		seen := map[string]bool{}
		if err := tr.walk(bodiedPackage(), func(n Node) error {
			if lit, ok := n.(*Literal); ok && lit.Value != "" {
				seen[lit.Value] = true
			}
			return nil
		}); err != nil {
			t.Fatalf("%s: %v", tr.name, err)
		}
		// These carry no marker of their own because every other marker is
		// reached through them: the walk's entry points, and the handler record
		// whose Func holds each handler marker. Skipping any of them loses the
		// markers underneath, so they stay covered.
		reachedThrough := map[string]bool{
			"Package.Funcs": true, "Func.Block": true, "EventHandler.Func": true,
		}
		for _, slot := range bodySlots() {
			if reachedThrough[slot] || seen[slot] {
				continue
			}
			t.Errorf("ir.%s never visited %s\n"+
				"\tEvery field holding IR a node owns must be reached by the walk, or\n"+
				"\tnamed in referenceSlots as IR owned somewhere else. If this is a new\n"+
				"\tfield: wire it into walkexprs.go and walk.go, and add a marker to\n"+
				"\tbodiedPackage.", tr.name, slot)
		}
	}
}

// TestNodeRegistryIsComplete keeps irNodeTypes from falling behind the IR: it
// reads node.go's irNode() receivers, which is the one place every concrete
// node is written down.
func TestNodeRegistryIsComplete(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "node.go", nil, 0)
	if err != nil {
		t.Fatalf("parse node.go: %v", err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "irNode" || fd.Recv == nil || len(fd.Recv.List) != 1 {
			continue
		}
		star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		id, ok := star.X.(*ast.Ident)
		if !ok {
			continue
		}
		if _, known := irNodeTypes[id.Name]; !known {
			t.Errorf("ir node %s is not in irNodeTypes; add it so the walk-coverage "+
				"tests see its fields", id.Name)
		}
	}
}
