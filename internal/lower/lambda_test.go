package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestAnalyzeCapturesImmutableRead(t *testing.T) {
	// Lambda body reads outer `n` once; n is read-only inside the body.
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt}},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 1 {
		t.Fatalf("want 1 capture, got %d", len(caps))
	}
	if caps[0].Sym != outerN {
		t.Errorf("captured wrong sym")
	}
	if caps[0].Mutable {
		t.Errorf("read-only capture marked mutable")
	}
}

func TestAnalyzeCapturesMutableWrite(t *testing.T) {
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value:  &ir.Literal{Type: ir.TypInt, Value: "1"},
		},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 1 || !caps[0].Mutable {
		t.Errorf("expected mutable capture; got %+v", caps)
	}
}

func TestAnalyzeCapturesParamNotCaptured(t *testing.T) {
	p := &ir.Param{Name: "x", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "x", Sym: p, Type: ir.TypInt}},
	}
	caps := analyzeCaptures(body, []*ir.Param{p})
	if len(caps) != 0 {
		t.Errorf("param should not be captured: %+v", caps)
	}
}

func TestAnalyzeCapturesLoopVar(t *testing.T) {
	// Lambda body reads outer for-loop variable `x`. LoopVar is a Sym kind
	// that should be captured (read-only — loop vars are immutable per
	// iteration in SNGL).
	loopX := &ir.LoopVar{Name: "x", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "x", Sym: loopX, Type: ir.TypInt}},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 1 || caps[0].Sym != loopX {
		t.Fatalf("expected LoopVar to be captured; got %+v", caps)
	}
	if caps[0].Mutable {
		t.Errorf("LoopVar should be read-only, not mutable")
	}
}

func TestAnalyzeCapturesOrderStable(t *testing.T) {
	a := &ir.Var{Name: "a", Type: ir.TypInt}
	b := &ir.Var{Name: "b", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Binary{
			Op:    0, // any
			Left:  &ir.Ident{Name: "a", Sym: a, Type: ir.TypInt},
			Right: &ir.Ident{Name: "b", Sym: b, Type: ir.TypInt},
			Type:  ir.TypInt,
		}},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 2 || caps[0].Sym != a || caps[1].Sym != b {
		t.Errorf("order unstable: %+v", caps)
	}
}

func TestLifterBasicReadOnlyCapture(t *testing.T) {
	pkg := &ir.Package{
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	pkg.Vars = []*ir.Var{outerN}

	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt}},
	}
	l := &lifter{pkg: pkg}
	cl := l.Lift(body, nil, ir.TypInt, nil)

	if cl == nil || cl.Func == nil || cl.State == nil {
		t.Fatalf("Lift returned bad shape: %+v", cl)
	}
	if len(cl.Func.Params) != 1 {
		t.Errorf("lifted Func should have one (state) param; got %d", len(cl.Func.Params))
	}
	if cl.State.Def == nil || len(cl.State.Def.Fields) != 1 {
		t.Errorf("state struct should have one field; got %+v", cl.State.Def)
	}
	field := cl.State.Def.Fields[0]
	if field.Type.Kind != ir.TypeInt {
		t.Errorf("read-only capture field should be int, got %v", field.Type.Kind)
	}
	if len(pkg.Funcs) != 1 || pkg.Funcs[0] != cl.Func {
		t.Errorf("lifted Func not appended to pkg.Funcs")
	}
	if len(pkg.Structs) != 1 || pkg.Structs[0] != cl.State.Def {
		t.Errorf("state struct not appended to pkg.Structs")
	}
	capMap := pkg.LiftedCaptures[cl.Func]
	if capMap[outerN] != "n" {
		t.Errorf("LiftedCaptures missing entry for outerN")
	}
}

func TestLifterMutableCaptureUsesRefType(t *testing.T) {
	pkg := &ir.Package{
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	pkg.Vars = []*ir.Var{outerN}

	body := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value:  &ir.Literal{Type: ir.TypInt, Value: "1"},
		},
	}
	l := &lifter{pkg: pkg}
	cl := l.Lift(body, nil, nil, nil)

	field := cl.State.Def.Fields[0]
	if field.Type.Kind != ir.TypeRef {
		t.Errorf("mutable capture field should be ref<int>, got %v", field.Type.Kind)
	}
	if len(field.Type.Elems) == 0 || field.Type.Elems[0] == nil || field.Type.Elems[0].Kind != ir.TypeInt {
		t.Errorf("ref elem should be int, got %v", field.Type.Elems)
	}
	// State init field value must be Unary{UnaryAddr, Ident{outerN}}.
	init := cl.State.Fields[0].Value
	u, ok := init.(*ir.Unary)
	if !ok || u.Op != ast.UnaryAddr {
		t.Errorf("mutable capture init should be &outerN; got %T", init)
	}
}

func TestLifterFreshNameCollision(t *testing.T) {
	// pkg already has a struct named __lambda0_caps; lifter must skip past it.
	pkg := &ir.Package{
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
		Structs: []*ir.StructDef{
			{Name: "__lambda0_caps"},
		},
	}
	body := []ir.Stmt{}
	l := &lifter{pkg: pkg}
	cl := l.Lift(body, nil, nil, nil)
	if cl.State.Def.Name == "__lambda0_caps" {
		t.Errorf("lifter did not skip existing name")
	}
}

func TestAnalyzeCapturesNestedLambda(t *testing.T) {
	// Outer lambda's body contains an inner lambda that mutates `n`.
	// analyzeCaptures on the outer body must see `n` as a capture marked
	// Mutable (so the outer lifts it as ref<int> and the mutation can
	// propagate through both frames).
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	innerBody := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value: &ir.Binary{
				Left:  &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
				Right: &ir.Literal{Type: ir.TypInt, Value: "1"},
				Type:  ir.TypInt,
			},
		},
	}
	innerLambda := &ir.Lambda{
		Func: &ir.Func{
			Block: innerBody,
		},
	}
	outerBody := []ir.Stmt{
		&ir.Return{Value: innerLambda},
	}
	caps := analyzeCaptures(outerBody, nil)
	if len(caps) != 1 {
		t.Fatalf("outer should capture n via inner; got %d caps: %+v", len(caps), caps)
	}
	if caps[0].Sym != outerN {
		t.Errorf("captured wrong sym: %v", caps[0].Sym)
	}
	if !caps[0].Mutable {
		t.Errorf("inner mutates n; outer's capture must be Mutable=true")
	}
}

func TestLifterNestedClosureSharesRef(t *testing.T) {
	// Equivalent to fixture lambda_nested.txtar:
	//   var n int = 0
	//   var outer = func() { return func() { n = n + 1 } }
	// After lifting, both frames' caps fields for n must be ref<int>, and
	// the inner closure's caps init must reference state.n (the bare Select
	// from the outer frame), not &n directly.
	pkg := &ir.Package{
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	pkg.Vars = []*ir.Var{outerN}

	innerBody := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value: &ir.Binary{
				Left:  &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
				Right: &ir.Literal{Type: ir.TypInt, Value: "1"},
				Type:  ir.TypInt,
			},
		},
	}
	innerLambda := &ir.Lambda{
		Func: &ir.Func{
			Block: innerBody,
		},
	}
	outerBody := []ir.Stmt{
		&ir.Return{Value: innerLambda},
	}

	l := &lifter{pkg: pkg}
	outerCl := l.Lift(outerBody, nil, nil, nil)

	// Outer's caps: must have one field, n, of type ref<int>.
	if len(outerCl.State.Def.Fields) != 1 {
		t.Fatalf("outer caps: want 1 field, got %d", len(outerCl.State.Def.Fields))
	}
	outerField := outerCl.State.Def.Fields[0]
	if outerField.Name != "n" || outerField.Type.Kind != ir.TypeRef {
		t.Errorf("outer caps n: want ref<int>, got %s %v", outerField.Name, outerField.Type)
	}

	// Outer's caps init for n: &n (Unary{Addr, Ident{outerN}}).
	outerInit := outerCl.State.Fields[0].Value
	uOuter, ok := outerInit.(*ir.Unary)
	if !ok || uOuter.Op != ast.UnaryAddr {
		t.Fatalf("outer caps init: want &n; got %T", outerInit)
	}

	// Outer's lifted body should be: return __closure(innerFunc, innerCaps{n = state.n})
	if len(outerCl.Func.Block) != 1 {
		t.Fatalf("outer body: want 1 stmt, got %d", len(outerCl.Func.Block))
	}
	ret, ok := outerCl.Func.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("outer body[0]: want Return, got %T", outerCl.Func.Block[0])
	}
	innerCl, ok := ret.Value.(*ir.Closure)
	if !ok {
		t.Fatalf("outer Return.Value: want Closure (inner lifted), got %T", ret.Value)
	}

	// Inner's caps: one field, n, ref<int>.
	if len(innerCl.State.Def.Fields) != 1 {
		t.Fatalf("inner caps: want 1 field, got %d", len(innerCl.State.Def.Fields))
	}
	innerField := innerCl.State.Def.Fields[0]
	if innerField.Name != "n" || innerField.Type.Kind != ir.TypeRef {
		t.Errorf("inner caps n: want ref<int>, got %s %v", innerField.Name, innerField.Type)
	}

	// Inner's caps init for n: bare Select{state, n} (typed ref<int>),
	// NOT &n and NOT *state.n.
	innerInit := innerCl.State.Fields[0].Value
	sel, ok := innerInit.(*ir.Select)
	if !ok {
		t.Fatalf("inner caps init: want bare Select{state, n}; got %T (%v)", innerInit, innerInit)
	}
	if sel.Field != "n" {
		t.Errorf("inner caps init Select.Field: want n, got %s", sel.Field)
	}
	if sel.Type == nil || sel.Type.Kind != ir.TypeRef {
		t.Errorf("inner caps init Select.Type: want ref<int>, got %v", sel.Type)
	}

	// Inner's body assignments must deref state.n: *state.n = *state.n + 1.
	if len(innerCl.Func.Block) != 1 {
		t.Fatalf("inner body: want 1 stmt, got %d", len(innerCl.Func.Block))
	}
	asg, ok := innerCl.Func.Block[0].(*ir.Assign)
	if !ok {
		t.Fatalf("inner body[0]: want Assign, got %T", innerCl.Func.Block[0])
	}
	target, ok := asg.Target.(*ir.Unary)
	if !ok || target.Op != ast.UnaryDeref {
		t.Errorf("inner body assign target: want *state.n, got %T", asg.Target)
	}
}
