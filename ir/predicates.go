package ir

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// IR-level predicates and accessors shared by the checker and every codegen
// backend. These are properties of the IR, not of any target language, so they
// live here alongside IsColorStruct/IsDateStruct/etc. rather than being
// reimplemented per consumer.

// IsNullToFuncConv reports whether n converts a null literal to a func type
// (`null` used where a func value is expected). Consumers render their own
// language-specific stub for the callable substitute; the detection is shared.
func IsNullToFuncConv(n *Conversion) bool {
	if n == nil || n.Type == nil || n.Type.Kind != TypeFunc {
		return false
	}
	lit, ok := n.Operand.(*Literal)
	return ok && lit.Type != nil && lit.Type.Kind == TypeNull
}

// IsErrorRaiseFunc reports whether fn is the error-raise intrinsic: either the
// "error.raise" intrinsic, or the stdlib error.raise method (whose wrapper does
// not carry Intrinsic, so the receiver+name pair is the stable identifier).
func IsErrorRaiseFunc(fn *Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic == "error.raise" {
		return true
	}
	return fn.Receiver == "error" && fn.Name == "raise"
}

// StmtPos returns the source position of an IR statement when one is available.
// Each statement's AST field points back at the originating AST node whose Pos
// is the position. Returns the zero Pos for statements with no AST origin
// (e.g. lower-pass-synthesized statements).
func StmtPos(s Stmt) ast.Pos {
	switch n := s.(type) {
	case *Assign:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *Toggle:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *CallStmt:
		if n.Call != nil && n.Call.AST != nil {
			return n.Call.AST.Pos
		}
	case *Emit:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *LocalVar:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *Return:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *NodeInst:
		// The one statement whose AST field is the interface rather than a
		// concrete node, which is why it was missing: a diagnostic about a
		// node -- an effect in the wrong scope, a prop a backend cannot emit
		// -- had nowhere to say where the node was written.
		if n.AST != nil {
			if p := n.AST.StmtPos(); p != nil {
				return *p
			}
		}
	case *Window:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *If:
		if n.AST != nil {
			return n.AST.Pos
		}
	case *For:
		if n.AST != nil {
			return n.AST.Pos
		}
	}
	return ast.Pos{}
}

// RebuildComparable reports whether two values of t can be asked "is this the
// same one" and get the same answer on every target.
//
// That is the question a keyed lifetime asks of its `on`, and a built instance
// of a #[construct] prop. It is not isComparable, which asks what may be a map
// key and admits any named struct: a name is no promise that two values compare
// alike everywhere, and the defect this answers was a struct key that held one
// lifetime on the Go build and tore down and remounted on every settle of the
// JS one.
//
// Structural and recursive rather than a list of primitive kinds, because the
// comparison is emitted from this same walk: a struct compares field by field,
// so a struct whose fields all compare alike does too, and one with no fields
// is always equal to another -- which is exactly what a bracket with no `on`
// wants of `effect<T = struct {}>`.
func RebuildComparable(t *Type) bool { return RebuildIncomparable(t) == "" }

// RebuildIncomparable is "" when t is RebuildComparable, and otherwise the type
// that is not, with the field path that reaches it in parentheses.
//
// A phrase rather than a bool because a struct's answer is about something the
// type's name does not show. "struct changed is not comparable" sends a reader
// to the wrong declaration; "list<string> (field tags of struct changed)" names
// the line to change.
func RebuildIncomparable(t *Type) string {
	bad, path := rebuildIncomparable(t, nil)
	switch {
	case bad == "":
		return ""
	case len(path) == 0:
		return bad
	}
	return bad + " (" + strings.Join(path, ", ") + ")"
}

// rebuildIncomparable answers the offending type and the field path down to it,
// outermost last. seen is the structs already on the walk, so a type that
// contains itself answers rather than recursing: a cycle has no finite
// field-wise comparison to emit.
func rebuildIncomparable(t *Type, seen []*StructDef) (string, []string) {
	if t == nil {
		return "an unknown type", nil
	}
	switch t.Kind {
	case TypeBool, TypeInt, TypeFloat, TypeString, TypeEnum, TypeUnit:
		return "", nil
	case TypeTypeParam:
		// Judged where the parameter is bound to a concrete type. Answering
		// here would make a generic declaration undeclarable.
		return "", nil
	case TypeStruct:
		sd, _ := t.Decl.(*StructDef)
		if sd == nil {
			// An anonymous struct literal. It has no declaration, so it has no
			// fields to walk and no name a second value could be built under.
			return t.String(), nil
		}
		// A #[builtin] struct that declares no fields is opaque: a target holds
		// its own date for a `date`, so walking the zero fields would answer
		// "always equal" for two different days. `color` is not one of these --
		// it declares four numbers and every target holds it as four numbers,
		// so the field walk is the right answer for it.
		if sd.Builtin != BuiltinNone && len(sd.Fields) == 0 {
			return t.String(), nil
		}
		if slices.Contains(seen, sd) {
			return "struct " + sd.Name, []string{"which contains itself"}
		}
		seen = append(seen, sd)
		for _, f := range sd.Fields {
			if f == nil {
				continue
			}
			if bad, path := rebuildIncomparable(f.Type, seen); bad != "" {
				return bad, append(path, fmt.Sprintf("field %s of struct %s", f.Name, sd.Name))
			}
		}
		return "", nil
	}
	return t.String(), nil
}
