package html

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// routeFixture builds a checked-shape IR package + WindowCtx that mirrors the
// route_post_action fixture AFTER native import resolution and reactivity
// lowering: a `count` state var, a backend @click handler whose body is the
// logical `count = api.Persist(count+1)` plus a lowered DOM-patch
// `__n0.value = "count " + count`, and a reactive `text(value="count {count}")`
// binding.
//
// The IR is constructed directly (rather than parse→check→optimize→lower)
// because native go: resolution requires the Go toolchain importer, which the
// in-package parity harness intentionally omits — so a parsed fixture's go:
// call never carries the Func.Foreign.Path linkage handlerPlacement keys on.
func routeFixture() (*ir.Package, *codegen.WindowCtx) {
	const importPath = "example.com/route-post/api"

	persist := &ir.Func{Name: "Persist", Foreign: ir.Foreign{Path: importPath}}
	pkg := &ir.Package{
		Imports: []*ir.Import{{
			Alias:  "api",
			AST:    &ast.Import{Path: "go:" + importPath},
			Native: &ir.NativeImport{ImportPath: importPath, Funcs: []*ir.Func{persist}},
		}},
	}

	countVar := &ir.Var{Name: "count", Type: ir.TypInt, Init: &ir.Literal{Type: ir.TypInt, Value: "0"}}
	pkg.Vars = []*ir.Var{countVar}

	countRef := func() ir.Expr { return &ir.Ident{Name: "count", Type: ir.TypInt} }

	// value binding: "count " + count
	binding := &ir.Binary{
		Op:    ast.BinAdd,
		Left:  &ir.Literal{Type: ir.TypString, Value: `"count "`},
		Right: &ir.Conversion{Operand: countRef(), Type: ir.TypString},
	}

	// Backend handler body:
	//   count = api.Persist(count + 1)             (logical)
	//   __n0.value = "count " + count              (DOM patch — must be dropped)
	logical := &ir.Assign{
		Target: &ir.Ident{Name: "count", Type: ir.TypInt},
		Op:     ast.AssignSet,
		Value: &ir.Call{
			Func: persist,
			Args: []ir.CallArg{{Value: &ir.Binary{Op: ast.BinAdd, Left: countRef(), Right: &ir.Literal{Type: ir.TypInt, Value: "1"}}}},
		},
	}
	domPatch := &ir.Assign{
		Target: &ir.Select{Operand: &ir.Ident{Name: "__n0", IsElementRef: true}, Field: "value"},
		Op:     ast.AssignSet,
		Value:  binding,
	}
	handler := ir.EventHandler{
		Name: "click",
		Func: &ir.Func{Block: []ir.Stmt{logical, domPatch}},
	}

	button := &ir.NodeInst{
		Name:     "button",
		Props:    []ir.Arg{{Name: "text", Value: &ir.Literal{Type: ir.TypString, Value: `"Save"`}}},
		Handlers: []ir.EventHandler{handler},
	}
	// `textContent`, not `value`: lowering inlines sngl.text into html's
	// `<span textContent=...>` before any render sees it, and `value` on an
	// arbitrary element is an attribute -- writing it as a text node gave an
	// <input> a child and no value.
	textNode := &ir.NodeInst{
		Name:  "span",
		ID:    "__n0",
		Props: []ir.Arg{{Name: "textContent", Value: binding}},
	}
	vbox := &ir.NodeInst{Name: "vbox", Children: []ir.Stmt{button, textNode}}

	win := &codegen.WindowCtx{
		Name: "app",
		Vars: []*ir.Var{countVar},
		Body: []ir.Stmt{vbox},
	}
	return pkg, win
}

func TestBuildRenderModel(t *testing.T) {
	pkg, win := routeFixture()

	_, actionIdx := collectActions(pkg, win, buildNativeFuncMap(pkg, "go"))
	rr, err := buildRenderModel(pkg, win, "/", actionIdx)
	if err != nil {
		t.Fatalf("buildRenderModel: %v", err)
	}
	if rr == nil {
		t.Fatal("buildRenderModel returned nil")
	}
	if len(rr.Chunks) != len(rr.Holes)+1 {
		t.Fatalf("chunk/hole invariant violated: %d chunks, %d holes", len(rr.Chunks), len(rr.Holes))
	}

	var textHoles []codegen.RouteHole
	for _, h := range rr.Holes {
		if h.Kind == codegen.HoleText {
			textHoles = append(textHoles, h)
		}
	}
	if len(textHoles) != 1 {
		t.Fatalf("expected exactly one HoleText, got %d (holes=%+v)", len(textHoles), rr.Holes)
	}
	if !exprReadsVar(textHoles[0].Expr, "count") {
		t.Fatalf("HoleText expr does not read count: %T", textHoles[0].Expr)
	}

	joined := strings.Join(rr.Chunks, "")
	if !strings.Contains(joined, `<form method="post" action="/"`) {
		t.Fatalf("chunks missing form wrapper:\n%s", joined)
	}
	if !strings.Contains(joined, `name="_action"`) {
		t.Fatalf("chunks missing hidden _action input:\n%s", joined)
	}
}

func TestRouteStateVars(t *testing.T) {
	pkg, _ := routeFixture()
	svs := routeStateVars(pkg, nil)
	var count *codegen.StateVar
	for i := range svs {
		if svs[i].Name == "count" {
			count = &svs[i]
		}
	}
	if count == nil {
		t.Fatalf("count not in StateVars: %+v", svs)
	}
	if count.Type == nil || count.Type.Kind != ir.TypeInt {
		t.Fatalf("count type want int, got %v", count.Type)
	}
}

func TestCollectActionsLogicalMutations(t *testing.T) {
	pkg, win := routeFixture()
	targets := buildNativeFuncMap(pkg, "go")
	actions, _ := collectActions(pkg, win, targets)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	// Mutations (legacy) keeps the full block including the DOM patch.
	if len(actions[0].Mutations) != 2 {
		t.Fatalf("Mutations want full 2-stmt block, got %d", len(actions[0].Mutations))
	}
	// LogicalMutations drops the DOM-patch statement.
	logical := actions[0].LogicalMutations
	if len(logical) != 1 {
		t.Fatalf("expected 1 logical mutation, got %d: %+v", len(logical), logical)
	}
	a, ok := logical[0].(*ir.Assign)
	if !ok {
		t.Fatalf("logical mutation not an Assign: %T", logical[0])
	}
	id, ok := a.Target.(*ir.Ident)
	if !ok || id.Name != "count" || id.IsElementRef {
		t.Fatalf("logical mutation target want Ident(count), got %s", debugExpr(a.Target))
	}
	for _, s := range logical {
		if isDOMPatchStmt(s) {
			t.Fatalf("DOM-patch statement leaked into LogicalMutations")
		}
	}
}

// TestActionIndexSingleSourceOfTruth pins fix #1: the form's hidden _action
// value must equal the index of the POST switch case (the action slice index)
// that runs that handler's mutations. The regression case mixes a backend
// var-handler (which mints an action but emits NO form) with a backend node
// handler — under the old independent per-node counter the node form got index
// 0 while collectActions assigned the node handler index 1, a silent desync.
func TestActionIndexSingleSourceOfTruth(t *testing.T) {
	const importPath = "example.com/route-post/api"
	persist := &ir.Func{Name: "Persist", Foreign: ir.Foreign{Path: importPath}}
	pkg := &ir.Package{
		Imports: []*ir.Import{{
			Alias:  "api",
			AST:    &ast.Import{Path: "go:" + importPath},
			Native: &ir.NativeImport{ImportPath: importPath, Funcs: []*ir.Func{persist}},
		}},
	}
	countVar := &ir.Var{Name: "count", Type: ir.TypInt, Init: &ir.Literal{Type: ir.TypInt, Value: "0"}}
	pkg.Vars = []*ir.Var{countVar}

	backendBody := func() []ir.Stmt {
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Ident{Name: "count", Type: ir.TypInt},
			Op:     ast.AssignSet,
			Value: &ir.Call{
				Func: persist,
				Args: []ir.CallArg{{Value: &ir.Ident{Name: "count", Type: ir.TypInt}}},
			},
		}}
	}

	// Backend var-handler: mints action index 0, emits no form.
	varHandler := &ir.EventHandler{Name: "change", Func: &ir.Func{Block: backendBody()}}
	countVar.Handlers = []*ir.EventHandler{varHandler}

	// Backend node handler: must get action index 1.
	nodeHandler := ir.EventHandler{Name: "click", Func: &ir.Func{Block: backendBody()}}
	button := &ir.NodeInst{
		Name:     "button",
		Props:    []ir.Arg{{Name: "text", Value: &ir.Literal{Type: ir.TypString, Value: `"Save"`}}},
		Handlers: []ir.EventHandler{nodeHandler},
	}
	vbox := &ir.NodeInst{Name: "vbox", Children: []ir.Stmt{button}}
	win := &codegen.WindowCtx{Name: "app", Vars: []*ir.Var{countVar}, Body: []ir.Stmt{vbox}}

	targets := buildNativeFuncMap(pkg, "go")
	actions, actionIdx := collectActions(pkg, win, targets)
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (var + node handler), got %d", len(actions))
	}
	// The node handler must map to switch case 1, not 0.
	if got := actionIdx[&button.Handlers[0]]; got != 1 {
		t.Fatalf("node handler action index: want 1, got %d", got)
	}

	rr, err := buildRenderModel(pkg, win, "/", actionIdx)
	if err != nil {
		t.Fatalf("buildRenderModel: %v", err)
	}
	joined := strings.Join(rr.Chunks, "")
	// The single emitted form (for the node handler) must carry _action="1".
	if !strings.Contains(joined, `name="_action" value="1"`) {
		t.Fatalf("form _action value must equal node handler switch case 1:\n%s", joined)
	}
	if strings.Contains(joined, `name="_action" value="0"`) {
		t.Fatalf("form must not carry the var-handler's index 0 (desync):\n%s", joined)
	}
}

func exprReadsVar(e ir.Expr, name string) bool {
	found := false
	ir.WalkExprs(e, func(x ir.Expr) error {
		if id, ok := x.(*ir.Ident); ok && id.Name == name && !id.IsElementRef {
			found = true
		}
		return nil
	})
	return found
}

func debugExpr(e ir.Expr) string {
	switch x := e.(type) {
	case *ir.Ident:
		return "Ident(" + x.Name + ")"
	case *ir.Select:
		return "Select(." + x.Field + ")"
	}
	return "?"
}
