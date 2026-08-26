package parser_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	. "git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func mustParse(t *testing.T, src string) *ast.Document {
	t.Helper()
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return doc
}

func TestParseLiterals(t *testing.T) {
	doc := mustParse(t, `const x = 42`)
	if len(doc.Stmts) != 1 {
		t.Fatalf("expected 1 stmt, got %d", len(doc.Stmts))
	}
	cd, ok := doc.Stmts[0].(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected ConstDecl, got %T", doc.Stmts[0])
	}
	if len(cd.Specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(cd.Specs))
	}
	lit, ok := cd.Specs[0].Default.(*ast.LiteralExpr)
	if !ok {
		t.Fatalf("expected LiteralExpr, got %T", cd.Specs[0].Default)
	}
	if lit.Kind != ast.LiteralInt || lit.Raw != "42" {
		t.Errorf("expected int 42, got %v %q", lit.Kind, lit.Raw)
	}
}

func TestParseStringLiteral(t *testing.T) {
	doc := mustParse(t, `const s = "hello"`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	lit := cd.Specs[0].Default.(*ast.LiteralExpr)
	if lit.Kind != ast.LiteralStringQuoted {
		t.Errorf("expected string, got %v", lit.Kind)
	}
}

func TestParseBinaryExpr(t *testing.T) {
	doc := mustParse(t, `const x = 1 + 2 * 3`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	// Should be 1 + (2 * 3) due to precedence
	bin, ok := cd.Specs[0].Default.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", cd.Specs[0].Default)
	}
	if bin.Op != ast.BinAdd {
		t.Errorf("expected BinAdd, got %v", bin.Op)
	}
	right, ok := bin.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr on right, got %T", bin.Right)
	}
	if right.Op != ast.BinMul {
		t.Errorf("expected BinMul, got %v", right.Op)
	}
}

func TestParseTernary(t *testing.T) {
	doc := mustParse(t, `const x = a ? b : c`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	tern, ok := cd.Specs[0].Default.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected TernaryExpr, got %T", cd.Specs[0].Default)
	}

	if id, ok := tern.Cond.(*ast.IdentExpr); !ok || id.Name != "a" {
		t.Errorf("expected cond=a, got %v", tern.Cond)
	}
}

func TestParseUnary(t *testing.T) {
	doc := mustParse(t, `const x = !flag`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	un, ok := cd.Specs[0].Default.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", cd.Specs[0].Default)
	}
	if un.Op != ast.UnaryNot {
		t.Errorf("expected UnaryNot, got %v", un.Op)
	}
}

func TestParseStructDecl(t *testing.T) {
	doc := mustParse(t, `struct Point {
		x int
		y int = 0
	}`)
	sd, ok := doc.Stmts[0].(*ast.StructDef)
	if !ok {
		t.Fatalf("expected StructDef, got %T", doc.Stmts[0])
	}
	if sd.Name != "Point" {
		t.Errorf("expected name Point, got %q", sd.Name)
	}
	if len(sd.Fields()) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sd.Fields()))
	}
	if len(sd.Fields()[0].Names) != 1 || sd.Fields()[0].Names[0] != "x" {
		t.Errorf("expected field x, got %v", sd.Fields()[0].Names)
	}
	if sd.Fields()[1].Default == nil {
		t.Error("expected default on field y")
	}
}

func TestParseEnumDecl(t *testing.T) {
	doc := mustParse(t, `enum Color { Red, Green, Blue }`)
	ed, ok := doc.Stmts[0].(*ast.EnumDef)
	if !ok {
		t.Fatalf("expected EnumDef, got %T", doc.Stmts[0])
	}
	if ed.Name != "Color" {
		t.Errorf("expected name Color, got %q", ed.Name)
	}
	if len(ed.Members()) != 3 {
		t.Fatalf("expected 3 members, got %d", len(ed.Members()))
	}
	if ed.Members()[0].Name != "Red" {
		t.Errorf("expected Red, got %q", ed.Members()[0].Name)
	}
}

func TestParseImport(t *testing.T) {
	doc := mustParse(t, `import "math"`)
	imp, ok := doc.Stmts[0].(*ast.Import)
	if !ok {
		t.Fatalf("expected Import, got %T", doc.Stmts[0])
	}
	if imp.Path != "math" {
		t.Errorf("expected path math, got %q", imp.Path)
	}
}

func TestParseImportAliased(t *testing.T) {
	doc := mustParse(t, `import m "math"`)
	imp := doc.Stmts[0].(*ast.Import)
	if imp.Alias != "m" || imp.Path != "math" || imp.Replace != "" {
		t.Errorf("expected alias=m path=math replace=\"\", got %q %q %q", imp.Alias, imp.Path, imp.Replace)
	}
}

func TestParseImportReplace(t *testing.T) {
	doc := mustParse(t, `import "math" => "git://example.com/math@v1#-"`)
	imp := doc.Stmts[0].(*ast.Import)
	if imp.Alias != "" || imp.Path != "math" || imp.Replace != "git://example.com/math@v1#-" {
		t.Errorf("replace form: got alias=%q path=%q replace=%q", imp.Alias, imp.Path, imp.Replace)
	}
}

func TestParseImportAliasAndReplace(t *testing.T) {
	doc := mustParse(t, `import m "math" => "git://example.com/math@v1#-"`)
	imp := doc.Stmts[0].(*ast.Import)
	if imp.Alias != "m" || imp.Path != "math" || imp.Replace != "git://example.com/math@v1#-" {
		t.Errorf("alias+replace: got alias=%q path=%q replace=%q", imp.Alias, imp.Path, imp.Replace)
	}
}

func TestParseFuncExprBody(t *testing.T) {
	doc := mustParse(t, `func add(a int, b int) => a + b`)
	fd, ok := doc.Stmts[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("expected FuncDef, got %T", doc.Stmts[0])
	}
	if fd.Name != "add" {
		t.Errorf("expected name add, got %q", fd.Name)
	}
	if len(fd.Params.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fd.Params.Params))
	}
	if fd.Body == nil {
		t.Error("expected expression body")
	}
}

func TestParseFuncBlockBody(t *testing.T) {
	doc := mustParse(t, `func greet(name string) {
		return name
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	if !fd.Block.IsDefined() {
		t.Error("expected block body")
	}
	if len(fd.Block.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in block, got %d", len(fd.Block.Stmts))
	}
	ret, ok := fd.Block.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected ReturnStmt, got %T", fd.Block.Stmts[0])
	}
	if ret.Value == nil {
		t.Error("expected return value")
	}
}

func TestParseFuncMethod(t *testing.T) {
	doc := mustParse(t, `func int.double => self * 2`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	if fd.Name != "int.double" {
		t.Errorf("expected int.double, got %q", fd.Name)
	}
}

func TestParseFuncGeneric(t *testing.T) {
	doc := mustParse(t, `func identity<T>(x T) => x`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	if len(fd.TypeParams) != 1 || fd.TypeParams[0] != "T" {
		t.Errorf("expected type param T, got %v", fd.TypeParams)
	}
}

func TestParseVarDecl(t *testing.T) {
	doc := mustParse(t, `var count int = 0`)
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	if len(vd.Specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(vd.Specs))
	}
	if vd.Specs[0].Names[0] != "count" {
		t.Errorf("expected name count, got %v", vd.Specs[0].Names)
	}
}

func TestParseIfStmt(t *testing.T) {
	doc := mustParse(t, `func f {
		if x > 0 {
			return x
		} else {
			return 0
		}
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	ifStmt, ok := fd.Block.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt, got %T", fd.Block.Stmts[0])
	}
	if ifStmt.Cond == nil {
		t.Error("expected condition")
	}
	if !ifStmt.Else.IsDefined() {
		t.Error("expected else block")
	}
}

func TestParseForStmt(t *testing.T) {
	doc := mustParse(t, `func f {
		for i = items {
			Text(i)
		}
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	forStmt, ok := fd.Block.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", fd.Block.Stmts[0])
	}
	if forStmt.Key != "i" {
		t.Errorf("expected key i, got %q", forStmt.Key)
	}
}

func TestParseForStmtTwoVars(t *testing.T) {
	doc := mustParse(t, `func f {
		for k, v = map {
			Text(v)
		}
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	forStmt := fd.Block.Stmts[0].(*ast.ForStmt)
	if forStmt.Key != "k" || forStmt.Value != "v" {
		t.Errorf("expected k,v got %q,%q", forStmt.Key, forStmt.Value)
	}
}

func TestParseComponent(t *testing.T) {
	doc := mustParse(t, `component Button(label string) {
		Text(label)
	}`)
	cd, ok := doc.Stmts[0].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
	if cd.Name != "Button" {
		t.Errorf("expected Button, got %q", cd.Name)
	}
	if len(cd.Props.Props) != 1 {
		t.Fatalf("expected 1 prop, got %d", len(cd.Props.Props))
	}
}

func TestParseVisualNode(t *testing.T) {
	doc := mustParse(t, `component App {
		Button(label="Click") {
			Text("hello")
		}
	}`)
	cd := doc.Stmts[0].(*ast.ComponentDecl)
	if len(cd.Body.Stmts) != 1 {
		t.Fatalf("expected 1 stmt, got %d", len(cd.Body.Stmts))
	}
	vn, ok := cd.Body.Stmts[0].(*ast.VisualNode)
	if !ok {
		t.Fatalf("expected VisualNode, got %T", cd.Body.Stmts[0])
	}
	if vn.Target == nil {
		t.Error("expected target")
	}
	if len(vn.Args.Args) != 1 {
		t.Errorf("expected 1 arg, got %d", len(vn.Args.Args))
	}
	if !vn.Block.IsDefined() {
		t.Error("expected block body")
	}
}

func TestParseAssignment(t *testing.T) {
	doc := mustParse(t, `func f {
		x = 1
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	assign, ok := fd.Block.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", fd.Block.Stmts[0])
	}
	if assign.Op != ast.AssignSet {
		t.Errorf("expected AssignSet, got %v", assign.Op)
	}
}

func TestParseCompoundAssignment(t *testing.T) {
	doc := mustParse(t, `func f {
		x += 1
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	assign := fd.Block.Stmts[0].(*ast.AssignStmt)
	if assign.Op != ast.AssignAdd {
		t.Errorf("expected AssignAdd, got %v", assign.Op)
	}
}

func TestParseIncrement(t *testing.T) {
	doc := mustParse(t, `func f {
		x++
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	inc, ok := fd.Block.Stmts[0].(*ast.IncDecStmt)
	if !ok {
		t.Fatalf("expected IncDecStmt, got %T", fd.Block.Stmts[0])
	}
	if inc.IsDec {
		t.Errorf("expected increment, got decrement")
	}
	ident, ok := inc.Target.(*ast.IdentExpr)
	if !ok || ident.Name != "x" {
		t.Errorf("expected target x, got %v", inc.Target)
	}
}

func TestParseDecrement(t *testing.T) {
	doc := mustParse(t, `func f {
		x--
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	inc, ok := fd.Block.Stmts[0].(*ast.IncDecStmt)
	if !ok {
		t.Fatalf("expected IncDecStmt, got %T", fd.Block.Stmts[0])
	}
	if !inc.IsDec {
		t.Errorf("expected decrement, got increment")
	}
}

func TestParseToggle(t *testing.T) {
	doc := mustParse(t, `func f {
		visible!!
	}`)
	fd := doc.Stmts[0].(*ast.FuncDef)
	toggle, ok := fd.Block.Stmts[0].(*ast.ToggleStmt)
	if !ok {
		t.Fatalf("expected ToggleStmt, got %T", fd.Block.Stmts[0])
	}
	ident, ok := toggle.Target.(*ast.IdentExpr)
	if !ok || ident.Name != "visible" {
		t.Errorf("expected target visible, got %v", toggle.Target)
	}
}

func TestParseListLiteral(t *testing.T) {
	doc := mustParse(t, `const xs = [1, 2, 3]`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	list, ok := cd.Specs[0].Default.(*ast.ListExpr)
	if !ok {
		t.Fatalf("expected ListExpr, got %T", cd.Specs[0].Default)
	}
	if len(list.Elements) != 3 {
		t.Errorf("expected 3 elements, got %d", len(list.Elements))
	}
}

func TestParseAnonStructLit(t *testing.T) {
	doc := mustParse(t, `const p = {x=1, y=2}`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	s, ok := cd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr, got %T", cd.Specs[0].Default)
	}
	if s.Name != "" {
		t.Errorf("expected anonymous, got name %q", s.Name)
	}
	if len(s.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(s.Fields))
	}
}

func TestParseNamedStructLit(t *testing.T) {
	doc := mustParse(t, `const p = Point{x=1, y=2}`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	s, ok := cd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr, got %T", cd.Specs[0].Default)
	}
	if s.Name != "Point" {
		t.Errorf("expected name Point, got %q", s.Name)
	}
	if len(s.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(s.Fields))
	}
}

func TestParseQualifiedStructLit(t *testing.T) {
	doc := mustParse(t, `const p = geo.Point{x=1, y=2}`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	s, ok := cd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr, got %T", cd.Specs[0].Default)
	}
	if s.Package != "geo" || s.Name != "Point" {
		t.Errorf("expected geo.Point, got %q.%q", s.Package, s.Name)
	}
}

func TestParseEmptyStructLit(t *testing.T) {
	doc := mustParse(t, `const p = Point{}`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	s, ok := cd.Specs[0].Default.(*ast.StructExpr)
	if !ok {
		t.Fatalf("expected StructExpr, got %T", cd.Specs[0].Default)
	}
	if s.Name != "Point" {
		t.Errorf("expected Point, got %q", s.Name)
	}
	if len(s.Fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(s.Fields))
	}
}

func TestParseFuncLit(t *testing.T) {
	doc := mustParse(t, `const f = func(x) => x + 1`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	lam, ok := cd.Specs[0].Default.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr, got %T", cd.Specs[0].Default)
	}
	if len(lam.Params.Params) != 1 {
		t.Errorf("expected 1 param, got %d", len(lam.Params.Params))
	}
	if lam.Body == nil {
		t.Error("expected expression body")
	}
}

func TestParseSelectExpr(t *testing.T) {
	doc := mustParse(t, `const x = foo.bar`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	sel, ok := cd.Specs[0].Default.(*ast.SelectExpr)
	if !ok {
		t.Fatalf("expected SelectExpr, got %T", cd.Specs[0].Default)
	}
	if sel.Field != "bar" {
		t.Errorf("expected field bar, got %q", sel.Field)
	}
}

func TestParseCallExpr(t *testing.T) {
	doc := mustParse(t, `const x = foo(1, 2)`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	call, ok := cd.Specs[0].Default.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", cd.Specs[0].Default)
	}
	if len(call.Args.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(call.Args.Args))
	}
}

func TestParseIndexExpr(t *testing.T) {
	doc := mustParse(t, `const x = arr[0]`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	idx, ok := cd.Specs[0].Default.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected IndexExpr, got %T", cd.Specs[0].Default)
	}
	if idx.Index == nil {
		t.Error("expected index expression")
	}
}

func TestParseDisabledDecl(t *testing.T) {
	doc := mustParse(t, `/- const x = 1`)
	dd, ok := doc.Stmts[0].(*ast.DisabledDecl)
	if !ok {
		t.Fatalf("expected DisabledDecl, got %T", doc.Stmts[0])
	}
	if dd.Inner == nil {
		t.Error("expected inner stmt")
	}
}

func TestParseMarkedDecl(t *testing.T) {
	src := `#[canvas.shape]
component rect() {}`
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	comp, ok := doc.Stmts[0].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
	if len(comp.Attrs) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(comp.Attrs))
	}
	attr := comp.Attrs[0]
	if attr.Alias != "canvas" || attr.Name != "shape" {
		t.Errorf("expected canvas.shape, got %s.%s", attr.Alias, attr.Name)
	}
}

func TestParseMarkedDeclWithArgs(t *testing.T) {
	src := "#[canvas.shape(\"rect\", 1)]\ncomponent foo() {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	comp, ok := doc.Stmts[0].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
	if len(comp.Attrs[0].Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(comp.Attrs[0].Args))
	}
}

func TestParseMarkedDeclBareNameNoAlias(t *testing.T) {
	src := "#[shape]\ncomponent foo() {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	comp, ok := doc.Stmts[0].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
	attr := comp.Attrs[0]
	if attr.Alias != "" || attr.Name != "shape" {
		t.Errorf("expected bare name shape, got alias=%q name=%q", attr.Alias, attr.Name)
	}
}

func TestParseMarkedDeclNestedBrackets(t *testing.T) {
	// #[ with args that contain subscript — must not trigger spurious semicolons
	src := "#[canvas.items(arr)]\ncomponent foo() {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	comp, ok := doc.Stmts[0].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
	if len(comp.Attrs) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(comp.Attrs))
	}
}

func TestParseType(t *testing.T) {
	doc := mustParse(t, `const x List<int> = [1]`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	nt, ok := cd.Specs[0].Type.(*ast.NamedType)
	if !ok {
		t.Fatalf("expected NamedType, got %T", cd.Specs[0].Type)
	}
	if nt.Name != "List" {
		t.Errorf("expected List, got %q", nt.Name)
	}
	if len(nt.TypeArgs) == 0 {
		t.Error("expected type args")
	}
}

func TestParseUnitDecl(t *testing.T) {
	doc := mustParse(t, `unit Length { px, em, rem }`)
	ud, ok := doc.Stmts[0].(*ast.UnitDef)
	if !ok {
		t.Fatalf("expected UnitDef, got %T", doc.Stmts[0])
	}
	if ud.Name != "Length" {
		t.Errorf("expected Length, got %q", ud.Name)
	}
	if len(ud.Suffixes) != 3 {
		t.Errorf("expected 3 suffixes, got %d", len(ud.Suffixes))
	}
}

func TestParsePos(t *testing.T) {
	doc := mustParse(t, `const x = 1`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	if cd.Pos.Line != 1 || cd.Pos.Column != 1 {
		t.Errorf("expected pos 1:1, got %d:%d", cd.Pos.Line, cd.Pos.Column)
	}
}

func identNameTest(e ast.Expr) string {
	if id, ok := e.(*ast.IdentExpr); ok {
		return id.Name
	}
	return ""
}

func TestParseConstExpr(t *testing.T) {
	// `const` is a unary-precedence prefix; paren groups the operand.
	doc := mustParse(t, `const x = const (1 + 2)`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	ce, ok := cd.Specs[0].Default.(*ast.ConstExpr)
	if !ok {
		t.Fatalf("expected ConstExpr, got %T", cd.Specs[0].Default)
	}
	paren, ok := ce.Operand.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected ParenExpr inside const, got %T", ce.Operand)
	}
	bin, ok := paren.Inner.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr operand, got %T", paren.Inner)
	}
	if bin.Op != ast.BinAdd {
		t.Errorf("expected BinAdd, got %v", bin.Op)
	}
}

func TestParseConstExprUnaryPrecedence(t *testing.T) {
	// Without parens, `const` binds like a unary op: `const 1 + 2` → (const 1) + 2.
	doc := mustParse(t, `const x = const 1 + 2`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	bin, ok := cd.Specs[0].Default.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr at top, got %T", cd.Specs[0].Default)
	}
	if _, ok := bin.Left.(*ast.ConstExpr); !ok {
		t.Errorf("expected ConstExpr on left, got %T", bin.Left)
	}
}

func TestParseConstExprInArg(t *testing.T) {
	// `const` as arg prefix must route through Arg's kw_const branch.
	doc := mustParse(t, `const x = foo(const 1)`)
	cd := doc.Stmts[0].(*ast.ConstDecl)
	call, ok := cd.Specs[0].Default.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", cd.Specs[0].Default)
	}
	if len(call.Args.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(call.Args.Args))
	}
	arg := call.Args.Args[0].(ast.Arg)
	if _, ok := arg.Value.(*ast.ConstExpr); !ok {
		t.Errorf("expected ConstExpr arg, got %T", arg.Value)
	}
}

func TestParseTrailingInputErrors(t *testing.T) {
	// Inputs that contain a valid prefix followed by a token the grammar
	// can't continue from must error rather than silently truncate.
	cases := []struct {
		name string
		src  string
	}{
		{"trailing_amp", "var b = 5 & 3"},
		{"trailing_bang", "var b = 5 ! 3"},
		{"trailing_garbage_after_decl", "const x = 1\n) extra"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse("test.sngl", []byte(c.src))
			if err == nil {
				t.Fatalf("expected error for %q; got none", c.src)
			}
		})
	}
}

func TestParseGenericStruct(t *testing.T) {
	src := "struct list<T> {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Stmts[0].(*ast.StructDef)
	if s.Name != "list" {
		t.Errorf("Name = %q, want list", s.Name)
	}
	if len(s.TypeParams) != 1 || s.TypeParams[0] != "T" {
		t.Errorf("TypeParams = %v, want [T]", s.TypeParams)
	}
}

func TestParseGenericStructTwoParams(t *testing.T) {
	src := "struct map<K, V> {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Stmts[0].(*ast.StructDef)
	if len(s.TypeParams) != 2 || s.TypeParams[0] != "K" || s.TypeParams[1] != "V" {
		t.Errorf("TypeParams = %v, want [K V]", s.TypeParams)
	}
}

func TestParseStructNoTypeParamsStillWorks(t *testing.T) {
	src := "struct Color {\n\tr int\n\tg int\n\tb int\n}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Stmts[0].(*ast.StructDef)
	if len(s.TypeParams) != 0 {
		t.Errorf("TypeParams should be empty for non-generic struct, got %v", s.TypeParams)
	}
}

// --- Generic receiver method tests (B1+B2) ---

func firstFuncDef(t *testing.T, doc *ast.Document) *ast.FuncDef {
	t.Helper()
	for _, s := range doc.Stmts {
		if f, ok := s.(*ast.FuncDef); ok {
			return f
		}
	}
	t.Fatal("no FuncDef in document")
	return nil
}

func TestParseGenericReceiverMethod(t *testing.T) {
	src := "struct list<T> {}\nfunc list<T>.length() int {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := firstFuncDef(t, doc)
	if len(fn.RecvTypeParams) != 1 || fn.RecvTypeParams[0] != "T" {
		t.Errorf("RecvTypeParams = %v, want [T]", fn.RecvTypeParams)
	}
	if len(fn.TypeParams) != 0 {
		t.Errorf("TypeParams should be empty (no method-level params), got %v", fn.TypeParams)
	}
}

func TestParseGenericReceiverWithTwoParams(t *testing.T) {
	src := "struct map<K, V> {}\nfunc map<K, V>.keys() list<K> {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := firstFuncDef(t, doc)
	if len(fn.RecvTypeParams) != 2 || fn.RecvTypeParams[0] != "K" || fn.RecvTypeParams[1] != "V" {
		t.Errorf("RecvTypeParams = %v, want [K V]", fn.RecvTypeParams)
	}
}

func TestParsePlainMethodStillWorks(t *testing.T) {
	src := "struct Color {\n\tr int\n}\nfunc Color.lighten() int { return 0 }"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := firstFuncDef(t, doc)
	if len(fn.RecvTypeParams) != 0 {
		t.Errorf("RecvTypeParams should be empty for plain receiver, got %v", fn.RecvTypeParams)
	}
}

func TestParseGenericFunctionStillWorks(t *testing.T) {
	src := "func id<T>(x T) T { return x }"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := doc.Stmts[0].(*ast.FuncDef)
	if len(fn.TypeParams) != 1 || fn.TypeParams[0] != "T" {
		t.Errorf("TypeParams = %v, want [T]", fn.TypeParams)
	}
	if len(fn.RecvTypeParams) != 0 {
		t.Errorf("RecvTypeParams should be empty, got %v", fn.RecvTypeParams)
	}
}

func TestParseGenericReceiverWithMethodTypeParam(t *testing.T) {
	src := "struct list<T> {}\nfunc list<T>.map<U>(f func(T) U) list<U> {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var fn *ast.FuncDef
	for _, s := range doc.Stmts {
		if f, ok := s.(*ast.FuncDef); ok {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no FuncDef")
	}
	if len(fn.RecvTypeParams) != 1 || fn.RecvTypeParams[0] != "T" {
		t.Errorf("RecvTypeParams = %v, want [T]", fn.RecvTypeParams)
	}
	if len(fn.TypeParams) != 1 || fn.TypeParams[0] != "U" {
		t.Errorf("TypeParams = %v, want [U]", fn.TypeParams)
	}
}

func TestParseContextDecl(t *testing.T) {
	// Context declarations are parsed as visual nodes at top level.
	// The syntax is: context #identifier(arg)
	doc := mustParse(t, `context #theme("light")

window #home(title="Home", href="/") {
    text(value="hello")
}`)
	if len(doc.Stmts) != 2 {
		t.Fatalf("expected 2 stmts, got %d", len(doc.Stmts))
	}

	// First statement should be the context declaration (as a CallStmt)
	cs, ok := doc.Stmts[0].(*ast.CallStmt)
	if !ok {
		t.Fatalf("expected CallStmt for context decl, got %T", doc.Stmts[0])
	}

	// The call's func should be the bare `context` identifier carrying the
	// element-ref id (`context #theme`).
	ident, ok := cs.Call.Func.(*ast.IdentExpr)
	if !ok {
		t.Fatalf("expected IdentExpr, got %T", cs.Call.Func)
	}
	if ident.Name != "context" {
		t.Errorf("expected callee 'context', got %q", ident.Name)
	}
	if cs.Call.ID != "theme" {
		t.Errorf("expected element-ref id 'theme', got %q", cs.Call.ID)
	}

	// Second statement should be the window declaration
	vn, ok := doc.Stmts[1].(*ast.VisualNode)
	if !ok {
		t.Fatalf("expected VisualNode for window, got %T", doc.Stmts[1])
	}
	if vn.ID != "home" {
		t.Errorf("expected window id 'home', got %q", vn.ID)
	}
}

func TestParseFuncTypeNamedParams(t *testing.T) {
	doc := mustParse(t, `var f func(x int, y string) bool`)
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	ft, ok := vd.Specs[0].Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", vd.Specs[0].Type)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(ft.Params))
	}
	if ft.Params[0].Name != "x" {
		t.Errorf("params[0].Name = %q, want %q", ft.Params[0].Name, "x")
	}
	if _, ok := ft.Params[0].Type.(*ast.NamedType); !ok {
		t.Errorf("params[0].Type = %T, want *ast.NamedType", ft.Params[0].Type)
	}
	if ft.Params[1].Name != "y" {
		t.Errorf("params[1].Name = %q, want %q", ft.Params[1].Name, "y")
	}
}

func TestParseFuncTypeAnonParams(t *testing.T) {
	doc := mustParse(t, `var f func(int, string) bool`)
	vd, ok := doc.Stmts[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
	}
	ft, ok := vd.Specs[0].Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", vd.Specs[0].Type)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(ft.Params))
	}
	if ft.Params[0].Name != "" {
		t.Errorf("params[0].Name = %q, want empty (anonymous)", ft.Params[0].Name)
	}
	if ft.Params[1].Name != "" {
		t.Errorf("params[1].Name = %q, want empty (anonymous)", ft.Params[1].Name)
	}
	nt0, ok := ft.Params[0].Type.(*ast.NamedType)
	if !ok {
		t.Errorf("params[0].Type = %T, want *ast.NamedType", ft.Params[0].Type)
	} else if nt0.Name != "int" {
		t.Errorf("params[0].Type.Name = %q, want %q", nt0.Name, "int")
	}
	nt1, ok := ft.Params[1].Type.(*ast.NamedType)
	if !ok {
		t.Errorf("params[1].Type = %T, want *ast.NamedType", ft.Params[1].Type)
	} else if nt1.Name != "string" {
		t.Errorf("params[1].Type.Name = %q, want %q", nt1.Name, "string")
	}
}

func TestFormatMarkedDecl(t *testing.T) {
	// Empty-param list is elided by the formatter; use the canonical form.
	src := "#[canvas.shape]\ncomponent rect {}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Format(doc)
	// Format adds a trailing newline
	want := src + "\n"
	if got != want {
		t.Errorf("format round-trip mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestParseTestdata(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			_, err := Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Errorf("parse failed: %v", err)
			}
		})
	}
}

func TestFormatStructFieldAttr(t *testing.T) {
	// A field mark is part of the field. If the formatter dropped it, `sngl
	// fmt` would silently delete a codegen fact from a source file.
	src := "struct Entry {\n    #[foreign(\"Title\")]\n    title string\n}"
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Format(doc)
	want := src + "\n"
	if got != want {
		t.Errorf("format round-trip mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
