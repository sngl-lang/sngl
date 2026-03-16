package snglparser

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Format writes an ast.Document as .sngl source text.
func Format(doc *ast.Document) string {
	f := &formatter{}
	f.formatDocument(doc)
	return f.sb.String()
}

type formatter struct {
	sb     strings.Builder
	indent int
}

func (f *formatter) write(s string) { f.sb.WriteString(s) }

func (f *formatter) newline() {
	f.sb.WriteByte('\n')
}

func (f *formatter) indentStr() string {
	return strings.Repeat("    ", f.indent)
}

func (f *formatter) writeLine(s string) {
	f.write(f.indentStr())
	f.write(s)
	f.newline()
}

func (f *formatter) formatDocument(doc *ast.Document) {
	needBlank := false

	// Imports
	if len(doc.Imports) > 0 {
		for _, imp := range doc.Imports {
			f.writeLine(fmt.Sprintf("import %q", imp.Path))
		}
		needBlank = true
	}

	// Outputs
	if len(doc.Outputs) > 0 {
		if needBlank {
			f.newline()
		}
		if len(doc.Outputs) == 1 {
			o := doc.Outputs[0]
			line := "output " + o.Lang + " " + o.Platform
			if len(o.Options) > 0 {
				line += "(" + formatKV(o.Options) + ")"
			}
			f.writeLine(line)
		} else {
			f.writeLine("output {")
			f.indent++
			for _, o := range doc.Outputs {
				line := o.Lang + " " + o.Platform
				if len(o.Options) > 0 {
					line += "(" + formatKV(o.Options) + ")"
				}
				f.writeLine(line)
			}
			f.indent--
			f.writeLine("}")
		}
		needBlank = true
	}

	// Structs
	for _, s := range doc.Structs {
		if needBlank {
			f.newline()
		}
		f.formatStruct(s)
		needBlank = true
	}

	// Enums
	for _, e := range doc.Enums {
		if needBlank {
			f.newline()
		}
		f.writeLine(fmt.Sprintf("enum %s { %s }", e.Name, strings.Join(e.Values, ", ")))
		needBlank = true
	}

	// Units
	for _, u := range doc.Units {
		if needBlank {
			f.newline()
		}
		f.formatUnitDecl(u)
		needBlank = true
	}

	// Named styles
	for _, s := range doc.Styles {
		if needBlank {
			f.newline()
		}
		f.formatStyleDecl(s)
		needBlank = true
	}

	// Styles schema (StyleDefs)
	if len(doc.StyleDefs) > 0 {
		if needBlank {
			f.newline()
		}
		f.formatStyleDefs(doc.StyleDefs)
		needBlank = true
	}

	// Non-main components
	for _, comp := range doc.Components {
		if needBlank {
			f.newline()
		}
		f.formatComponent(comp)
		needBlank = true
	}

	// component main (combines data, computed, consts, app)
	hasMain := doc.App != nil || len(doc.Data) > 0 || len(doc.Computeds) > 0 || len(doc.Consts) > 0
	if hasMain {
		if needBlank {
			f.newline()
		}
		f.writeLine("component main {")
		f.indent++

		memberBlank := false

		// Consts
		if len(doc.Consts) > 0 {
			f.formatConsts(doc.Consts)
			memberBlank = true
		}

		// Vars
		if len(doc.Data) > 0 {
			if memberBlank {
				f.newline()
			}
			f.formatVars(doc.Data)
			memberBlank = true
		}

		// Computeds
		if len(doc.Computeds) > 0 {
			if memberBlank {
				f.newline()
			}
			f.formatComputeds(doc.Computeds)
			memberBlank = true
		}

		// App body
		if doc.App != nil && len(doc.App.Children) > 0 {
			if memberBlank {
				f.newline()
			}
			for _, vn := range doc.App.Children {
				f.formatVisualNode(vn)
			}
		}

		f.indent--
		f.writeLine("}")
	}
}

func (f *formatter) formatStruct(s *ast.StructDef) {
	f.writeLine("struct " + s.Name + " {")
	f.indent++
	for _, field := range s.Fields {
		line := field.Name + " " + field.Type
		line += " = " + f.formatExprValue(field.Default)
		f.writeLine(line)
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatUnitDecl(u *ast.UnitDef) {
	var sb strings.Builder
	sb.WriteString("unit ")
	sb.WriteString(u.Name)
	sb.WriteString("(")
	for i, s := range u.Suffixes {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(s.Name)
		if s.Factor != nil {
			sb.WriteString(" = ")
			sb.WriteString(FormatNode(s.Factor))
		}
	}
	sb.WriteString(")")
	f.writeLine(sb.String())
}

func (f *formatter) formatStyleDecl(s *ast.StyleDecl) {
	f.writeLine("style " + s.Name + " {")
	f.indent++
	for _, k := range sortedKeys(s.Props) {
		f.writeLine(k + " = " + f.formatExprValue(s.Props[k]))
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatStyleDefs(defs []*ast.StylePropDef) {
	f.writeLine("styles {")
	f.indent++
	for _, d := range defs {
		line := d.Name + " " + d.TypeHint
		if len(d.Enum) > 0 {
			line += " enum(" + strings.Join(d.Enum, ", ") + ")"
		}
		f.writeLine(line)
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatConsts(consts []*ast.Const) {
	if len(consts) == 1 {
		c := consts[0]
		f.writeLine("const " + c.Name + " = " + f.formatExprValue(c.Init))
		return
	}
	f.writeLine("const (")
	f.indent++
	for _, c := range consts {
		f.writeLine(c.Name + " = " + f.formatExprValue(c.Init))
	}
	f.indent--
	f.writeLine(")")
}

func (f *formatter) formatVars(data []*ast.Data) {
	if len(data) == 1 {
		f.writeLine("var " + f.formatVarDecl(data[0]))
		return
	}
	f.writeLine("var (")
	f.indent++
	for _, d := range data {
		f.writeLine(f.formatVarDecl(d))
	}
	f.indent--
	f.writeLine(")")
}

func (f *formatter) formatVarDecl(d *ast.Data) string {
	var sb strings.Builder
	sb.WriteString(d.Name)

	typeStr := typeHintStr(d.Init.TypeHint, d)
	hasInit := d.Init.SNGL != nil || d.Init.Literal != nil || d.Init.CEL != ""

	if typeStr != "" {
		// Omit type when it can be inferred from the default value
		if !canInferType(d.Init, typeStr) {
			sb.WriteByte(' ')
			sb.WriteString(typeStr)
		}
	}

	if hasInit {
		sb.WriteString(" = ")
		sb.WriteString(f.formatExprValue(d.Init))
	}

	// Modifiers
	if d.Extern {
		sb.WriteString(" extern")
	}
	if d.Trigger != "" {
		autoName := "On" + strings.ToUpper(d.Name[:1]) + d.Name[1:] + "Changed"
		if d.Trigger == autoName {
			sb.WriteString(" trigger")
		} else {
			sb.WriteString(fmt.Sprintf(" trigger(%q)", d.Trigger))
		}
	}

	return sb.String()
}

// canInferType reports whether the type can be inferred from the expression.
func canInferType(expr ast.Expr, typeStr string) bool {
	// If the value is just null, we need the type
	if expr.Literal == nil && expr.SNGL != nil {
		if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralNull {
			return false
		}
	}
	if expr.Literal == nil && expr.CEL == "" && expr.SNGL == nil {
		return false
	}

	// Primitive types can be inferred from literal values
	switch typeStr {
	case "int":
		_, isInt := expr.Literal.(int)
		return isInt
	case "float":
		_, isFloat := expr.Literal.(float64)
		return isFloat
	case "string":
		_, isStr := expr.Literal.(string)
		return isStr
	case "bool":
		_, isBool := expr.Literal.(bool)
		return isBool
	case "color":
		if s, ok := expr.Literal.(string); ok {
			return strings.HasPrefix(s, "#")
		}
	}
	// Unit types (duration, measurement, etc.) can be inferred from unit literals
	if strings.HasPrefix(typeStr, "unit:") {
		_, ok := expr.Literal.(ast.UnitLiteral)
		return ok
	}

	// CEL expressions with complex types (list, struct, enum, func) need explicit type
	return false
}

func (f *formatter) formatComputeds(computeds []*ast.Computed) {
	if len(computeds) == 1 {
		c := computeds[0]
		f.writeLine("computed " + c.Name + " = " + f.formatExprValue(c.Expr))
		return
	}
	f.writeLine("computed (")
	f.indent++
	for _, c := range computeds {
		f.writeLine(c.Name + " = " + f.formatExprValue(c.Expr))
	}
	f.indent--
	f.writeLine(")")
}

func (f *formatter) formatComponent(comp *ast.Component) {
	f.writeLine("component " + comp.Name + " {")
	f.indent++

	memberBlank := false

	// Params
	for _, p := range comp.Params {
		line := "param " + p.Name
		hasDefault := p.Default.SNGL != nil || p.Default.Literal != nil || p.Default.CEL != ""
		typeStr := typeHintStr(p.Default.TypeHint, nil)
		if typeStr != "" && !canInferType(p.Default, typeStr) {
			line += " " + typeStr
		}
		if hasDefault {
			line += " = " + f.formatExprValue(p.Default)
		}
		if p.Required {
			line += " required"
		}
		f.writeLine(line)
		memberBlank = true
	}

	// Prop decls (stdlib)
	for _, p := range comp.PropDecls {
		line := "prop " + p.Name + " " + p.TypeHint
		if len(p.Enum) > 0 {
			line += " enum(" + strings.Join(p.Enum, ", ") + ")"
		}
		f.writeLine(line)
		memberBlank = true
	}

	// Event decls (stdlib)
	for _, e := range comp.EventDecls {
		f.writeLine("event " + e.Name + " " + e.PayloadType)
		memberBlank = true
	}

	// Children policy (stdlib)
	if comp.ChildPolicy != "" {
		f.writeLine("children " + comp.ChildPolicy)
		memberBlank = true
	}

	// Consts
	if len(comp.Consts) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatConsts(comp.Consts)
		memberBlank = true
	}

	// Data (var)
	if len(comp.Data) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatVars(comp.Data)
		memberBlank = true
	}

	// Computeds
	if len(comp.Computeds) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatComputeds(comp.Computeds)
		memberBlank = true
	}

	// Visual body
	if len(comp.Body) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, vn := range comp.Body {
			f.formatVisualNode(vn)
		}
	}

	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatVisualNode(vn *ast.VisualNode) {
	// If / For wrapping
	if vn.If != nil || vn.For != nil {
		if vn.For != nil {
			fc := vn.For
			line := "for " + fc.Variable
			if fc.IndexVar != "" {
				line += ", " + fc.IndexVar
			}
			line += " in " + f.formatExprValue(fc.Iterable)
			line += " {"
			f.writeLine(line)
			f.indent++
			f.formatVisualNodeInner(vn)
			f.indent--
			f.writeLine("}")
			return
		}
		// if
		line := "if " + f.formatExprValue(*vn.If) + " {"
		f.writeLine(line)
		f.indent++
		f.formatVisualNodeInner(vn)
		f.indent--
		f.writeLine("}")
		return
	}
	f.formatVisualNodeInner(vn)
}

func (f *formatter) formatVisualNodeInner(vn *ast.VisualNode) {
	// Build prop list
	var props []string

	// Special props
	if vn.ID != nil {
		props = append(props, "id="+f.formatExprValue(*vn.ID))
	}
	if vn.Key != nil {
		props = append(props, "key="+f.formatExprValue(*vn.Key))
	}
	if vn.Class != nil {
		props = append(props, "class="+f.formatExprValue(*vn.Class))
	}
	if vn.Ref != nil {
		props = append(props, "ref="+f.formatExprValue(*vn.Ref))
	}

	// Regular props (sorted for deterministic output)
	propKeys := sortedKeys(vn.Props)
	for _, k := range propKeys {
		props = append(props, k+"="+f.formatExprValue(vn.Props[k]))
	}

	// Events (sorted for deterministic output)
	eventKeys := sortedKeys(vn.Events)
	for _, name := range eventKeys {
		props = append(props, "@"+name+"="+f.formatEventValue(vn.Events[name]))
	}

	// Inline style attrs (merged from StyleAttrs + StyleBlock)
	if len(vn.StyleAttrs) > 0 || len(vn.StyleBlock) > 0 {
		var styleParts []string
		for _, k := range sortedKeys(vn.StyleAttrs) {
			styleParts = append(styleParts, k+"="+f.formatExprValue(vn.StyleAttrs[k]))
		}
		for _, k := range sortedKeys(vn.StyleBlock) {
			styleParts = append(styleParts, k+"="+f.formatExprValue(vn.StyleBlock[k]))
		}
		props = append(props, "style={"+strings.Join(styleParts, ", ")+"}")
	}

	// Build the line
	line := vn.Component
	if len(props) > 0 {
		line += "(" + strings.Join(props, ", ") + ")"
	}

	hasBody := len(vn.Children) > 0 || len(vn.AttrNodes) > 0
	if hasBody {
		line += " {"
		f.writeLine(line)
		f.indent++

		// Attr nodes
		for _, an := range vn.AttrNodes {
			var anProps []string
			for k, v := range an.Props {
				anProps = append(anProps, k+"="+f.formatExprValue(v))
			}
			f.writeLine("@" + an.Name + "(" + strings.Join(anProps, ", ") + ")")
		}

		// Children
		for _, child := range vn.Children {
			f.formatVisualNode(child)
		}

		f.indent--
		f.writeLine("}")
	} else {
		f.writeLine(line)
	}
}

func (f *formatter) formatEventValue(expr ast.Expr) string {
	if expr.SNGL != nil {
		return "{ " + FormatStmt(expr.SNGL) + " }"
	}
	// Fallback: format CEL as-is
	return "{ " + expr.CEL + " }"
}

// formatExprValue formats an Expr as SNGL source.
func (f *formatter) formatExprValue(expr ast.Expr) string {
	if expr.SNGL != nil {
		return FormatNode(expr.SNGL)
	}
	if expr.Literal != nil {
		return formatLiteral(expr.Literal, expr.TypeHint)
	}
	if expr.CEL != "" {
		// Shouldn't happen after CEL→SNGL, but fallback
		return expr.CEL
	}
	return "null"
}

// FormatNode formats an ast.Node as SNGL expression syntax.
func FormatNode(n ast.Node) string {
	if n == nil {
		return "null"
	}
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return formatLiteralExpr(e)
	case *ast.IdentExpr:
		return e.Name
	case *ast.BinaryExpr:
		left := FormatNode(e.Left)
		right := FormatNode(e.Right)
		return left + " " + binOpString(e.Op) + " " + right
	case *ast.UnaryExpr:
		operand := FormatNode(e.Operand)
		if e.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		return FormatNode(e.Cond) + " ? " + FormatNode(e.Then) + " : " + FormatNode(e.Else)
	case *ast.SelectExpr:
		return FormatNode(e.Operand) + "." + e.Field
	case *ast.IndexExpr:
		return FormatNode(e.Operand) + "[" + FormatNode(e.Index) + "]"
	case *ast.CallExpr:
		args := formatArgs(e.Args)
		return e.Func + "(" + args + ")"
	case *ast.MethodExpr:
		args := formatArgs(e.Args)
		return FormatNode(e.Receiver) + "." + e.Method + "(" + args + ")"
	case *ast.StructExpr:
		var fields []string
		for _, field := range e.Fields {
			fields = append(fields, field.Name+": "+FormatNode(field.Value))
		}
		return e.Name + "{" + strings.Join(fields, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(e.Elements))
		for i, el := range e.Elements {
			parts[i] = FormatNode(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.InterpolationExpr:
		var sb strings.Builder
		sb.WriteByte('"')
		for _, p := range e.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				sb.WriteString(escapeStringContent(fmt.Sprintf("%v", lit.Value)))
			} else {
				sb.WriteByte('{')
				sb.WriteString(FormatNode(p))
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('"')
		return sb.String()
	case *ast.AssignStmt:
		return FormatNode(e.Target) + " " + assignOpString(e.Op) + " " + FormatNode(e.Value)
	case *ast.ToggleStmt:
		return FormatNode(e.Target) + "!!"
	case *ast.EmitStmt:
		args := formatArgs(e.Args)
		return "@" + e.Name + "(" + args + ")"
	case *ast.StmtBlock:
		return FormatStmt(e)
	default:
		return fmt.Sprintf("/* unknown %T */", n)
	}
}

// FormatStmt formats a statement node (or block) as SNGL source.
func FormatStmt(n ast.Node) string {
	switch e := n.(type) {
	case *ast.StmtBlock:
		stmts := make([]string, len(e.Stmts))
		for i, s := range e.Stmts {
			stmts[i] = FormatStmt(s)
		}
		return strings.Join(stmts, "; ")
	default:
		return FormatNode(n)
	}
}

func formatLiteralExpr(e *ast.LiteralExpr) string {
	switch e.Kind {
	case ast.LiteralInt:
		return fmt.Sprintf("%d", e.Value)
	case ast.LiteralFloat:
		return fmt.Sprintf("%v", e.Value)
	case ast.LiteralString:
		return fmt.Sprintf("%q", e.Value)
	case ast.LiteralBool:
		if e.Value.(bool) {
			return "true"
		}
		return "false"
	case ast.LiteralNull:
		return "null"
	case ast.LiteralColor:
		return fmt.Sprintf("%v", e.Value)
	case ast.LiteralUnit:
		ul := e.Value.(ast.UnitLiteral)
		return ul.Number + ul.Suffix
	default:
		return fmt.Sprintf("%v", e.Value)
	}
}

func formatLiteral(v any, typeHint string) string {
	switch val := v.(type) {
	case ast.UnitLiteral:
		return val.Number + val.Suffix
	case string:
		// Color literals
		if strings.HasPrefix(val, "#") && (typeHint == "color" || typeHint == "") {
			return val
		}
		return fmt.Sprintf("%q", val)
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatArgs(args []ast.Node) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = FormatNode(a)
	}
	return strings.Join(parts, ", ")
}

func binOpString(op ast.BinaryOp) string {
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
		return "=="
	case ast.BinNeq:
		return "!="
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

func assignOpString(op ast.AssignOp) string {
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

func escapeStringContent(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "{", `\{`)
	return s
}

// typeHintStr formats a type hint for display in SNGL source.
func typeHintStr(hint string, d *ast.Data) string {
	if hint == "" {
		return ""
	}

	// Handle function types via Data fields
	if d != nil && d.IsFunc {
		var sb strings.Builder
		sb.WriteString("func(")
		sb.WriteString(strings.Join(d.ParamTypes, ", "))
		sb.WriteString(")")
		if d.ReturnType != "" {
			sb.WriteString(" -> ")
			sb.WriteString(d.ReturnType)
		}
		return sb.String()
	}

	// Inline enum: "enum:light|dark" → "enum<light | dark>"
	if strings.HasPrefix(hint, "enum:") {
		values := strings.Split(strings.TrimPrefix(hint, "enum:"), "|")
		return "enum<" + strings.Join(values, " | ") + ">"
	}

	// Unit types: "unit:s" — inferred from unit literals, omit
	if strings.HasPrefix(hint, "unit:") {
		return ""
	}

	// Generic types: "list:Todo" → "list<Todo>"
	if strings.Contains(hint, ":") {
		parts := strings.SplitN(hint, ":", 2)
		return parts[0] + "<" + parts[1] + ">"
	}

	return hint
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatKV(opts map[string]string) string {
	var parts []string
	for k, v := range opts {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	return strings.Join(parts, ", ")
}
