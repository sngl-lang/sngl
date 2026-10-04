package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

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
	items, ok := loopItems(fs, ctx)
	if !ok {
		return nil
	}

	// Nothing to iterate is the else case, and unrolling is the only thing
	// that will ever run it here: the loop is about to be replaced by its
	// expansion, and passForElse and passViewForElse -- which state the else
	// for a target that keeps the loop -- have already run. Dropping the whole
	// statement lost the else outright, so a const-empty loop rendered neither
	// its body nor its empty case.
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

	keyVar, valueVar := loopVars(fs)
	result := make([]ir.Stmt, 0, len(items))
	for i, item := range items {
		childCtx := bindLoopVars(fs, ctx, keyVar, valueVar, i, item)
		// foldStmts so nested const-iterable fors and const-cond ifs unroll
		// and inline too.
		result = append(result, foldStmts(cloneStmts(fs.Body), childCtx)...)
		ctx.fileAssets = childCtx.fileAssets
		if ctx.err == nil {
			ctx.err = childCtx.err
		}
	}
	return result
}

// loopVars is the symbols a loop binds. The checker records them on the loop;
// a loop built without them is searched for, which misses a variable read only
// in a handler.
func loopVars(fs *ir.For) (key, value *ir.LoopVar) {
	key, value = fs.KeySym, fs.ValueSym
	if key == nil {
		key = findLoopVar(fs, fs.Key)
	}
	if value == nil {
		value = findLoopVar(fs, fs.Value)
	}
	return key, value
}

// findLoopVar searches the for-loop's body for a LoopVar with the given name.
// The checker creates LoopVar symbols and references them from Ident nodes
// inside the for body.
func findLoopVar(fs *ir.For, name string) *ir.LoopVar {
	if name == "" {
		return nil
	}
	var found *ir.LoopVar
	_ = ir.WalkExprs(fs.Body, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok {
			if lv, ok := id.Sym.(*ir.LoopVar); ok && lv.Name == name {
				found = lv
			}
		}
		return nil
	})
	return found
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
