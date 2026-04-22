package javascript

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string          { return "js" }
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("javascript: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("javascript: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("javascript: WriteType not yet implemented")
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
}

func (t *Translator) Eval(expr ir.Expr) string {
	return fmt.Sprintf("/* eval not implemented: %T */", expr)
}

// TranslateIRExpr translates an IR expression directly to its JS form.
func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	return translateIRExpr(e, scope)
}

// TranslateIRMutation translates an IR mutation statement to JS statements.
func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return translateIRMutation(s, scope)
}

// TranslateIRLiteral translates an IR literal expression to its JS literal.
// Handles scalar literals, list literals, and struct literals — anything
// `codegen.IRIsLiteral` reports true for.
func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Literal:
		return translateIRLiteral(n)
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = t.TranslateIRLiteral(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ir.StructLit:
		parts := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "..."+t.TranslateIRLiteral(f.Value))
			} else {
				parts = append(parts, f.Name+": "+t.TranslateIRLiteral(f.Value))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return `""`
}

func (t *Translator) TypeToNative(hint string) string {
	if strings.HasPrefix(hint, "option:") {
		return t.TypeToNative(hint[7:]) // JS has no option types; everything is nullable
	}
	switch hint {
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "string"
	default:
		return "any"
	}
}

func (t *Translator) ExportName(name string) string {
	return name
}

func binaryOpStr(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
	case ast.BinEq:
		return "==="
	case ast.BinNeq:
		return "!=="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	default:
		return "?"
	}
}

// isIntNode reports whether a SNGL node is known to produce an integer value.
func isIntNode(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return n.Kind == ast.LiteralInt
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.IdentExpr); ok {
			return ident.Name == "int"
		}
		return false
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinAdd, ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
			return isIntNode(n.Left) && isIntNode(n.Right)
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNeg {
			return isIntNode(n.Operand)
		}
	case *ast.ParenExpr:
		return isIntNode(n.Inner)
	}
	return false
}

func assignOpStr(op ast.AssignOp) string {
	switch op {
	case ast.AssignSet:
		return "="
	case ast.AssignAdd:
		return "+="
	case ast.AssignSub:
		return "-="
	case ast.AssignMul:
		return "*="
	case ast.AssignDiv:
		return "/="
	case ast.AssignMod:
		return "%="
	default:
		return "="
	}
}
