package parser

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Format writes an ast.Document as .sngl source text.
func Format(doc *ast.Document) string {
	f := &formatter{comments: doc.Comments}
	f.formatDocument(doc)
	// Emit any trailing comments (only for legacy path — Decls path handles comments inline)
	if len(doc.Decls) == 0 {
		f.emitRemainingComments()
	}
	return alignInlineComments(f.sb.String())
}

// alignInlineComments finds runs of consecutive lines that all have inline
// comments (code followed by " //") and pads each line so the "//" starts
// at the same column.
func alignInlineComments(s string) string {
	lines := strings.Split(s, "\n")
	// Find the code/comment split for each line. -1 means no inline comment.
	type split struct {
		code    string
		comment string
	}
	splits := make([]split, len(lines))
	for i, line := range lines {
		if idx := findInlineComment(line); idx >= 0 {
			splits[i] = split{code: strings.TrimRight(line[:idx], " "), comment: line[idx:]}
		} else {
			splits[i] = split{code: line, comment: ""}
		}
	}

	// Process runs of consecutive lines with comments
	i := 0
	for i < len(lines) {
		if splits[i].comment == "" {
			i++
			continue
		}
		// Start of a run — must begin with an inline comment (code + comment),
		// not a solo line comment. Solo line comments can continue a run.
		if strings.TrimSpace(splits[i].code) == "" {
			// Solo line comment with no preceding inline comment — skip
			i++
			continue
		}
		j := i
		for j < len(lines) && splits[j].comment != "" {
			j++
		}
		// Only align if 2+ consecutive lines have comments
		if j-i >= 2 {
			maxCode := 0
			for k := i; k < j; k++ {
				if len(splits[k].code) > maxCode {
					maxCode = len(splits[k].code)
				}
			}
			for k := i; k < j; k++ {
				pad := maxCode - len(splits[k].code)
				lines[k] = splits[k].code + strings.Repeat(" ", pad) + " " + splits[k].comment
			}
		}
		i = j
	}
	return strings.Join(lines, "\n")
}

// findInlineComment returns the index of " //" in a line, respecting strings.
// Returns -1 if no inline comment found.
func findInlineComment(line string) int {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			if !inStr {
				inStr = true
			} else if i > 0 && line[i-1] != '\\' {
				inStr = false
			}
		case '/':
			if !inStr && i+1 < len(line) && line[i+1] == '/' && i > 0 && line[i-1] == ' ' {
				return i
			}
		}
	}
	return -1
}

// emitCommentsBefore emits all comments with position before the given line.
func (f *formatter) emitCommentsBefore(line int) {
	for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line < line {
		c := f.comments[f.commentI]
		f.writeLine(c.Text)
		f.commentI++
	}
}

// emitPendingInlineComment emits a pending inline comment (from Decls) on the current line.
func (f *formatter) emitPendingInlineComment() {
	if f.pendingInlineComment == nil {
		return
	}
	c := f.pendingInlineComment
	f.pendingInlineComment = nil
	s := f.sb.String()
	if len(s) > 0 && s[len(s)-1] == '\n' {
		f.sb.Reset()
		f.sb.WriteString(s[:len(s)-1])
	}
	f.write(" " + c.Text)
	f.newline()
	// Skip this comment in f.comments too (it appears in both Decls and Comments)
	for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line <= c.Pos.Line {
		f.commentI++
	}
}

// emitInlineComment emits an inline comment on the given line if one exists.
func (f *formatter) emitInlineComment(line int) {
	if f.commentI < len(f.comments) {
		c := f.comments[f.commentI]
		if c.Inline && c.Pos.Line == line {
			s := f.sb.String()
			if len(s) > 0 && s[len(s)-1] == '\n' {
				f.sb.Reset()
				f.sb.WriteString(s[:len(s)-1])
			}
			f.write(" " + c.Text)
			f.newline()
			f.commentI++
		}
	}
}

// emitRemainingComments emits any comments not yet emitted.
func (f *formatter) emitRemainingComments() {
	for f.commentI < len(f.comments) {
		c := f.comments[f.commentI]
		f.writeLine(c.Text)
		f.commentI++
	}
}

type formatter struct {
	sb                   strings.Builder
	indent               int
	comments             []ast.Comment
	commentI             int           // next comment index to emit
	pendingInlineComment *ast.Comment  // inline comment to emit on the visual node's line
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

// writeDisabledLine writes a line prefixed with /- if disabled is true.
func (f *formatter) writeDisabledLine(disabled bool, s string) {
	if disabled {
		f.write(f.indentStr())
		f.write("/- ")
		f.write(s)
		f.newline()
	} else {
		f.writeLine(s)
	}
}

func (f *formatter) formatDocument(doc *ast.Document) {
	if len(doc.Decls) > 0 {
		f.formatDocumentDecls(doc)
		return
	}
	// Legacy path: when Decls is empty, use the old ordered approach
	f.formatDocumentLegacy(doc)
}

func (f *formatter) formatDocumentDecls(doc *ast.Document) {
	prevEndLine := 0
	outputsSeen := false

	for di := 0; di < len(doc.Decls); di++ {
		d := doc.Decls[di]
		switch decl := d.(type) {
		case *ast.Comment:
			if decl.Inline {
				// Skip if already consumed by emitInlineComment inside a component/test
				if f.commentI > 0 && f.comments[f.commentI-1].Pos.Line == decl.Pos.Line {
					continue
				}
				// Inline comment: remove last \n, append comment on same line
				s := f.sb.String()
				if len(s) > 0 && s[len(s)-1] == '\n' {
					f.sb.Reset()
					f.sb.WriteString(s[:len(s)-1])
				}
				f.write(" " + decl.Text)
				f.newline()
			} else {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.writeLine(decl.Text)
			}
			prevEndLine = decl.Pos.Line
			continue
		case *ast.Import:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.writeDisabledLine(decl.Disabled, fmt.Sprintf("import \"%s\"", escapeStringContent(decl.Path)))
			prevEndLine = decl.Pos.Line
		case *ast.Output:
			if !outputsSeen {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.formatOutputGroup(doc)
				outputsSeen = true
				prevEndLine = decl.Pos.Line
			}
			// Skip subsequent Output decls since formatOutputGroup handles all
		case *ast.StructDef:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Disabled {
				f.write(f.indentStr() + "/- ")
			}
			f.formatStruct(decl)
			prevEndLine = decl.Pos.Line
		case *ast.EnumDef:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.writeDisabledLine(decl.Disabled, fmt.Sprintf("enum %s { %s }", decl.Name, strings.Join(decl.Values, ", ")))
			prevEndLine = decl.Pos.Line
		case *ast.UnitDef:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatUnitDecl(decl)
			prevEndLine = decl.Pos.Line
		case *ast.StyleDecl:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatStyleDecl(decl)
			prevEndLine = decl.Pos.Line
		case *ast.Component:
			if decl.Name == "main" {
				// Format using the component's own Decls if available
				f.formatComponentAsMain(decl)
			} else {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				if decl.Disabled {
					f.write(f.indentStr() + "/- ")
				}
				f.formatComponent(decl)
			}
			prevEndLine = declEndLine(decl)
		case *ast.TestDef:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Disabled {
				f.write(f.indentStr() + "/- ")
			}
			f.formatTestDef(decl, true)
			prevEndLine = declEndLine(decl)
		case *ast.Const:
			// Consts at document level are part of main
			// (they're handled via formatDocMain)
		case *ast.Data:
			// Data at document level is part of main
		case *ast.FuncDef:
			// Functions at document level are part of main
		case *ast.Timer:
			// Timers at document level are part of main
		}
	}

	// If we haven't seen a main component in Decls but have main content,
	// format it now (backward compat)
	hasMainInDecls := false
	for _, d := range doc.Decls {
		if comp, ok := d.(*ast.Component); ok && comp.Name == "main" {
			hasMainInDecls = true
			break
		}
	}
	if !hasMainInDecls {
		f.formatDocMain(doc)
	}
}

func (f *formatter) formatComponentAsMain(comp *ast.Component) {
	if f.sb.Len() > 0 {
		f.newline()
	}
	f.formatComponent(comp)
}

func (f *formatter) formatDocMain(doc *ast.Document) {
	hasUserFuncs := false
	for _, fn := range doc.Functions {
		if !fn.IsStdlib {
			hasUserFuncs = true
			break
		}
	}
	hasMain := doc.App != nil || len(doc.Data) > 0 || len(doc.Consts) > 0 || hasUserFuncs
	if !hasMain {
		return
	}
	if f.sb.Len() > 0 {
		f.newline()
	}
	f.writeLine("component main {")
	f.indent++

	memberBlank := false

	// Consts
	if len(doc.Consts) > 0 {
		f.formatConstsGrouped(doc.Consts)
		memberBlank = true
	}

	// Vars
	if len(doc.Data) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatVarsGrouped(doc.Data)
		memberBlank = true
	}

	// Functions
	if len(doc.Functions) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatFuncDefs(doc.Functions)
		memberBlank = true
	}

	// Timers
	if len(doc.Timers) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, t := range doc.Timers {
			f.formatTimer(t)
		}
		memberBlank = true
	}

	// App body
	if doc.App != nil && len(doc.App.Children) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, vn := range doc.App.Children {
			f.emitCommentsBefore(vn.Pos.Line)
			f.formatVisualNode(vn)
		}
	}

	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatDocumentLegacy(doc *ast.Document) {
	needBlank := false

	// Imports
	if len(doc.Imports) > 0 {
		for _, imp := range doc.Imports {
			f.emitCommentsBefore(imp.Pos.Line)
			f.writeDisabledLine(imp.Disabled, fmt.Sprintf("import \"%s\"", escapeStringContent(imp.Path)))
		}
		needBlank = true
	}

	// Outputs
	if len(doc.Outputs) > 0 {
		if needBlank {
			f.newline()
		}
		f.formatOutputGroup(doc)
		needBlank = true
	}

	// Structs
	for _, s := range doc.Structs {
		if needBlank {
			f.newline()
		}
		f.emitCommentsBefore(s.Pos.Line)
		if s.Disabled {
			f.write(f.indentStr() + "/- ")
		}
		f.formatStruct(s)
		needBlank = true
	}

	// Enums
	for _, e := range doc.Enums {
		if needBlank {
			f.newline()
		}
		f.emitCommentsBefore(e.Pos.Line)
		f.writeDisabledLine(e.Disabled, fmt.Sprintf("enum %s { %s }", e.Name, strings.Join(e.Values, ", ")))
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

	// Non-main components
	for _, comp := range doc.Components {
		if needBlank {
			f.newline()
		}
		f.emitCommentsBefore(comp.Pos.Line)
		if comp.Disabled {
			f.write(f.indentStr() + "/- ")
		}
		f.formatComponent(comp)
		needBlank = true
	}

	// Tests
	for _, td := range doc.Tests {
		if needBlank {
			f.newline()
		}
		f.emitCommentsBefore(td.Pos.Line)
		if td.Disabled {
			f.write(f.indentStr() + "/- ")
		}
		f.formatTestDef(td, true)
		needBlank = true
	}

	// component main (combines data, computed, consts, functions, app)
	hasUserFuncs := false
	for _, fn := range doc.Functions {
		if !fn.IsStdlib {
			hasUserFuncs = true
			break
		}
	}
	hasMain := doc.App != nil || len(doc.Data) > 0 || len(doc.Consts) > 0 || hasUserFuncs
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

		// Functions
		if len(doc.Functions) > 0 {
			if memberBlank {
				f.newline()
			}
			f.formatFuncDefs(doc.Functions)
			memberBlank = true
		}

		// Timers
		if len(doc.Timers) > 0 {
			if memberBlank {
				f.newline()
			}
			for _, t := range doc.Timers {
				f.formatTimer(t)
			}
			memberBlank = true
		}

		// App body
		if doc.App != nil && len(doc.App.Children) > 0 {
			if memberBlank {
				f.newline()
			}
			for _, vn := range doc.App.Children {
				f.emitCommentsBefore(vn.Pos.Line)
				f.formatVisualNode(vn)
			}
		}

		f.indent--
		f.writeLine("}")
	}
}

func (f *formatter) formatOutputGroup(doc *ast.Document) {
	header := "output"
	if len(doc.OutputDefaults) > 0 {
		header += "(" + formatKV(doc.OutputDefaults) + ")"
	}
	f.writeLine(header + " {")
	f.indent++
	// Group outputs by lang
	type langGroup struct {
		lang    string
		outputs []*ast.Output
	}
	var groups []langGroup
	seen := map[string]int{}
	for _, o := range doc.Outputs {
		if idx, ok := seen[o.Lang]; ok {
			groups[idx].outputs = append(groups[idx].outputs, o)
		} else {
			seen[o.Lang] = len(groups)
			groups = append(groups, langGroup{lang: o.Lang, outputs: []*ast.Output{o}})
		}
	}
	for _, g := range groups {
		// One-liner: single platform, no options, and source was one line
		if len(g.outputs) == 1 && len(g.outputs[0].Options) == 0 && g.outputs[0].LangLine == g.outputs[0].Pos.Line {
			f.writeLine(g.lang + " { " + g.outputs[0].Platform + " }")
			continue
		}
		f.writeLine(g.lang + " {")
		f.indent++
		for _, o := range g.outputs {
			line := o.Platform
			if len(o.Options) > 0 {
				line += "(" + formatKV(o.Options) + ")"
			}
			f.writeLine(line)
		}
		f.indent--
		f.writeLine("}")
	}
	f.indent--
	f.writeLine("}")
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
	keys := s.PropOrder
	if len(keys) == 0 {
		keys = sortedKeys(s.Props)
	}
	for _, k := range keys {
		f.writeLine(k + " = " + f.formatExprValue(s.Props[k]))
	}
	f.indent--
	f.writeLine("}")
}


func (f *formatter) formatConsts(consts []*ast.Const) {
	if len(consts) == 1 && !consts[0].Grouped {
		c := consts[0]
		line := "const " + c.Name
		if c.ExplicitType {
			if ts := typeHintStr(c.Init.TypeHint, nil); ts != "" {
				line += " " + ts
			}
		}
		line += " = " + f.formatExprValue(c.Init)
		f.writeDisabledLine(c.Disabled, line)
		return
	}
	f.writeLine("const (")
	f.indent++
	for _, c := range consts {
		line := c.Name
		if c.ExplicitType {
			if ts := typeHintStr(c.Init.TypeHint, nil); ts != "" {
				line += " " + ts
			}
		}
		line += " = " + f.formatExprValue(c.Init)
		f.writeDisabledLine(c.Disabled, line)
	}
	f.indent--
	f.writeLine(")")
}

func (f *formatter) formatVars(data []*ast.Data) {
	if len(data) == 1 && !data[0].Grouped {
		f.writeDisabledLine(data[0].Disabled, "var "+f.formatVarDecl(data[0]))
		return
	}
	f.writeLine("var (")
	f.indent++
	for _, d := range data {
		f.writeDisabledLine(d.Disabled, f.formatVarDecl(d))
	}
	f.indent--
	f.writeLine(")")
}

// formatConstsGrouped emits consts, grouping consecutive Grouped items together
// and emitting non-grouped items individually.
func (f *formatter) formatConstsGrouped(consts []*ast.Const) {
	i := 0
	for i < len(consts) {
		if consts[i].Grouped {
			// Collect consecutive grouped consts
			j := i
			for j < len(consts) && consts[j].Grouped {
				j++
			}
			f.formatConsts(consts[i:j])
			i = j
		} else {
			f.formatConsts(consts[i : i+1])
			i++
		}
	}
}

// formatVarsGrouped emits vars, grouping consecutive Grouped items together
// and emitting non-grouped items individually.
func (f *formatter) formatVarsGrouped(data []*ast.Data) {
	i := 0
	for i < len(data) {
		if data[i].Grouped {
			j := i
			for j < len(data) && data[j].Grouped {
				j++
			}
			f.formatVars(data[i:j])
			i = j
		} else {
			f.formatVars(data[i : i+1])
			i++
		}
	}
}

func (f *formatter) formatVarDecl(d *ast.Data) string {
	var sb strings.Builder
	sb.WriteString(d.Name)

	hasInit := d.Init.SNGL != nil || d.Init.Literal != nil

	if d.ExplicitType {
		if typeStr := typeHintStr(d.Init.TypeHint, d); typeStr != "" {
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
			sb.WriteString(" @")
		} else {
			sb.WriteString(" @" + d.Trigger)
		}
	}

	return sb.String()
}


func (f *formatter) formatFuncDefs(funcs []*ast.FuncDef) {
	for _, fn := range funcs {
		if fn.IsStdlib {
			continue
		}
		f.formatFuncDef(fn)
	}
}

func (f *formatter) formatFuncDef(fn *ast.FuncDef) {
	var sb strings.Builder
	sb.WriteString("func ")
	sb.WriteString(fn.Name)
	if len(fn.TypeParams) > 0 {
		sb.WriteString("<")
		sb.WriteString(strings.Join(fn.TypeParams, ", "))
		sb.WriteString(">")
	}
	if len(fn.Params) > 0 || fn.Block != nil || fn.HasParens {
		sb.WriteString("(")
		for i, p := range fn.Params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.Name)
			sb.WriteString(" ")
			sb.WriteString(typeHintStr(p.Type, nil))
		}
		sb.WriteString(")")
	}
	if fn.Block != nil {
		if fn.ReturnType != "" {
			sb.WriteString(" ")
			sb.WriteString(typeHintStr(fn.ReturnType, nil))
		}
		sb.WriteString(" {")
		f.writeLine(sb.String())
		f.indent++
		for _, stmt := range fn.Block.Stmts {
			f.writeLine(FormatNode(stmt))
		}
		if fn.Block.Return != nil {
			f.writeLine("return " + FormatNode(fn.Block.Return))
		}
		f.indent--
		f.writeLine("}")
	} else {
		sb.WriteString(" => ")
		sb.WriteString(f.formatExprValue(fn.Body))
		f.writeLine(sb.String())
	}
}

func (f *formatter) formatTimer(t *ast.Timer) {
	line := "timer " + f.formatExprValue(t.Interval) + " " + t.Active + " {"
	f.writeLine(line)
	f.indent++
	if sb, ok := t.Body.(*ast.StmtBlock); ok {
		for _, stmt := range sb.Stmts {
			f.writeLine(FormatNode(stmt))
		}
	} else {
		f.writeLine(FormatNode(t.Body))
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatComponent(comp *ast.Component) {
	header := "component " + comp.Name

	// Collect bidirectional names to avoid duplicating their auto-generated events
	biNames := map[string]bool{}
	for _, p := range comp.Params {
		if p.Bidirectional {
			biNames[p.Name] = true
		}
	}

	// Separate events not auto-generated by bidirectional params
	var events []*ast.EventDecl
	for _, e := range comp.EventDecls {
		if !biNames[e.Name] {
			events = append(events, e)
		}
	}

	if len(comp.Params) > 0 || len(events) > 0 {
		header += "("
		first := true
		for _, p := range comp.Params {
			if !first {
				header += ", "
			}
			first = false
			if p.Bidirectional {
				header += ":"
			}
			header += p.Name
			typeStr := typeHintStr(p.Default.TypeHint, nil)
			if typeStr != "" {
				header += " " + typeStr
			}
			hasDefault := p.Default.SNGL != nil || p.Default.Literal != nil
			if len(p.Enum) > 0 {
				header += " enum(" + strings.Join(p.Enum, ", ") + ")"
			}
			if hasDefault {
				header += " = " + f.formatExprValue(p.Default)
			}
			if p.Required {
				header += " required"
			}
		}
		for _, e := range events {
			if !first {
				header += ", "
			}
			first = false
			header += "@" + e.Name
			if e.PayloadType != "" {
				header += " " + e.PayloadType
			}
		}
		header += ")"
	}

	if comp.ChildrenType != "" {
		header += " " + typeHintStr(comp.ChildrenType, nil)
	}

	f.writeLine(header + " {")
	f.emitInlineComment(comp.Pos.Line)
	f.indent++

	if len(comp.Decls) > 0 {
		f.formatDeclSlice(comp)
		// Advance comment index past the component body to prevent
		// emitCommentsBefore from re-emitting comments handled by Decls.
		for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line < comp.EndLine {
			f.commentI++
		}
	} else {
		f.formatComponentLegacy(comp)
	}

	f.indent--
	f.writeLine("}")
}

// declEndLine returns the last source line occupied by a declaration.
func declEndLine(d ast.Decl) int {
	switch decl := d.(type) {
	case *ast.FuncDef:
		if decl.EndLine > 0 {
			return decl.EndLine
		}
	case *ast.TestDef:
		if decl.EndLine > 0 {
			return decl.EndLine
		}
	case *ast.VisualNode:
		if decl.EndLine > 0 {
			return decl.EndLine
		}
	}
	return d.DeclPos().Line
}

// declCategory returns a category string for blank-line grouping.
// Different categories get mandatory blank lines between them.

// formatDeclSlice formats the body of a component using its ordered Decls slice.
func (f *formatter) formatDeclSlice(comp *ast.Component) {
	// Skip f.comments entries that are inside this component body —
	// they'll be handled by Decls, not emitCommentsBefore.
	for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line < comp.EndLine {
		f.commentI++
	}

	decls := comp.Decls
	prevEndLine := 0

	for i := 0; i < len(decls); i++ {
		d := decls[i]
		switch decl := d.(type) {
		case *ast.Comment:
			if decl.Inline {
				s := f.sb.String()
				if len(s) > 0 && s[len(s)-1] == '\n' {
					f.sb.Reset()
					f.sb.WriteString(s[:len(s)-1])
				}
				f.write(" " + decl.Text)
				f.newline()
			} else {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.writeLine(decl.Text)
			}
			prevEndLine = decl.Pos.Line
			continue
		case *ast.Const:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Grouped {
				group := []*ast.Const{decl}
				for i+1 < len(decls) {
					if next, ok := decls[i+1].(*ast.Const); ok && next.Grouped {
						group = append(group, next)
						i++
					} else {
						break
					}
				}
				f.formatConsts(group)
			} else {
				f.formatConsts([]*ast.Const{decl})
			}
			prevEndLine = decl.Pos.Line
		case *ast.Data:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Grouped {
				group := []*ast.Data{decl}
				for i+1 < len(decls) {
					if next, ok := decls[i+1].(*ast.Data); ok && next.Grouped {
						group = append(group, next)
						i++
					} else {
						break
					}
				}
				f.formatVars(group)
			} else {
				f.formatVars([]*ast.Data{decl})
			}
			prevEndLine = decl.Pos.Line
		case *ast.FuncDef:
			if decl.IsStdlib {
				continue
			}
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatFuncDef(decl)
			prevEndLine = declEndLine(decl)
		case *ast.Timer:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatTimer(decl)
			prevEndLine = decl.Pos.Line
		case *ast.VisualNode:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			// Peek ahead for inline comment on same line as this node
			var inlineComment *ast.Comment
			if i+1 < len(decls) {
				if c, ok := decls[i+1].(*ast.Comment); ok && c.Inline && c.Pos.Line == decl.Pos.Line {
					inlineComment = c
					i++ // consume the comment
				}
			}
			f.formatVisualNodeWithComment(decl, inlineComment)
			prevEndLine = declEndLine(decl)
		}
	}

	// Platform-conditional bodies
	for platName, nodes := range comp.PlatformBodies {
		if prevEndLine > 0 {
			f.newline()
		}
		f.writeLine("platform " + platName + " {")
		f.indent++
		for _, vn := range nodes {
			f.formatVisualNode(vn)
		}
		f.indent--
		f.writeLine("}")
		prevEndLine = -1
	}
}

// formatComponentLegacy formats a component body using the old ordered approach.
func (f *formatter) formatComponentLegacy(comp *ast.Component) {
	memberBlank := false

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

	// Functions
	if len(comp.Functions) > 0 {
		if memberBlank {
			f.newline()
		}
		f.formatFuncDefs(comp.Functions)
		memberBlank = true
	}

	// Timers
	if len(comp.Timers) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, t := range comp.Timers {
			f.formatTimer(t)
		}
		memberBlank = true
	}

	// Visual body
	if len(comp.Body) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, vn := range comp.Body {
			f.emitCommentsBefore(vn.Pos.Line)
			f.formatVisualNode(vn)
		}
		memberBlank = true
	}

	// Platform-conditional bodies
	for platName, nodes := range comp.PlatformBodies {
		if memberBlank {
			f.newline()
		}
		f.writeLine("platform " + platName + " {")
		f.indent++
		for _, vn := range nodes {
			f.emitCommentsBefore(vn.Pos.Line)
			f.formatVisualNode(vn)
		}
		f.indent--
		f.writeLine("}")
		memberBlank = true
	}
}

func (f *formatter) formatTestDef(td *ast.TestDef, topLevel bool) {
	line := "test "
	if topLevel && td.Component != "" {
		line += td.Component + " "
	}
	if td.Desc != "" {
		line += "\"" + escapeStringContent(td.Desc) + "\" "
	}
	line += "{"
	f.writeLine(line)
	f.emitInlineComment(td.Pos.Line)
	f.indent++
	// Skip f.comments entries inside this test body — they're handled by Decls.
	if td.EndLine > 0 {
		for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line < td.EndLine {
			f.commentI++
		}
	}
	if len(td.Decls) > 0 {
		prevEndLine := 0
		for _, d := range td.Decls {
			switch v := d.(type) {
			case *ast.StmtDecl:
				if prevEndLine > 0 && v.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.writeLine(FormatStmt(v.Stmt))
				prevEndLine = v.Pos.Line
			case *ast.TestDef:
				if prevEndLine > 0 && v.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.formatTestDef(v, false)
				prevEndLine = declEndLine(v)
			case *ast.Comment:
				if v.Inline {
					s := f.sb.String()
					if len(s) > 0 && s[len(s)-1] == '\n' {
						f.sb.Reset()
						f.sb.WriteString(s[:len(s)-1])
					}
					f.write(" " + v.Text)
					f.newline()
				} else {
					if prevEndLine > 0 && v.Pos.Line > prevEndLine+1 {
						f.newline()
					}
					f.writeLine(v.Text)
				}
				prevEndLine = v.Pos.Line
			}
		}
	} else {
		for _, stmt := range td.Body {
			f.writeLine(FormatStmt(stmt))
		}
		for _, sub := range td.Subtests {
			if len(td.Body) > 0 || sub != td.Subtests[0] {
				f.newline()
			}
			f.formatTestDef(sub, false)
		}
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatVisualNodeWithComment(vn *ast.VisualNode, inlineComment *ast.Comment) {
	f.pendingInlineComment = inlineComment
	f.formatVisualNode(vn)
	f.pendingInlineComment = nil
}

func (f *formatter) formatVisualNode(vn *ast.VisualNode) {
	if vn.Disabled {
		f.write(f.indentStr() + "/- ")
		// Temporarily reduce indent so the inner node doesn't double-indent
		saved := f.indent
		f.indent = 0
		f.formatVisualNodeInner(vn)
		f.indent = saved
		return
	}
	// If / For wrapping
	if vn.If != nil || vn.For != nil {
		if vn.For != nil {
			fc := vn.For
			line := "for " + fc.Variable
			if fc.IndexVar != "" {
				line += ", " + fc.IndexVar
			}
			line += " = " + f.formatExprValue(fc.Iterable)
			line += " {"
			f.writeLine(line)
			f.indent++
			f.formatVisualNodeInner(vn)
			f.indent--
			if len(fc.Else) > 0 {
				f.writeLine("} else {")
				f.indent++
				for _, child := range fc.Else {
					f.formatVisualNode(child)
				}
				f.indent--
			}
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

	if len(vn.PropOrder) > 0 {
		// Use insertion order from parser
		for _, entry := range vn.PropOrder {
			if strings.HasPrefix(entry, "@") {
				name := entry[1:]
				if expr, ok := vn.Events[name]; ok {
					props = append(props, "@"+name+"="+f.formatEventValue(expr))
				}
			} else if strings.HasPrefix(entry, ":") {
				name := entry[1:]
				if expr, ok := vn.Bindings[name]; ok {
					props = append(props, ":"+name+"="+f.formatExprValue(expr))
				}
			} else {
				switch entry {
				case "key":
					if vn.Key != nil {
						props = append(props, "key="+f.formatExprValue(*vn.Key))
					}
				case "class":
					if vn.Class != nil {
						props = append(props, "class="+f.formatExprValue(*vn.Class))
					}
				case "ref":
					if vn.Ref != nil {
						props = append(props, "ref="+f.formatExprValue(*vn.Ref))
					}
				default:
					if expr, ok := vn.Props[entry]; ok {
						props = append(props, entry+"="+f.formatExprValue(expr))
					}
				}
			}
		}
	} else {
		// Fallback: sorted keys for deterministic output
		// Special props
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

		// Bindings (sorted for deterministic output)
		bindingKeys := sortedKeys(vn.Bindings)
		for _, name := range bindingKeys {
			props = append(props, ":"+name+"="+f.formatExprValue(vn.Bindings[name]))
		}

		// Events (sorted for deterministic output)
		eventKeys := sortedKeys(vn.Events)
		for _, name := range eventKeys {
			props = append(props, "@"+name+"="+f.formatEventValue(vn.Events[name]))
		}
	}

	// Build the line
	line := vn.Component
	if vn.ID != "" {
		line += " #" + vn.ID
	}
	if len(props) > 0 {
		if vn.MultilineProps {
			propIndent := f.indentStr() + "    "
			line += "(\n"
			for _, p := range props {
				// Re-indent multi-line prop values (e.g., multi-line struct literals)
				if strings.Contains(p, "\n") {
					lines := strings.Split(p, "\n")
					for i, pl := range lines {
						if i == 0 {
							line += propIndent + pl + "\n"
						} else if i == len(lines)-1 {
							// Closing brace — same indent as the prop
							line += propIndent + pl + ",\n"
						} else {
							line += propIndent + "    " + pl + "\n"
						}
					}
				} else {
					line += propIndent + p + ",\n"
				}
			}
			line += f.indentStr() + ")"
		} else {
			line += "(" + strings.Join(props, ", ") + ")"
		}
	} else if vn.HasProps {
		line += "()"
	}

	hasBody := vn.HasBody || len(vn.Children) > 0
	if hasBody {
		if len(vn.Children) == 0 {
			if vn.EndLine > vn.Pos.Line {
				// Multi-line empty body in source — preserve as multi-line
				line += " {"
				f.writeLine(line)
				f.emitPendingInlineComment()
				f.emitInlineComment(vn.Pos.Line)
				f.writeLine("}")
			} else {
				line += " { }"
				f.writeLine(line)
				f.emitPendingInlineComment()
			}
		} else {
			line += " {"
			f.writeLine(line)
			f.emitPendingInlineComment()
			f.emitInlineComment(vn.Pos.Line)
			f.indent++

			// Children
			for _, child := range vn.Children {
				f.emitCommentsBefore(child.Pos.Line)
				f.formatVisualNode(child)
				f.emitInlineComment(child.Pos.Line)
			}

			f.indent--
			f.writeLine("}")
		}
	} else {
		f.writeLine(line)
		f.emitPendingInlineComment()
	}
}

func (f *formatter) formatEventValue(expr ast.Expr) string {
	if expr.SNGL != nil {
		if sb, ok := expr.SNGL.(*ast.StmtBlock); ok && len(sb.Stmts) > 1 {
			// Multi-line event handler: statements at indent+1, closing } at indent
			innerIndent := strings.Repeat("    ", f.indent+1)
			outerIndent := strings.Repeat("    ", f.indent)
			var lines []string
			lines = append(lines, "{")
			for _, stmt := range sb.Stmts {
				lines = append(lines, innerIndent+FormatNode(stmt))
			}
			lines = append(lines, outerIndent+"}")
			return strings.Join(lines, "\n")
		}
		return "{ " + FormatStmt(expr.SNGL) + " }"
	}
	return "{ null }"
}

// formatExprValue formats an Expr as SNGL source.
func (f *formatter) formatExprValue(expr ast.Expr) string {
	if expr.SNGL != nil {
		return FormatNode(expr.SNGL)
	}
	if expr.Literal != nil {
		return formatLiteral(expr.Literal, expr.TypeHint)
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
	case *ast.ElementRefExpr:
		return "#" + e.Name
	case *ast.LambdaExpr:
		body := FormatNode(e.Body)
		var params []string
		for i, name := range e.Params {
			if i < len(e.ParamTypes) && e.ParamTypes[i] != "" {
				params = append(params, name+" "+e.ParamTypes[i])
			} else {
				params = append(params, name)
			}
		}
		return "(" + strings.Join(params, ", ") + ") => " + body
	case *ast.BinaryExpr:
		left := FormatNode(e.Left)
		right := FormatNode(e.Right)
		myPrec := binPrec(e.Op)
		// Wrap left operand if it has lower precedence or is a ternary
		if lb, ok := e.Left.(*ast.BinaryExpr); ok && binPrec(lb.Op) < myPrec {
			left = "(" + left + ")"
		} else if _, ok := e.Left.(*ast.TernaryExpr); ok {
			left = "(" + left + ")"
		}
		// Wrap right operand if it has lower precedence or is a ternary
		if rb, ok := e.Right.(*ast.BinaryExpr); ok && binPrec(rb.Op) < myPrec {
			right = "(" + right + ")"
		} else if _, ok := e.Right.(*ast.TernaryExpr); ok {
			right = "(" + right + ")"
		}
		return left + " " + binOpString(e.Op) + " " + right
	case *ast.UnaryExpr:
		operand := FormatNode(e.Operand)
		// Wrap binary/ternary operands in parens
		switch e.Operand.(type) {
		case *ast.BinaryExpr, *ast.TernaryExpr:
			operand = "(" + operand + ")"
		}
		if e.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		return FormatNode(e.Cond) + " ? " + FormatNode(e.Then) + " : " + FormatNode(e.Else)
	case *ast.SelectExpr:
		return formatPostfixOperand(e.Operand) + "." + e.Field
	case *ast.IndexExpr:
		return FormatNode(e.Operand) + "[" + FormatNode(e.Index) + "]"
	case *ast.CallExpr:
		args := formatArgs(e.Args)
		return e.Func + "(" + args + ")"
	case *ast.MethodExpr:
		args := formatArgs(e.Args)
		return formatPostfixOperand(e.Receiver) + "." + e.Method + "(" + args + ")"
	case *ast.StructExpr:
		var fields []string
		sep := ": " // named struct uses ":"
		if e.Name == "" {
			sep = "=" // anonymous struct uses "="
		}
		for _, field := range e.Fields {
			if field.Spread {
				fields = append(fields, "..."+FormatNode(field.Value))
			} else {
				fields = append(fields, field.Name+sep+FormatNode(field.Value))
			}
		}
		if e.Multiline {
			var sb strings.Builder
			sb.WriteString(e.Name + "{\n")
			for _, f := range fields {
				sb.WriteString(f + ",\n")
			}
			sb.WriteString("}")
			return sb.String()
		}
		return e.Name + "{" + strings.Join(fields, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(e.Elements))
		for i, el := range e.Elements {
			parts[i] = FormatNode(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.SpreadExpr:
		return "..." + FormatNode(e.Operand)
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
	case *ast.VarStmt:
		if e.Type != "" {
			return "var " + e.Name + " " + typeHintStr(e.Type, nil) + " = " + FormatNode(e.Init)
		}
		return "var " + e.Name + " = " + FormatNode(e.Init)
	case *ast.ReturnStmt:
		if e.Value == nil {
			return "return"
		}
		return "return " + FormatNode(e.Value)
	case *ast.CallStmt:
		return FormatNode(e.Call)
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
		if e.Raw != "" {
			return e.Raw
		}
		return fmt.Sprintf("%d", e.Value)
	case ast.LiteralFloat:
		if e.Raw != "" {
			return e.Raw
		}
		return fmt.Sprintf("%v", e.Value)
	case ast.LiteralString:
		s := fmt.Sprintf("%v", e.Value)
		// Use escapeStringContent to also escape { for interpolation safety.
		return "\"" + escapeStringContent(s) + "\""
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
		return "\"" + escapeStringContent(val) + "\""
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

// formatPostfixOperand wraps numeric literals in parens to prevent
// ambiguity with dot access (e.g. 0.field would parse as float 0.).
func formatPostfixOperand(n ast.Node) string {
	if lit, ok := n.(*ast.LiteralExpr); ok {
		if lit.Kind == ast.LiteralInt || lit.Kind == ast.LiteralFloat {
			return "(" + FormatNode(n) + ")"
		}
	}
	return FormatNode(n)
}

func formatArgs(args []ast.Node) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = FormatNode(a)
	}
	return strings.Join(parts, ", ")
}

// binPrec returns the precedence level for a binary operator.
// Higher values bind tighter.
func binPrec(op ast.BinaryOp) int {
	switch op {
	case ast.BinOr:
		return 1
	case ast.BinAnd:
		return 2
	case ast.BinEq, ast.BinNeq:
		return 3
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		return 4
	case ast.BinAdd, ast.BinSub:
		return 5
	case ast.BinMul, ast.BinDiv, ast.BinMod:
		return 6
	default:
		return 0
	}
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
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		case '{':
			sb.WriteString(`\{`)
		default:
			// Drop control characters that SNGL can't represent.
			if r >= 0x20 && r != 0x7f {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
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
		for i, p := range d.ParamTypes {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(typeHintStr(p, nil))
		}
		sb.WriteString(")")
		if d.ReturnType != "" {
			sb.WriteString(" -> ")
			sb.WriteString(typeHintStr(d.ReturnType, nil))
		}
		return sb.String()
	}

	// Bare func type (e.g. nested "func:" or "func:string~int")
	if strings.HasPrefix(hint, "func:") {
		params, ret := splitFuncBody(hint[5:])
		var sb strings.Builder
		sb.WriteString("func(")
		for i, p := range params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(typeHintStr(p, nil))
		}
		sb.WriteString(")")
		if ret != "" {
			sb.WriteString(" -> ")
			sb.WriteString(typeHintStr(ret, nil))
		}
		return sb.String()
	}

	// Inline enum: "enum:light|dark" → "enum<light | dark>"
	if after, ok := strings.CutPrefix(hint, "enum:"); ok {
		values := strings.Split(after, "|")
		return "enum<" + strings.Join(values, " | ") + ">"
	}

	// Unit types: "unit:s" — inferred from unit literals, omit
	if strings.HasPrefix(hint, "unit:") {
		return ""
	}

	// Raw generic types stored with <> (e.g. "A<func:B>") — pass through as-is
	// but recursively format the inner type.
	if i := strings.Index(hint, "<"); i >= 0 && strings.HasSuffix(hint, ">") {
		name := hint[:i]
		inner := hint[i+1 : len(hint)-1]
		return name + "<" + typeHintStr(inner, nil) + ">"
	}

	// Generic types: "list:Todo" → "list<Todo>"
	if strings.Contains(hint, ":") {
		parts := strings.SplitN(hint, ":", 2)
		return parts[0] + "<" + typeHintStr(parts[1], nil) + ">"
	}

	return hint
}

// consumeEncodedType consumes one type from the encoded type string and
// returns the consumed type and the remaining string.
func consumeEncodedType(s string) (typ, rest string) {
	if strings.HasPrefix(s, "func:") {
		inner := s[5:]
		var params []string
		for inner != "" && inner[0] != '~' {
			var param string
			param, inner = consumeEncodedType(inner)
			params = append(params, param)
			if inner != "" && inner[0] == ':' {
				inner = inner[1:]
			}
		}
		result := "func:" + strings.Join(params, ":")
		if inner != "" && inner[0] == '~' {
			inner = inner[1:]
			var ret string
			ret, inner = consumeEncodedType(inner)
			result += "~" + ret
		}
		return result, inner
	}
	for i := range len(s) {
		if s[i] == ':' || s[i] == '~' {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// splitFuncBody splits an encoded func body into param types and return type,
// correctly handling nested func types.
func splitFuncBody(body string) (params []string, ret string) {
	for body != "" && body[0] != '~' {
		var param string
		param, body = consumeEncodedType(body)
		if param != "" {
			params = append(params, param)
		}
		if body != "" && body[0] == ':' {
			body = body[1:]
		}
	}
	if body != "" && body[0] == '~' {
		ret = body[1:]
	}
	return
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
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, escapeStringContent(v)))
	}
	return strings.Join(parts, ", ")
}
