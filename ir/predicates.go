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

// IsOptionUnwrap reports whether a conversion is the unwrap a null test
// earns: an option<T> read in the branch where it cannot be null, typed T.
//
// It is a conversion rather than a Unary{UnaryDeref} because a deref means
// ref<T> to everything that already reads one -- passNoRef rewrites it to
// `.value` on a box, and the folder drops it outright when the operand is not
// a ref. What each language emits for it differs: Go's option<T> is *T and
// needs the star, Kotlin's is T? and needs `!!`, and JavaScript's is the value
// itself and needs nothing.
func IsOptionUnwrap(n *Conversion) bool {
	if n == nil || n.Type == nil || n.Operand == nil {
		return false
	}
	src := n.Operand.ExprType()
	if src == nil || src.Kind != TypeOption || len(src.Elems) != 1 || src.Elems[0] == nil {
		return false
	}
	return src.Elems[0].Equal(n.Type)
}

// IsOptionWrap reports whether a conversion is the promotion of a bare T into
// the option<T> a declaration asked for -- the inverse of IsOptionUnwrap.
//
// A null operand is not one: `null` already IS the empty option on every
// target, and each language answers it before it reaches here. Neither is an
// option operand, which is variance between two options rather than a
// promotion.
//
// The wrap has to survive folding and reach codegen because it is not a no-op
// everywhere: Go's option<T> is *T, so the T needs a box. Kotlin's is T? and
// JavaScript's is the value itself, and both emit the operand unchanged.
func IsOptionWrap(n *Conversion) bool {
	if n == nil || n.Type == nil || n.Operand == nil {
		return false
	}
	if n.Type.Kind != TypeOption || len(n.Type.Elems) != 1 || n.Type.Elems[0] == nil {
		return false
	}
	src := n.Operand.ExprType()
	if src == nil {
		return false
	}
	switch src.Kind {
	case TypeNull, TypeOption, TypeDyn, TypeInvalid:
		return false
	}
	return src.Equal(n.Type.Elems[0])
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
		//
		// That claim is owed a place where the answer is actually given, and
		// for a while there was none for an effect's `on`: the guard ran on
		// the effect node, where the type is still the parameter, and the
		// declaration's own call site was never asked. The checker's
		// checkRebuildKeys is that place for both rules -- it walks the
		// package's instances with the bindings in hand, so the site that
		// pinned the parameter is the site that answers, however many generics
		// the value passed through on the way down.
		return "", nil
	case TypeStruct:
		sd, _ := t.Decl.(*StructDef)
		if sd == nil {
			// An unresolved struct type: no fields to walk.
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

// ComponentSelfRefs reports whether comp's body instantiates comp -- direct
// self-recursion. Mutual recursion is not this question: a caller detects that
// with an in-flight set, since the cycle is not visible from one declaration.
//
// A recursive component cannot be substituted into its caller to a finite
// body, so every pass that substitutes one asks this. It is a full walk rather
// than a switch over the statements a body usually holds: a self-call under a
// boundary, a context override or a handler is still a self-call, and each
// hand-rolled copy of this walk was missing a different one of them.
func ComponentSelfRefs(comp *Component) bool {
	if comp == nil {
		return false
	}
	found := false
	Walk(comp.Body, func(n Node) error {
		if inst, ok := n.(*NodeInst); ok && inst.Component == comp {
			found = true
		}
		return nil
	})
	return found
}

// IsHostValue reports whether v is a const that *is* a host identifier: a
// `#[<lang>.native]` mark naming `math.Pi`, `StrokeCap.Round`, `Math.PI`.
//
// The declaration is never emitted and every reference resolves to the host
// name, which is the rule a native func already follows. Unmarked, the way
// Foreign says "this is the host's own and not a name to spell beside it".
func IsHostValue(v *Var) bool {
	return v != nil && v.IsConst && v.Foreign.Name != "" && !v.Foreign.Marked
}
