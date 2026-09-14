package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// expandForWindows walks the main component body and expands for-loops
// that contain window declarations over const iterables into individual
// window statements. This enables multi-page HTML generation.
func expandForWindows(pkg *ir.Package, ctx *evalCtx) {
	var mainComp *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			mainComp = c
			break
		}
	}
	if mainComp == nil {
		return
	}

	var expanded []ir.Stmt
	for _, s := range mainComp.Body {
		if fs, ok := s.(*ir.For); ok {
			if stmts := expandForStmt(fs, ctx); stmts != nil {
				expanded = append(expanded, stmts...)
				continue
			}
		}
		expanded = append(expanded, s)
	}
	mainComp.Body = expanded
}

// maxStaticUnroll bounds an unroll on a target that has no alternative to
// one. A static artifact holds the iterations themselves -- a node per
// element, written into the output -- so a loop with a large constant count
// is a page nobody wanted: `for seq.count(200000)` produced 3.4 MB of markup
// and 200000 spans, where a target that runs the loop writes a `for` and a
// kilobyte.
//
// It is a diagnostic rather than a silent truncation, and rather than a
// runtime loop, because there is nowhere to run one: the author's options are
// a smaller count or a target that runs it, and only they can pick.
// The bound is per loop, so nested loops multiply and each is reported where
// it stands.
const maxStaticUnroll = 10000

// expandForStmt tries to expand a for-loop over a const iterable.
// Returns nil if the iterable can't be evaluated. A successful expansion to
// zero items returns the loop's else body — which is what a loop that ran no
// iterations leaves behind, and is empty for the loops that have no else, so
// it is still distinct from "couldn't evaluate" and the caller still drops
// the for-loop rather than leaving it for codegen to choke on.
func expandForStmt(fs *ir.For, ctx *evalCtx) []ir.Stmt {
	// A loop that walks nothing has no iterable to evaluate, so there are no
	// iterations to write out: a condition is tested at run time, and a
	// headless loop has no head at all. Both are refused in a view body, so
	// the target this unrolls for never sees one -- but a nil Iter reaching
	// evalExpr would be read as an unevaluable iterable rather than as a
	// different kind of loop.
	if fs.Iter == nil {
		return nil
	}
	if t := fs.Iter.ExprType(); t != nil && t.Kind == ir.TypeBool {
		return nil
	}
	val, ok := evalExpr(fs.Iter, ctx)
	if !ok {
		return nil
	}
	items, ok := val.([]any)
	if !ok {
		// A nil is an absent list, not an unevaluable one: a pure native call
		// returning an empty (or nil) slice evaluates fine and iterates zero
		// times. Reporting "could not evaluate" instead left the loop in the
		// tree with its body unfolded.
		if val == nil {
			items, ok = nil, true
		} else {
			return nil
		}
	}

	if len(items) > maxStaticUnroll && ctx.err == nil {
		pos := ""
		if fs.AST != nil {
			pos = fs.AST.Pos.String() + ": "
		}
		ctx.err = fmt.Errorf("%sthis loop repeats %d times, and this target writes every iteration into its output (limit %d): give it a smaller count, or build for a target that can run the loop",
			pos, len(items), maxStaticUnroll)
		return nil
	}

	// Find the LoopVar symbols for the for statement's key and value.
	keyVar := findLoopVar(fs, fs.Key)
	valueVar := findLoopVar(fs, fs.Value)

	// Collect window struct values per #id across iterations so hoisted
	// list<Window> symbols can be bound after expansion.
	windowsByID := map[string][]any{}

	// Nothing to iterate is the else case, and unrolling is the only thing
	// that will ever run it here: the loop is about to be replaced by its
	// expansion, and passForElse -- which is what states the else for a
	// target that keeps the loop -- runs after the optimizer and would find
	// nothing left to state it about. Dropping the whole statement lost the
	// else outright, so a const-empty loop rendered neither its body nor its
	// empty case.
	if len(items) == 0 {
		// Non-nil even when there is no else, since nil is how this function
		// says it could not evaluate the iterable. Returning the folded else
		// alone left an else-less empty loop looking unevaluable, so it stayed
		// in the tree and rendered its body once with its variable bound to
		// nothing -- an empty row per list that happened to be empty.
		if expanded := foldStmts(cloneStmts(fs.Else), ctx); expanded != nil {
			return expanded
		}
		return []ir.Stmt{}
	}

	result := make([]ir.Stmt, 0, len(items))
	for i, item := range items {
		// Create a child context with loop variables bound.
		childCtx := ctx.child()
		// Match the checker's loop-var typing (expr.go): for the two-var form
		// `for var key, value = list` the key is the index (int) and the value is
		// the element; for the single-var form `for var item = list` the sole var
		// is the element. Decide on the SYNTACTIC form (fs.Value != "") not on
		// whether the var is referenced — findLoopVar returns nil for an unused
		// var, so keying off valueVar would treat `for i, x` with an unused x as
		// single-var and bind i to the element.
		if fs.Value != "" {
			if keyVar != nil {
				childCtx.values[keyVar] = i
			}
			if valueVar != nil {
				childCtx.values[valueVar] = item
			}
		} else if keyVar != nil {
			childCtx.values[keyVar] = item
		}

		// Clone and optimize the body for this iteration. Using foldStmts so
		// nested const-iterable fors and const-cond ifs unroll/inline too.
		cloned := make([]ir.Stmt, len(fs.Body))
		for i, bodyStmt := range fs.Body {
			cloned[i] = cloneStmt(bodyStmt)
		}
		folded := foldStmts(cloned, childCtx)
		collectWindowStructValues(folded, windowsByID)
		result = append(result, folded...)

		// Propagate file assets back.
		ctx.fileAssets = childCtx.fileAssets
		if ctx.err == nil {
			ctx.err = childCtx.err
		}
	}

	// Bind each hoisted list<Window> symbol to the accumulated values.
	for _, v := range fs.HoistedWindowIDs {
		if vals, ok := windowsByID[v.Name]; ok {
			ctx.values[v] = vals
		}
	}
	return result
}

// collectWindowStructValues recursively walks unrolled statements looking for
// *ir.Window declarations with a non-empty Name, and appends a const-eval
// struct value (map[string]any with href/title/favicon) into result keyed by
// the window's Name.
func collectWindowStructValues(stmts []ir.Stmt, result map[string][]any) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Window:
			if n.Name != "" {
				result[n.Name] = append(result[n.Name], windowStructValue(n))
			}
		case *ir.If:
			collectWindowStructValues(n.Body, result)
			collectWindowStructValues(n.Else, result)
		case *ir.For:
			collectWindowStructValues(n.Body, result)
			collectWindowStructValues(n.Else, result)
		case *ir.ContextProvider:
			collectWindowStructValues(n.Children, result)
		case *ir.SlotInst:
			collectWindowStructValues(n.Children, result)
		case *ir.ErrorBoundary:
			collectWindowStructValues(n.Children, result)
		case *ir.NodeInst:
			collectWindowStructValues(n.Children, result)
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// No nested window declarations.
		default:
			panic(fmt.Sprintf("collectWindowStructValues: unhandled stmt %T", n))
		}
	}
}

// windowStructValue produces the const-eval shape (map[string]any) for an
// unrolled window: each prop literal-folded to a Go value, when available.
// Non-foldable expressions are omitted.
func windowStructValue(w *ir.Window) any {
	m := map[string]any{}
	for _, p := range w.Props {
		lit, ok := p.Value.(*ir.Literal)
		if !ok {
			continue
		}
		if v := parseLiteral(lit); v != nil {
			m[p.Name] = v
		}
	}
	return m
}

// findLoopVar searches the for-loop's body for a LoopVar with the given name.
// The checker creates LoopVar symbols and references them from Ident nodes
// inside the for body.
func findLoopVar(fs *ir.For, name string) *ir.LoopVar {
	if name == "" {
		return nil
	}
	var found *ir.LoopVar
	walkForBody(fs.Body, func(e ir.Expr) {
		if id, ok := e.(*ir.Ident); ok {
			if lv, ok := id.Sym.(*ir.LoopVar); ok && lv.Name == name {
				found = lv
			}
		}
	})
	return found
}

func walkForBody(stmts []ir.Stmt, visit func(ir.Expr)) {
	for _, s := range stmts {
		walkStmtExprs(s, visit)
	}
}

func walkStmtExprs(s ir.Stmt, visit func(ir.Expr)) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, p := range n.Props {
			walkAllExprs(p.Value, visit)
		}
		for _, s := range n.Children {
			walkStmtExprs(s, visit)
		}
	case *ir.If:
		walkAllExprs(n.Cond, visit)
		walkForBody(n.Body, visit)
		walkForBody(n.Else, visit)
	case *ir.For:
		walkAllExprs(n.Iter, visit)
		walkForBody(n.Body, visit)
	case *ir.Assign:
		walkAllExprs(n.Value, visit)
	case *ir.Return:
		walkAllExprs(n.Value, visit)
	case *ir.LocalVar:
		walkAllExprs(n.Init, visit)
	case *ir.CallStmt:
		if n.Call != nil {
			walkAllExprs(n.Call, visit)
		}
	case *ir.Window:
		for i := range n.Props {
			walkAllExprs(n.Props[i].Value, visit)
		}
		walkForBody(n.Body, visit)
	case *ir.SlotInst:
		walkForBody(n.Children, visit)
	case *ir.ContextProvider:
		walkAllExprs(n.Value, visit)
		walkForBody(n.Children, visit)
	case *ir.ErrorBoundary:
		walkForBody(n.Children, visit)
	case *ir.Emit:
		for _, a := range n.Args {
			walkAllExprs(a.Value, visit)
		}
	case *ir.Toggle:
		walkAllExprs(n.Target, visit)
	case *ir.CanvasRedrawStmt, *ir.Break, *ir.Continue:
		// No expressions.
	default:
		panic(fmt.Sprintf("walkStmtExprs: unhandled stmt %T", n))
	}
}

func walkAllExprs(e ir.Expr, visit func(ir.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch x := e.(type) {
	case *ir.Binary:
		walkAllExprs(x.Left, visit)
		walkAllExprs(x.Right, visit)
	case *ir.Unary:
		walkAllExprs(x.Operand, visit)
	case *ir.Ternary:
		walkAllExprs(x.Cond, visit)
		walkAllExprs(x.Then, visit)
		walkAllExprs(x.Else, visit)
	case *ir.Call:
		walkAllExprs(x.Receiver, visit)
		for _, a := range x.Args {
			walkAllExprs(a.Value, visit)
		}
	case *ir.Conversion:
		walkAllExprs(x.Operand, visit)
	case *ir.Select:
		walkAllExprs(x.Operand, visit)
	case *ir.Index:
		walkAllExprs(x.Operand, visit)
		walkAllExprs(x.Idx, visit)
	case *ir.ListLit:
		for _, el := range x.Elems {
			walkAllExprs(el, visit)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			walkAllExprs(f.Value, visit)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			walkAllExprs(kv.Key, visit)
			walkAllExprs(kv.Value, visit)
		}
	case *ir.Spread:
		walkAllExprs(x.Operand, visit)
	case *ir.Lambda, *ir.Closure:
		// Lambda/closure bodies are opaque to this walker; for-loop
		// expansion only inspects expressions at the lexical scope of
		// the for body, not inside nested lambda bodies.
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// No subexpressions.
	default:
		panic(fmt.Sprintf("walkAllExprs: unhandled expr %T", x))
	}
}
