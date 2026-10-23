package bubbletea

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// emitPresenceChecks writes, for each `#id` a test null-tests, whether the
// node is rendered now. View keeps no tree to look in, so the check is the
// view with every other node taken out, answering true where View would draw
// this one.
func emitPresenceChecks(b *strings.Builder, pkg *ir.Package, stmts []ir.Stmt, ids map[string]bool, gc *golang.GoIRContext) {
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, id := range names {
		body, found := presencePath(stmts, id)
		if !found {
			if !codegen.RendersNowhere(pkg, id) {
				continue
			}
			body = nil
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
	return presenceWalk(stmts, id, nil, func([]*ir.For) ir.Stmt { return &ir.Return{Value: boolLit(true)} })
}

// presenceWalk is presencePath with answer the statement put where the node
// renders, handed the loops around it.
func presenceWalk(stmts []ir.Stmt, id string, loops []*ir.For, answer func([]*ir.For) ir.Stmt) ([]ir.Stmt, bool) {
	var out []ir.Stmt
	found := false
	for _, s := range stmts {
		if ins := ir.InstancesOf(s); ins != nil && slices.Contains(*ins, id) {
			return append(out, answer(loops)), true
		}
		switch n := s.(type) {
		case *ir.If:
			// A record's content counts the loops around where it was
			// written, not its container's loop over the records.
			body, inBody := presenceWalk(n.Body, id, lower.BlockLoops(n, &n.Body, loops), answer)
			els, inElse := presenceWalk(n.Else, id, loops, answer)
			if len(body) > 0 || len(els) > 0 {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
			}
			found = found || inBody || inElse
		case *ir.Match:
			arms := make([][]ir.Stmt, len(n.Arms))
			held := false
			for i, arm := range n.Arms {
				var in bool
				arms[i], in = presenceWalk(arm.Body, id, loops, answer)
				found = found || in
				held = held || len(arms[i]) > 0
			}
			if held {
				out = append(out, n.Over(func(i int, _ []ir.Stmt) []ir.Stmt { return arms[i] }))
			}
		case *ir.For:
			body, inBody := presenceWalk(n.Body, id, lower.BlockLoops(n, &n.Body, loops), answer)
			els, inElse := presenceWalk(n.Else, id, loops, answer)
			if len(body) > 0 || len(els) > 0 {
				cp := *n
				cp.Body, cp.Else = body, els
				out = append(out, &cp)
			}
			found = found || inBody || inElse
		case *ir.ErrorBoundary:
			body, ok := presenceWalk(n.Children, id, loops, answer)
			out, found = append(out, body...), found || ok
		case *ir.NodeInst:
			if n.ID == id {
				return append(out, answer(loops)), true
			}
			for _, sc := range ir.SuppliedContent(n) {
				body, ok := presenceWalk(sc.Body, id, loops, answer)
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

// emitKeyedPresenceChecks writes, for each `#id` a test null-tests one copy
// of, whether the copy with the key is rendered now: presencePath, answering
// true where the node renders in the iteration the key names -- a spliced
// instance's copy included, which carries no key of its own.
func emitKeyedPresenceChecks(b *strings.Builder, stmts []ir.Stmt, ids map[string]bool, gc *golang.GoIRContext) {
	key := &ir.Ident{Name: "key", Type: ir.TypString, Synthesized: true}
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		body, _ := presenceWalk(stmts, id, nil, func(loops []*ir.For) ir.Stmt {
			if len(loops) == 0 {
				return &ir.Return{Value: boolLit(false)}
			}
			return &ir.If{
				Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: key, Right: lower.CopyKey(loops)},
				Body: []ir.Stmt{&ir.Return{Value: boolLit(true)}},
			}
		})
		fn := &ir.Func{
			Name:    codegen.PresenceAtMethod(id),
			Lowered: ir.FuncLowering{HostReceiver: "Model"},
			Params:  []*ir.Param{{Name: key.Name, Type: ir.TypString}},
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
