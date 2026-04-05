package parser

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Format writes an ast.Document as .sngl source text.
func Format(doc *ast.Document) string {
	var sb strings.Builder
	sb.Grow(estimateDocSize(doc))
	FormatTo(doc, &sb)
	return sb.String()
}

// estimateDocSize returns a rough byte estimate of the formatted output.
func estimateDocSize(doc *ast.Document) int {
	n := 0
	for _, imp := range doc.Imports {
		n += 10 + len(imp.Path) // import "path"\n
	}
	for _, o := range doc.Outputs {
		n += 20 + len(o.Platform) + len(o.Lang) // output platform lang { ... }\n
		for k, v := range o.Options {
			n += len(k) + len(v) + 8
		}
	}
	for _, s := range doc.Structs {
		n += 12 + len(s.Name)
		for _, f := range s.Fields {
			n += 8 + len(f.Name) + len(f.Type) + estimateExprSize(f.Default)
		}
	}
	for _, e := range doc.Enums {
		n += 10 + len(e.Name)
		for _, v := range e.Values {
			n += len(v) + 2
		}
	}
	for _, c := range doc.Consts {
		n += 12 + len(c.Name) + estimateExprSize(c.Init)
	}
	for _, d := range doc.Data {
		n += 8 + len(d.Name) + estimateExprSize(d.Init)
	}
	for _, f := range doc.Functions {
		n += estimateFuncSize(f)
	}
	for _, comp := range doc.Components {
		n += estimateCompSize(comp)
	}
	for _, c := range doc.Comments {
		n += len(c.Text) + 1
	}
	for _, win := range doc.Windows {
		n += 16 + len(win.Name)
		for _, vn := range win.Children {
			n += estimateVNSize(vn)
		}
	}
	if doc.App != nil {
		n += 10
		for _, vn := range doc.App.Children {
			n += estimateVNSize(vn)
		}
		for _, win := range doc.App.Windows {
			n += 16 + len(win.Name)
			for _, vn := range win.Children {
				n += estimateVNSize(vn)
			}
		}
	}
	if n < 256 {
		n = 256
	}
	return n
}

func estimateCompSize(comp *ast.Component) int {
	n := 16 + len(comp.Name)
	for _, p := range comp.Params {
		n += 8 + len(p.Name) + estimateExprSize(p.Default)
	}
	for _, c := range comp.Consts {
		n += 12 + len(c.Name) + estimateExprSize(c.Init)
	}
	for _, d := range comp.Data {
		n += 8 + len(d.Name) + estimateExprSize(d.Init)
	}
	for _, f := range comp.Functions {
		n += estimateFuncSize(f)
	}
	for _, vn := range comp.Body {
		n += estimateVNSize(vn)
	}
	return n
}

func estimateFuncSize(f *ast.FuncDef) int {
	n := 10 + len(f.Name)
	for _, p := range f.Params {
		n += len(p.Name) + len(p.Type) + 4
	}
	if f.Block != nil {
		n += 8
		for _, s := range f.Block.Stmts {
			n += estimateNodeSize(s) + 8
		}
		if f.Block.Return != nil {
			n += estimateNodeSize(f.Block.Return) + 12
		}
	} else {
		n += estimateExprSize(f.Body) + 8
	}
	return n
}

func estimateVNSize(vn *ast.VisualNode) int {
	n := 8 + len(vn.Component)
	for k, v := range vn.Props {
		n += len(k) + estimateExprSize(v) + 4
	}
	for k, v := range vn.Events {
		n += len(k) + estimateExprSize(v) + 4
	}
	for k, v := range vn.Bindings {
		n += len(k) + estimateExprSize(v) + 4
	}
	for _, child := range vn.Children {
		n += estimateVNSize(child)
	}
	return n
}

func estimateExprSize(e ast.Expr) int {
	if e.SNGL != nil {
		return estimateNodeSize(e.SNGL)
	}
	return 8
}

func estimateNodeSize(n ast.Node) int {
	if n == nil {
		return 4
	}
	switch e := n.(type) {
	case *ast.IdentExpr:
		return len(e.Name)
	case *ast.LiteralExpr:
		if e.Raw != "" {
			return len(e.Raw)
		}
		return 8
	case *ast.BinaryExpr:
		return estimateNodeSize(e.Left) + estimateNodeSize(e.Right) + 4
	case *ast.UnaryExpr:
		return estimateNodeSize(e.Operand) + 1
	case *ast.CallExpr:
		n := len(e.Func) + 2
		for _, a := range e.Args {
			n += estimateNodeSize(a) + 2
		}
		return n
	case *ast.MethodExpr:
		n := estimateNodeSize(e.Receiver) + len(e.Method) + 3
		for _, a := range e.Args {
			n += estimateNodeSize(a) + 2
		}
		return n
	case *ast.SelectExpr:
		return estimateNodeSize(e.Operand) + len(e.Field) + 1
	case *ast.InterpolationExpr:
		n := 2
		for _, p := range e.Parts {
			n += estimateNodeSize(p) + 2
		}
		return n
	case *ast.TernaryExpr:
		return estimateNodeSize(e.Cond) + estimateNodeSize(e.Then) + estimateNodeSize(e.Else) + 6
	case *ast.StmtBlock:
		n := 0
		for _, s := range e.Stmts {
			n += estimateNodeSize(s) + 2
		}
		return n
	default:
		return 16
	}
}

// FormatTo writes an ast.Document as .sngl source text to w.
func FormatTo(doc *ast.Document, w io.Writer) (int, error) {
	f := &formatter{w: w, comments: doc.Comments}
	f.formatDocument(doc)
	f.flush()
	return f.written, f.err
}

// commentAlignTarget maps a Decls index to the target column width for inline
// comment alignment. Only populated for runs of 2+ consecutive declarations
// that each have an inline comment.
type commentAlignTarget map[int]int

// computeCommentAlignment pre-scans a Decls slice to find runs of consecutive
// declaration+inline-comment pairs and computes the alignment target width
// (max formatted declaration width) for each run.
func (f *formatter) computeCommentAlignment(decls []ast.Decl) commentAlignTarget {
	// Identify runs of decl+inline-comment pairs.
	type pair struct {
		declIdx int
		decl    ast.Decl
	}
	var current []pair
	var runs [][]pair

	flush := func() {
		if len(current) >= 2 {
			runs = append(runs, current)
		}
		current = nil
	}

	for i := 0; i < len(decls); i++ {
		d := decls[i]
		if _, ok := d.(*ast.Comment); ok {
			continue // skip standalone comments
		}
		// Check if next decl is an inline comment
		if i+1 < len(decls) {
			if c, ok := decls[i+1].(*ast.Comment); ok && c.Inline {
				current = append(current, pair{i, d})
				i++ // skip the comment
				continue
			}
		}
		// No inline comment follows — break any current run
		flush()
	}
	flush()

	if len(runs) == 0 {
		return nil
	}

	targets := commentAlignTarget{}
	var buf strings.Builder
	for _, run := range runs {
		maxWidth := 0
		widths := make([]int, len(run))
		for j, p := range run {
			w := f.measureDeclWidth(&buf, p.decl)
			widths[j] = w
			if w > maxWidth {
				maxWidth = w
			}
		}
		for j, p := range run {
			if widths[j] < maxWidth {
				targets[p.declIdx] = maxWidth
			}
		}
	}
	return targets
}

// measureDeclWidth computes the exact formatted line width of a declaration
// by writing to a scratch buffer.
func (f *formatter) measureDeclWidth(buf *strings.Builder, d ast.Decl) int {
	buf.Reset()
	buf.WriteString(indentStr(f.indent))
	switch decl := d.(type) {
	case *ast.Const:
		if decl.Disabled {
			buf.WriteString("/- ")
		}
		buf.WriteString("const ")
		buf.WriteString(decl.Name)
		if decl.ExplicitType {
			if ts := typeHintStr(decl.Init.TypeHint, nil); ts != "" {
				buf.WriteByte(' ')
				buf.WriteString(ts)
			}
		}
		buf.WriteString(" = ")
		f.writeExprTo(buf, decl.Init)
	case *ast.Data:
		if decl.Disabled {
			buf.WriteString("/- ")
		}
		buf.WriteString("var ")
		buf.WriteString(decl.Name)
		hasInit := decl.Init.SNGL != nil || decl.Init.Literal != nil
		if decl.ExplicitType {
			if typeStr := typeHintStr(decl.Init.TypeHint, decl); typeStr != "" {
				buf.WriteByte(' ')
				buf.WriteString(typeStr)
			}
		}
		if hasInit {
			buf.WriteString(" = ")
			f.writeExprTo(buf, decl.Init)
		}
		if decl.Extern {
			buf.WriteString(" extern")
		}
		if decl.Trigger != "" {
			autoName := "On" + strings.ToUpper(decl.Name[:1]) + decl.Name[1:] + "Changed"
			if decl.Trigger == autoName {
				buf.WriteString(" @")
			} else {
				buf.WriteString(" @")
				buf.WriteString(decl.Trigger)
			}
		}
	case *ast.FuncDef:
		buf.WriteString("func ")
		buf.WriteString(decl.Name)
		if len(decl.TypeParams) > 0 {
			buf.WriteByte('<')
			buf.WriteString(strings.Join(decl.TypeParams, ", "))
			buf.WriteByte('>')
		}
		if len(decl.Params) > 0 || decl.Block != nil || decl.HasParens {
			buf.WriteByte('(')
			for i, p := range decl.Params {
				if i > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(p.Name)
				buf.WriteByte(' ')
				buf.WriteString(typeHintStr(p.Type, nil))
			}
			buf.WriteByte(')')
		}
		if decl.Block == nil {
			buf.WriteString(" => ")
			f.writeExprTo(buf, decl.Body)
		}
	case *ast.VisualNode:
		f.measureVisualNode(buf, decl)
	default:
		// Timers, imports, etc. — measure conservatively
		buf.WriteString("???")
	}
	return buf.Len()
}

// measureVisualNode writes the single-line representation of a leaf visual node
// to buf for width measurement.
func (f *formatter) measureVisualNode(buf *strings.Builder, vn *ast.VisualNode) {
	if vn.Disabled {
		buf.WriteString("/- ")
	}
	buf.WriteString(vn.Component)
	if vn.ID != "" {
		buf.WriteString(" #")
		buf.WriteString(vn.ID)
	}

	// Build props using a separate buffer to avoid clobbering propBuf
	var props []string
	var pb strings.Builder
	buildPropTo := func(prefix string, expr ast.Expr) string {
		pb.Reset()
		pb.WriteString(prefix)
		pb.WriteByte('=')
		f.writeExprTo(&pb, expr)
		return pb.String()
	}
	buildEventTo := func(prefix string, expr ast.Expr) string {
		pb.Reset()
		pb.WriteString(prefix)
		pb.WriteByte('=')
		f.writeEventValue(&pb, expr)
		return pb.String()
	}

	if len(vn.PropOrder) > 0 {
		for _, entry := range vn.PropOrder {
			if strings.HasPrefix(entry, "@") {
				name := entry[1:]
				if expr, ok := vn.Events[name]; ok {
					props = append(props, buildEventTo("@"+name, expr))
				}
			} else if strings.HasPrefix(entry, ":") {
				name := entry[1:]
				if expr, ok := vn.Bindings[name]; ok {
					props = append(props, buildPropTo(":"+name, expr))
				}
			} else {
				switch entry {
				case "key":
					if vn.Key != nil {
						props = append(props, buildPropTo("key", *vn.Key))
					}
				case "class":
					if vn.Class != nil {
						props = append(props, buildPropTo("class", *vn.Class))
					}
				case "ref":
					if vn.Ref != nil {
						props = append(props, buildPropTo("ref", *vn.Ref))
					}
				default:
					if expr, ok := vn.Props[entry]; ok {
						props = append(props, buildPropTo(entry, expr))
					}
				}
			}
		}
	} else {
		if vn.Key != nil {
			props = append(props, buildPropTo("key", *vn.Key))
		}
		if vn.Class != nil {
			props = append(props, buildPropTo("class", *vn.Class))
		}
		if vn.Ref != nil {
			props = append(props, buildPropTo("ref", *vn.Ref))
		}
		for _, k := range sortedKeys(vn.Props) {
			props = append(props, buildPropTo(k, vn.Props[k]))
		}
		for _, name := range sortedKeys(vn.Bindings) {
			props = append(props, buildPropTo(":"+name, vn.Bindings[name]))
		}
		for _, name := range sortedKeys(vn.Events) {
			props = append(props, buildEventTo("@"+name, vn.Events[name]))
		}
	}

	if len(props) > 0 {
		if vn.MultilineProps {
			// Multiline props: just measure the first line (component + id + "(")
			buf.WriteByte('(')
		} else {
			buf.WriteByte('(')
			for i, p := range props {
				if i > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(p)
			}
			buf.WriteByte(')')
		}
	} else if vn.HasProps {
		buf.WriteString("()")
	}

	if vn.HasBody || len(vn.Children) > 0 {
		if len(vn.Children) == 0 && vn.EndLine <= vn.Pos.Line {
			buf.WriteString(" { }")
		}
	}
}

// emitAlignedInlineComment appends an inline comment with padding to reach
// targetWidth. If targetWidth is 0, emits with a single space separator.
func (f *formatter) emitAlignedInlineComment(c *ast.Comment, targetWidth int) {
	f.pendingNL = false // absorb trailing newline
	if targetWidth > 0 {
		for f.lineWidth < targetWidth {
			io.WriteString(f, " ")
		}
	}
	f.write(" " + c.Text)
	f.newline()
}

// emitCommentsBefore emits all comments with position before the given line.
func (f *formatter) emitCommentsBefore(line int) {
	for f.commentI < len(f.comments) && f.comments[f.commentI].Pos.Line < line {
		c := f.comments[f.commentI]
		f.writeLine(c.Text)
		f.commentI++
	}
}

// emitInlineComment emits an inline comment on the given line if one exists.
func (f *formatter) emitInlineComment(line int) {
	if f.commentI < len(f.comments) {
		c := f.comments[f.commentI]
		if c.Inline && c.Pos.Line == line && !f.emittedInline[[2]int{c.Pos.Line, c.Pos.Column}] {
			f.pendingNL = false // absorb trailing newline
			f.write(" " + c.Text)
			f.newline()
			if f.emittedInline == nil {
				f.emittedInline = map[[2]int]bool{}
			}
			f.emittedInline[[2]int{c.Pos.Line, c.Pos.Column}] = true
			f.commentI++
		}
	}
}

type formatter struct {
	w         io.Writer
	written   int   // total bytes written
	lineWidth int   // bytes on current line (for comment alignment)
	pendingNL bool  // deferred newline, flushed before next write
	err       error // first write error; short-circuits subsequent writes

	indent        int
	comments      []ast.Comment
	commentI      int             // next comment index to emit
	emittedInline map[[2]int]bool // tracks inline comments emitted by emitInlineComment (key: [line, col])
	propBuf       strings.Builder // reusable buffer for building prop strings
}

// Write implements io.Writer, flushing any pending newline before writing.
func (f *formatter) Write(p []byte) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.flush()
	n, err := f.w.Write(p)
	f.written += n
	if i := bytes.LastIndexByte(p[:n], '\n'); i >= 0 {
		f.lineWidth = n - i - 1
	} else {
		f.lineWidth += n
	}
	f.err = err
	return n, err
}

func (f *formatter) flush() {
	if f.pendingNL && f.err == nil {
		f.pendingNL = false
		_, f.err = f.w.Write([]byte{'\n'})
		f.written++
		f.lineWidth = 0
	}
}

func (f *formatter) write(s string) { io.WriteString(f, s) }

func (f *formatter) newline() {
	f.pendingNL = true
}

// Cached indent strings to avoid repeated allocation.
var indentCache = [...]string{
	0: "",
	1: "    ",
	2: "        ",
	3: "            ",
	4: "                ",
	5: "                    ",
	6: "                        ",
	7: "                            ",
	8: "                                ",
}

func indentStr(level int) string {
	if level < len(indentCache) {
		return indentCache[level]
	}
	return strings.Repeat("    ", level)
}

func (f *formatter) writeIndent() {
	io.WriteString(f, indentStr(f.indent))
}

func (f *formatter) writeLine(s string) {
	f.writeIndent()
	f.write(s)
	f.newline()
}

func (f *formatter) writeDisabledLine(disabled bool, s string) {
	f.writeIndent()
	if disabled {
		f.write("/- ")
	}
	f.write(s)
	f.newline()
}

func (f *formatter) formatDocument(doc *ast.Document) {
	prevEndLine := 0
	outputsSeen := false
	alignTargets := f.computeCommentAlignment(doc.Decls)
	for di := 0; di < len(doc.Decls); di++ {
		d := doc.Decls[di]
		switch decl := d.(type) {
		case *ast.Comment:
			if decl.Inline {
				if f.emittedInline[[2]int{decl.Pos.Line, decl.Pos.Column}] {
					continue
				}
				target := alignTargets[di-1]
				f.emitAlignedInlineComment(decl, target)
				if f.emittedInline == nil {
					f.emittedInline = map[[2]int]bool{}
				}
				f.emittedInline[[2]int{decl.Pos.Line, decl.Pos.Column}] = true
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
		case *ast.StructDef:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Disabled {
				f.writeIndent()
				f.write("/- ")
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
				f.formatComponentAsMain(decl)
			} else {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				if decl.Disabled {
					f.writeIndent()
					f.write("/- ")
				}
				f.formatComponent(decl)
			}
			prevEndLine = declEndLine(decl)
		case *ast.Const:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatConsts([]*ast.Const{decl})
			prevEndLine = decl.Pos.Line
		case *ast.Data:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatVars([]*ast.Data{decl})
			prevEndLine = decl.Pos.Line
		case *ast.FuncDef:
			if !decl.IsStdlib {
				if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
					f.newline()
				}
				f.formatFuncDef(decl)
				prevEndLine = declEndLine(decl)
			}
		case *ast.Timer:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatTimer(decl)
			prevEndLine = decl.Pos.Line
		case *ast.Window:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Disabled {
				f.writeIndent()
				f.write("/- ")
			}
			f.formatWindow(decl)
			prevEndLine = declEndLine(decl)
		}
	}
	hasMainInDecls := false
	for _, d := range doc.Decls {
		if comp, ok := d.(*ast.Component); ok && comp.Name == "main" {
			hasMainInDecls = true
			break
		}
	}
	if !hasMainInDecls && doc.App != nil && len(doc.App.Children) > 0 {
		f.formatDocMain(doc)
	}
	if len(doc.Decls) == 0 {
		for f.commentI < len(f.comments) {
			c := f.comments[f.commentI]
			f.writeLine(c.Text)
			f.commentI++
		}
	}
}

func (f *formatter) formatComponentAsMain(comp *ast.Component) {
	if f.written > 0 {
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
	if f.written > 0 {
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

	// App windows
	if doc.App != nil && len(doc.App.Windows) > 0 {
		if memberBlank {
			f.newline()
		}
		for _, win := range doc.App.Windows {
			f.formatWindow(win)
		}
	}

	f.indent--
	f.writeLine("}")
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
		f.writeIndent()
		f.write(field.Name)
		f.write(" ")
		f.write(typeHintStr(field.Type, nil))
		if field.Default.SNGL != nil || field.Default.Literal != nil {
			f.write(" = ")
			f.writeExprValue(field.Default)
		}
		f.newline()
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
		f.writeIndent()
		f.write(k)
		f.write(" = ")
		f.writeExprValue(s.Props[k])
		f.newline()
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) writeConstDecl(c *ast.Const) {
	f.write(c.Name)
	if c.ExplicitType {
		if ts := typeHintStr(c.Init.TypeHint, nil); ts != "" {
			f.write(" ")
			f.write(ts)
		}
	}
	f.write(" = ")
	f.writeExprValue(c.Init)
}

func (f *formatter) formatConsts(consts []*ast.Const) {
	if len(consts) == 1 && !consts[0].Grouped {
		c := consts[0]
		f.writeIndent()
		if c.Disabled {
			f.write("/- ")
		}
		f.write("const ")
		f.writeConstDecl(c)
		f.newline()
		return
	}
	f.writeLine("const (")
	f.indent++
	for _, c := range consts {
		f.writeIndent()
		if c.Disabled {
			f.write("/- ")
		}
		f.writeConstDecl(c)
		f.newline()
	}
	f.indent--
	f.writeLine(")")
}

func (f *formatter) formatVars(data []*ast.Data) {
	if len(data) == 1 && !data[0].Grouped {
		f.writeIndent()
		if data[0].Disabled {
			f.write("/- ")
		}
		f.write("var ")
		f.writeVarDecl(data[0])
		f.newline()
		return
	}
	f.writeLine("var (")
	f.indent++
	for _, d := range data {
		f.writeIndent()
		if d.Disabled {
			f.write("/- ")
		}
		f.writeVarDecl(d)
		f.newline()
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

func (f *formatter) writeVarDecl(d *ast.Data) {
	f.write(d.Name)

	hasInit := d.Init.SNGL != nil || d.Init.Literal != nil

	if d.ExplicitType {
		if typeStr := typeHintStr(d.Init.TypeHint, d); typeStr != "" {
			f.write(" ")
			f.write(typeStr)
		}
	}

	if hasInit {
		f.write(" = ")
		f.writeExprValue(d.Init)
	}

	// Modifiers
	if d.Extern {
		f.write(" extern")
	}
	if d.Trigger != "" {
		autoName := "On" + strings.ToUpper(d.Name[:1]) + d.Name[1:] + "Changed"
		if d.Trigger == autoName {
			f.write(" @")
		} else {
			f.write(" @")
			f.write(d.Trigger)
		}
	}
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
	f.writeIndent()
	f.write("func ")
	f.write(fn.Name)
	if len(fn.TypeParams) > 0 {
		f.write("<")
		f.write(strings.Join(fn.TypeParams, ", "))
		f.write(">")
	}
	if len(fn.Params) > 0 || fn.Block != nil || fn.HasParens {
		f.write("(")
		for i, p := range fn.Params {
			if i > 0 {
				f.write(", ")
			}
			f.write(p.Name)
			f.write(" ")
			f.write(typeHintStr(p.Type, nil))
		}
		f.write(")")
	}
	if fn.Block != nil {
		if fn.ReturnType != "" {
			f.write(" ")
			f.write(typeHintStr(fn.ReturnType, nil))
		}
		f.write(" {")
		f.newline()
		f.indent++
		for _, stmt := range fn.Block.Stmts {
			f.writeIndent()
			writeNode(f, stmt)
			f.newline()
		}
		if fn.Block.Return != nil {
			f.writeIndent()
			f.write("return ")
			writeNode(f, fn.Block.Return)
			f.newline()
		}
		f.indent--
		f.writeLine("}")
	} else {
		f.write(" => ")
		f.writeExprValue(fn.Body)
		f.newline()
	}
}

func (f *formatter) formatTimer(t *ast.Timer) {
	f.writeIndent()
	f.write("timer ")
	f.writeExprValue(t.Interval)
	f.write(" ")
	f.write(t.Active)
	f.write(" {")
	f.newline()
	f.indent++
	if sb, ok := t.Body.(*ast.StmtBlock); ok {
		for _, stmt := range sb.Stmts {
			f.writeIndent()
			writeNode(f, stmt)
			f.newline()
		}
	} else {
		f.writeIndent()
		writeNode(f, t.Body)
		f.newline()
	}
	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatWindow(win *ast.Window) {
	f.writeIndent()
	f.write("window")

	if win.Name != "" {
		f.write(" ")
		f.write(win.Name)
	}

	if win.HasProps && len(win.PropOrder) > 0 {
		f.write("(")
		for i, key := range win.PropOrder {
			if i > 0 {
				f.write(", ")
			}
			f.write(key)
			f.write("=")
			f.writeExprValue(win.Props[key])
		}
		f.write(")")
	}

	f.write(" {")
	f.newline()
	f.indent++

	// Format declarations and visual nodes in order
	prevEndLine := 0
	for _, d := range win.Decls {
		switch decl := d.(type) {
		case *ast.Comment:
			if decl.Inline {
				continue
			}
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.writeLine(decl.Text)
			prevEndLine = decl.Pos.Line
		case *ast.Const:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatConsts([]*ast.Const{decl})
			prevEndLine = decl.Pos.Line
		case *ast.Data:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			f.formatVars([]*ast.Data{decl})
			prevEndLine = decl.Pos.Line
		case *ast.FuncDef:
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
			f.formatVisualNode(decl)
			prevEndLine = declEndLine(decl)
		}
	}

	f.indent--
	f.writeLine("}")
}

func (f *formatter) formatComponent(comp *ast.Component) {
	f.writeIndent()
	f.write("component ")
	f.write(comp.Name)

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
		f.write("(")
		first := true
		for _, p := range comp.Params {
			if !first {
				f.write(", ")
			}
			first = false
			if p.Bidirectional {
				f.write(":")
			}
			f.write(p.Name)
			typeStr := typeHintStr(p.Default.TypeHint, nil)
			if typeStr != "" {
				f.write(" ")
				f.write(typeStr)
			}
			hasDefault := p.Default.SNGL != nil || p.Default.Literal != nil
			if len(p.Enum) > 0 {
				f.write(" enum(")
				f.write(strings.Join(p.Enum, ", "))
				f.write(")")
			}
			if hasDefault {
				f.write(" = ")
				f.writeExprValue(p.Default)
			}
			if p.Required {
				f.write(" required")
			}
		}
		for _, e := range events {
			if !first {
				f.write(", ")
			}
			first = false
			f.write("@")
			f.write(e.Name)
			if e.PayloadType != "" {
				f.write(" ")
				f.write(e.PayloadType)
			}
		}
		f.write(")")
	}

	if comp.ChildrenType != "" {
		f.write(" ")
		f.write(typeHintStr(comp.ChildrenType, nil))
	}

	f.write(" {")
	f.newline()
	if comp.EndLine != comp.Pos.Line {
		f.emitInlineComment(comp.Pos.Line)
	}
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
	case *ast.VisualNode:
		if decl.EndLine > 0 {
			return decl.EndLine
		}
	case *ast.Window:
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
	alignTargets := f.computeCommentAlignment(decls)

	for i := 0; i < len(decls); i++ {
		d := decls[i]
		switch decl := d.(type) {
		case *ast.Comment:
			if decl.Inline {
				target := alignTargets[i-1]
				f.emitAlignedInlineComment(decl, target)
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
			if inlineComment != nil {
				target := alignTargets[i-1] // i was advanced past the comment; i-1 is the comment, i-2 is the VN... but we need the VN index
				f.formatVisualNode(decl)
				f.emitAlignedInlineComment(inlineComment, target)
			} else {
				f.formatVisualNode(decl)
			}
			prevEndLine = declEndLine(decl)
		case *ast.Window:
			if prevEndLine > 0 && decl.Pos.Line > prevEndLine+1 {
				f.newline()
			}
			if decl.Disabled {
				f.writeIndent()
				f.write("/- ")
			}
			f.formatWindow(decl)
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

func (f *formatter) formatVisualNode(vn *ast.VisualNode) {
	if vn.Disabled {
		f.writeIndent()
		f.write("/- ")
		// Temporarily reduce indent so the inner node doesn't double-indent
		saved := f.indent
		f.indent = 0
		f.formatVisualNodeInner(vn)
		f.indent = saved
		return
	}
	// If / For wrapping
	if vn.If != nil || vn.For != nil {
		// When both If and For are set (if cond { for ... { ... } }),
		// emit the if wrapper first, then recurse for the for.
		if vn.If != nil && vn.For != nil {
			f.writeIndent()
			f.write("if ")
			f.writeExprValue(*vn.If)
			f.write(" {")
			f.newline()
			f.indent++
			// Temporarily clear If and format the for+body
			savedIf := vn.If
			vn.If = nil
			f.formatVisualNode(vn)
			vn.If = savedIf
			f.indent--
			f.writeLine("}")
			return
		}
		if vn.For != nil {
			fc := vn.For
			f.writeIndent()
			f.write("for ")
			f.write(fc.Variable)
			if fc.IndexVar != "" {
				f.write(", ")
				f.write(fc.IndexVar)
			}
			f.write(" = ")
			f.writeExprValue(fc.Iterable)
			f.write(" {")
			f.newline()
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
		f.writeIndent()
		f.write("if ")
		f.writeExprValue(*vn.If)
		f.write(" {")
		f.newline()
		f.indent++
		f.formatVisualNodeInner(vn)
		f.indent--
		f.writeLine("}")
		return
	}
	f.formatVisualNodeInner(vn)
}

// buildProp builds a "prefix=value" prop string using the shared propBuf.
func (f *formatter) buildProp(prefix string, expr ast.Expr) string {
	f.propBuf.Reset()
	f.propBuf.WriteString(prefix)
	f.propBuf.WriteByte('=')
	f.writeExprTo(&f.propBuf, expr)
	return f.propBuf.String()
}

func (f *formatter) buildEventProp(prefix string, expr ast.Expr) string {
	f.propBuf.Reset()
	f.propBuf.WriteString(prefix)
	f.propBuf.WriteByte('=')
	f.writeEventValue(&f.propBuf, expr)
	return f.propBuf.String()
}

func (f *formatter) writeExprTo(sb io.Writer, expr ast.Expr) {
	if expr.SNGL != nil {
		writeNode(sb, expr.SNGL)
		return
	}
	if expr.Literal != nil {
		writeLiteral(sb, expr.Literal, expr.TypeHint)
		return
	}
	io.WriteString(sb, "null")
}

func (f *formatter) formatVisualNodeInner(vn *ast.VisualNode) {
	// Build prop list
	var props []string

	if len(vn.PropOrder) > 0 {
		for _, entry := range vn.PropOrder {
			if strings.HasPrefix(entry, "@") {
				name := entry[1:]
				if expr, ok := vn.Events[name]; ok {
					props = append(props, f.buildEventProp("@"+name, expr))
				}
			} else if strings.HasPrefix(entry, ":") {
				name := entry[1:]
				if expr, ok := vn.Bindings[name]; ok {
					props = append(props, f.buildProp(":"+name, expr))
				}
			} else {
				switch entry {
				case "key":
					if vn.Key != nil {
						props = append(props, f.buildProp("key", *vn.Key))
					}
				case "class":
					if vn.Class != nil {
						props = append(props, f.buildProp("class", *vn.Class))
					}
				case "ref":
					if vn.Ref != nil {
						props = append(props, f.buildProp("ref", *vn.Ref))
					}
				default:
					if expr, ok := vn.Props[entry]; ok {
						props = append(props, f.buildProp(entry, expr))
					}
				}
			}
		}
	} else {
		if vn.Key != nil {
			props = append(props, f.buildProp("key", *vn.Key))
		}
		if vn.Class != nil {
			props = append(props, f.buildProp("class", *vn.Class))
		}
		if vn.Ref != nil {
			props = append(props, f.buildProp("ref", *vn.Ref))
		}
		for _, k := range sortedKeys(vn.Props) {
			props = append(props, f.buildProp(k, vn.Props[k]))
		}
		for _, name := range sortedKeys(vn.Bindings) {
			props = append(props, f.buildProp(":"+name, vn.Bindings[name]))
		}
		for _, name := range sortedKeys(vn.Events) {
			props = append(props, f.buildEventProp("@"+name, vn.Events[name]))
		}
	}

	// Write component name and ID
	f.writeIndent()
	f.write(vn.Component)
	if vn.ID != "" {
		f.write(" #")
		f.write(vn.ID)
	}
	if len(props) > 0 {
		if vn.MultilineProps {
			f.write("(\n")
			propIndent := indentStr(f.indent + 1)
			for _, p := range props {
				if strings.Contains(p, "\n") {
					lines := strings.Split(p, "\n")
					for i, pl := range lines {
						f.write(propIndent)
						if i > 0 && i < len(lines)-1 {
							f.write("    ")
						}
						f.write(pl)
						if i == len(lines)-1 {
							f.write(",")
						}
						f.write("\n")
					}
				} else {
					f.write(propIndent)
					f.write(p)
					f.write(",\n")
				}
			}
			f.writeIndent()
			f.write(")")
		} else {
			f.write("(")
			for i, p := range props {
				if i > 0 {
					f.write(", ")
				}
				f.write(p)
			}
			f.write(")")
		}
	} else if vn.HasProps {
		f.write("()")
	}

	hasBody := vn.HasBody || len(vn.Children) > 0
	if hasBody {
		if len(vn.Children) == 0 {
			if vn.EndLine > vn.Pos.Line {
				f.write(" {")
				f.newline()

				f.emitInlineComment(vn.Pos.Line)
				f.writeLine("}")
			} else {
				f.write(" { }")
				f.newline()

			}
		} else {
			f.write(" {")
			f.newline()

			f.emitInlineComment(vn.Pos.Line)
			f.indent++
			for _, child := range vn.Children {
				f.emitCommentsBefore(child.Pos.Line)
				f.formatVisualNode(child)
				f.emitInlineComment(child.Pos.Line)
			}
			f.indent--
			f.writeLine("}")
		}
	} else {
		f.newline()
	}
}

func (f *formatter) writeEventValue(sb io.Writer, expr ast.Expr) {
	if expr.SNGL != nil {
		if block, ok := expr.SNGL.(*ast.StmtBlock); ok && len(block.Stmts) > 1 {
			io.WriteString(sb, string('{'))
			for _, stmt := range block.Stmts {
				io.WriteString(sb, "\n")
				io.WriteString(sb, indentStr(f.indent+1))
				writeNode(sb, stmt)
			}
			io.WriteString(sb, "\n")
			io.WriteString(sb, indentStr(f.indent))
			io.WriteString(sb, string('}'))
			return
		}
		io.WriteString(sb, "{ ")
		writeNode(sb, expr.SNGL)
		io.WriteString(sb, " }")
		return
	}
	io.WriteString(sb, "{ null }")
}

// writeExprValue writes an Expr directly to the output writer.
func (f *formatter) writeExprValue(expr ast.Expr) {
	if expr.SNGL != nil {
		writeNode(f, expr.SNGL)
		return
	}
	if expr.Literal != nil {
		writeLiteral(f, expr.Literal, expr.TypeHint)
		return
	}
	f.write("null")
}

// FormatNode formats an ast.Node as SNGL expression syntax.
func FormatNode(n ast.Node) string {
	// Fast paths for common leaf nodes to avoid builder allocation
	if n == nil {
		return "null"
	}
	switch e := n.(type) {
	case *ast.IdentExpr:
		return e.Name
	case *ast.LiteralExpr:
		if e.Kind == ast.LiteralBool {
			if e.Value.(bool) {
				return "true"
			}
			return "false"
		}
		if e.Kind == ast.LiteralNull {
			return "null"
		}
		if e.Raw != "" {
			return e.Raw
		}
	}
	var sb strings.Builder
	writeNode(&sb, n)
	return sb.String()
}

func writeNode(sb io.Writer, n ast.Node) {
	if n == nil {
		io.WriteString(sb, "null")
		return
	}
	switch e := n.(type) {
	case *ast.LiteralExpr:
		writeLiteralExpr(sb, e)
	case *ast.IdentExpr:
		io.WriteString(sb, e.Name)
	case *ast.ElementRefExpr:
		io.WriteString(sb, string('#'))
		io.WriteString(sb, e.Name)
	case *ast.LambdaExpr:
		if e.Block != nil {
			io.WriteString(sb, "func(")
			for i, name := range e.Params {
				if i > 0 {
					io.WriteString(sb, ", ")
				}
				io.WriteString(sb, name)
				if i < len(e.ParamTypes) && e.ParamTypes[i] != "" {
					io.WriteString(sb, string(' '))
					io.WriteString(sb, e.ParamTypes[i])
				}
			}
			io.WriteString(sb, ") {\n")
			for _, stmt := range e.Block.Stmts {
				io.WriteString(sb, "    ")
				writeNode(sb, stmt)
				io.WriteString(sb, "\n")
			}
			if e.Block.Return != nil {
				io.WriteString(sb, "    return ")
				writeNode(sb, e.Block.Return)
				io.WriteString(sb, "\n")
			}
			io.WriteString(sb, string('}'))
		} else {
			io.WriteString(sb, string('('))
			for i, name := range e.Params {
				if i > 0 {
					io.WriteString(sb, ", ")
				}
				io.WriteString(sb, name)
				if i < len(e.ParamTypes) && e.ParamTypes[i] != "" {
					io.WriteString(sb, string(' '))
					io.WriteString(sb, e.ParamTypes[i])
				}
			}
			io.WriteString(sb, ") => ")
			writeNode(sb, e.Body)
		}
	case *ast.ParenExpr:
		io.WriteString(sb, string('('))
		writeNode(sb, e.Inner)
		io.WriteString(sb, string(')'))
	case *ast.BinaryExpr:
		writeNode(sb, e.Left)
		io.WriteString(sb, string(' '))
		io.WriteString(sb, binOpString(e.Op))
		io.WriteString(sb, string(' '))
		writeNode(sb, e.Right)
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			io.WriteString(sb, string('!'))
			// Avoid !! ambiguity: if operand starts with !, add space
			if u, ok := e.Operand.(*ast.UnaryExpr); ok && u.Op == ast.UnaryNot {
				io.WriteString(sb, string(' '))
			}
			writeNode(sb, e.Operand)
		} else {
			io.WriteString(sb, string('-'))
			writeNode(sb, e.Operand)
		}
	case *ast.TernaryExpr:
		writeNode(sb, e.Cond)
		io.WriteString(sb, " ? ")
		writeNode(sb, e.Then)
		io.WriteString(sb, " : ")
		writeNode(sb, e.Else)
	case *ast.SelectExpr:
		writeNode(sb, e.Operand)
		io.WriteString(sb, string('.'))
		io.WriteString(sb, e.Field)
	case *ast.IndexExpr:
		writeNode(sb, e.Operand)
		io.WriteString(sb, string('['))
		writeNode(sb, e.Index)
		io.WriteString(sb, string(']'))
	case *ast.CallExpr:
		io.WriteString(sb, e.Func)
		io.WriteString(sb, string('('))
		writeArgs(sb, e.Args)
		io.WriteString(sb, string(')'))
	case *ast.MethodExpr:
		writeNode(sb, e.Receiver)
		io.WriteString(sb, string('.'))
		io.WriteString(sb, e.Method)
		io.WriteString(sb, string('('))
		writeArgs(sb, e.Args)
		io.WriteString(sb, string(')'))
	case *ast.StructExpr:
		sep := ": "
		if e.Name == "" {
			sep = "="
		}
		io.WriteString(sb, e.Name)
		io.WriteString(sb, string('{'))
		if e.Multiline {
			io.WriteString(sb, "\n")
			for _, field := range e.Fields {
				if field.Spread {
					io.WriteString(sb, "...")
					writeNode(sb, field.Value)
				} else {
					io.WriteString(sb, field.Name)
					io.WriteString(sb, sep)
					writeNode(sb, field.Value)
				}
				io.WriteString(sb, ",\n")
			}
		} else {
			for i, field := range e.Fields {
				if i > 0 {
					io.WriteString(sb, ", ")
				}
				if field.Spread {
					io.WriteString(sb, "...")
					writeNode(sb, field.Value)
				} else {
					io.WriteString(sb, field.Name)
					io.WriteString(sb, sep)
					writeNode(sb, field.Value)
				}
			}
		}
		io.WriteString(sb, string('}'))
	case *ast.ListExpr:
		io.WriteString(sb, string('['))
		for i, el := range e.Elements {
			if i > 0 {
				io.WriteString(sb, ", ")
			}
			writeNode(sb, el)
		}
		io.WriteString(sb, string(']'))
	case *ast.SpreadExpr:
		io.WriteString(sb, "...")
		writeNode(sb, e.Operand)
	case *ast.InterpolationExpr:
		io.WriteString(sb, string('"'))
		for _, p := range e.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				io.WriteString(sb, escapeStringContent(fmt.Sprintf("%v", lit.Value)))
			} else {
				io.WriteString(sb, string('{'))
				writeNode(sb, p)
				io.WriteString(sb, string('}'))
			}
		}
		io.WriteString(sb, string('"'))
	case *ast.AssignStmt:
		writeNode(sb, e.Target)
		io.WriteString(sb, string(' '))
		io.WriteString(sb, assignOpString(e.Op))
		io.WriteString(sb, string(' '))
		writeNode(sb, e.Value)
	case *ast.ToggleStmt:
		writeNode(sb, e.Target)
		io.WriteString(sb, "!!")
	case *ast.EmitStmt:
		io.WriteString(sb, string('@'))
		io.WriteString(sb, e.Name)
		io.WriteString(sb, string('('))
		writeArgs(sb, e.Args)
		io.WriteString(sb, string(')'))
	case *ast.StmtBlock:
		for i, s := range e.Stmts {
			if i > 0 {
				io.WriteString(sb, "; ")
			}
			writeNode(sb, s)
		}
	case *ast.VarStmt:
		io.WriteString(sb, "var ")
		io.WriteString(sb, e.Name)
		if e.Type != "" {
			io.WriteString(sb, string(' '))
			io.WriteString(sb, typeHintStr(e.Type, nil))
		}
		io.WriteString(sb, " = ")
		writeNode(sb, e.Init)
	case *ast.ReturnStmt:
		io.WriteString(sb, "return")
		if e.Value != nil {
			io.WriteString(sb, string(' '))
			writeNode(sb, e.Value)
		}
	case *ast.CallStmt:
		writeNode(sb, e.Call)
	default:
		fmt.Fprintf(sb, "/* unknown %T */", n)
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

func writeLiteralExpr(sb io.Writer, e *ast.LiteralExpr) {
	switch e.Kind {
	case ast.LiteralInt:
		if e.Raw != "" {
			io.WriteString(sb, e.Raw)
		} else {
			fmt.Fprintf(sb, "%d", e.Value)
		}
	case ast.LiteralFloat:
		if e.Raw != "" {
			io.WriteString(sb, e.Raw)
		} else {
			fmt.Fprintf(sb, "%v", e.Value)
		}
	case ast.LiteralString:
		io.WriteString(sb, string('"'))
		io.WriteString(sb, escapeStringContent(fmt.Sprintf("%v", e.Value)))
		io.WriteString(sb, string('"'))
	case ast.LiteralBool:
		if e.Value.(bool) {
			io.WriteString(sb, "true")
		} else {
			io.WriteString(sb, "false")
		}
	case ast.LiteralNull:
		io.WriteString(sb, "null")
	case ast.LiteralColor:
		fmt.Fprintf(sb, "%v", e.Value)
	case ast.LiteralUnit:
		ul := e.Value.(ast.UnitLiteral)
		io.WriteString(sb, ul.Number)
		io.WriteString(sb, ul.Suffix)
	default:
		fmt.Fprintf(sb, "%v", e.Value)
	}
}

func writeLiteral(sb io.Writer, v any, typeHint string) {
	switch val := v.(type) {
	case ast.UnitLiteral:
		io.WriteString(sb, val.Number)
		io.WriteString(sb, val.Suffix)
	case string:
		if strings.HasPrefix(val, "#") && (typeHint == "color" || typeHint == "") {
			io.WriteString(sb, val)
		} else {
			io.WriteString(sb, string('"'))
			io.WriteString(sb, escapeStringContent(val))
			io.WriteString(sb, string('"'))
		}
	case int:
		fmt.Fprintf(sb, "%d", val)
	case float64:
		fmt.Fprintf(sb, "%v", val)
	case bool:
		if val {
			io.WriteString(sb, "true")
		} else {
			io.WriteString(sb, "false")
		}
	case nil:
		io.WriteString(sb, "null")
	default:
		fmt.Fprintf(sb, "%v", v)
	}
}

// formatPostfixOperand wraps numeric literals in parens to prevent
// ambiguity with dot access (e.g. 0.field would parse as float 0.).
func writeArgs(sb io.Writer, args []ast.Node) {
	for i, a := range args {
		if i > 0 {
			io.WriteString(sb, ", ")
		}
		writeNode(sb, a)
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
		case 0:
			sb.WriteString(`\0`)
		default:
			if r < 0x20 || r == 0x7f {
				sb.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else {
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
		for inner != "" && inner[0] != '~' && inner[0] != ',' {
			var param string
			param, inner = consumeEncodedType(inner)
			if param != "" {
				params = append(params, param)
			}
			if inner != "" && inner[0] == ',' {
				inner = inner[1:]
			}
		}
		result := "func:" + strings.Join(params, ",")
		if inner != "" && inner[0] == '~' {
			inner = inner[1:]
			var ret string
			ret, inner = consumeEncodedType(inner)
			result += "~" + ret
		}
		return result, inner
	}
	for i := range len(s) {
		if s[i] == ',' || s[i] == '~' {
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
		if body != "" && body[0] == ',' {
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
