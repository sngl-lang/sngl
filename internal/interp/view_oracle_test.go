package interp

// This file holds the IR walk that ResolveElementRef used before the retained
// tree, kept verbatim as the oracle TestMountAgreesWithResolveElementRef
// compares against. It is frozen on purpose: its value is that it is the
// implementation the tree replaced, so agreement with it is evidence the
// replacement changed nothing. It goes when the tree is the only walk left --
// testrunner still has two of its own.

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// oracleResolve is the pre-tree ResolveElementRef.
func oracleResolve(env *Env, id string) (any, error) {
	if env.BodyStmts == nil {
		return nil, nil
	}
	var matches []map[string]any
	oracleByStmts(env, env.BodyStmts, id, &matches)
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	out := make([]any, len(matches))
	for i, m := range matches {
		out[i] = m
	}
	return out, nil
}

// collectByStmts walks IR statements collecting rendered nodes with matching id.
func oracleByStmts(env *Env, stmts []ir.Stmt, id string, out *[]map[string]any) {
	// Frozen walk, a second exception: a `var` in a block of a view is state
	// of the block, which the walk read as nothing at all. It is bound at its
	// initializer, which is what a fresh mount shows.
	if hasViewVar(stmts) {
		env = env.Snapshot()
		for _, s := range stmts {
			if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil {
				env.Set(lv.Sym, evalInit(env, lv.Init))
			}
		}
	}
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			oracleNodeByID(env, n, id, out)
		case *ir.CallStmt:
			oracleCallStmtByID(env, n, id, out)
		case *ir.If:
			cond, err := env.Eval(n.Cond)
			b, _ := cond.(bool)
			if err != nil || !b {
				oracleByStmts(env, n.Else, id, out)
				continue
			}
			oracleByStmts(env, n.Body, id, out)
		case *ir.For:
			iterVal, err := env.Eval(n.Iter)
			if err != nil {
				continue
			}
			// asIterable rather than a []any assertion: a sequence value
			// (sngl:seq) is not a list, and the walk this pins predates the
			// type rather than having an answer about it.
			count, at, ok := asIterable(iterVal)
			if !ok || count == 0 {
				oracleByStmts(env, n.Else, id, out)
				continue
			}
			list, _ := iterVal.([]any)
			for i := range count {
				item := at(i)
				child := env.Snapshot()
				// Frozen walk, one exception: it bound a two-variable loop's
				// element to the index name, which every compiled backend
				// contradicted. The oracle exists to pin what the walk
				// answered, not to preserve a disagreement with codegen.
				bindLoopElem(child, n, i, item, list)
				oracleByStmts(child, n.Body, id, out)
			}
		case *ir.SlotInst:
			oracleByStmts(env, n.Children, id, out)
		case *ir.ErrorBoundary:
			oracleByStmts(env, n.Children, id, out)
		case *ir.ContextProvider:
			oracleByStmts(env, n.Children, id, out)
		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
			// Imperative stmts contain no rendered nodes.
		default:
			panic(fmt.Sprintf("testrunner.collectByStmts: unhandled ir.Stmt %T", n))
		}
	}
}

func oracleNodeByID(env *Env, node *ir.NodeInst, id string, out *[]map[string]any) {
	// User-defined component with a real body — expand inline.
	if node.Component != nil && len(node.Component.Body) > 0 {
		if env.RenderDepth >= maxCallDepth {
			return
		}
		childEnv := env.componentEnv(node.Component, node, "")
		childEnv.RenderDepth = env.RenderDepth + 1
		oracleByStmts(childEnv, childEnv.BodyStmts, id, out)
		return
	}
	if node.ID == id {
		m := env.renderNodeProps(node)
		*out = append(*out, m)
	}
	oracleByStmts(env, node.Children, id, out)
}

// collectCallStmtByID handles children-less element calls (`text #id(...)`)
// which the checker emits as CallStmt rather than NodeInst. The element name
// and #id live on the AST back-reference.
func oracleCallStmtByID(env *Env, cs *ir.CallStmt, id string, out *[]map[string]any) {
	elemName, elemID := elemCallInfo(cs)
	if elemName == "" {
		return
	}
	// User-defined component — expand inline.
	if env.Pkg != nil {
		if comp := FindComponent(env.Pkg, elemName); comp != nil {
			if env.RenderDepth >= maxCallDepth {
				return
			}
			child := env.componentEnvFromCall(comp, cs.Call)
			child.RenderDepth = env.RenderDepth + 1
			oracleByStmts(child, child.BodyStmts, id, out)
			return
		}
	}
	if elemID == id {
		m := env.renderCallStmtProps(cs, elemName)
		*out = append(*out, m)
	}
}
