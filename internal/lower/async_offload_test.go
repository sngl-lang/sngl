package lower

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// blockingHandlerPkg is one handler: a state write, a blocking call whose
// answer is assigned, and a state write after it.
func blockingHandlerPkg() (*ir.Package, *ir.Func) {
	fetch := &ir.Func{Name: "fetch", IsAsync: true, Return: ir.TypString}
	busy := &ir.Var{Name: "busy", Type: ir.TypBool}
	out := &ir.Var{Name: "out", Type: ir.TypString}
	handler := &ir.Func{
		Name:   "click",
		Return: ir.TypVoid,
		Block: []ir.Stmt{
			&ir.Assign{Target: &ir.Ident{Name: "busy", Sym: busy, Type: ir.TypBool}, Value: &ir.Literal{Type: ir.TypBool, Value: "true"}},
			&ir.Assign{
				Target: &ir.Ident{Name: "out", Sym: out, Type: ir.TypString},
				Value:  &ir.Call{Func: fetch, Type: ir.TypString},
			},
			&ir.Assign{Target: &ir.Ident{Name: "busy", Sym: busy, Type: ir.TypBool}, Value: &ir.Literal{Type: ir.TypBool, Value: "false"}},
		},
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{fetch, handler},
		Vars:           []*ir.Var{busy, out},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return pkg, handler
}

// The split is three regions, and which region a statement lands in is the
// whole claim: what ran before the blocking call keeps running on the thread
// that draws, the call itself does not, and what depends on its answer waits
// for it.
func TestABlockingCallSplitsTheBodyInThree(t *testing.T) {
	pkg, handler := blockingHandlerPkg()
	caps := Caps{NoAsyncCalls: true, AsyncPost: true}
	if err := applyAsyncOffload(pkg, caps, Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	if len(handler.Block) != 2 {
		t.Fatalf("handler has %d statements, want the write that stayed put and the spawn", len(handler.Block))
	}
	if _, ok := handler.Block[0].(*ir.Assign); !ok {
		t.Errorf("first statement is %T; the write before the blocking call must stay where it was written", handler.Block[0])
	}
	spawn := intrinsicClosure(t, handler.Block[1], AsyncSpawnIntrinsic)

	// Inside the goroutine: the call bound to a temp, then the post.
	if len(spawn) != 2 {
		t.Fatalf("the goroutine holds %d statements, want the bound call and the post", len(spawn))
	}
	tmp, ok := spawn[0].(*ir.LocalVar)
	if !ok {
		t.Fatalf("the goroutine's first statement is %T, want the blocking call bound to a local", spawn[0])
	}
	if !exprWaits(tmp.Init) {
		t.Error("the local the goroutine binds is not the blocking call")
	}
	post := intrinsicClosure(t, spawn[1], AsyncPostIntrinsic)

	// Back on the drawing thread: the answer written to state, then the write
	// that followed it.
	if len(post) != 2 {
		t.Fatalf("the posted closure holds %d statements, want the answer and the write after it", len(post))
	}
	assign, ok := post[0].(*ir.Assign)
	if !ok {
		t.Fatalf("the posted closure starts with %T, want the assignment of the answer", post[0])
	}
	if id, ok := assign.Value.(*ir.Ident); !ok || id.Name != tmp.Name {
		t.Errorf("the assignment reads %v, want the local the goroutine bound", assign.Value)
	}
}

// A target with no way back to its own thread has nowhere to put the answer,
// and the honest answer is a positioned refusal. Emitting the call where it
// stands would freeze the interface and report nothing.
func TestABlockingCallIsRefusedWithNowhereToPostIt(t *testing.T) {
	pkg, _ := blockingHandlerPkg()
	err := applyAsyncOffload(pkg, Caps{NoAsyncCalls: true}, Options{Platform: "bubbletea"})
	if err == nil {
		t.Fatal("a blocking call was accepted by a platform that cannot post back to its own thread")
	}
	if !strings.Contains(err.Error(), "bubbletea") {
		t.Errorf("the refusal does not name the platform that has to change: %v", err)
	}
}

// intrinsicClosure asserts s is a call to id with one closure argument, and
// returns that closure's body.
func intrinsicClosure(t *testing.T, s ir.Stmt, id string) []ir.Stmt {
	t.Helper()
	cs, ok := s.(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.Func == nil || cs.Call.Func.Intrinsic != id {
		t.Fatalf("statement is %T, want a call to %s", s, id)
	}
	if len(cs.Call.Args) != 1 {
		t.Fatalf("%s takes %d arguments, want the one closure", id, len(cs.Call.Args))
	}
	lam, ok := cs.Call.Args[0].Value.(*ir.Lambda)
	if !ok || lam.Func == nil {
		t.Fatalf("%s was handed %T, want a closure", id, cs.Call.Args[0].Value)
	}
	return lam.Func.Block
}
