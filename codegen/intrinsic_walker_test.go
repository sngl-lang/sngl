package codegen

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// trace records each translator call as a string so tests can assert
// dispatch order without comparing source-language output.
type trace struct{ lines []string }

func (t *trace) OnCreateNode(_ context.Context, id, tag string) []ir.Stmt {
	t.add("create %s %s", id, tag)
	return nil
}
func (t *trace) OnAppendChild(_ context.Context, p, c ir.Expr) []ir.Stmt {
	t.add("append %s %s", identName(p), identName(c))
	return nil
}
func (t *trace) OnRemoveChild(_ context.Context, p, c ir.Expr) []ir.Stmt {
	t.add("remove %s %s", identName(p), identName(c))
	return nil
}
func (t *trace) OnAttachHandler(_ context.Context, n ir.Expr, e string, h ir.Expr) []ir.Stmt {
	t.add("attach %s %s %s", identName(n), e, identName(h))
	return nil
}
func (t *trace) OnPropAssign(_ context.Context, n ir.Expr, p string, v ir.Expr) []ir.Stmt {
	t.add("prop %s %s", identName(n), p)
	return nil
}
func (t *trace) OnSlotReset(_ context.Context, s *ir.Var) []ir.Stmt {
	t.add("reset %s", s.Name)
	return nil
}
func (t *trace) OnSlotAppend(_ context.Context, s *ir.Var, c ir.Expr) []ir.Stmt {
	t.add("append-slot %s %s", s.Name, identName(c))
	return nil
}
func (t *trace) OnCreateComponent(_ context.Context, id string, c *ir.Call) []ir.Stmt {
	t.add("create-component %s", id)
	return nil
}
func (t *trace) OnComponentRoot(_ context.Context, id string, inst ir.Expr) []ir.Stmt {
	t.add("component-root %s %s", id, identName(inst))
	return nil
}
func (t *trace) OnUpdateComponent(_ context.Context, inst ir.Expr, p string, v ir.Expr) []ir.Stmt {
	t.add("update-component %s %s", identName(inst), p)
	return nil
}
func (t *trace) OnDestroyComponent(_ context.Context, inst ir.Expr) []ir.Stmt {
	t.add("destroy-component %s", identName(inst))
	return nil
}
func (t *trace) OnInsertBefore(_ context.Context, p, c, ref ir.Expr) []ir.Stmt {
	t.add("insert %s %s before %s", identName(p), identName(c), identName(ref))
	return nil
}
func (t *trace) OnIter(_ context.Context, e ir.Expr) ir.Expr { return e }
func (t *trace) OnCond(_ context.Context, e ir.Expr) ir.Expr { return e }
func (t *trace) OnDefault(_ context.Context, s ir.Stmt) []ir.Stmt {
	t.add("default %T", s)
	return nil
}
func (t *trace) add(f string, args ...any) { t.lines = append(t.lines, fmt.Sprintf(f, args...)) }

func TestWalkLoweredDispatchesCreate(t *testing.T) {
	createFunc := &ir.Func{Name: "CreateNode", Intrinsic: "CreateNode"}
	stmts := []ir.Stmt{
		&ir.LocalVar{
			Name: "__n0",
			Type: ir.TypDyn,
			Init: &ir.Call{
				Receiver: &ir.Ident{Name: "lower"},
				Func:     createFunc,
				Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: "vbox"}}},
			},
		},
	}
	tr := &trace{}
	WalkLowered(context.Background(), stmts, tr)
	got := strings.Join(tr.lines, "\n")
	if got != "create __n0 vbox" {
		t.Errorf("dispatch mismatch:\ngot:  %q\nwant: %q", got, "create __n0 vbox")
	}
}

func TestWalkLoweredDispatchesAll(t *testing.T) {
	stmts := []ir.Stmt{
		// var __n0 dyn = lower.CreateNode("vbox")
		&ir.LocalVar{Name: "__n0", Type: ir.TypDyn, Init: &ir.Call{
			Func: &ir.Func{Name: "CreateNode", Intrinsic: "CreateNode"},
			Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: "vbox"}}},
		}},
		// #__n0.text = "hi"
		&ir.Assign{
			Target: &ir.Select{
				Operand: &ir.Ident{Name: "__n0", IsElementRef: true},
				Field:   "text",
			},
			Value: &ir.Literal{Type: ir.TypString, Value: "hi"},
		},
		// lower.AttachHandler(#__n0, "click", h)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "AttachHandler", Intrinsic: "AttachHandler"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
				{Value: &ir.Literal{Type: ir.TypString, Value: "click"}},
				{Value: &ir.Ident{Name: "h"}},
			},
		}},
		// lower.AppendChild(#__parent, #__n0)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "AppendChild", Intrinsic: "AppendChild"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__parent", IsElementRef: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		}},
		// lower.RemoveChild(#__parent, #__n0)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "RemoveChild", Intrinsic: "RemoveChild"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__parent", IsElementRef: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		}},
	}
	tr := &trace{}
	WalkLowered(context.Background(), stmts, tr)
	want := []string{
		"create __n0 vbox",
		"prop __n0 text",
		"attach __n0 click h",
		"append __parent __n0",
		"remove __parent __n0",
	}
	if strings.Join(tr.lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("dispatch order mismatch:\ngot:\n%s\nwant:\n%s", strings.Join(tr.lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestWalkLowered_SlotReset(t *testing.T) {
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := &trace{}
	WalkLowered(context.Background(), []ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "reset __slot0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}

func TestWalkLowered_SlotAppend(t *testing.T) {
	slotVar := &ir.Var{Name: "__slot0", Synthesized: true}
	listPush := &ir.Func{Name: "list.push", Intrinsic: "list.push"}
	// A call, not an assignment to the slot: push mutates its receiver and
	// returns nothing, which is what renderSlotBody builds.
	stmt := &ir.CallStmt{
		Call: &ir.Call{
			Func: listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
			},
		},
	}
	tr := &trace{}
	WalkLowered(context.Background(), []ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "append-slot __slot0 __n0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}

func TestWalkLowered_ForIfRecursion(t *testing.T) {
	appendChild := &ir.Func{Name: "AppendChild", Intrinsic: "AppendChild"}
	stmt := &ir.For{
		Key:  "x",
		Iter: &ir.Ident{Name: "items"},
		Body: []ir.Stmt{
			&ir.If{
				Cond: &ir.Ident{Name: "x"},
				Body: []ir.Stmt{
					&ir.CallStmt{Call: &ir.Call{
						Func: appendChild,
						Args: []ir.CallArg{
							{Value: &ir.Ident{Name: "p", IsElementRef: true}},
							{Value: &ir.Ident{Name: "c", IsElementRef: true}},
						},
					}},
				},
			},
		},
	}
	tr := &trace{}
	WalkLowered(context.Background(), []ir.Stmt{stmt}, tr)
	got := strings.Join(tr.lines, "\n")
	if !strings.Contains(got, "append p c") {
		t.Errorf("structural recursion missed AppendChild; trace: %v", tr.lines)
	}
}

// nodeOpShapes is the canonical call shape for every ir.NodeOps entry: the
// statement a lowering pass emits, and the trace line the translator method
// answering it is expected to add.
//
// The contract written twice, on purpose. WalkLowered matches an op by id; an
// op with no arm falls through to OnDefault and is emitted as whatever the
// language renderer makes of a call to a function nothing declares. That is
// how `__cf_<name>` -- the JS factory CreateComponent compiles to -- came to be
// referenced for years without ever being defined anywhere.
func nodeOpShapes() map[string]struct {
	stmt ir.Stmt
	want string
} {
	inst := func() ir.Expr { return &ir.Ident{Name: "__i0", Synthesized: true} }
	lower := func(op string, args ...ir.Expr) *ir.Call {
		c := &ir.Call{Receiver: &ir.Ident{Name: "lower"}, Func: &ir.Func{Name: op, Intrinsic: op}}
		for _, a := range args {
			c.Args = append(c.Args, ir.CallArg{Value: a})
		}
		return c
	}
	str := func(s string) ir.Expr { return &ir.Literal{Type: ir.TypString, Value: s} }
	bind := func(name string, c *ir.Call) ir.Stmt {
		return &ir.LocalVar{Name: name, Type: ir.TypDyn, Init: c}
	}
	call := func(c *ir.Call) ir.Stmt { return &ir.CallStmt{Call: c} }

	return map[string]struct {
		stmt ir.Stmt
		want string
	}{
		ir.NodeOpCreateNode:       {bind("__n0", lower(ir.NodeOpCreateNode, str("vbox"))), "create __n0 vbox"},
		ir.NodeOpCreateComponent:  {bind("__i0", lower(ir.NodeOpCreateComponent, &ir.Ident{Name: "Badge"}, &ir.StructLit{})), "create-component __i0"},
		ir.NodeOpComponentRoot:    {bind("__n1", lower(ir.NodeOpComponentRoot, inst())), "component-root __n1 __i0"},
		ir.NodeOpUpdateComponent:  {call(lower(ir.NodeOpUpdateComponent, inst(), str("label"), str("x"))), "update-component __i0 label"},
		ir.NodeOpDestroyComponent: {call(lower(ir.NodeOpDestroyComponent, inst())), "destroy-component __i0"},
		ir.NodeOpAppendChild:      {call(lower(ir.NodeOpAppendChild, &ir.Ident{Name: "p"}, &ir.Ident{Name: "c"})), "append p c"},
		ir.NodeOpRemoveChild:      {call(lower(ir.NodeOpRemoveChild, &ir.Ident{Name: "p"}, &ir.Ident{Name: "c"})), "remove p c"},
		ir.NodeOpAttachHandler:    {call(lower(ir.NodeOpAttachHandler, &ir.Ident{Name: "n"}, str("click"), &ir.Ident{Name: "h"})), "attach n click h"},
		ir.NodeOpInsertBefore:     {call(lower(ir.NodeOpInsertBefore, &ir.Ident{Name: "p"}, &ir.Ident{Name: "c"}, &ir.Ident{Name: "r"})), "insert p c before r"},
	}
}

// TestEveryNodeOpIsDispatched holds WalkLowered to ir.NodeOps: every op has an
// arm, and none of them reaches OnDefault.
func TestEveryNodeOpIsDispatched(t *testing.T) {
	shapes := nodeOpShapes()
	for _, op := range ir.NodeOps {
		shape, described := shapes[op]
		if !described {
			t.Errorf("%s is in ir.NodeOps with no shape in this table; add one so the op is held to a dispatch arm", op)
			continue
		}
		tr := &trace{}
		WalkLowered(context.Background(), []ir.Stmt{shape.stmt}, tr)
		got := strings.Join(tr.lines, "; ")
		if got != shape.want {
			t.Errorf("%s dispatched to %q, want %q", op, got, shape.want)
		}
	}
}

// noInserter is a translator without ChildInserter: a platform that never
// declared Features.InsertBefore, and so should never meet the op.
//
// The embedded interface is what makes it one — it promotes the whole base
// contract and nothing else, so the optional method is genuinely absent.
// Embedding trace instead would have inherited OnInsertBefore and quietly
// tested nothing.
type noInserter struct{ IntrinsicTranslator }

// TestInsertBeforeWithoutInserterIsReported pins the other half of the
// pairing. A platform declaring the capability without implementing the
// interface would otherwise have the op fall through to OnDefault and render
// as a call to a function nothing declares -- silently, which is the failure
// mode this protocol keeps having. It is a panic naming both halves instead.
func TestInsertBeforeWithoutInserterIsReported(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("want a panic naming the missing ChildInserter")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "ChildInserter") {
			t.Errorf("panic = %v; want it to name ChildInserter", r)
		}
	}()
	stmt := nodeOpShapes()[ir.NodeOpInsertBefore].stmt
	WalkLowered(context.Background(), []ir.Stmt{stmt}, &noInserter{})
}
