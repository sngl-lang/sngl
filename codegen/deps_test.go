package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// makeVar constructs a minimal *ir.Var for testing.
func makeVar(name string) *ir.Var {
	return &ir.Var{Name: name, Type: &ir.Type{Kind: ir.TypeInt}}
}

// makeComp constructs a minimal *ir.Component for testing.
func makeComp(name string, vars ...*ir.Var) *ir.Component {
	return &ir.Component{Name: name, Vars: vars}
}

// asSet returns a set with the given vars.
func asSet(vars ...*ir.Var) map[ir.Symbol]struct{} {
	s := make(map[ir.Symbol]struct{}, len(vars))
	for _, v := range vars {
		s[v] = struct{}{}
	}
	return s
}

func equalVarSet(a, b map[ir.Symbol]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func TestExtractor_BareIdent(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	dt := NewDepTrackerFromPkg(pkg)

	// Expression: `x`
	expr := &ir.Ident{Name: "x", Sym: x}

	got := dt.ExprDeps(comp, expr)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractor_SubstituteOneCall verifies that deps are resolved through a
// function call by substituting the caller's argument for the callee's param.
func TestExtractor_SubstituteOneCall(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	// func double(c main) => c.x * 2
	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	body := &ir.Binary{
		Op: ast.BinMul,
		Left: &ir.Select{
			Operand: &ir.Ident{Name: "c", Sym: cParam},
			Field:   "x",
		},
		Right: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Value: "2"},
	}
	double := &ir.Func{
		Name:     "double",
		Receiver: "main",
		Params:   []*ir.Param{cParam},
		Block:    []ir.Stmt{&ir.Return{Value: body}},
		AST:      &ast.FuncDef{},
	}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{double}}
	dt := NewDepTrackerFromPkg(pkg)

	cInst := &ir.Ident{Name: "self", Sym: comp}
	call := &ir.Call{Func: double, Args: []ir.CallArg{{Value: cInst}}}

	got := dt.ExprDeps(comp, call)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractor_ImplicitThis verifies that `this.x` resolves to the owning
// component's var when no explicit binding is in scope.
func TestExtractor_ImplicitThis(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)
	expr := &ir.Select{
		Operand: &ir.Ident{Name: "this"},
		Field:   "x",
	}
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	dt := NewDepTrackerFromPkg(pkg)

	got := dt.ExprDeps(comp, expr)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractor_WriteThroughCompParam verifies that a write to a field of a
// component-typed param propagates back to the caller's var.
func TestExtractor_WriteThroughCompParam(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	body := &ir.Assign{
		Target: &ir.Select{
			Operand: &ir.Ident{Name: "c", Sym: cParam},
			Field:   "x",
		},
		Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Value: "0"},
	}
	reset := &ir.Func{
		Name:     "reset",
		Receiver: "main",
		Params:   []*ir.Param{cParam},
		Block:    []ir.Stmt{body},
		AST:      &ast.FuncDef{},
	}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{reset}}
	dt := NewDepTrackerFromPkg(pkg)

	cInst := &ir.Ident{Name: "self", Sym: comp}
	callStmt := &ir.CallStmt{
		Call: &ir.Call{Func: reset, Args: []ir.CallArg{{Value: cInst}}},
	}

	got := MutatedFields(comp, dt, callStmt)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractor_WriteThroughValueStructParam verifies that writes through a
// value-typed struct param do NOT propagate back to the caller.
func TestExtractor_WriteThroughValueStructParam(t *testing.T) {
	pointDef := &ir.StructDef{Name: "Point", Fields: []*ir.StructField{
		{Name: "x", Type: &ir.Type{Kind: ir.TypeInt}},
	}}
	pointType := &ir.Type{Kind: ir.TypeStruct, Decl: pointDef}

	p := &ir.Var{Name: "p", Type: pointType}
	comp := makeComp("main", p)

	arg := &ir.Param{Name: "arg", Type: pointType}
	body := &ir.Assign{
		Target: &ir.Select{Operand: &ir.Ident{Name: "arg", Sym: arg}, Field: "x"},
		Value:  &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Value: "0"},
	}
	mutate := &ir.Func{
		Name:   "mutate",
		Params: []*ir.Param{arg},
		Block:  []ir.Stmt{body},
		AST:    &ast.FuncDef{},
	}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{mutate}}
	dt := NewDepTrackerFromPkg(pkg)

	pInst := &ir.Ident{Name: "p", Sym: p}
	callStmt := &ir.CallStmt{
		Call: &ir.Call{Func: mutate, Args: []ir.CallArg{{Value: pInst}}},
	}

	got := MutatedFields(comp, dt, callStmt)
	if len(got) != 0 {
		t.Errorf("write through value-typed struct param should not propagate; got %v", got)
	}
}

// TestDepsPropBindingTarget verifies that a PropBinding's Target is tracked as
// a write-dep when walking a NodeInst statement.
func TestDepsPropBindingTarget(t *testing.T) {
	steps := makeVar("steps")
	comp := makeComp("main", steps)
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	dt := NewDepTrackerFromPkg(pkg)

	target := &ir.Ident{Name: "steps", Sym: steps}
	n := &ir.NodeInst{
		Name: "Stepper",
		Bindings: []ir.PropBinding{
			{PropName: "count", Target: target},
		},
	}

	got := MutatedFields(comp, dt, n)
	want := asSet(steps)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestExtractor_Recursion verifies the recursion guard prevents infinite loops
// when a function calls itself, and that an empty dep set is returned.
func TestExtractor_Recursion(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	loop := &ir.Func{
		Name:     "loop",
		Receiver: "main",
		Params:   []*ir.Param{cParam},
		AST:      &ast.FuncDef{},
	}
	loop.Block = []ir.Stmt{&ir.Return{Value: &ir.Call{
		Func: loop,
		Args: []ir.CallArg{{Value: &ir.Ident{Name: "c", Sym: cParam}}},
	}}}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{loop}}
	dt := NewDepTrackerFromPkg(pkg)

	cInst := &ir.Ident{Name: "self", Sym: comp}
	call := &ir.Call{Func: loop, Args: []ir.CallArg{{Value: cInst}}}

	// Must not stack-overflow. Result expected: empty (no var reads).
	got := dt.ExprDeps(comp, call)
	if len(got) != 0 {
		t.Errorf("unexpected deps: %v", got)
	}
}
