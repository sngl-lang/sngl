package checker

import (
	"fmt"
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
