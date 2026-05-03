package parser

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Format formats a parsed document back to source.
func Format(doc *ast.Document) string {
	var buf strings.Builder
	f := newFormatter(&buf)
	f.formatDocument(doc)
	return buf.String()
}

// FormatTo writes the formatted document to w.
func FormatTo(doc *ast.Document, w io.Writer) (int, error) {
	var buf strings.Builder
	f := newFormatter(&buf)
	f.formatDocument(doc)
	return io.WriteString(w, buf.String())
}

// FormatExpr formats a single expression.
func FormatExpr(e ast.Expr) string {
	var buf strings.Builder
	f := newFormatter(&buf)
	f.writeExpr(e)
	return buf.String()
}

// FormatType formats a single type expression.
func FormatType(te ast.TypeExpr) string {
	if te == nil {
		return ""
	}
	var buf strings.Builder
	f := newFormatter(&buf)
	f.writeType(te)
	return buf.String()
}

// --- formatter ---

const indentStr = "    " // 4 spaces

type formatter struct {
	buf          *strings.Builder
	indent       int
	atLineStart  bool
	pendingBlank bool
}

func newFormatter(buf *strings.Builder) *formatter {
	return &formatter{
		buf:         buf,
		atLineStart: true,
	}
}

func (f *formatter) write(s string) {
	if f.atLineStart && s != "" && s != "\n" {
		f.writeIndent()
		f.atLineStart = false
	}
	f.buf.WriteString(s)
}

func (f *formatter) writef(format string, args ...any) {
	f.write(fmt.Sprintf(format, args...))
}

func (f *formatter) writeIndent() {
	for range f.indent {
		f.buf.WriteString(indentStr)
	}
}

func (f *formatter) newline() {
	f.buf.WriteByte('\n')
	f.atLineStart = true
	f.pendingBlank = false
}

func (f *formatter) blankLine() {
	if f.pendingBlank {
		return
	}
	f.buf.WriteByte('\n')
	f.pendingBlank = true
}

func (f *formatter) flushBlank() {
	if f.pendingBlank {
		f.pendingBlank = false
	}
}

// --- operator string tables ---

var binOpStr = [...]string{
	ast.BinAdd: "+",
	ast.BinSub: "-",
	ast.BinMul: "*",
	ast.BinDiv: "/",
	ast.BinMod: "%",
	ast.BinEq:  "==",
	ast.BinNeq: "!=",
	ast.BinLt:  "<",
	ast.BinLte: "<=",
	ast.BinGt:  ">",
	ast.BinGte: ">=",
	ast.BinAnd: "&&",
	ast.BinOr:  "||",
}

var unaryOpStr = [...]string{
	ast.UnaryNot: "!",
	ast.UnaryNeg: "-",
}

var assignOpStr = [...]string{
	ast.AssignSet: "=",
	ast.AssignAdd: "+=",
	ast.AssignSub: "-=",
	ast.AssignMul: "*=",
	ast.AssignDiv: "/=",
	ast.AssignMod: "%=",
}

// --- document ---

func (f *formatter) formatDocument(doc *ast.Document) {
	prevLine := 0
	for i, s := range doc.Stmts {
		pos := s.StmtPos()

		if i > 0 && prevLine > 0 {
			if pos != nil && pos.Line > prevLine+1 {
				f.blankLine()
			}
		}

		f.formatStmt(s)
		f.newline()

		if pos != nil && pos.Line > 0 {
			prevLine = f.endLine(s)
		}
	}
}

// endLine estimates the last line of a statement for blank-line spacing.
func (f *formatter) endLine(s ast.Stmt) int {
	switch x := s.(type) {
	case *ast.FuncDef:
		if x.Block.IsDefined() {
			return f.blockEndLine(x.Pos.Line, &x.Block)
		}
		return x.Pos.Line
	case *ast.ComponentDecl:
		return f.blockEndLine(x.Pos.Line, &x.Body)
	case *ast.VisualNode:
		if x.Block.IsDefined() {
			return f.blockEndLine(x.Pos.Line, &x.Block)
		}
		return x.Pos.Line
	case *ast.IfStmt:
		if x.Else.IsDefined() {
			return f.blockEndLine(x.Pos.Line, &x.Else)
		}
		return f.blockEndLine(x.Pos.Line, &x.Body)
	case *ast.ForStmt:
		if x.Else.IsDefined() {
			return f.blockEndLine(x.Pos.Line, &x.Else)
		}
		return f.blockEndLine(x.Pos.Line, &x.Body)
	case *ast.PlatformStmt:
		return f.blockEndLine(x.Pos.Line, &x.Body)
	case *ast.StructDef:
		if x.IsMultiline {
			return x.Pos.Line + len(x.Fields) + 1
		}
	case *ast.EnumDef:
		if x.IsMultiline {
			return x.Pos.Line + len(x.Members) + 1
		}
	}
	if p := s.StmtPos(); p != nil {
		return p.Line
	}
	return 0
}

func (f *formatter) blockEndLine(start int, block *ast.StmtBlock) int {
	if block.IsMultiline {
		return start + len(block.Stmts) + 1
	}
	return start
}

// --- top-level and block statements ---

func (f *formatter) formatStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Import:
		f.writeImport(x)
	case *ast.StructDef:
		f.writeStructDef(x)
	case *ast.EnumDef:
		f.writeEnumDef(x)
	case *ast.UnitDef:
		f.writeUnitDef(x)
	case *ast.ConstDecl:
		f.writeConstDecl(x)
	case *ast.VarDecl:
		f.writeVarDecl(x)
	case *ast.FuncDef:
		f.writeFuncDef(x)
	case *ast.ComponentDecl:
		f.writeComponentDecl(x)
	case *ast.VisualNode:
		f.writeVisualNode(x)
	case *ast.IfStmt:
		f.writeIfStmt(x)
	case *ast.ForStmt:
		f.writeForStmt(x)
	case *ast.PlatformStmt:
		f.writePlatformStmt(x)
	case *ast.AssignStmt:
		f.writeAssignStmt(x)
	case *ast.ToggleStmt:
		f.writeToggleStmt(x)
	case *ast.IncDecStmt:
		f.writeIncDecStmt(x)
	case *ast.EmitStmt:
		f.writeEmitStmt(x)
	case *ast.VarStmt:
		f.writeVarStmt(x)
	case *ast.ReturnStmt:
		f.writeReturnStmt(x)
	case *ast.CallStmt:
		f.writeExpr(x.Call)
	case *ast.Comment:
		f.writeComment(x)
	case *ast.DisabledDecl:
		f.writeDisabledDecl(x)
	}
}

// --- imports ---

func (f *formatter) writeImport(imp *ast.Import) {
	switch {
	case imp.Replace != "" && imp.Alias != "":
		f.writef("import %s %q => %q", imp.Alias, imp.Path, imp.Replace)
	case imp.Replace != "":
		f.writef("import %q => %q", imp.Path, imp.Replace)
	case imp.Alias != "":
		f.writef("import %s %q", imp.Alias, imp.Path)
	default:
		f.writef("import %q", imp.Path)
	}
}

// --- struct ---

func (f *formatter) writeStructDef(s *ast.StructDef) {
	f.write("struct ")
	if s.Name != "" {
		f.write(s.Name)
		f.write(" ")
	}
	if len(s.Fields) == 0 {
		f.write("{}")
		return
	}
	// Struct fields require semicolons (ASI newlines), so always multiline.
	f.write("{")
	f.newline()
	f.indent++
	for _, field := range s.Fields {
		f.write(strings.Join(field.Names, ", "))
		if field.Type != nil {
			f.write(" ")
			f.writeType(field.Type)
		}
		if field.Default != nil {
			f.write(" = ")
			f.writeExpr(field.Default)
		}
		f.newline()
	}
	f.indent--
	f.write("}")
}

// --- enum ---

func (f *formatter) writeEnumDef(e *ast.EnumDef) {
	f.write("enum ")
	if e.Name != "" {
		f.write(e.Name)
		f.write(" ")
	}
	f.write("{")
	if e.IsMultiline {
		f.newline()
		f.indent++
		for _, m := range e.Members {
			f.write(m.Name)
			if m.Value != nil {
				f.write(" = ")
				f.writeExpr(m.Value)
			}
			f.newline()
		}
		f.indent--
		f.write("}")
	} else {
		f.write(" ")
		for i, m := range e.Members {
			if i > 0 {
				f.write(", ")
			}
			f.write(m.Name)
			if m.Value != nil {
				f.write(" = ")
				f.writeExpr(m.Value)
			}
		}
		f.write(" }")
	}
}

// --- unit ---

func (f *formatter) writeUnitDef(u *ast.UnitDef) {
	f.write("unit ")
	if u.Name != "" {
		f.write(u.Name)
		f.write(" ")
	}
	f.write("{")
	if u.IsMultiline {
		f.newline()
		f.indent++
		for _, s := range u.Suffixes {
			f.write(s.Name)
			if s.Factor != nil {
				f.write(" = ")
				f.writeExpr(s.Factor)
			}
			f.newline()
		}
		f.indent--
		f.write("}")
	} else {
		f.write(" ")
		for i, s := range u.Suffixes {
			if i > 0 {
				f.write(", ")
			}
			f.write(s.Name)
			if s.Factor != nil {
				f.write(" = ")
				f.writeExpr(s.Factor)
			}
		}
		f.write(" }")
	}
}

// --- const ---

func (f *formatter) writeConstDecl(c *ast.ConstDecl) {
	if c.IsGrouped {
		f.write("const (")
		f.newline()
		f.indent++
		for _, spec := range c.Specs {
			f.writeVarSpec(spec)
			f.newline()
		}
		f.indent--
		f.write(")")
	} else {
		f.write("const ")
		for i, spec := range c.Specs {
			if i > 0 {
				f.write(", ")
			}
			f.writeVarSpec(spec)
		}
	}
}

// --- var ---

func (f *formatter) writeVarDecl(v *ast.VarDecl) {
	if v.IsGrouped {
		f.write("var (")
		f.newline()
		f.indent++
		for _, spec := range v.Specs {
			f.writeVarSpec(spec)
			f.newline()
		}
		f.indent--
		f.write(")")
	} else {
		f.write("var ")
		for i, spec := range v.Specs {
			if i > 0 {
				f.write(", ")
			}
			f.writeVarSpec(spec)
		}
	}
}

func (f *formatter) writeVarSpec(spec ast.VarSpec) {
	for i, name := range spec.Names {
		if i > 0 {
			f.write(", ")
		}
		f.write(name)
	}
	if spec.Type != nil {
		f.write(" ")
		f.writeType(spec.Type)
	}
	if spec.Default != nil {
		f.write(" = ")
		f.writeExpr(spec.Default)
	}
	for _, h := range spec.Handlers {
		f.write(" ")
		f.writeEventHandler(h)
	}
}

// --- func ---

func (f *formatter) writeFuncDef(fn *ast.FuncDef) {
	f.write("func ")
	f.write(fn.Name)
	if len(fn.TypeParams) > 0 {
		f.write("<")
		f.write(strings.Join(fn.TypeParams, ", "))
		f.write(">")
	}
	f.write("(")
	f.writeParams(fn.Params)
	f.write(")")
	if fn.Body != nil {
		// Expression form: func name(params) => expr
		f.write(" => ")
		f.writeExpr(fn.Body)
	} else {
		// Block form: func name(params) ReturnType { stmts }
		if fn.ReturnType != nil {
			f.write(" ")
			f.writeType(fn.ReturnType)
		}
		f.write(" ")
		f.writeBlock(&fn.Block)
	}
}

// --- component ---

func (f *formatter) writeComponentDecl(c *ast.ComponentDecl) {
	f.write("component ")
	f.write(c.Name)
	if len(c.Props.Props) > 0 {
		f.write("(")
		f.writeProps(c.Props)
		f.write(")")
	}
	if c.ChildrenType != nil {
		f.write(" ")
		f.writeType(c.ChildrenType)
	}
	f.write(" ")
	f.writeBlock(&c.Body)
}

// --- visual node ---

func (f *formatter) writeVisualNode(vn *ast.VisualNode) {
	f.writeExpr(vn.Target)
	if vn.ID != "" {
		f.write(" #")
		f.write(vn.ID)
	}
	if len(vn.Args.Args) > 0 {
		f.write("(")
		f.writeArgs(vn.Args)
		f.write(")")
	}
	if vn.Block.IsDefined() {
		f.write(" ")
		f.writeBlock(&vn.Block)
	}
}

// --- control flow ---

func (f *formatter) writeIfStmt(s *ast.IfStmt) {
	f.write("if ")
	f.writeExpr(s.Cond)
	f.write(" ")
	f.writeBlock(&s.Body)
	if s.Else.IsDefined() {
		f.write(" else ")
		f.writeBlock(&s.Else)
	}
}

func (f *formatter) writeForStmt(s *ast.ForStmt) {
	f.write("for ")
	f.write(s.Key)
	if s.Value != "" {
		f.write(", ")
		f.write(s.Value)
	}
	f.write(" = ")
	f.writeExpr(s.Iter)
	f.write(" ")
	f.writeBlock(&s.Body)
	if s.Else.IsDefined() {
		f.write(" else ")
		f.writeBlock(&s.Else)
	}
}

func (f *formatter) writePlatformStmt(s *ast.PlatformStmt) {
	f.write("platform ")
	f.write(s.Platform)
	f.write(" ")
	f.writeBlock(&s.Body)
}

// --- block statements ---

func (f *formatter) writeAssignStmt(s *ast.AssignStmt) {
	f.writeExpr(s.Target)
	f.write(" ")
	f.write(assignOpStr[s.Op])
	f.write(" ")
	f.writeExpr(s.Value)
}

func (f *formatter) writeToggleStmt(s *ast.ToggleStmt) {
	f.writeExpr(s.Target)
	f.write("!!")
}

func (f *formatter) writeIncDecStmt(s *ast.IncDecStmt) {
	f.writeExpr(s.Target)
	if s.IsDec {
		f.write("--")
	} else {
		f.write("++")
	}
}

func (f *formatter) writeEmitStmt(s *ast.EmitStmt) {
	f.write("@")
	f.write(s.Name)
	if len(s.Args.Args) > 0 {
		f.write("(")
		f.writeArgs(s.Args)
		f.write(")")
	}
}

func (f *formatter) writeVarStmt(s *ast.VarStmt) {
	f.write("var ")
	f.write(s.Name)
	if s.Type != nil {
		f.write(" ")
		f.writeType(s.Type)
	}
	if s.Init != nil {
		f.write(" = ")
		f.writeExpr(s.Init)
	}
}

func (f *formatter) writeReturnStmt(s *ast.ReturnStmt) {
	f.write("return")
	if s.Value != nil {
		f.write(" ")
		f.writeExpr(s.Value)
	}
}

// --- block ---

func (f *formatter) writeBlock(block *ast.StmtBlock) {
	f.write("{")
	if block.IsMultiline {
		f.newline()
		f.indent++
		for _, s := range block.Stmts {
			f.formatStmt(s)
			f.newline()
		}
		f.indent--
		f.write("}")
	} else {
		if len(block.Stmts) > 0 {
			f.write(" ")
			for i, s := range block.Stmts {
				if i > 0 {
					f.write("; ")
				}
				f.formatStmt(s)
			}
			f.write(" ")
		}
		f.write("}")
	}
}

// --- args ---

func (f *formatter) writeArgs(args ast.ArgList) {
	if args.IsMultiline {
		f.newline()
		f.indent++
		for i, a := range args.Args {
			if i > 0 {
				f.write(",")
				f.newline()
			}
			f.writeArgOrHandler(a)
		}
		f.write(",")
		f.newline()
		f.indent--
	} else {
		for i, a := range args.Args {
			if i > 0 {
				f.write(", ")
			}
			f.writeArgOrHandler(a)
		}
	}
}

func (f *formatter) writeArgOrHandler(a ast.ArgOrEventHandler) {
	switch v := a.(type) {
	case ast.Arg:
		if v.Name != "" {
			f.write(v.Name)
			f.write("=")
		}
		f.writeExpr(v.Value)
	case ast.EventHandler:
		f.writeEventHandler(v)
	}
}

func (f *formatter) writeEventHandler(h ast.EventHandler) {
	f.write("@")
	f.write(h.Name)
	if len(h.Params.Params) > 0 {
		f.write("(")
		f.writeParams(h.Params)
		f.write(")")
	}
	f.write(" ")
	f.writeBlock(&h.Body)
}

// --- params ---

func (f *formatter) writeParams(pl ast.ParamList) {
	if pl.IsMultiline {
		f.newline()
		f.indent++
		for i, p := range pl.Params {
			if i > 0 {
				f.write(",")
				f.newline()
			}
			f.writeParam(p)
		}
		f.write(",")
		f.newline()
		f.indent--
	} else {
		for i, p := range pl.Params {
			if i > 0 {
				f.write(", ")
			}
			f.writeParam(p)
		}
	}
}

func (f *formatter) writeParam(p ast.Param) {
	if p.Bidirectional {
		f.write(":")
	}
	f.write(p.Name)
	if p.Type != nil {
		f.write(" ")
		f.writeType(p.Type)
	}
	if p.Default != nil {
		f.write(" = ")
		f.writeExpr(p.Default)
	}
}

// --- props (component params) ---

func (f *formatter) writeProps(pl ast.PropList) {
	if pl.IsMultiline {
		f.newline()
		f.indent++
		for i, p := range pl.Props {
			if i > 0 {
				f.write(",")
				f.newline()
			}
			f.writePropOrEvent(p)
		}
		f.write(",")
		f.newline()
		f.indent--
	} else {
		for i, p := range pl.Props {
			if i > 0 {
				f.write(", ")
			}
			f.writePropOrEvent(p)
		}
	}
}

func (f *formatter) writePropOrEvent(p ast.ParamOrEventDecl) {
	switch v := p.(type) {
	case ast.Param:
		f.writeParam(v)
	case ast.EventDecl:
		f.write("@")
		f.write(v.Name)
		if v.Type != nil {
			f.write(" ")
			f.writeType(v.Type)
		}
	}
}

// --- expressions ---

func (f *formatter) writeExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.LiteralExpr:
		f.writeLiteral(x)
	case *ast.UnitLiteral:
		// x.Raw already includes the suffix (parser stores "1rem" in Raw).
		f.write(x.Raw)
	case *ast.IdentExpr:
		f.write(x.Name)
	case *ast.EventRefExpr:
		f.write("@")
		f.write(x.Name)
	case *ast.ElementRefExpr:
		f.write("#")
		f.write(x.Name)
	case *ast.BinaryExpr:
		f.writeExpr(x.Left)
		f.write(" ")
		f.write(binOpStr[x.Op])
		f.write(" ")
		f.writeExpr(x.Right)
	case *ast.UnaryExpr:
		f.write(unaryOpStr[x.Op])
		f.writeExpr(x.Operand)
	case *ast.TernaryExpr:
		f.writeExpr(x.Cond)
		f.write(" ? ")
		f.writeExpr(x.Then)
		f.write(" : ")
		f.writeExpr(x.Else)
	case *ast.SelectExpr:
		f.writeSelectExpr(x)
	case *ast.IndexExpr:
		f.writeExpr(x.Operand)
		f.write("[")
		f.writeExpr(x.Index)
		f.write("]")
	case *ast.CallExpr:
		f.writeExpr(x.Func)
		f.write("(")
		f.writeArgs(x.Args)
		f.write(")")
	case *ast.StructExpr:
		f.writeStructExpr(x)
	case *ast.ListExpr:
		f.writeListExpr(x)
	case *ast.SpreadExpr:
		f.write("...")
		f.writeExpr(x.Operand)
	case *ast.InterpolationExpr:
		f.writeInterpolation(x)
	case *ast.LambdaExpr:
		f.writeLambda(x)
	case *ast.ParenExpr:
		f.write("(")
		f.writeExpr(x.Inner)
		f.write(")")
	case *ast.ConstExpr:
		f.write("const ")
		f.writeExpr(x.Operand)
	}
}

func (f *formatter) writeLiteral(lit *ast.LiteralExpr) {
	switch lit.Kind {
	case ast.LiteralStringQuoted:
		// Re-escape via the interpolation rules so that bare `{`/`}` in the
		// decoded literal don't reparse as the start of an interpolation.
		f.write(`"`)
		f.write(escapeInterpLiteral(lit.Raw, ast.StyleDouble))
		f.write(`"`)
	case ast.LiteralStringBackticked:
		f.write("`")
		f.write(lit.Raw)
		f.write("`")
	case ast.LiteralStringTrippleQuoted:
		f.write(`"""`)
		f.write(lit.Raw)
		f.write(`"""`)
	default:
		f.write(lit.Raw)
	}
}

func (f *formatter) writeSelectExpr(x *ast.SelectExpr) {
	f.writeExpr(x.Operand)
	switch x.Kind {
	case ast.SelectField:
		f.write(".")
		f.write(x.Field)
	case ast.SelectEvent:
		f.write(".@")
		f.write(x.Field)
	case ast.SelectElemRef:
		f.write(".#")
		f.write(x.Field)
	}
}

func (f *formatter) writeStructExpr(x *ast.StructExpr) {
	if x.Package != "" {
		f.write(x.Package)
		f.write(".")
	}
	if x.Name != "" {
		f.write(x.Name)
	}
	f.write("{")
	if x.Multiline {
		f.newline()
		f.indent++
		for i, field := range x.Fields {
			if i > 0 {
				f.write(",")
				f.newline()
			}
			f.writeStructFieldLit(field)
		}
		if len(x.Fields) > 0 {
			f.write(",")
		}
		f.newline()
		f.indent--
		f.write("}")
	} else {
		for i, field := range x.Fields {
			if i > 0 {
				f.write(", ")
			}
			f.writeStructFieldLit(field)
		}
		f.write("}")
	}
}

func (f *formatter) writeStructFieldLit(field ast.StructFieldLit) {
	if field.Spread {
		f.write("...")
		f.writeExpr(field.Value)
	} else {
		f.write(field.Name)
		f.write(" = ")
		f.writeExpr(field.Value)
	}
}

func (f *formatter) writeListExpr(x *ast.ListExpr) {
	f.write("[")
	if x.IsMultiline {
		f.newline()
		f.indent++
		for i, el := range x.Elements {
			if i > 0 {
				f.write(",")
				f.newline()
			}
			f.writeExpr(el)
		}
		if len(x.Elements) > 0 {
			f.write(",")
		}
		f.newline()
		f.indent--
		f.write("]")
	} else {
		for i, el := range x.Elements {
			if i > 0 {
				f.write(", ")
			}
			f.writeExpr(el)
		}
		f.write("]")
	}
}

func (f *formatter) writeInterpolation(x *ast.InterpolationExpr) {
	switch x.Style {
	case ast.StyleTriple:
		f.write(`"""`)
	case ast.StyleRaw:
		f.write("`")
	default:
		f.write(`"`)
	}
	for _, part := range x.Parts {
		if lit, ok := part.(*ast.LiteralExpr); ok {
			f.write(escapeInterpLiteral(lit.Raw, x.Style))
		} else {
			f.write("{")
			f.writeExpr(part)
			f.write("}")
		}
	}
	switch x.Style {
	case ast.StyleTriple:
		f.write(`"""`)
	case ast.StyleRaw:
		f.write("`")
	default:
		f.write(`"`)
	}
}

// escapeInterpLiteral re-escapes a literal segment of an interpolated string
// so that round-tripping through Format → Parse preserves meaning. Brace
// escapes (`\{`, `\}`) get added back: lexed segments hold the decoded
// content, so a literal `{` in the segment would otherwise be re-parsed as
// the start of an interpolation and a `\` as a stray escape.
func escapeInterpLiteral(s string, style ast.StringStyle) string {
	if style == ast.StyleRaw {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		case '"':
			if style == ast.StyleTriple {
				b.WriteRune(r)
			} else {
				b.WriteString(`\"`)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (f *formatter) writeLambda(x *ast.LambdaExpr) {
	f.write("func(")
	f.writeParams(x.Params)
	f.write(")")
	if x.Body != nil {
		f.write(" => ")
		f.writeExpr(x.Body)
	} else {
		if x.ReturnType != nil {
			f.write(" ")
			f.writeType(x.ReturnType)
		}
		f.write(" ")
		f.writeBlock(&x.Block)
	}
}

// --- type expressions ---

func (f *formatter) writeType(te ast.TypeExpr) {
	if te == nil {
		return
	}
	switch t := te.(type) {
	case *ast.NamedType:
		if t.Package != "" {
			f.write(t.Package)
			f.write(".")
		}
		f.write(t.Name)
		if len(t.TypeArgs) > 0 {
			f.write("<")
			for i, arg := range t.TypeArgs {
				if i > 0 {
					f.write(", ")
				}
				f.writeType(arg)
			}
			f.write(">")
		}
	case *ast.FuncType:
		f.write("func(")
		for i, p := range t.Params {
			if i > 0 {
				f.write(", ")
			}
			f.writeType(p)
		}
		f.write(")")
		if t.Return != nil {
			f.write(" ")
			f.writeType(t.Return)
		}
	case *ast.StructDef:
		f.writeStructDef(t)
	case *ast.EnumDef:
		f.writeEnumDef(t)
	case *ast.UnitDef:
		f.writeUnitDef(t)
	}
}

// --- comments ---

func (f *formatter) writeComment(c *ast.Comment) {
	f.write(c.Text)
}

// --- disabled ---

func (f *formatter) writeDisabledDecl(d *ast.DisabledDecl) {
	f.write("/- ")
	f.formatStmt(d.Inner)
}
