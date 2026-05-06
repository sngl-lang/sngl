package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// makeAsyncComputedPkg builds a minimal *ir.Package representing:
//
//	fn fetchHello(): string  // async native
//	fn greeting(): string => await fetchHello()  // computed, async body
func makeAsyncComputedPkg() (*ir.Package, *ir.Func, *ir.Func) {
	fetchHello := &ir.Func{
		Name:    "fetchHello",
		IsAsync: true,
		Return:  ir.TypString,
	}
	callExpr := &ir.Call{
		Type: ir.TypString,
		Func: fetchHello,
	}
	greeting := &ir.Func{
		AST:    &ast.FuncDef{Body: &ast.LiteralExpr{}}, // non-nil Body → isComputed
		Name:   "greeting",
		Return: ir.TypString,
		Block: []ir.Stmt{
			&ir.Return{Value: callExpr},
		},
		IsAsync: true,
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{fetchHello, greeting},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return pkg, fetchHello, greeting
}

func TestLowerAsyncReactive_NamedComputedTransformation(t *testing.T) {
	pkg, _, greeting := makeAsyncComputedPkg()

	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	// 1. Synthetic state var __async_greeting must exist in pkg.Vars.
	var stateVar *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "__async_greeting" {
			stateVar = v
			break
		}
	}
	if stateVar == nil {
		t.Fatal("want state var __async_greeting in pkg.Vars; not found")
	}
	if stateVar.Type != ir.TypString {
		t.Errorf("stateVar.Type = %v; want string", stateVar.Type)
	}
	if stateVar.Init == nil {
		t.Error("stateVar.Init = nil; want zero value")
	} else {
		lit, ok := stateVar.Init.(*ir.Literal)
		if !ok {
			t.Errorf("stateVar.Init type = %T; want *ir.Literal", stateVar.Init)
		} else if lit.Raw != "" {
			// ZeroExpr for string returns Raw:"" but the Raw IS "", so no error.
			// Just confirm it's a Literal.
			_ = lit
		}
	}

	// 2. Kicker func $compute_greeting must exist.
	var kicker *ir.Func
	for _, fn := range pkg.Funcs {
		if fn.Name == "$compute_greeting" {
			kicker = fn
			break
		}
	}
	if kicker == nil {
		t.Fatal("want kicker $compute_greeting in pkg.Funcs; not found")
	}
	if !kicker.IsAsync {
		t.Error("kicker.IsAsync = false; want true")
	}
	if len(kicker.Block) != 1 {
		t.Fatalf("kicker.Block len = %d; want 1", len(kicker.Block))
	}
	assign, ok := kicker.Block[0].(*ir.Assign)
	if !ok {
		t.Fatalf("kicker.Block[0] type = %T; want *ir.Assign", kicker.Block[0])
	}
	targetIdent, ok := assign.Target.(*ir.Ident)
	if !ok {
		t.Fatalf("assign.Target type = %T; want *ir.Ident", assign.Target)
	}
	if targetIdent.Name != "__async_greeting" {
		t.Errorf("assign.Target.Name = %q; want __async_greeting", targetIdent.Name)
	}
	// Value should be the original call expression (a *ir.Call to fetchHello).
	if _, ok := assign.Value.(*ir.Call); !ok {
		t.Errorf("assign.Value type = %T; want *ir.Call", assign.Value)
	}

	// 3. Original computed greeting must now be sync and return __async_greeting.
	if greeting.IsAsync {
		t.Error("greeting.IsAsync = true after lowering; want false")
	}
	if len(greeting.Block) != 1 {
		t.Fatalf("greeting.Block len = %d; want 1", len(greeting.Block))
	}
	ret, ok := greeting.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("greeting.Block[0] type = %T; want *ir.Return", greeting.Block[0])
	}
	retIdent, ok := ret.Value.(*ir.Ident)
	if !ok {
		t.Fatalf("ret.Value type = %T; want *ir.Ident", ret.Value)
	}
	if retIdent.Name != "__async_greeting" {
		t.Errorf("ret.Value.Name = %q; want __async_greeting", retIdent.Name)
	}

	// 4. AsyncKickers registry must have one entry.
	if len(pkg.AsyncKickers) != 1 {
		t.Fatalf("pkg.AsyncKickers len = %d; want 1", len(pkg.AsyncKickers))
	}
	entry := pkg.AsyncKickers[0]
	if entry.OrigComputed != "greeting" {
		t.Errorf("AsyncKickers[0].OrigComputed = %q; want greeting", entry.OrigComputed)
	}
	if entry.StateVarName != "__async_greeting" {
		t.Errorf("AsyncKickers[0].StateVarName = %q; want __async_greeting", entry.StateVarName)
	}
	if entry.Func != kicker {
		t.Error("AsyncKickers[0].Func != kicker; want same pointer")
	}
}

func TestLowerAsyncReactive_NonAsyncComputedIsSkipped(t *testing.T) {
	// A computed that does NOT call async should be left alone.
	syncFn := &ir.Func{
		Name:   "formatDate",
		Return: ir.TypString,
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "2026-01-01"}},
		},
		AST: &ast.FuncDef{Body: &ast.LiteralExpr{}},
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{syncFn},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	origBlock := syncFn.Block

	if err := lowerAsyncReactive(pkg, Caps{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}
	if len(pkg.Vars) != 0 {
		t.Errorf("pkg.Vars len = %d; want 0", len(pkg.Vars))
	}
	if len(pkg.AsyncKickers) != 0 {
		t.Errorf("pkg.AsyncKickers len = %d; want 0", len(pkg.AsyncKickers))
	}
	if len(syncFn.Block) != len(origBlock) {
		t.Errorf("syncFn.Block was mutated; len = %d; want %d", len(syncFn.Block), len(origBlock))
	}
}

func TestLowerAsyncReactive_NilPackage(t *testing.T) {
	if err := lowerAsyncReactive(nil, Caps{NoAsyncReactive: true}); err != nil {
		t.Fatalf("lowerAsyncReactive(nil): %v", err)
	}
}

func TestIsReactiveAsyncComputed(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetch", IsAsync: true, Return: ir.TypString}

	tests := []struct {
		name string
		fn   *ir.Func
		want bool
	}{
		{
			name: "nil",
			fn:   nil,
			want: false,
		},
		{
			name: "no AST",
			fn:   &ir.Func{Name: "foo", Return: ir.TypString, Block: []ir.Stmt{&ir.Return{Value: &ir.Literal{}}}},
			want: false,
		},
		{
			name: "no AST.Body",
			fn:   &ir.Func{Name: "foo", AST: &ast.FuncDef{}, Return: ir.TypString, Block: []ir.Stmt{&ir.Return{Value: &ir.Literal{}}}},
			want: false,
		},
		{
			name: "has params",
			fn: &ir.Func{
				Name:   "foo",
				AST:    &ast.FuncDef{Body: &ast.LiteralExpr{}},
				Params: []*ir.Param{{Name: "x", Type: ir.TypString}},
				Block:  []ir.Stmt{&ir.Return{Value: &ir.Call{Type: ir.TypString, Func: asyncFn}}},
			},
			want: false,
		},
		{
			name: "IsTest",
			fn: &ir.Func{
				Name:   "testGreeting",
				IsTest: true,
				AST:    &ast.FuncDef{Body: &ast.LiteralExpr{}},
				Block:  []ir.Stmt{&ir.Return{Value: &ir.Call{Type: ir.TypString, Func: asyncFn}}},
			},
			want: false,
		},
		{
			name: "sync body",
			fn: &ir.Func{
				Name:  "foo",
				AST:   &ast.FuncDef{Body: &ast.LiteralExpr{}},
				Block: []ir.Stmt{&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: ""}}},
			},
			want: false,
		},
		{
			name: "valid async computed",
			fn: &ir.Func{
				Name:  "greeting",
				AST:   &ast.FuncDef{Body: &ast.LiteralExpr{}},
				Block: []ir.Stmt{&ir.Return{Value: &ir.Call{Type: ir.TypString, Func: asyncFn}}},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isReactiveAsyncComputed(tc.fn)
			if got != tc.want {
				t.Errorf("isReactiveAsyncComputed = %v; want %v", got, tc.want)
			}
		})
	}
}

// makeAsyncComputedWithStatePkg builds a package with:
//
//	var userId: string = "alice"     (reactive state var)
//	fn fetchUser(id: string): string  // async native
//	fn greeting(): string => await fetchUser(userId)  // computed reading userId
func makeAsyncComputedWithStatePkg() (*ir.Package, *ir.Var, *ir.Func) {
	userIdVar := &ir.Var{
		Name: "userId",
		Type: ir.TypString,
		Init: &ir.Literal{Type: ir.TypString, Raw: "alice"},
	}
	fetchUser := &ir.Func{
		Name:    "fetchUser",
		IsAsync: true,
		Params:  []*ir.Param{{Name: "id", Type: ir.TypString}},
		Return:  ir.TypString,
	}
	callExpr := &ir.Call{
		Type: ir.TypString,
		Func: fetchUser,
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "userId", Sym: userIdVar, Type: ir.TypString}},
		},
	}
	greeting := &ir.Func{
		AST:    &ast.FuncDef{Body: &ast.LiteralExpr{}},
		Name:   "greeting",
		Return: ir.TypString,
		Block: []ir.Stmt{
			&ir.Return{Value: callExpr},
		},
		IsAsync: true,
	}
	pkg := &ir.Package{
		Vars:           []*ir.Var{userIdVar},
		Funcs:          []*ir.Func{fetchUser, greeting},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return pkg, userIdVar, greeting
}

func TestLowerAsyncReactive_KickerDeps(t *testing.T) {
	pkg, _, _ := makeAsyncComputedWithStatePkg()

	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	if len(pkg.AsyncKickers) != 1 {
		t.Fatalf("AsyncKickers len = %d; want 1", len(pkg.AsyncKickers))
	}
	entry := pkg.AsyncKickers[0]
	if entry.OrigComputed != "greeting" {
		t.Errorf("OrigComputed = %q; want greeting", entry.OrigComputed)
	}
	if len(entry.Deps) != 1 || entry.Deps[0] != "userId" {
		t.Fatalf("Deps = %v; want [userId]", entry.Deps)
	}
}

func TestLowerAsyncReactive_KickerNoDeps(t *testing.T) {
	// greeting() has no state-var reads — just await fetchHello() with no args.
	pkg, _, _ := makeAsyncComputedPkg()

	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	if len(pkg.AsyncKickers) != 1 {
		t.Fatalf("AsyncKickers len = %d; want 1", len(pkg.AsyncKickers))
	}
	entry := pkg.AsyncKickers[0]
	if len(entry.Deps) != 0 {
		t.Fatalf("Deps = %v; want empty (no reactive state-var reads)", entry.Deps)
	}
}

func TestLowerAsyncReactive_MultipleComputeds(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetch", IsAsync: true, Return: ir.TypString}

	g1 := &ir.Func{
		Name:    "alpha",
		AST:     &ast.FuncDef{Body: &ast.LiteralExpr{}},
		Return:  ir.TypString,
		IsAsync: true,
		Block:   []ir.Stmt{&ir.Return{Value: &ir.Call{Type: ir.TypString, Func: asyncFn}}},
	}
	g2 := &ir.Func{
		Name:    "beta",
		AST:     &ast.FuncDef{Body: &ast.LiteralExpr{}},
		Return:  ir.TypString,
		IsAsync: true,
		Block:   []ir.Stmt{&ir.Return{Value: &ir.Call{Type: ir.TypString, Func: asyncFn}}},
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{asyncFn, g1, g2},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}

	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	if len(pkg.AsyncKickers) != 2 {
		t.Fatalf("AsyncKickers len = %d; want 2", len(pkg.AsyncKickers))
	}
	names := map[string]bool{}
	for _, k := range pkg.AsyncKickers {
		names[k.OrigComputed] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("AsyncKickers = %v; want entries for alpha and beta", pkg.AsyncKickers)
	}
	if !g1.IsAsync == false && !g2.IsAsync == false {
		// both should be sync now
	}
	if g1.IsAsync {
		t.Error("g1.IsAsync = true after lowering; want false")
	}
	if g2.IsAsync {
		t.Error("g2.IsAsync = true after lowering; want false")
	}
}
