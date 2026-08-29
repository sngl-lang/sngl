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
	return alignTrailingComments(buf.String(), f.trailing)
}

// FormatTo writes the formatted document to w.
func FormatTo(doc *ast.Document, w io.Writer) (int, error) {
	var buf strings.Builder
	f := newFormatter(&buf)
	f.formatDocument(doc)
	return io.WriteString(w, alignTrailingComments(buf.String(), f.trailing))
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
	// doc is the document being formatted, read for the blank lines its
	// source had. Nil when a fragment is formatted on its own.
	doc *ast.Document
	// attrTrailer is a comment written after a declaration's mark, held while
	// the declaration is formatted so writeAttrs can put it back on that line.
	attrTrailer *ast.Comment
	// line counts the newlines written so far, and lineStart is where the
	// current line begins in buf. Together they place a trailing comment for
	// the alignment pass, which runs once every width is known.
	line      int
	lineStart int
	trailing  []trailingMark
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
	f.line++
	f.lineStart = f.buf.Len()
}

func (f *formatter) blankLine() {
	if f.pendingBlank {
		return
	}
	f.buf.WriteByte('\n')
	f.pendingBlank = true
	f.line++
	f.lineStart = f.buf.Len()
}

// trailingMark records where a comment was written after code on its line.
type trailingMark struct {
	line int // 0-based line index in the output
	col  int // bytes of code before the comment
}

// writeTrailing writes a comment after the code on the current line, and notes
// where it landed so the alignment pass can line it up with its neighbours.
func (f *formatter) writeTrailing(c *ast.Comment) {
	if c == nil {
		return
	}
	f.trailing = append(f.trailing, trailingMark{line: f.line, col: f.buf.Len() - f.lineStart})
	f.write(" ")
	f.writeComment(c)
}

// alignTrailingComments lines up the comments on a run of consecutive lines,
// the way they are written by hand: one space past the longest line in the
// run. A run of one gets a single space, having nothing to line up with.
func alignTrailingComments(out string, marks []trailingMark) string {
	if len(marks) == 0 {
		return out
	}
	lines := strings.Split(out, "\n")
	pad := make(map[int]int, len(marks))
	for i := 0; i < len(marks); {
		j := i + 1
		for j < len(marks) && marks[j].line == marks[j-1].line+1 {
			j++
		}
		width := 0
		for _, m := range marks[i:j] {
			if m.col > width {
				width = m.col
			}
		}
		for _, m := range marks[i:j] {
			pad[m.line] = width - m.col
		}
		i = j
	}
	for _, m := range marks {
		n := pad[m.line]
		if n <= 0 || m.line >= len(lines) || m.col > len(lines[m.line]) {
			continue
		}
		line := lines[m.line]
		lines[m.line] = line[:m.col] + strings.Repeat(" ", n) + line[m.col:]
	}
	return strings.Join(lines, "\n")
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
	ast.UnaryNot:   "!",
	ast.UnaryNeg:   "-",
	ast.UnaryAddr:  "&",
	ast.UnaryDeref: "*",
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
	f.doc = doc
	f.formatStmtSeq(doc.Stmts)
}

// formatStmtSeq writes a run of statements one per line, keeping a single
// blank line wherever the source left one or more. Two statements the source
// wrote apart stay apart, in a block as at the top level. A comment that
// trailed a statement is written back on that statement's line.
func (f *formatter) formatStmtSeq(stmts []ast.Stmt) {
	prevLine := 0
	for i := 0; i < len(stmts); i++ {
		s := stmts[i]
		pos := s.StmtPos()

		// Two statements the source wrote on one line are not separated by
		// the blank line above that line — only the first of them is.
		if i > 0 && pos != nil && pos.Line > prevLine && f.doc != nil && f.doc.BlankBefore(pos.Line) {
			f.blankLine()
		}
		if pos != nil {
			prevLine = pos.Line
		}

		c, hasTrailer := trailingComment(stmts, i)
		if hasTrailer {
			i++
			// A comment on a mark's line belongs to the mark, not to the
			// declaration below it — `#[options] // ERROR(check) …` names the
			// line the diagnostic lands on.
			if markLine(s) == c.Pos.Line {
				f.attrTrailer = c
				c = nil
			}
		}
		f.formatStmt(s)
		f.writeTrailing(c)
		f.newline()
	}
}

// markLine is the line a statement's last mark was written on, or 0 when it
// carries none.
func markLine(s ast.Stmt) int {
	a, ok := s.(ast.Attributed)
	if !ok {
		return 0
	}
	attrs := a.MacroAttrs()
	if len(attrs) == 0 {
		return 0
	}
	return attrs[len(attrs)-1].Pos.Line
}

// trailingComment returns the comment written after stmts[i] on its line.
func trailingComment(stmts []ast.Stmt, i int) (*ast.Comment, bool) {
	if i+1 >= len(stmts) {
		return nil, false
	}
	c, ok := stmts[i+1].(*ast.Comment)
	if !ok || !c.Inline {
		return nil, false
	}
	return c, true
}

// --- top-level and block statements ---

func (f *formatter) formatStmt(s ast.Stmt) {
	if a, ok := s.(ast.Attributed); ok {
		f.writeAttrs(a.MacroAttrs())
	}
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
	case *ast.SlotNode:
		f.writeSlotNode(x)
	case *ast.AssignStmt:
		f.writeAssignStmt(x)
	case *ast.ToggleStmt:
		f.writeToggleStmt(x)
	case *ast.IncDecStmt:
		f.writeIncDecStmt(x)
	case *ast.VarStmt:
		f.writeVarStmt(x)
	case *ast.ReturnStmt:
		f.writeReturnStmt(x)
	case *ast.CallStmt:
		f.writeCallStmt(x)
	case *ast.Comment:
		f.writeComment(x)
	case *ast.DisabledDecl:
		f.writeDisabledDecl(x)
	}
}

// --- imports ---

func (f *formatter) writeImport(imp *ast.Import) {
	// The alias slot already holds either an identifier or the dot.
	prefix := imp.Alias
	switch {
	case imp.Replace != "" && prefix != "":
		f.writef("import %s %q => %q", prefix, imp.Path, imp.Replace)
	case imp.Replace != "":
		f.writef("import %q => %q", imp.Path, imp.Replace)
	case prefix != "":
		f.writef("import %s %q", prefix, imp.Path)
	default:
		f.writef("import %q", imp.Path)
	}
}

// --- struct ---

func (f *formatter) writeStructDef(s *ast.StructDef) {
	f.write("struct ")
	if s.Name != "" {
		f.write(s.Name)
		if len(s.TypeParams) > 0 {
			f.write("<")
			for k, tp := range s.TypeParams {
				if k > 0 {
					f.write(", ")
				}
				f.write(tp.Name)
				if tp.Default != nil {
					f.write(" = ")
					f.writeType(tp.Default)
				}
			}
			f.write(">")
		}
		f.write(" ")
	}
	if len(s.Body) == 0 {
		f.write("{}")
		return
	}
	// Struct fields require semicolons (ASI newlines), so always multiline.
	f.write("{")
	f.newline()
	f.indent++
	for i := 0; i < len(s.Body); i++ {
		f.blankBeforeBodyItem(i, bodyItemLine(s.Body[i]))
		if f.writeBodyComment(s.Body[i]) {
			continue
		}
		f.writeStructBodyItem(s.Body[i])
		if i+1 < len(s.Body) {
			if c, ok := s.Body[i+1].(*ast.Comment); ok && c.Inline {
				f.writeTrailing(c)
				i++
			}
		}
		f.newline()
	}
	f.indent--
	f.write("}")
}

// leadLine is the first line an item occupies, comments and marks included:
// the blank line an author left sits above those, not above the declaration.
func leadLine(line int, leading []*ast.Comment, attrs []ast.MacroAttr) int {
	if len(leading) > 0 && leading[0].Pos.Line > 0 {
		return leading[0].Pos.Line
	}
	if len(attrs) > 0 && attrs[0].Pos.Line > 0 {
		return attrs[0].Pos.Line
	}
	return line
}

// propLeadLine is leadLine for a component prop, whichever form it takes.
func propLeadLine(p ast.ParamOrEventDecl) int {
	switch v := p.(type) {
	case ast.Param:
		return leadLine(v.Pos.Line, v.Leading, v.Attrs)
	case ast.EventDecl:
		return leadLine(v.Pos.Line, v.Leading, v.Attrs)
	case ast.SlotDecl:
		return leadLine(v.Pos.Line, v.Leading, v.Attrs)
	}
	return 0
}

// blankBeforeBodyItem keeps the blank line the source left above a struct or
// enum body item — the grouping in a long field list is the author's.
func (f *formatter) blankBeforeBodyItem(i, line int) {
	if i > 0 && line > 0 && f.doc != nil && f.doc.BlankBefore(line) {
		f.blankLine()
	}
}

// writeBodyComment writes a struct or enum body item that is a comment on a
// line of its own, and reports whether it did. A trailing one is written by
// the loop that just wrote the item it trails.
func (f *formatter) writeBodyComment(item any) bool {
	c, ok := item.(*ast.Comment)
	if !ok || c.Inline {
		return false
	}
	f.writeComment(c)
	f.newline()
	return true
}

func (f *formatter) writeStructBodyItem(item ast.StructBodyItem) {
	if a, ok := item.(ast.Attributed); ok {
		f.writeAttrs(a.MacroAttrs())
	}
	switch it := item.(type) {
	case *ast.StructField:
		f.write(strings.Join(it.Names, ", "))
		if it.Type != nil {
			f.write(" ")
			f.writeType(it.Type)
		}
		if it.Default != nil {
			f.write(" = ")
			f.writeExpr(it.Default)
		}
	case *ast.FuncDef:
		f.writeFuncDef(it)
	}
}

// --- enum ---

func (f *formatter) writeEnumDef(e *ast.EnumDef) {
	f.write("enum ")
	if e.Name != "" {
		f.write(e.Name)
		f.write(" ")
	}
	f.write("{")
	// Funcs in the body force multiline form regardless of IsMultiline.
	hasFuncs := false
	for _, it := range e.Body {
		if _, ok := it.(*ast.FuncDef); ok {
			hasFuncs = true
			break
		}
	}
	if e.IsMultiline || hasFuncs {
		f.newline()
		f.indent++
		for i := 0; i < len(e.Body); i++ {
			f.blankBeforeBodyItem(i, bodyItemLine(e.Body[i]))
			if f.writeBodyComment(e.Body[i]) {
				continue
			}
			item := e.Body[i]
			switch it := item.(type) {
			case *ast.EnumMember:
				f.write(it.Name)
				if it.Value != nil {
					f.write(" = ")
					f.writeExpr(it.Value)
				}
			case *ast.FuncDef:
				f.writeFuncDef(it)
			}
			if i+1 < len(e.Body) {
				if c, ok := e.Body[i+1].(*ast.Comment); ok && c.Inline {
					f.writeTrailing(c)
					i++
				}
			}
			f.newline()
		}
		f.indent--
		f.write("}")
		return
	}
	// Single-line form: members only (no funcs by construction).
	members := e.Members()
	if len(members) == 0 {
		f.write("}")
		return
	}
	f.write(" ")
	for i, m := range members {
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
	if len(fn.RecvTypeParams) > 0 {
		// recv<T>.method form: Name is "recv.method", split and insert type params.
		dot := strings.IndexByte(fn.Name, '.')
		f.write(fn.Name[:dot])
		f.write("<")
		f.write(strings.Join(fn.RecvTypeParams, ", "))
		f.write(">")
		f.write(fn.Name[dot:]) // ".method"
	} else {
		f.write(fn.Name)
	}
	if len(fn.TypeParams) > 0 {
		f.write("<")
		f.write(strings.Join(fn.TypeParams, ", "))
		f.write(">")
	}
	f.writeTargetIndex(fn.Target)
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

// writeTargetIndex writes the `[target]` index naming what a declaration
// overrides. Nothing when the declaration overrides nothing.
func (f *formatter) writeTargetIndex(target ast.Expr) {
	if target == nil {
		return
	}
	f.write("[")
	f.writeExpr(target)
	f.write("]")
}

func (f *formatter) writeComponentDecl(c *ast.ComponentDecl) {
	f.write("component ")
	f.write(c.Name)
	f.writeTargetIndex(c.Target)
	// An empty prop list is written when the source wrote one: `component X()`
	// and `component X` are the same declaration, and the parens are the
	// author's.
	if len(c.Props.Props) > 0 || c.HasParens {
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
	if len(vn.Args.Args) > 0 || vn.HasParens {
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
		// An else block that is exactly one IfStmt is an `else if` chain;
		// print it as such rather than `else { if ... }`.
		if len(s.Else.Stmts) == 1 {
			if elseIf, ok := s.Else.Stmts[0].(*ast.IfStmt); ok {
				f.write(" else ")
				f.writeIfStmt(elseIf)
				return
			}
		}
		f.write(" else ")
		f.writeBlock(&s.Else)
	}
}

func (f *formatter) writeForStmt(s *ast.ForStmt) {
	f.write("for ")
	if s.KeyRef {
		f.write("&")
	}
	f.write(s.Key)
	if s.Value != "" {
		f.write(", ")
		if s.ValueRef {
			f.write("&")
		}
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
	stmts := block.Stmts
	if block.IsMultiline {
		// A comment on the opening brace's line trails the brace, not the
		// first statement inside.
		if len(stmts) > 0 {
			if c, ok := stmts[0].(*ast.Comment); ok && c.Inline {
				f.writeTrailing(c)
				stmts = stmts[1:]
			}
		}
		f.newline()
		f.indent++
		f.formatStmtSeq(stmts)
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
			f.blankBeforeBodyItem(i, leadLine(p.Pos.Line, p.Leading, p.Attrs))
			f.writeParam(p, true)
			f.write(",")
			f.writeTrailing(p.Trailing)
			f.newline()
		}
		f.indent--
	} else {
		for i, p := range pl.Params {
			if i > 0 {
				f.write(", ")
			}
			f.writeParam(p, false)
		}
	}
}

func (f *formatter) writeParam(p ast.Param, multiline bool) {
	f.writeLeadingComments(p.Leading)
	f.writeParamAttrs(p.Attrs, p.Pos, multiline)
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

// propComment is the comment written after a component prop on its line.
func propComment(p ast.ParamOrEventDecl) *ast.Comment {
	switch v := p.(type) {
	case ast.Param:
		return v.Trailing
	case ast.EventDecl:
		return v.Trailing
	case ast.SlotDecl:
		return v.Trailing
	}
	return nil
}

// writeLeadingComments writes comments above whatever follows, one per line.
func (f *formatter) writeLeadingComments(comments []*ast.Comment) {
	for _, c := range comments {
		f.writeComment(c)
		f.newline()
	}
}

func (f *formatter) writeProps(pl ast.PropList) {
	if pl.IsMultiline {
		f.newline()
		f.indent++
		for i, p := range pl.Props {
			f.blankBeforeBodyItem(i, propLeadLine(p))
			f.writePropOrEvent(p, true)
			f.write(",")
			f.writeTrailing(propComment(p))
			f.newline()
		}
		f.indent--
	} else {
		for i, p := range pl.Props {
			if i > 0 {
				f.write(", ")
			}
			f.writePropOrEvent(p, false)
		}
	}
}

func (f *formatter) writePropOrEvent(p ast.ParamOrEventDecl, multiline bool) {
	switch v := p.(type) {
	case ast.Param:
		f.writeParam(v, multiline)
	case ast.EventDecl:
		f.writeLeadingComments(v.Leading)
		f.writeParamAttrs(v.Attrs, v.Pos, multiline)
		f.write("@")
		f.write(v.Name)
		if v.Type != nil {
			f.write(" ")
			f.writeType(v.Type)
		}
	case ast.SlotDecl:
		f.writeLeadingComments(v.Leading)
		f.writeParamAttrs(v.Attrs, v.Pos, multiline)
		f.write("slot ")
		f.write(v.Name)
		if len(v.Params) > 0 {
			f.write("(")
			for i, t := range v.Params {
				if i > 0 {
					f.write(", ")
				}
				f.writeType(t)
			}
			f.write(")")
		}
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
		if x.ID != "" {
			// Element-reference declaration: `name #id(...)`.
			f.write(" #")
			f.write(x.ID)
		}
		f.write("(")
		f.writeArgs(x.Args)
		f.write(")")
	case *ast.StructExpr:
		f.writeStructExpr(x)
	case *ast.MapLit:
		f.writeMapLit(x)
	case *ast.ListExpr:
		f.writeListExpr(x)
	case *ast.SpreadExpr:
		f.write("...")
		f.writeExpr(x.Operand)
	case *ast.InterpolationExpr:
		f.writeInterpolation(x)
	case *ast.I18nInterpExpr:
		f.writeI18nInterp(x)
	case *ast.I18nPlaceholderExpr:
		f.writeI18nPlaceholder(x)
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
		f.write(`"`)
		f.write(lit.Raw)
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

// writeCallStmt emits a statement-level call. The `name #id(...)`
// element-reference form is handled by the generic CallExpr writer
// (which emits the `#id` from CallExpr.ID).
func (f *formatter) writeCallStmt(s *ast.CallStmt) {
	f.writeExpr(s.Call)
}

func (f *formatter) writeSelectExpr(x *ast.SelectExpr) {
	f.writeExpr(x.Operand)
	f.write(".")
	f.write(x.Field)
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
			f.writeStructFieldLit(field, true)
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
			f.writeStructFieldLit(field, false)
		}
		f.write("}")
	}
}

// writeStructFieldLit writes one field of a struct literal. A literal written
// on one line is nearly always an argument, where the surrounding list already
// spells `name=value`; spacing the `=` there would put both spellings a few
// characters apart on one line. Written across lines each field stands alone,
// and reads as the assignment it is.
func (f *formatter) writeStructFieldLit(field ast.StructFieldLit, multiline bool) {
	if field.Spread {
		f.write("...")
		f.writeExpr(field.Value)
		return
	}
	f.write(field.Name)
	f.write(fieldAssign(multiline))
	f.writeExpr(field.Value)
}

// fieldAssign is the `=` of a struct or map literal field, spaced or not.
func fieldAssign(multiline bool) string {
	if multiline {
		return " = "
	}
	return "="
}

func (f *formatter) writeMapLit(x *ast.MapLit) {
	f.write("{")
	if x.Multiline {
		f.newline()
		f.indent++
		for _, e := range x.Entries {
			f.writeExpr(e.Key)
			f.write(fieldAssign(true))
			f.writeExpr(e.Value)
			f.write(",")
			f.newline()
		}
		f.indent--
		f.write("}")
		return
	}
	for i, e := range x.Entries {
		if i > 0 {
			f.write(", ")
		}
		f.writeExpr(e.Key)
		f.write(fieldAssign(false))
		f.writeExpr(e.Value)
	}
	f.write("}")
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
			f.write(lit.Raw)
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

func (f *formatter) writeI18nInterp(x *ast.I18nInterpExpr) {
	f.write("$")
	switch x.Style {
	case ast.StyleTriple:
		f.write(`"""`)
	default:
		f.write(`"`)
	}
	for _, part := range x.Parts {
		if lit, ok := part.(*ast.LiteralExpr); ok {
			f.write(lit.Raw)
		} else {
			f.write("{")
			f.writeExpr(part)
			f.write("}")
		}
	}
	switch x.Style {
	case ast.StyleTriple:
		f.write(`"""`)
	default:
		f.write(`"`)
	}
}

func (f *formatter) writeI18nPlaceholder(x *ast.I18nPlaceholderExpr) {
	f.writeExpr(x.Value)
	if x.Type != "" {
		f.write(", ")
		f.write(x.Type)
	}
	if x.Style != "" {
		f.write(", ")
		f.write(x.Style)
	}
	if len(x.Cases) > 0 {
		writeCase := func(c ast.I18nCase) {
			f.write(c.Selector)
			f.write("{")
			for _, p := range c.Body {
				if lit, ok := p.(*ast.LiteralExpr); ok {
					f.write(lit.Raw)
				} else {
					f.write("{")
					f.writeExpr(p)
					f.write("}")
				}
			}
			f.write("}")
		}
		if x.Multiline {
			// The closing brace is the caller's, and lands back at this
			// indent once the cases are done.
			f.write(",")
			f.indent++
			for _, c := range x.Cases {
				for _, cm := range c.Leading {
					f.newline()
					f.writeComment(cm)
				}
				f.newline()
				writeCase(c)
			}
			f.indent--
			f.newline()
			return
		}
		f.write(", ")
		for i, c := range x.Cases {
			if i > 0 {
				f.write(" ")
			}
			writeCase(c)
		}
	}
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
			if p.Name != "" {
				f.write(p.Name)
				f.write(" ")
			}
			f.writeType(p.Type)
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

// --- attr ---

func (f *formatter) writeAttrs(attrs []ast.MacroAttr) {
	for i, attr := range attrs {
		f.writeAttr(attr)
		if i == len(attrs)-1 && f.attrTrailer != nil {
			f.writeTrailing(f.attrTrailer)
			f.attrTrailer = nil
		}
		f.newline()
	}
}

func (f *formatter) writeAttr(attr ast.MacroAttr) {
	f.write("#[")
	if attr.Alias != "" {
		f.write(attr.Alias)
		f.write(".")
	}
	f.write(attr.Name)
	if len(attr.Args) > 0 {
		f.write("(")
		for i, arg := range attr.Args {
			if i > 0 {
				f.write(", ")
			}
			f.writeExpr(arg)
		}
		f.write(")")
	}
	f.write("]")
}

// writeParamAttrs places a parameter's marks where the source put them: on
// their own line above the parameter, or inline ahead of it. A multiline list
// is not the question — `#[wildcard(...)] data map<string, string>` reads as
// one parameter whichever way the list is written.
func (f *formatter) writeParamAttrs(attrs []ast.MacroAttr, declPos ast.Pos, multiline bool) {
	for _, attr := range attrs {
		f.writeAttr(attr)
		ownLine := multiline
		if attr.Pos.IsSet() && declPos.IsSet() {
			// A synthesized declaration has no source to follow; one that was
			// written says where its marks go.
			ownLine = attr.Pos.Line < declPos.Line
		}
		if ownLine {
			f.newline()
		} else {
			f.write(" ")
		}
	}
}

func (f *formatter) writeSlotNode(s *ast.SlotNode) {
	f.write("slot")
	if s.Name != "" {
		f.write(" ")
		f.write(s.Name)
	}
	if len(s.Args) > 0 {
		f.write("(")
		for i, a := range s.Args {
			if i > 0 {
				f.write(", ")
			}
			f.writeExpr(a)
		}
		f.write(")")
	}
	if s.Block.IsDefined() {
		f.write(" ")
		f.writeBlock(&s.Block)
	}
}
