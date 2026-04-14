package codegen

import (
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Extraction helpers for v2 Document. The v2 Document only has Stmts []Stmt;
// these helpers provide the v1-style named-slice access patterns that
// downstream code (platforms, cmd) still uses during the migration.

func DocVarDecls(doc *ast.Document) []*ast.VarDecl {
	var out []*ast.VarDecl
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.VarDecl); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocConstDecls(doc *ast.Document) []*ast.ConstDecl {
	var out []*ast.ConstDecl
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.ConstDecl); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocFuncDefs(doc *ast.Document) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, s := range doc.Stmts {
		if f, ok := s.(*ast.FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}

func DocStructDefs(doc *ast.Document) []*ast.StructDef {
	var out []*ast.StructDef
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.StructDef); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocEnumDefs(doc *ast.Document) []*ast.EnumDef {
	var out []*ast.EnumDef
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.EnumDef); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocComponents(doc *ast.Document) []*ast.ComponentDecl {
	var out []*ast.ComponentDecl
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.ComponentDecl); ok {
			out = append(out, c)
		}
	}
	return out
}

func DocUnitDefs(doc *ast.Document) []*ast.UnitDef {
	var out []*ast.UnitDef
	for _, s := range doc.Stmts {
		if u, ok := s.(*ast.UnitDef); ok {
			out = append(out, u)
		}
	}
	return out
}

func DocImports(doc *ast.Document) []*ast.Import {
	var out []*ast.Import
	for _, s := range doc.Stmts {
		if i, ok := s.(*ast.Import); ok {
			out = append(out, i)
		}
	}
	return out
}

// DocBodyStmts returns top-level visual statements (VisualNode, IfStmt, ForStmt).
func DocBodyStmts(doc *ast.Document) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range doc.Stmts {
		switch s.(type) {
		case *ast.VisualNode, *ast.IfStmt, *ast.ForStmt:
			out = append(out, s)
		}
	}
	return out
}

// FindMainComponent finds the "main" ComponentDecl in a document.
func FindMainComponent(doc *ast.Document) *ast.ComponentDecl {
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.ComponentDecl); ok && c.Name == "main" {
			return c
		}
	}
	return nil
}

// --- VisualNode helpers ---

// VnProps extracts named arguments (props) from a VisualNode's ArgList.
func VnProps(vn *ast.VisualNode) map[string]ast.Expr {
	props := make(map[string]ast.Expr)
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
			props[arg.Name] = arg.Value
		}
	}
	return props
}

// VnProp returns a single named prop value, or nil if not found.
func VnProp(vn *ast.VisualNode, name string) ast.Expr {
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == name {
			return arg.Value
		}
	}
	return nil
}

// VnEvents extracts event handlers from a VisualNode's ArgList.
func VnEvents(vn *ast.VisualNode) map[string]*ast.EventHandler {
	events := make(map[string]*ast.EventHandler)
	for i := range vn.Args.Args {
		if eh, ok := vn.Args.Args[i].(ast.EventHandler); ok {
			events[eh.Name] = &eh
		}
	}
	return events
}

// VnEvent returns a single event handler by name, or nil if not found.
func VnEvent(vn *ast.VisualNode, name string) *ast.EventHandler {
	for i := range vn.Args.Args {
		if eh, ok := vn.Args.Args[i].(ast.EventHandler); ok && eh.Name == name {
			return &eh
		}
	}
	return nil
}

// VnHasEvents returns true if the VisualNode has any event handlers.
func VnHasEvents(vn *ast.VisualNode) bool {
	for _, a := range vn.Args.Args {
		if _, ok := a.(ast.EventHandler); ok {
			return true
		}
	}
	return false
}

// VnChildren returns child statements from a VisualNode's Block.
func VnChildren(vn *ast.VisualNode) []ast.Stmt {
	return vn.Block.Stmts
}

// VnChildNodes returns child VisualNodes from a VisualNode's Block.
func VnChildNodes(vn *ast.VisualNode) []*ast.VisualNode {
	var out []*ast.VisualNode
	for _, s := range vn.Block.Stmts {
		if child, ok := s.(*ast.VisualNode); ok {
			out = append(out, child)
		}
	}
	return out
}

// --- Expr helpers ---

// ExprLiteralString extracts a string value from a LiteralExpr.
func ExprLiteralString(e ast.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return "", false
	}
	switch lit.Kind {
	case ast.LiteralStringQuoted:
		if s, err := strconv.Unquote(lit.Raw); err == nil {
			return s, true
		}
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringBackticked:
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '`' && raw[len(raw)-1] == '`' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringTrippleQuoted:
		raw := lit.Raw
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) {
			return raw[3 : len(raw)-3], true
		}
		return lit.Raw, true
	}
	return "", false
}

// ExprLiteralBool extracts a bool value from a LiteralExpr.
func ExprLiteralBool(e ast.Expr) (bool, bool) {
	if e == nil {
		return false, false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return false, false
	}
	if lit.Kind != ast.LiteralBool {
		return false, false
	}
	return lit.Raw == "true", true
}

// ExprLiteralInt extracts an int value from a LiteralExpr.
func ExprLiteralInt(e ast.Expr) (int, bool) {
	if e == nil {
		return 0, false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return 0, false
	}
	if lit.Kind != ast.LiteralInt {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ExprLiteralFloat extracts a float64 value from a LiteralExpr.
func ExprLiteralFloat(e ast.Expr) (float64, bool) {
	if e == nil {
		return 0, false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return 0, false
	}
	if lit.Kind != ast.LiteralFloat {
		return 0, false
	}
	f, err := strconv.ParseFloat(lit.Raw, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// ExprIsLiteral returns true if the expression is a literal value.
func ExprIsLiteral(e ast.Expr) bool {
	if e == nil {
		return false
	}
	_, ok := e.(*ast.LiteralExpr)
	return ok
}

// ExprLiteralAny extracts the literal value as any type.
func ExprLiteralAny(e ast.Expr) any {
	if e == nil {
		return nil
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil
	}
	switch lit.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		if s, ok := ExprLiteralString(e); ok {
			return s
		}
		return lit.Raw
	case ast.LiteralBool:
		return lit.Raw == "true"
	case ast.LiteralInt:
		n, _ := strconv.Atoi(lit.Raw)
		return n
	case ast.LiteralFloat:
		f, _ := strconv.ParseFloat(lit.Raw, 64)
		return f
	case ast.LiteralNull:
		return nil
	default:
		return lit.Raw
	}
}

// ExprIsReactive returns true if the expression is not a simple literal.
func ExprIsReactive(e ast.Expr) bool {
	if e == nil {
		return false
	}
	_, isLit := e.(*ast.LiteralExpr)
	return !isLit
}

// --- CallExpr helpers ---

// CallFuncName extracts the function name from a CallExpr.Func.
func CallFuncName(c *ast.CallExpr) string {
	switch f := c.Func.(type) {
	case *ast.IdentExpr:
		return f.Name
	case *ast.SelectExpr:
		if ident, ok := f.Operand.(*ast.IdentExpr); ok {
			return ident.Name + "." + f.Field
		}
		return f.Field
	}
	return ""
}

// CallArgs extracts argument expressions from a CallExpr.Args.
func CallArgs(c *ast.CallExpr) []ast.Expr {
	var out []ast.Expr
	for _, a := range c.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			out = append(out, arg.Value)
		}
	}
	return out
}

// CallArgCount returns the number of positional arguments.
func CallArgCount(c *ast.CallExpr) int {
	n := 0
	for _, a := range c.Args.Args {
		if _, ok := a.(ast.Arg); ok {
			n++
		}
	}
	return n
}

// --- ComponentDecl helpers ---

// CompParams extracts Param entries from a ComponentDecl's Props.
func CompParams(comp *ast.ComponentDecl) []ast.Param {
	var out []ast.Param
	for _, p := range comp.Props.Props {
		if param, ok := p.(ast.Param); ok {
			out = append(out, param)
		}
	}
	return out
}

// CompHasChildren returns true if the component accepts children.
func CompHasChildren(comp *ast.ComponentDecl) bool {
	return comp.ChildrenType != nil
}

// CompBodyStmts returns the visual/control-flow statements in a component body.
func CompBodyStmts(comp *ast.ComponentDecl) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range comp.Body.Stmts {
		switch s.(type) {
		case *ast.VisualNode, *ast.IfStmt, *ast.ForStmt:
			out = append(out, s)
		}
	}
	return out
}

// ExprTypeHint returns the type hint string from a TypeExpr if it's a named type.
func ExprTypeHint(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	if nt, ok := t.(*ast.NamedType); ok {
		return nt.Name
	}
	return ""
}

// ResolveComponentBody returns the platform-specific visual body of a component
// if a PkgSource override exists for the given platform. Returns nil if no
// override is found (caller should fall back to the default body).
func ResolveComponentBody(comp *ast.ComponentDecl, platform string) []*ast.VisualNode {
	// In v2, platform-specific component bodies are defined in the .sngl
	// PkgSource file for each platform, and the component body is resolved
	// during checker merge. We return the visual body nodes from the
	// component's body stmts.
	var nodes []*ast.VisualNode
	for _, s := range comp.Body.Stmts {
		if vn, ok := s.(*ast.VisualNode); ok {
			nodes = append(nodes, vn)
		}
	}
	// Only return if the component appears to be an abstract/override component
	// (i.e., has visual body but no data fields). This prevents normal user
	// components from being treated as overrides.
	// For now, return nil — the expandComponent path will only be used when
	// a future PkgSource mechanism populates override bodies.
	_ = nodes
	return nil
}

// VnStyleFields extracts "style" sub-properties from a VisualNode's props.
func VnStyleFields(vn *ast.VisualNode) map[string]ast.Expr {
	style := VnProp(vn, "style")
	if style == nil {
		return nil
	}
	se, ok := style.(*ast.StructExpr)
	if !ok {
		return nil
	}
	fields := make(map[string]ast.Expr, len(se.Fields))
	for _, f := range se.Fields {
		fields[f.Name] = f.Value
	}
	return fields
}
