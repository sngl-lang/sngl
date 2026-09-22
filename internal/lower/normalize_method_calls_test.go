package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestNoImplicitRecv_SynthesizesComponentSelf(t *testing.T) {
	main := &ir.Component{Name: "main"}
	thisType := &ir.Type{Kind: ir.TypeComponent, Decl: main}

	status := &ir.Func{
		Receiver: "main",
		Name:     "status",
		Params:   []*ir.Param{{Name: "this", Type: thisType}},
	}

	call := &ir.Call{
		Func: status,
		Args: nil,
	}
	main.Body = []ir.Stmt{&ir.Return{Value: call}}
	main.Funcs = []*ir.Func{status}

	pkg := &ir.Package{Components: []*ir.Component{main}}

	if err := lowerNoImplicitRecv(pkg, without("implicitRecv"), Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 1 {
		t.Fatalf("Args after pass: got len=%d, want 1", len(call.Args))
	}
	got, ok := call.Args[0].Value.(*ir.Ident)
	if !ok {
		t.Fatalf("Args[0]: got %T, want *ir.Ident", call.Args[0].Value)
	}
	comp, ok := got.Sym.(*ir.Component)
	if !ok || comp != main {
		t.Errorf("Args[0].Sym: got %v, want *ir.Component{main}", got.Sym)
	}
}

func TestNoImplicitRecv_LeavesExplicitRecvUnchanged(t *testing.T) {
	main := &ir.Component{Name: "main"}
	thisType := &ir.Type{Kind: ir.TypeComponent, Decl: main}
	status := &ir.Func{
		Receiver: "main",
		Name:     "status",
		Params:   []*ir.Param{{Name: "this", Type: thisType}},
	}

	explicitRecv := &ir.Ident{Name: "self", Sym: main}
	call := &ir.Call{
		Func: status,
		Args: []ir.CallArg{{Value: explicitRecv}},
	}
	main.Body = []ir.Stmt{&ir.Return{Value: call}}
	main.Funcs = []*ir.Func{status}

	pkg := &ir.Package{Components: []*ir.Component{main}}

	if err := lowerNoImplicitRecv(pkg, without("implicitRecv"), Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 1 {
		t.Errorf("Args length: got %d, want 1 (unchanged)", len(call.Args))
	}
	if call.Args[0].Value != explicitRecv {
		t.Errorf("Args[0].Value: was reassigned; pass should leave Pattern B alone")
	}
}

func TestNoImplicitRecv_IgnoresFreeFunctionCalls(t *testing.T) {
	add := &ir.Func{
		Receiver: "",
		Name:     "add",
		Params: []*ir.Param{
			{Name: "a", Type: &ir.Type{Kind: ir.TypeInt}},
			{Name: "b", Type: &ir.Type{Kind: ir.TypeInt}},
		},
	}
	call := &ir.Call{
		Func: add,
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Value: "1"}},
			{Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}, Value: "2"}},
		},
	}
	main := &ir.Component{Name: "main", Body: []ir.Stmt{&ir.Return{Value: call}}}
	pkg := &ir.Package{Components: []*ir.Component{main}, Funcs: []*ir.Func{add}}

	if err := lowerNoImplicitRecv(pkg, without("implicitRecv"), Options{}); err != nil {
		t.Fatal(err)
	}

	if len(call.Args) != 2 {
		t.Errorf("free-function Args length changed: got %d, want 2", len(call.Args))
	}
}
