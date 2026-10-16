package bubbletea

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// emitPresenceChecks writes, for each `#id` a test null-tests, whether the
// node is rendered now. View keeps no tree to look in, so the check is the
// view with every other node taken out, answering true where View would draw
// this one.
func emitPresenceChecks(b *strings.Builder, stmts []ir.Stmt, ids map[string]bool, gc *golang.GoIRContext) {
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, id := range names {
		body, found := presencePath(stmts, id)
		if !found {
			continue
		}
		fn := &ir.Func{
			Name:    codegen.PresenceMethod(id),
			Lowered: ir.FuncLowering{HostReceiver: "Model"},
			Return:  ir.TypBool,
			Block:   append(body, &ir.Return{Value: boolLit(false)}),
		}
		for _, line := range gc.EmitFuncDef(fn) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
}

// presencePath is stmts with every node taken out but the one with the id,
// which returns true where it renders. What is not a node stays, so a
// condition reads the temps the view computed it from; each local is read once
// into `_`, since the node that read it may be gone. A spliced instance renders
// where the roots of its body stand, whatever an `if` or a `for` among them
// decides.
func presencePath(stmts []ir.Stmt, id string) ([]ir.Stmt, bool) {
	var out []ir.Stmt
	found := false
	for _, s := range stmts {
		if ins := ir.InstancesOf(s); ins != nil && slices.Contains(*ins, id) {
			return append(out, &ir.Return{Value: boolLit(true)}), true
		}
		switch n := s.(type) {
		case *ir.If:
			body, inBody := presencePath(n.Body, id)
			els, inElse := presencePath(n.Else, id)
			if len(body) > 0 || len(els) > 0 {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
			}
			found = found || inBody || inElse
		case *ir.For:
			body, inBody := presencePath(n.Body, id)
			els, inElse := presencePath(n.Else, id)
			if len(body) > 0 || len(els) > 0 {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
			}
			found = found || inBody || inElse
		case *ir.ErrorBoundary:
			body, ok := presencePath(n.Children, id)
			out, found = append(out, body...), found || ok
		case *ir.NodeInst:
			if n.ID == id {
				return append(out, &ir.Return{Value: boolLit(true)}), true
			}
			for _, sc := range ir.SuppliedContent(n) {
				body, ok := presencePath(sc.Body, id)
				out, found = append(out, body...), found || ok
			}
		case *ir.LocalVar:
			out = append(out, n, &ir.Assign{
				Target: &ir.Ident{Name: "_", Type: n.Type, Synthesized: true},
				Value:  &ir.Ident{Name: n.Name, Type: n.Type, Sym: n.Sym, Synthesized: true},
			})
		default:
			out = append(out, s)
		}
	}
	return out, found
}

func boolLit(v bool) *ir.Literal { return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(v)} }
