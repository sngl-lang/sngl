package bubbletea

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// emitPresenceChecks writes, for each `#id` a test null-tests, whether the
// node is rendered now. View keeps no tree to look in, so the answer is the
// conditions of the `if`s around the node -- what View asks before it draws
// it. A node under a loop is reached through a list and never null-tested.
func emitPresenceChecks(b *strings.Builder, stmts []ir.Stmt, ids map[string]bool, gc *golang.GoIRContext) {
	if len(ids) == 0 {
		return
	}
	found := map[string][]string{}
	var walk func(stmts []ir.Stmt, guards []string)
	walk = func(stmts []ir.Stmt, guards []string) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.If:
				cond := gc.EvalExpr(n.Cond)
				walk(n.Body, append(guards[:len(guards):len(guards)], cond))
				walk(n.Else, append(guards[:len(guards):len(guards)], "!("+cond+")"))
			case *ir.ErrorBoundary:
				walk(n.Children, guards)
			case *ir.NodeInst:
				if ids[n.ID] {
					all := "true"
					if len(guards) > 0 {
						all = strings.Join(guards, " && ")
					}
					found[n.ID] = append(found[n.ID], all)
				}
				for _, sc := range ir.SuppliedContent(n) {
					walk(sc.Body, guards)
				}
			}
		}
	}
	walk(stmts, nil)

	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, id := range names {
		at := found[id]
		if len(at) == 0 {
			continue
		}
		name := codegen.PresenceMethod(id)
		fmt.Fprintf(b, "// %s reports whether the #%s widget is rendered now; for tests.\n", name, id)
		fmt.Fprintf(b, "func (m *Model) %s() bool {\n\treturn %s\n}\n\n", name, strings.Join(at, " || "))
	}
}
