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
			Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
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
	}
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	pkg.Vars = []*ir.Var{outerN}

	body := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
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
