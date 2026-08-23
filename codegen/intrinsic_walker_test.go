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
				Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "vbox"}}},
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
			Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "vbox"}}},
		}},
		// #__n0.text = "hi"
		&ir.Assign{
			Target: &ir.Select{
				Operand: &ir.Ident{Name: "__n0", IsElementRef: true},
				Field:   "text",
			},
			Value: &ir.Literal{Type: ir.TypString, Raw: "hi"},
		},
		// lower.AttachHandler(#__n0, "click", h)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "AttachHandler", Intrinsic: "AttachHandler"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
				{Value: &ir.Literal{Type: ir.TypString, Raw: "click"}},
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
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true, Sym: slotVar},
		Value: &ir.Call{
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
