package bubbletea

import (
	"sort"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// emitPresenceChecks writes, for each `#id` a test null-tests, whether the
// node is rendered now. View keeps no tree to look in, so the check walks the
// view's own `if`s and `for`s down to the node, keeping nothing else, and
// answers true where View would draw it: a tab's content is under the
// container's loop over its records once the container is spliced.
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

// presencePath is stmts cut down to the statements that decide whether the
// node with the id renders, each place it does returning true. A spliced
// instance renders where the roots of its body do.
func presencePath(stmts []ir.Stmt, id string) ([]ir.Stmt, bool) {
	var out []ir.Stmt
	found := false
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			body, inBody := presencePath(n.Body, id)
			els, inElse := presencePath(n.Else, id)
			if inBody || inElse {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
				found = true
			}
		case *ir.For:
			body, inBody := presencePath(n.Body, id)
			els, inElse := presencePath(n.Else, id)
			if inBody || inElse {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
				found = true
			}
		case *ir.ErrorBoundary:
			if body, ok := presencePath(n.Children, id); ok {
				out, found = append(out, body...), true
			}
		case *ir.NodeInst:
			if n.ID == id || n.Instance == id {
				return append(out, &ir.Return{Value: boolLit(true)}), true
			}
			for _, sc := range ir.SuppliedContent(n) {
				if body, ok := presencePath(sc.Body, id); ok {
					out, found = append(out, body...), true
				}
			}
		}
	}
	return out, found
}

func boolLit(v bool) *ir.Literal { return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(v)} }
