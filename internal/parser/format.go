package parser

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Format writes an ast.Document as .sngl source text.
func Format(doc *ast.Document) string {
	f := &formatter{comments: doc.Comments}
	f.sb.Grow(estimateDocSize(doc))
	f.formatDocument(doc)
	if len(doc.Decls) == 0 {
		f.emitRemainingComments()
	}
	return alignInlineComments(f.sb.String())
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
	if doc.App != nil {
		n += 10
		for _, vn := range doc.App.Children {
			n += estimateVNSize(vn)
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

// FormatTo writes the formatted .sngl source for doc to w.
func FormatTo(doc *ast.Document, w io.Writer) error {
	_, err := io.WriteString(w, Format(doc))
	return err
}

// alignInlineComments finds runs of consecutive lines that all have inline
// comments (code followed by " //") and pads each line so the "//" starts
// at the same column.
func alignInlineComments(s string) string {
	// Fast path: no inline comments at all
	if !strings.Contains(s, " //") {
		return s
	}

	// Scan lines to find comment positions (byte offset of " //" in each line).
	// Only lines with code before the comment get an entry.
	type lineInfo struct {
		start, end int // byte range in s (excluding \n)
		commentOff int // offset of "//" within the line, or -1
		codeLen    int // length of trimmed code portion
	}

	// Count lines for pre-allocation
	n := strings.Count(s, "\n") + 1
	lines := make([]lineInfo, 0, n)
	pos := 0
	for pos <= len(s) {
		nl := strings.IndexByte(s[pos:], '\n')
		var end int
		if nl < 0 {
			end = len(s)
		} else {
			end = pos + nl
		}
		line := s[pos:end]
		ci := findInlineComment(line)
		codeLen := 0
		if ci >= 0 {
			// Trim trailing spaces from code portion
			code := line[:ci]
			codeLen = len(strings.TrimRight(code, " "))
		}
		lines = append(lines, lineInfo{start: pos, end: end, commentOff: ci, codeLen: codeLen})
		if nl < 0 {
			break
		}
		pos = end + 1
	}

	// Find runs of consecutive lines with comments and check if any need padding.
	needsRewrite := false
	for i := 0; i < len(lines); {
		li := lines[i]
		if li.commentOff < 0 || isCommentOnlyLine(s[li.start:li.end]) {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j].commentOff >= 0 {
			j++
		}
		if j-i >= 2 {
			maxCode := 0
			for k := i; k < j; k++ {
				if lines[k].codeLen > maxCode {
					maxCode = lines[k].codeLen
				}
			}
			for k := i; k < j; k++ {
				if lines[k].codeLen < maxCode && !isCommentOnlyLine(s[lines[k].start:lines[k].end]) {
					needsRewrite = true
					break
				}
			}
		}
		if needsRewrite {
			break
		}
		i = j
	}

	if !needsRewrite {
		return s
	}

	// Rewrite: only modify runs that need alignment
	var sb strings.Builder
	sb.Grow(len(s) + 64)
	written := 0

	for i := 0; i < len(lines); {
		li := lines[i]
		if li.commentOff < 0 || isCommentOnlyLine(s[li.start:li.end]) {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j].commentOff >= 0 {
			j++
		}
		if j-i >= 2 {
			maxCode := 0
			for k := i; k < j; k++ {
				if lines[k].codeLen > maxCode {
					maxCode = lines[k].codeLen
				}
			}
			// Write everything before this run
			sb.WriteString(s[written:lines[i].start])
			for k := i; k < j; k++ {
				lk := lines[k]
				line := s[lk.start:lk.end]
				if isCommentOnlyLine(line) {
					sb.WriteString(line)
				} else {
					sb.WriteString(s[lk.start : lk.start+lk.codeLen])
					pad := maxCode - lk.codeLen
					for p := 0; p < pad+1; p++ {
						sb.WriteByte(' ')
					}
					sb.WriteString(s[lk.start+lk.commentOff : lk.end])
				}
				if lk.end < len(s) {
					sb.WriteByte('\n')
				}
			}
			written = lines[j-1].end
			if lines[j-1].end < len(s) {
				written++ // skip \n
			}
		}
		i = j
	}
	sb.WriteString(s[written:])
	return sb.String()
}

func isCommentOnlyLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "//")
}

// findInlineComment returns the index of " //" in a line, respecting strings.
// Returns -1 if no inline comment found.
func findInlineComment(line string) int {
	// If the line is entirely a comment, there's no "inline" comment to find
	if strings.HasPrefix(strings.TrimSpace(line), "//") {
		return -1
	}
	inStr := false
	inBlock := false
	for i := 0; i < len(line); i++ {
		if inBlock {
			if line[i] == '*' && i+1 < len(line) && line[i+1] == '/' {
				inBlock = false
				i++ // skip '/'
			}
			continue
		}
		switch line[i] {
		case '"':
			if !inStr {
				inStr = true
			} else if i > 0 && line[i-1] != '\\' {
				inStr = false
			}
		case '/':
			if !inStr && i+1 < len(line) {
				if line[i+1] == '/' && i > 0 && line[i-1] == ' ' {
					return i
				}
				if line[i+1] == '*' {
					inBlock = true
					i++ // skip '*'
				}
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
		if c.Inline && c.Pos.Line == line && !f.emittedInline[[2]int{c.Pos.Line, c.Pos.Column}] {
			s := f.sb.String()
			if len(s) > 0 && s[len(s)-1] == '\n' {
				f.sb.Reset()
				f.sb.WriteString(s[:len(s)-1])
			}
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
	commentI             int             // next comment index to emit
	pendingInlineComment *ast.Comment    // inline comment to emit on the visual node's line
	emittedInline        map[[2]int]bool // tracks inline comments emitted by emitInlineComment (key: [line, col])
	propBuf              strings.Builder // reusable buffer for building prop strings
}

func (f *formatter) write(s string) { f.sb.WriteString(s) }

func (f *formatter) newline() {
	f.sb.WriteByte('\n')
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
	f.sb.WriteString(indentStr(f.indent))
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
				if f.emittedInline[[2]int{decl.Pos.Line, decl.Pos.Column}] {
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
			// Skip subsequent Output decls since formatOutputGroup handles all
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
				// Format using the component's own Decls if available
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
		}
	}

	// If we haven't seen a main component in Decls but have main content
	// that wasn't already emitted (loose data/funcs handled above), wrap in
	// component main. Only needed when App has visual children.
	hasMainInDecls := false
	for _, d := range doc.Decls {
		if comp, ok := d.(*ast.Component); ok && comp.Name == "main" {
			hasMainInDecls = true
			break
		}
	}
	if !hasMainInDecls && doc.App != nil && len(doc.App.Children) > 0 {
		// Only emit component main wrapper if there are visual children
		// that need wrapping. Loose data/const/func were already emitted above.
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
			f.writeIndent()
			f.write("/- ")
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
			f.writeIndent()
			f.write("/- ")
		}
		f.formatComponent(comp)
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
			writeNode(&f.sb, stmt)
			f.newline()
		}
		if fn.Block.Return != nil {
			f.writeIndent()
			f.write("return ")
			writeNode(&f.sb, fn.Block.Return)
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
			writeNode(&f.sb, stmt)
			f.newline()
		}
	} else {
		f.writeIndent()
		writeNode(&f.sb, t.Body)
		f.newline()
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


func (f *formatter) formatVisualNodeWithComment(vn *ast.VisualNode, inlineComment *ast.Comment) {
	f.pendingInlineComment = inlineComment
	f.formatVisualNode(vn)
	f.pendingInlineComment = nil
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

func (f *formatter) writeExprTo(sb *strings.Builder, expr ast.Expr) {
	if expr.SNGL != nil {
		writeNode(sb, expr.SNGL)
		return
	}
	if expr.Literal != nil {
		writeLiteral(sb, expr.Literal, expr.TypeHint)
		return
	}
	sb.WriteString("null")
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

	// Write component name and ID directly to f.sb
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
				f.emitPendingInlineComment()
				f.emitInlineComment(vn.Pos.Line)
				f.writeLine("}")
			} else {
				f.write(" { }")
				f.newline()
				f.emitPendingInlineComment()
			}
		} else {
			f.write(" {")
			f.newline()
			f.emitPendingInlineComment()
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
		f.emitPendingInlineComment()
	}
}

func (f *formatter) writeEventValue(sb *strings.Builder, expr ast.Expr) {
	if expr.SNGL != nil {
		if block, ok := expr.SNGL.(*ast.StmtBlock); ok && len(block.Stmts) > 1 {
			sb.WriteByte('{')
			for _, stmt := range block.Stmts {
				sb.WriteByte('\n')
				sb.WriteString(indentStr(f.indent + 1))
				writeNode(sb, stmt)
			}
			sb.WriteByte('\n')
			sb.WriteString(indentStr(f.indent))
			sb.WriteByte('}')
			return
		}
		sb.WriteString("{ ")
		writeNode(sb, expr.SNGL)
		sb.WriteString(" }")
		return
	}
	sb.WriteString("{ null }")
}

// writeExprValue writes an Expr directly to f.sb, avoiding intermediate strings.
func (f *formatter) writeExprValue(expr ast.Expr) {
	if expr.SNGL != nil {
		writeNode(&f.sb, expr.SNGL)
		return
	}
	if expr.Literal != nil {
		writeLiteral(&f.sb, expr.Literal, expr.TypeHint)
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

func writeNode(sb *strings.Builder, n ast.Node) {
	if n == nil {
		sb.WriteString("null")
		return
	}
	switch e := n.(type) {
	case *ast.LiteralExpr:
		writeLiteralExpr(sb, e)
	case *ast.IdentExpr:
		sb.WriteString(e.Name)
	case *ast.ElementRefExpr:
		sb.WriteByte('#')
		sb.WriteString(e.Name)
	case *ast.LambdaExpr:
		if e.Block != nil {
			sb.WriteString("func(")
			for i, name := range e.Params {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(name)
				if i < len(e.ParamTypes) && e.ParamTypes[i] != "" {
					sb.WriteByte(' ')
					sb.WriteString(e.ParamTypes[i])
				}
			}
			sb.WriteString(") {\n")
			for _, stmt := range e.Block.Stmts {
				sb.WriteString("    ")
				writeNode(sb, stmt)
				sb.WriteByte('\n')
			}
			if e.Block.Return != nil {
				sb.WriteString("    return ")
				writeNode(sb, e.Block.Return)
				sb.WriteByte('\n')
			}
			sb.WriteByte('}')
		} else {
			sb.WriteByte('(')
			for i, name := range e.Params {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(name)
				if i < len(e.ParamTypes) && e.ParamTypes[i] != "" {
					sb.WriteByte(' ')
					sb.WriteString(e.ParamTypes[i])
				}
			}
			sb.WriteString(") => ")
			writeNode(sb, e.Body)
		}
	case *ast.ParenExpr:
		sb.WriteByte('(')
		writeNode(sb, e.Inner)
		sb.WriteByte(')')
	case *ast.BinaryExpr:
		writeNode(sb, e.Left)
		sb.WriteByte(' ')
		sb.WriteString(binOpString(e.Op))
		sb.WriteByte(' ')
		writeNode(sb, e.Right)
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			sb.WriteByte('!')
			// Avoid !! ambiguity: if operand starts with !, add space
			if u, ok := e.Operand.(*ast.UnaryExpr); ok && u.Op == ast.UnaryNot {
				sb.WriteByte(' ')
			}
			writeNode(sb, e.Operand)
		} else {
			sb.WriteByte('-')
			writeNode(sb, e.Operand)
		}
	case *ast.TernaryExpr:
		writeNode(sb, e.Cond)
		sb.WriteString(" ? ")
		writeNode(sb, e.Then)
		sb.WriteString(" : ")
		writeNode(sb, e.Else)
	case *ast.SelectExpr:
		writeNode(sb, e.Operand)
		sb.WriteByte('.')
		sb.WriteString(e.Field)
	case *ast.IndexExpr:
		writeNode(sb, e.Operand)
		sb.WriteByte('[')
		writeNode(sb, e.Index)
		sb.WriteByte(']')
	case *ast.CallExpr:
		sb.WriteString(e.Func)
		sb.WriteByte('(')
		writeArgs(sb, e.Args)
		sb.WriteByte(')')
	case *ast.MethodExpr:
		writeNode(sb, e.Receiver)
		sb.WriteByte('.')
		sb.WriteString(e.Method)
		sb.WriteByte('(')
		writeArgs(sb, e.Args)
		sb.WriteByte(')')
	case *ast.StructExpr:
		sep := ": "
		if e.Name == "" {
			sep = "="
		}
		sb.WriteString(e.Name)
		sb.WriteByte('{')
		if e.Multiline {
			sb.WriteByte('\n')
			for _, field := range e.Fields {
				if field.Spread {
					sb.WriteString("...")
					writeNode(sb, field.Value)
				} else {
					sb.WriteString(field.Name)
					sb.WriteString(sep)
					writeNode(sb, field.Value)
				}
				sb.WriteString(",\n")
			}
		} else {
			for i, field := range e.Fields {
				if i > 0 {
					sb.WriteString(", ")
				}
				if field.Spread {
					sb.WriteString("...")
					writeNode(sb, field.Value)
				} else {
					sb.WriteString(field.Name)
					sb.WriteString(sep)
					writeNode(sb, field.Value)
				}
			}
		}
		sb.WriteByte('}')
	case *ast.ListExpr:
		sb.WriteByte('[')
		for i, el := range e.Elements {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeNode(sb, el)
		}
		sb.WriteByte(']')
	case *ast.SpreadExpr:
		sb.WriteString("...")
		writeNode(sb, e.Operand)
	case *ast.InterpolationExpr:
		sb.WriteByte('"')
		for _, p := range e.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				sb.WriteString(escapeStringContent(fmt.Sprintf("%v", lit.Value)))
			} else {
				sb.WriteByte('{')
				writeNode(sb, p)
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('"')
	case *ast.AssignStmt:
		writeNode(sb, e.Target)
		sb.WriteByte(' ')
		sb.WriteString(assignOpString(e.Op))
		sb.WriteByte(' ')
		writeNode(sb, e.Value)
	case *ast.ToggleStmt:
		writeNode(sb, e.Target)
		sb.WriteString("!!")
	case *ast.EmitStmt:
		sb.WriteByte('@')
		sb.WriteString(e.Name)
		sb.WriteByte('(')
		writeArgs(sb, e.Args)
		sb.WriteByte(')')
	case *ast.StmtBlock:
		for i, s := range e.Stmts {
			if i > 0 {
				sb.WriteString("; ")
			}
			writeNode(sb, s)
		}
	case *ast.VarStmt:
		sb.WriteString("var ")
		sb.WriteString(e.Name)
		if e.Type != "" {
			sb.WriteByte(' ')
			sb.WriteString(typeHintStr(e.Type, nil))
		}
		sb.WriteString(" = ")
		writeNode(sb, e.Init)
	case *ast.ReturnStmt:
		sb.WriteString("return")
		if e.Value != nil {
			sb.WriteByte(' ')
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

func writeLiteralExpr(sb *strings.Builder, e *ast.LiteralExpr) {
	switch e.Kind {
	case ast.LiteralInt:
		if e.Raw != "" {
			sb.WriteString(e.Raw)
		} else {
			fmt.Fprintf(sb, "%d", e.Value)
		}
	case ast.LiteralFloat:
		if e.Raw != "" {
			sb.WriteString(e.Raw)
		} else {
			fmt.Fprintf(sb, "%v", e.Value)
		}
	case ast.LiteralString:
		sb.WriteByte('"')
		sb.WriteString(escapeStringContent(fmt.Sprintf("%v", e.Value)))
		sb.WriteByte('"')
	case ast.LiteralBool:
		if e.Value.(bool) {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case ast.LiteralNull:
		sb.WriteString("null")
	case ast.LiteralColor:
		fmt.Fprintf(sb, "%v", e.Value)
	case ast.LiteralUnit:
		ul := e.Value.(ast.UnitLiteral)
		sb.WriteString(ul.Number)
		sb.WriteString(ul.Suffix)
	default:
		fmt.Fprintf(sb, "%v", e.Value)
	}
}

func writeLiteral(sb *strings.Builder, v any, typeHint string) {
	switch val := v.(type) {
	case ast.UnitLiteral:
		sb.WriteString(val.Number)
		sb.WriteString(val.Suffix)
	case string:
		if strings.HasPrefix(val, "#") && (typeHint == "color" || typeHint == "") {
			sb.WriteString(val)
		} else {
			sb.WriteByte('"')
			sb.WriteString(escapeStringContent(val))
			sb.WriteByte('"')
		}
	case int:
		fmt.Fprintf(sb, "%d", val)
	case float64:
		fmt.Fprintf(sb, "%v", val)
	case bool:
		if val {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case nil:
		sb.WriteString("null")
	default:
		fmt.Fprintf(sb, "%v", v)
	}
}

// formatPostfixOperand wraps numeric literals in parens to prevent
// ambiguity with dot access (e.g. 0.field would parse as float 0.).
func writeArgs(sb *strings.Builder, args []ast.Node) {
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
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
				// Escape control characters as \xHH
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
