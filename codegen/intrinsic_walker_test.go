package codegen

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// trace records each translator call as a string so tests can assert
// dispatch order without comparing source-language output.
type trace struct{ lines []string }

func (t *trace) OnCreateNode(id, tag string) string { t.add("create %s %s", id, tag); return "" }
func (t *trace) OnAppendChild(p, c string) string   { t.add("append %s %s", p, c); return "" }
func (t *trace) OnRemoveChild(p, c string) string   { t.add("remove %s %s", p, c); return "" }
func (t *trace) OnAttachHandler(n, e, h string) string {
	t.add("attach %s %s %s", n, e, h)
	return ""
}
func (t *trace) OnPropAssign(n, p string, _ ir.Expr) string {
	t.add("prop %s %s", n, p)
	return ""
}
func (t *trace) OnDefault(stmt ir.Stmt) []string {
	t.add("default %T", stmt)
	return nil
}
func (t *trace) OnSlotReset(slotID string) string {
	t.add("reset %s", slotID)
	return ""
}
func (t *trace) OnSlotAppend(slotID, childID string) string {
	t.add("append-slot %s %s", slotID, childID)
	return ""
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
	WalkLowered(stmts, tr)
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
	WalkLowered(stmts, tr)
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
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	tr := &trace{}
	WalkLowered([]ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "reset __slot0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}

func TestWalkLowered_SlotAppend(t *testing.T) {
	listPush := &ir.Func{Name: "ListPush", Intrinsic: "ListPush"}
	stmt := &ir.Assign{
		Target: &ir.Ident{Name: "__slot0", Synthesized: true},
		Value: &ir.Call{
			Func: listPush,
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__slot0", Synthesized: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}},
			},
		},
	}
	tr := &trace{}
	WalkLowered([]ir.Stmt{stmt}, tr)
	if len(tr.lines) != 1 || tr.lines[0] != "append-slot __slot0 __n0" {
		t.Errorf("dispatch mismatch: %v", tr.lines)
	}
}
