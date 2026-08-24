package checker

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CheckNativeValue checks e against want and returns its IR form.
//
// e is a value a native-language encoder wrote as SNGL source — see
// pkg/go/consteval — so it differs from hand-written source in two ways that
// only the expected type can settle. A struct literal names the foreign type
// (`Item{...}`), which is not a SNGL binding: the declaration comes from want,
// where the scheme importer put it. And its fields carry the foreign names,
// which the importer recorded on each StructField as NativeName. Both are
// resolved off want rather than off the scope, so the result is ordinary
// checker output: a StructLit with its real Def, a list with its real element
// type, a literal at the width and unit want asks for.
//
// The scope holds nothing but the standard library. An encoded value is data —
// literals, lists, maps, struct literals and the predeclared true/false/null —
// so nothing in it can refer to a user declaration.
func CheckNativeValue(e ast.Expr, want *ir.Type) (ir.Expr, error) {
	if e == nil {
		return nil, fmt.Errorf("no expression")
	}
	nativeValueMu.Lock()
	defer nativeValueMu.Unlock()

	// One checker for the process. Building it re-registers the whole embedded
	// stdlib (~0.6ms), which would otherwise be paid per value rather than
	// per program, and nothing in an encoded value can leave state behind that
	// the next one would see.
	if nativeValueChecker == nil {
		nativeValueChecker = newChecker(&ast.Document{}, &Config{})
		nativeValueChecker.nativeValues = true
	}
	c := nativeValueChecker
	c.diags = nil
	out := c.checkExprExpecting(e, want)
	if len(c.diags) == 0 && want != nil && out != nil {
		// The encoder and the importer read the same Go type from opposite
		// ends, so a value that does not fit its declared type means they
		// disagree — a compiler bug, not a program error, and one that would
		// otherwise reach codegen as a well-formed literal of the wrong type.
		if got := out.ExprType(); got != nil && !got.IsAssignableTo(want) {
			gotStr, wantStr := ir.Contrast(got, want)
			return nil, fmt.Errorf("encoded value has type %s, which is not assignable to the declared %s", gotStr, wantStr)
		}
	}
	if len(c.diags) > 0 {
		msgs := make([]string, 0, len(c.diags))
		for _, d := range c.diags {
			msgs = append(msgs, d.Msg)
		}
		c.diags = nil
		return nil, fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	return out, nil
}

var (
	nativeValueMu      sync.Mutex
	nativeValueChecker *checker
)

// expectedStructDef returns the struct declaration t names, or nil.
func expectedStructDef(t *ir.Type) *ir.StructDef {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	sd, _ := t.Decl.(*ir.StructDef)
	return sd
}

// expectedEnumDef returns the enum declaration t names, or nil.
func expectedEnumDef(t *ir.Type) *ir.EnumDef {
	if t == nil || t.Kind != ir.TypeEnum {
		return nil
	}
	ed, _ := t.Decl.(*ir.EnumDef)
	return ed
}

// nativeEnumMember resolves an encoded literal to the member of ed it is the
// erasure of. A TypeScript enum member compiles to its value, so a folded call
// hands back a bare number or string with nothing on it naming the member; ed
// is reached through the expected type, the same way a struct literal reaches
// its declaration.
//
// Only a literal is claimed: a bare member *name* is an ident and stays with
// inferIdent. A claimed literal is never read as its own type afterwards —
// matching no member is this key's failure, because the alternative is a float
// compiled in where an enum was declared.
//
// Two members with one value resolve to the first declared. TypeScript's own
// reverse mapping answers with the last, but that mapping is an artifact of
// how the enum object is built and the compiler never sees it; declaration
// order is a rule readable off the source.
func (c *checker) nativeEnumMember(e ast.Expr, ed *ir.EnumDef) (ir.Expr, bool) {
	// A negative value is a unary over a literal, not a literal. Declining the
	// unary would not leave the value unclaimed: the expected type survives
	// the recursion into the operand, so the magnitude would be claimed on its
	// own and resolve to whichever member happens to hold the positive value.
	neg := false
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == ast.UnaryNeg {
		if inner, ok := u.Operand.(*ast.LiteralExpr); ok && isNumericLiteralKind(inner.Kind) {
			neg, e = true, inner
		}
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil, false
	}
	var match func(*ir.Literal) bool
	var shown string
	switch lit.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		shown = strconv.Quote(lit.Raw)
		match = func(v *ir.Literal) bool { return isStringLit(v) && v.Raw == lit.Raw }
	case ast.LiteralInt, ast.LiteralFloat:
		n, err := strconv.ParseFloat(lit.Raw, 64)
		if err != nil {
			return nil, false
		}
		shown = lit.Raw
		if neg {
			n, shown = -n, "-"+shown
		}
		match = func(v *ir.Literal) bool {
			if isStringLit(v) {
				return false
			}
			m, err := strconv.ParseFloat(v.Raw, 64)
			return err == nil && m == n
		}
	default:
		return nil, false
	}
	for _, m := range ed.Members {
		// A member with no recorded value matches nothing. The importer leaves
		// one unrecorded exactly when it could not read the value, so treating
		// it as a candidate would resolve by position rather than by value.
		v, _ := m.Value.(*ir.Literal)
		if v == nil {
			continue
		}
		if match(v) {
			return &ir.Ident{
				AST:    &ast.IdentExpr{Pos: lit.Pos, Name: m.Name},
				Type:   c.expected,
				Name:   m.Name,
				Member: m.Name,
			}, true
		}
	}
	c.error(lit.Pos, "encoded value %s is not the value of any member of %s", shown, ed.Name)
	return &ir.Literal{Type: TypDyn}, true
}

// isNumericLiteralKind reports whether a literal kind carries a number, which
// is the only domain a unary minus can be encoding over.
func isNumericLiteralKind(k ast.LiteralKind) bool {
	return k == ast.LiteralInt || k == ast.LiteralFloat
}

// isStringLit reports whether v holds text rather than a number, which decides
// which of the two enum domains it can match in.
func isStringLit(v *ir.Literal) bool {
	return v.Type != nil && v.Type.Kind == ir.TypeString
}

// findNativeField looks a field up by its source-language name, which is what
// a native-language encoder writes. It is the reverse of the mapping the
// scheme importer applied when it built the declaration, read back off the
// record the importer left rather than recomputed — so there is one naming
// rule, and it lives with the importer.
func findNativeField(sd *ir.StructDef, nativeName string) *ir.StructField {
	for _, f := range sd.Fields {
		if f.NativeName == nativeName {
			return f
		}
	}
	return nil
}
