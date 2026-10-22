package lower

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passStringBuilders writes sngl:builtin's builder as the string it builds,
// for a target that does not claim `stringBuilders`: the type is a string,
// the zero builder is "", `b.add(x)` is `b += x`, `b.line(x)` is
// `b += x + "\n"`, and `b.string()` is b. What that costs is what the builder
// exists to avoid -- each add copies the string -- and a target whose host
// joins `+=` as a rope, as every JavaScript engine does, pays nothing for it.
//
// Early, so that every pass after it -- ValueCopy, RefParams, reactivity --
// sees a string, which none of them has to be told about. A builder is no
// state (the checker refuses one in a var a view reads), so nothing it
// lowers is reactive.
var passStringBuilders = pass{
	name:    "StringBuilders",
	enabled: func(f Features) bool { return !f.StringBuilders },
	apply:   lowerStringBuilders,
}

func lowerStringBuilders(pkg *ir.Package, _ Features, _ Options) error {
	ir.RewriteBlocks(pkg, func(stmts []ir.Stmt) []ir.Stmt {
		for i, s := range stmts {
			if a := builderAdd(s); a != nil {
				stmts[i] = a
			}
		}
		return stmts
	})
	var err error
	if e := ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		switch x := e.(type) {
		case *ir.Call:
			if x.Func == nil {
				return e, nil
			}
			switch x.Func.Intrinsic {
			case ir.BuilderString:
				return x.Args[0].Value, nil
			case ir.BuilderAdd, ir.BuilderLine:
				// Only a statement adds: as a value it is the unit a
				// lambda's expression body returns, which a string's `+=`
				// has none of.
				if err == nil {
					var pos ast.Pos
					if x.AST != nil {
						pos = x.AST.Pos
					}
					err = fmt.Errorf("%s: an add to a builder is a statement; write the lambda with a block body", pos)
				}
			}
		case *ir.StructLit:
			if x.Def != nil && x.Def.Intrinsic == ir.BuilderIntrinsic {
				return stringLit(""), nil
			}
		}
		return e, nil
	}); e != nil {
		return e
	}
	if err != nil {
		return err
	}
	var toString func(*ir.Type) *ir.Type
	toString = func(t *ir.Type) *ir.Type {
		if ir.IsBuilder(t) {
			return ir.TypString
		}
		if !t.Any(ir.IsBuilder) {
			return t
		}
		return t.MapChildren(toString)
	}
	ir.RewriteTypes(pkg, toString)
	return nil
}

// builderAdd is s, an add to a builder, as the add to a string it means, or
// nil where s is no such add.
func builderAdd(s ir.Stmt) ir.Stmt {
	cs, ok := s.(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.Func == nil || len(cs.Call.Args) == 0 {
		return nil
	}
	call := cs.Call
	var text ir.Expr
	if len(call.Args) > 1 {
		text = call.Args[1].Value
	}
	switch call.Func.Intrinsic {
	case ir.BuilderAdd:
	case ir.BuilderLine:
		if text == nil {
			text = stringLit("\n")
		} else {
			text = &ir.Binary{Type: ir.TypString, Op: ast.BinAdd, Left: text, Right: stringLit("\n")}
		}
	default:
		return nil
	}
	return &ir.Assign{Target: call.Args[0].Value, Op: ast.AssignAdd, Value: text}
}
