package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// funcType returns a minimal funcvar type: () -> string.
func funcType() *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: ir.TypString}}
}

// funcTypeNoArgs returns a minimal funcvar type with no return value.
func funcTypeNoArgs() *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{}}
}

// identTo builds an *ir.Ident referring to the given Symbol.
func identTo(sym ir.Symbol) *ir.Ident {
	return &ir.Ident{Name: sym.SymName(), Sym: sym, Type: sym.SymType()}
}

// equalConstraint compares two constraint values for testing.
func equalConstraint(a, b constraint) bool {
	if a.dst != b.dst {
		return false
	}
	if len(a.funcs) != len(b.funcs) || len(a.srcs) != len(b.srcs) {
		return false
	}
	for i := range a.funcs {
		if a.funcs[i] != b.funcs[i] {
			return false
		}
	}
	for i := range a.srcs {
		if a.srcs[i] != b.srcs[i] {
			return false
		}
	}
	return true
}

// TestCollectConstraints_DirectAssign: var v = syncFn → constraint (SlotVarKey(v), {syncFn}).
func TestCollectConstraints_DirectAssign(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{
		Name: "v",
		Type: funcType(),
		Init: &ir.Ident{Name: "syncFn", Sym: syncFn, Type: funcType()},
	}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{syncFn}}

	cs := collectConstraints(pkg)
	want := constraint{dst: ir.SlotVarKey(v), funcs: []*ir.Func{syncFn}}
	if len(cs) != 1 || !equalConstraint(cs[0], want) {
		t.Fatalf("got %+v, want [%+v]", cs, want)
	}
}

// TestCollectConstraints_FuncvarParamPassthrough: g(syncFn) where g's param is funcvar
// → constraint (SlotParamKey(g.Params[0]), {syncFn}).
func TestCollectConstraints_FuncvarParamPassthrough(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	p := &ir.Param{Name: "fn", Type: funcType()}
	g := &ir.Func{
		Name:   "g",
		Params: []*ir.Param{p},
		Block:  []ir.Stmt{},
	}
	// caller func: { g(syncFn) }
	caller := &ir.Func{
		Name: "caller",
		Block: []ir.Stmt{
			&ir.CallStmt{
				Call: &ir.Call{
					Func: g,
					Args: []ir.CallArg{{Value: identTo(syncFn)}},
					Type: ir.TypVoid,
				},
			},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{syncFn, g, caller}}

	cs := collectConstraints(pkg)
	want := constraint{dst: ir.SlotParamKey(p), funcs: []*ir.Func{syncFn}}
	for _, c := range cs {
		if equalConstraint(c, want) {
			return
		}
	}
	t.Fatalf("expected constraint %+v in %+v", want, cs)
}

// TestCollectConstraints_AssignFuncvar: var v = syncFn; var w = v
// → two constraints: pts(v)⊇{syncFn} and pts(w)⊇pts(v).
func TestCollectConstraints_AssignFuncvar(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{Name: "v", Type: funcType(), Init: identTo(syncFn)}
	w := &ir.Var{Name: "w", Type: funcType(), Init: identTo(v)}
	pkg := &ir.Package{Vars: []*ir.Var{v, w}, Funcs: []*ir.Func{syncFn}}

	cs := collectConstraints(pkg)
	wantV := constraint{dst: ir.SlotVarKey(v), funcs: []*ir.Func{syncFn}}
	wantW := constraint{dst: ir.SlotVarKey(w), srcs: []ir.PointsToKey{ir.SlotVarKey(v)}}

	foundV, foundW := false, false
	for _, c := range cs {
		if equalConstraint(c, wantV) {
			foundV = true
		}
		if equalConstraint(c, wantW) {
			foundW = true
		}
	}
	if !foundV || !foundW {
		t.Fatalf("missing constraints; got %+v\nwantV=%+v foundV=%v\nwantW=%+v foundW=%v",
			cs, wantV, foundV, wantW, foundW)
	}
}

// TestCollectConstraints_StructField: assignment into s.onClick where s is a struct.
// Constraint targets SlotFieldKey(StructType, "onClick").
func TestCollectConstraints_StructField(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	sd := &ir.StructDef{Name: "Handler", Fields: []*ir.StructField{
		{Name: "onClick", Type: funcType()},
	}}
	structType := &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	sVar := &ir.Var{Name: "s", Type: structType}

	selectExpr := &ir.Select{
		Operand: identTo(sVar),
		Field:   "onClick",
		Type:    funcType(),
	}
	assign := &ir.Assign{
		Target: selectExpr,
		Value:  identTo(syncFn),
	}
	fn := &ir.Func{
		Name:  "setup",
		Block: []ir.Stmt{assign},
	}
	pkg := &ir.Package{
		Vars:  []*ir.Var{sVar},
		Funcs: []*ir.Func{syncFn, fn},
	}

	cs := collectConstraints(pkg)
	want := constraint{dst: ir.SlotFieldKey(structType, "onClick"), funcs: []*ir.Func{syncFn}}
	for _, c := range cs {
		if equalConstraint(c, want) {
			return
		}
	}
	t.Fatalf("expected constraint %+v in %+v", want, cs)
}

// TestCollectConstraints_ListElem: var hs: list<() -> string> = [syncFn, asyncFn]
// → both candidates flow into SlotListElemKey(ListType).
func TestCollectConstraints_ListElem(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	listType := &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{funcType()}}
	v := &ir.Var{
		Name: "hs",
		Type: listType,
		Init: &ir.ListLit{
			Type:  listType,
			Elems: []ir.Expr{identTo(syncFn), identTo(asyncFn)},
		},
	}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{syncFn, asyncFn}}

	cs := collectConstraints(pkg)
	wantSync := constraint{dst: ir.SlotListElemKey(listType), funcs: []*ir.Func{syncFn}}
	wantAsync := constraint{dst: ir.SlotListElemKey(listType), funcs: []*ir.Func{asyncFn}}
	foundSync, foundAsync := false, false
	for _, c := range cs {
		if equalConstraint(c, wantSync) {
			foundSync = true
		}
		if equalConstraint(c, wantAsync) {
			foundAsync = true
		}
	}
	if !foundSync || !foundAsync {
		t.Fatalf("missing constraints; got %+v", cs)
	}
}

// TestCollectConstraints_ReturnFuncvar: func pick() => asyncFn
// → SlotReturnKey(pick) ⊇ {asyncFn}.
func TestCollectConstraints_ReturnFuncvar(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	pick := &ir.Func{
		Name:   "pick",
		Return: funcType(),
		Block: []ir.Stmt{
			&ir.Return{Value: identTo(asyncFn)},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{asyncFn, pick}}

	cs := collectConstraints(pkg)
	want := constraint{dst: ir.SlotReturnKey(pick), funcs: []*ir.Func{asyncFn}}
	for _, c := range cs {
		if equalConstraint(c, want) {
			return
		}
	}
	t.Fatalf("expected constraint %+v in %+v", want, cs)
}
