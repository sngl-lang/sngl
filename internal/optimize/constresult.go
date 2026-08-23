package optimize

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// parseConstResults reads the document pkg/go/consteval wrote — one
// `const <key> = <expr>` per evaluated call — into folder values.
//
// This is deliberately a parse and not a check: the document names Go types
// the compiler has no declaration for, and the folder's value model
// (map[string]any / []any / primitives) is all the caller needs. Phase 2
// replaces this seam with a checker pass, so it stays small on purpose.
func parseConstResults(path string, src []byte) (map[string]any, error) {
	doc, err := parser.Parse(path, src)
	if err != nil {
		return nil, fmt.Errorf("parsing const evaluator results: %w", err)
	}
	out := map[string]any{}
	for _, stmt := range doc.Stmts {
		decl, ok := stmt.(*ast.ConstDecl)
		if !ok {
			continue
		}
		for _, spec := range decl.Specs {
			if len(spec.Names) != 1 {
				continue
			}
			v, ok := valueFromAST(spec.Default)
			if !ok {
				return nil, fmt.Errorf("const evaluator result %q is not a literal value", spec.Names[0])
			}
			out[spec.Names[0]] = v
		}
	}
	return out, nil
}

// valueFromAST converts a literal AST expression to a folder value. It is the
// inverse of the encoder in pkg/go/consteval, so it accepts exactly what that
// encoder writes.
func valueFromAST(e ast.Expr) (any, bool) {
	switch x := e.(type) {
	case *ast.LiteralExpr:
		return literalExprValue(x)
	case *ast.UnitLiteral:
		// A duration (`250ms`) carries its magnitude in the base unit, which
		// is what the folder's value model holds.
		return literalExprValue(&x.LiteralExpr)
	case *ast.UnaryExpr:
		if x.Op != ast.UnaryNeg {
			return nil, false
		}
		v, ok := valueFromAST(x.Operand)
		if !ok {
			return nil, false
		}
		switch n := v.(type) {
		case int:
			return -n, true
		case float64:
			return -n, true
		}
		return nil, false
	case *ast.ListExpr:
		out := make([]any, 0, len(x.Elements))
		for _, el := range x.Elements {
			v, ok := valueFromAST(el)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	case *ast.StructExpr:
		out := make(map[string]any, len(x.Fields))
		for _, f := range x.Fields {
			v, ok := valueFromAST(f.Value)
			if !ok {
				return nil, false
			}
			// The encoder writes Go field names; SNGL names them lowerCamel.
			out[lowerFirst(f.Name)] = v
		}
		return out, true
	case *ast.MapLit:
		out := make(map[string]any, len(x.Entries))
		for _, en := range x.Entries {
			k, ok := valueFromAST(en.Key)
			if !ok {
				return nil, false
			}
			v, ok := valueFromAST(en.Value)
			if !ok {
				return nil, false
			}
			// A map key is data, not a declared field name: it survives as
			// written.
			out[fmt.Sprint(k)] = v
		}
		return out, true
	case *ast.IdentExpr:
		// The parser hands `true`, `false` and `null` back as identifiers;
		// resolving them to literals is the checker's job, which this seam
		// deliberately skips.
		switch x.Name {
		case "true":
			return true, true
		case "false":
			return false, true
		case "null":
			return nil, true
		}
		return nil, false
	case *ast.ParenExpr:
		return valueFromAST(x.Inner)
	}
	return nil, false
}

func literalExprValue(lit *ast.LiteralExpr) (any, bool) {
	switch lit.Kind {
	case ast.LiteralNull:
		return nil, true
	case ast.LiteralBool:
		return lit.Raw == "true", true
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		return lit.Raw, true
	case ast.LiteralInt:
		n, err := strconv.Atoi(lit.Raw)
		if err != nil {
			return nil, false
		}
		return n, true
	case ast.LiteralFloat:
		f, err := strconv.ParseFloat(lit.Raw, 64)
		if err != nil {
			return nil, false
		}
		return normalizeFloat(f), true
	case ast.LiteralUnit:
		// A unit literal (e.g. `250ms`) carries its magnitude in Raw with the
		// suffix attached.
		num := strings.TrimRight(lit.Raw, "abcdefghijklmnopqrstuvwxyz")
		if f, err := strconv.ParseFloat(num, 64); err == nil {
			return normalizeFloat(f), true
		}
		return nil, false
	}
	return nil, false
}

// normalizeFloat matches what the old JSON result path produced: every number
// arrived as a float64, and a whole one became an int. Downstream folding and
// codegen are shaped around that, so it is preserved here rather than fixed.
func normalizeFloat(f float64) any {
	if f == float64(int(f)) {
		return int(f)
	}
	return f
}

// lowerFirst lowercases the first letter of a Go field name, matching SNGL's
// field naming convention. An all-caps name (an initialism like "URL") is
// lowercased whole.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}
