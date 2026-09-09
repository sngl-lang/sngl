package lspcore

import (
	"regexp"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// Complete returns completion items for the given position (1-based line and col).
func Complete(content string, doc *ast.Document, line, col int) []CompletionItem {
	// Check for namespace dot completion first (e.g., "html.|")
	if items := NamespaceCompletions(content, doc, line, col); items != nil {
		return items
	}

	ctx := CompletionContext(content, line, col)

	switch ctx {
	case CtxTopLevel:
		return TopLevelKeywords()
	case CtxComponent:
		return ComponentKeywords(content, doc)
	case CtxVisualNode:
		return ComponentNameCompletions(content, doc)
	case CtxPropList:
		return PropListCompletions(content, doc, line, col)
	case CtxStyleProp:
		return StylePropCompletions()
	case CtxEventHandler:
		return EventCompletions(content, doc, line)
	case CtxOutputOpts:
		return OutputOptsCompletions(content, line)
	case CtxOutputTarget:
		return OutputTargetCompletions(content, line)
	case CtxImportPath:
		return ImportPathCompletions(content, line)
	default:
		return ExpressionCompletions(doc)
	}
}

type CompletionCtx int

const (
	CtxTopLevel CompletionCtx = iota
	CtxComponent
	CtxVisualNode
	CtxPropList // inside component(|)
	CtxStyleProp
	CtxEventHandler
	CtxOutputOpts
	CtxOutputTarget
	CtxImportPath // inside the quotes of an import path
)

var outputShadowed = regexp.MustCompile(`(?m)^[ \t]*component[ \t]+output\b`)

func CompletionContext(content string, line, col int) CompletionCtx {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return CtxTopLevel
	}
	l := strings.TrimSpace(lines[line-1])

	// An import path, like an output line, is top-level and so is decided
	// before the brace-depth check. The cursor has to be inside the quotes:
	// an odd number of them before it means one is still open.
	if strings.HasPrefix(l, "import ") || l == "import" {
		raw := lines[line-1]
		prefix := raw[:min(col-1, len(raw))]
		if strings.Count(prefix, "\"")%2 == 1 {
			return CtxImportPath
		}
		return CtxTopLevel
	}

	// Output line detection — must be before brace depth check since outputs are top-level
	if (strings.HasPrefix(l, "output ") || l == "output") && !outputShadowed.MatchString(content) {
		// Check if cursor is inside parens
		raw := lines[line-1]
		prefix := raw[:min(col-1, len(raw))]
		if strings.Contains(prefix, "(") && !strings.Contains(prefix, ")") {
			return CtxOutputOpts
		}
		return CtxOutputTarget
	}

	if strings.HasPrefix(l, "@") {
		return CtxEventHandler
	}

	// Check if cursor is inside component prop parens: component_name(|)
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]
	if strings.Contains(l, "style={") || strings.Contains(prefix, "style=") {
		return CtxStyleProp
	}
	// Prop list: cursor is after an opening ( with no closing ) before it,
	// and NOT on an output line
	if strings.Contains(prefix, "(") && !strings.Contains(prefix, ")") {
		return CtxPropList
	}

	braceDepth := 0
	for i := 0; i < line-1 && i < len(lines); i++ {
		braceDepth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
	}

	if braceDepth == 0 {
		return CtxTopLevel
	}
	if braceDepth == 1 {
		return CtxComponent
	}
	return CtxVisualNode
}

func TopLevelKeywords() []CompletionItem {
	kws := []string{"import", "struct", "enum", "unit", "const", "style", "styles", "component"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	return append(items, scopeOf("", nil).components()...)
}

// ComponentKeywords is what can start a statement in a component body: the
// keywords, and the components a node could name.
func ComponentKeywords(content string, doc *ast.Document) []CompletionItem {
	kws := []string{"var", "const", "if", "for"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	return append(items, ComponentNameCompletions(content, doc)...)
}

// ComponentNameCompletions is every component the document can name: the ones
// it declares, and the ones its imports bring into scope. A component from a
// package it has not imported is not offered, because writing it would not
// compile.
func ComponentNameCompletions(content string, doc *ast.Document) []CompletionItem {
	items := scopeOf(content, doc).components()
	if doc != nil {
		for _, s := range doc.Stmts {
			if c, ok := s.(*ast.ComponentDecl); ok {
				items = append(items, CompletionItem{
					Label:            c.Name,
					Kind:             CIKClass,
					Detail:           "user component",
					Documentation:    docForPos(doc, c.Pos),
					InsertText:       c.Name + "($1)",
					InsertTextFormat: ITFSnippet,
				})
			}
		}
	}
	return items
}

func ExpressionCompletions(doc *ast.Document) []CompletionItem {
	var items []CompletionItem
	for _, kw := range []string{"true", "false", "null"} {
		items = append(items, CompletionItem{Label: kw, Kind: CIKKeyword})
	}
	if doc != nil {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.VarDecl:
				for _, spec := range s.Specs {
					hint := typeExprString(spec.Type)
					for _, name := range spec.Names {
						items = append(items, CompletionItem{Label: name, Kind: CIKVariable, Detail: hint})
					}
				}
			case *ast.FuncDef:
				if !s.Block.IsDefined() && len(s.Params.Params) == 0 {
					items = append(items, CompletionItem{Label: s.Name, Kind: CIKVariable, Detail: "func"})
				}
			case *ast.ConstDecl:
				for _, spec := range s.Specs {
					for _, name := range spec.Names {
						items = append(items, CompletionItem{Label: name, Kind: CIKConstant, Detail: "const"})
					}
				}
			case *ast.StructDef:
				items = append(items, CompletionItem{Label: s.Name, Kind: CIKStruct})
			case *ast.EnumDef:
				items = append(items, CompletionItem{Label: s.Name, Kind: CIKEnum})
			}
		}
	}
	return items
}

// EventCompletions offers the events of the node whose block the cursor is in,
// which is the only component whose handlers can be written there.
func EventCompletions(content string, doc *ast.Document, line int) []CompletionItem {
	name := enclosingNodeName(content, line)
	if name == "" {
		return nil
	}
	var items []CompletionItem
	if decl := userComponent(doc, name); decl != nil {
		for _, p := range decl.Props.Props {
			if e, ok := p.(ast.EventDecl); ok {
				items = append(items, eventItem(e.Name, typeExprString(e.Type)))
			}
		}
		return items
	}
	schema := scopeOf(content, doc).componentSchema(name)
	if schema == nil {
		return nil
	}
	names := make([]string, 0, len(schema.Events))
	for n := range schema.Events {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		items = append(items, eventItem(n, schema.Events[n]))
	}
	return items
}

func eventItem(name, payload string) CompletionItem {
	return CompletionItem{
		Label:            "@" + name,
		Kind:             CIKEvent,
		Detail:           payload,
		InsertText:       "@" + name + "={ $1 }",
		InsertTextFormat: ITFSnippet,
	}
}

// StylePropCompletions offers the fields of the Style struct, which is what
// every `style=` prop takes.
func StylePropCompletions() []CompletionItem {
	var items []CompletionItem
	for _, f := range styleFields() {
		detail := ""
		if f.Type != nil {
			detail = f.Type.String()
		}
		items = append(items, CompletionItem{
			Label:            f.Name,
			Kind:             CIKField,
			Detail:           detail,
			InsertText:       f.Name + "=$1",
			InsertTextFormat: ITFSnippet,
		})
	}
	return items
}

// enclosingNodeName is the component named by the innermost block the cursor
// sits in: the line that opened it, at the depth the cursor is at.
func enclosingNodeName(content string, line int) string {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	depth := 0
	for i := line - 2; i >= 0; i-- {
		l := lines[i]
		depth += strings.Count(l, "}") - strings.Count(l, "{")
		if depth < 0 {
			return nodeNameOnLine(l)
		}
	}
	return ""
}

// nodeNameOnLine reads the component a node line names, dropping an element
// ref and any prop list: `button #ok(text="hi") {` is a button.
func nodeNameOnLine(l string) string {
	l = strings.TrimSpace(l)
	l = strings.TrimSuffix(l, "{")
	if i := strings.IndexAny(l, "(#"); i >= 0 {
		l = l[:i]
	}
	l = strings.TrimSpace(l)
	if i := strings.LastIndexAny(l, " \t"); i >= 0 {
		l = l[i+1:]
	}
	if l == "" || !isIdentStart(l[0]) {
		return ""
	}
	return l
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func offers(items []CompletionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

func userComponent(doc *ast.Document, name string) *ast.ComponentDecl {
	if doc == nil {
		return nil
	}
	for _, stmt := range doc.Stmts {
		if c, ok := stmt.(*ast.ComponentDecl); ok && c.Name == name {
			return c
		}
	}
	return nil
}

// PropListCompletions returns props and events for the component whose
// prop list the cursor is inside.
func PropListCompletions(content string, doc *ast.Document, line, col int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]

	// Extract component name: find the identifier before the opening paren
	parenIdx := strings.LastIndex(prefix, "(")
	if parenIdx < 0 {
		return nil
	}
	before := strings.TrimRight(prefix[:parenIdx], " \t")
	// Component name is the last word (handle element refs like #id)
	compName := before
	if sp := strings.LastIndexAny(before, " \t{"); sp >= 0 {
		compName = before[sp+1:]
	}
	// Strip element ref prefix: "input #foo" -> take text before #
	if hi := strings.LastIndex(compName, "#"); hi > 0 {
		compName = strings.TrimSpace(compName[:hi])
	} else if strings.HasPrefix(compName, "#") {
		// Just a ref, look further back
		if sp := strings.LastIndexAny(before[:len(before)-len(compName)], " \t{"); sp >= 0 {
			compName = strings.TrimSpace(before[sp+1 : len(before)-len(compName)])
		}
	}
	if compName == "" {
		return nil
	}

	// A component the document declares itself, whose props are in the AST.
	var props []ast.Param
	var events []ast.EventDecl
	if c := userComponent(doc, compName); c != nil {
		for _, p := range c.Props.Props {
			switch pd := p.(type) {
			case ast.Param:
				props = append(props, pd)
			case ast.EventDecl:
				events = append(events, pd)
			}
		}
	}
	// Otherwise one an import brought in, whose prop types are resolved rather
	// than written: the schema is where those live.
	var schema *checker.ComponentSchema
	if props == nil && events == nil {
		schema = scopeOf(content, doc).componentSchema(compName)
	}

	// Collect already-used prop names on this line to exclude them
	used := map[string]bool{}
	insideParens := prefix[parenIdx+1:]
	for part := range strings.SplitSeq(insideParens, ",") {
		part = strings.TrimSpace(part)
		if eq := strings.Index(part, "="); eq > 0 {
			name := strings.TrimSpace(part[:eq])
			name = strings.TrimPrefix(name, "@")
			name = strings.TrimPrefix(name, ":")
			used[name] = true
		}
	}

	var items []CompletionItem

	// Props
	for _, p := range props {
		if used[p.Name] {
			continue
		}
		// A wildcard prop is never offered. It is a map of the names its
		// pattern covers, and binding it under its own name is legal only when
		// no covered name is written on the same call — which the checker
		// rejects as a conflict, so offering the name here would suggest a
		// spelling that may not compile. The names it does stand for are open
		// by construction and cannot be enumerated.
		if isWildcardProp(p) {
			continue
		}
		detail := typeExprString(p.Type)
		items = append(items, CompletionItem{
			Label:            p.Name,
			Kind:             CIKProperty,
			Detail:           detail,
			InsertText:       p.Name + "=$1",
			InsertTextFormat: ITFSnippet,
		})
	}

	// Events
	for _, e := range events {
		if used[e.Name] {
			continue
		}
		items = append(items, CompletionItem{
			Label:            "@" + e.Name,
			Kind:             CIKEvent,
			Detail:           typeExprString(e.Type),
			InsertText:       "@" + e.Name + "={ $1 }",
			InsertTextFormat: ITFSnippet,
		})
	}

	if schema != nil {
		names := make([]string, 0, len(schema.Props))
		for n := range schema.Props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if used[n] {
				continue
			}
			ps := schema.Props[n]
			items = append(items, CompletionItem{
				Label:            n,
				Kind:             CIKProperty,
				Detail:           (&ps.Type).String(),
				Documentation:    FirstLine(ps.Doc),
				InsertText:       n + "=$1",
				InsertTextFormat: ITFSnippet,
			})
		}
		enames := make([]string, 0, len(schema.Events))
		for n := range schema.Events {
			enames = append(enames, n)
		}
		sort.Strings(enames)
		for _, n := range enames {
			if used[n] {
				continue
			}
			items = append(items, eventItem(n, schema.Events[n]))
		}
	}

	// Every library component declares `style`, so the schema has already
	// offered it with its real type. This is for a component that does not.
	if !used["style"] && !offers(items, "style") {
		items = append(items, CompletionItem{
			Label:            "style",
			Kind:             CIKProperty,
			Detail:           "inline styles",
			InsertText:       "style={$1}",
			InsertTextFormat: ITFSnippet,
		})
	}

	return items
}

// OutputOptsCompletions returns completion items for output option keys
// inside the parenthesized options of an output declaration. The candidate set
// is the props of the three nodes that contribute options: `output` itself, the
// language node, and the platform node.
func OutputOptsCompletions(content string, line int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	l := strings.TrimSpace(lines[line-1])

	platformName := extractOutputPlatform(l)
	langName := extractOutputLang(l)

	// Build options are directive surface, not declarations the file imports:
	// an output block checks with no import at all, so these are read from the
	// packages that declare the target nodes rather than from whatever the
	// file has in scope. A node's props are its schema, and props are on the
	// loaded IR, so each package is loaded rather than only parsed.
	nodes := []*ir.Component{checker.OutputNode()}
	if langName != "" {
		nodes = append(nodes, checker.TargetNode("language/"+langName))
	}
	if platformName != "" {
		nodes = append(nodes, checker.TargetNode("platform/"+platformName))
	}

	seen := map[string]bool{}
	var items []CompletionItem
	for _, comp := range nodes {
		if comp == nil {
			continue
		}
		for _, prop := range comp.Props {
			if seen[prop.Name] {
				continue
			}
			seen[prop.Name] = true
			items = append(items, CompletionItem{
				Label:      prop.Name,
				Kind:       CIKProperty,
				Detail:     prop.Type.String(),
				InsertText: prop.Name + "=",
			})
		}
	}
	return items
}

// extractOutputLang returns the language identifier from an output line,
// e.g., "output js html(...)" -> "js". Returns "" if not present.
func extractOutputLang(line string) string {
	words := strings.Fields(line)
	if len(words) < 2 {
		return ""
	}
	lang := words[1]
	// Trim trailing parens or operators
	if idx := strings.IndexAny(lang, "({"); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

// OutputTargetCompletions returns completion items for lang/platform names
// on an output declaration line.
func OutputTargetCompletions(content string, line int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	l := strings.TrimSpace(lines[line-1])

	// Count words after "output" to determine position
	words := strings.Fields(l)
	// words[0] = "output", words[1] = lang (if present), words[2] = platform (if present)
	switch len(words) {
	case 1: // "output" — complete with lang names
		var items []CompletionItem
		for _, name := range codegen.Langs() {
			items = append(items, CompletionItem{Label: name, Kind: CIKKeyword, Detail: "language"})
		}
		return items
	case 2: // "output js" — complete with platforms supporting this lang
		lang := words[1]
		var items []CompletionItem
		for _, name := range codegen.PlatformsForLang(lang) {
			items = append(items, CompletionItem{Label: name, Kind: CIKKeyword, Detail: "platform"})
		}
		return items
	default:
		return nil
	}
}

// extractOutputPlatform extracts the platform name from an output line.
// e.g., "output js html(package=...)" -> "html"
func extractOutputPlatform(line string) string {
	words := strings.Fields(line)
	if len(words) < 3 {
		return ""
	}
	// words[2] may have parens: "html(" or "html(package="
	plat := words[2]
	if idx := strings.IndexByte(plat, '('); idx >= 0 {
		plat = plat[:idx]
	}
	return plat
}

// NamespaceCompletions returns completions for "namespace." (e.g., "html.").
// Returns nil if the cursor is not after a namespace dot.
func NamespaceCompletions(content string, doc *ast.Document, line, col int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]

	// Find "word." pattern at end of prefix
	dotIdx := strings.LastIndex(prefix, ".")
	if dotIdx < 0 {
		return nil
	}
	// Extract namespace name (word before the dot)
	before := prefix[:dotIdx]
	nsStart := len(before)
	for nsStart > 0 && isIdentChar(before[nsStart-1]) {
		nsStart--
	}
	nsName := before[nsStart:]
	if nsName == "" {
		return nil
	}

	// Check if this is a registered platform or language
	var pkgDoc *ast.Document
	if docs := codegen.PlatformDocs(codegen.LookupPlatform(nsName)); len(docs) > 0 {
		pkgDoc = docs[0]
	}
	if pkgDoc == nil {
		if docs := codegen.LangDocs(codegen.LookupLang(nsName)); len(docs) > 0 {
			pkgDoc = docs[0]
		}
	}

	// A library package the document imported under this alias. Its source is
	// what the platform and language branches above read too, so the members
	// render the same way.
	var pkgDocs []*ast.Document
	if pkgDoc != nil {
		pkgDocs = []*ast.Document{pkgDoc}
	} else if uri, ok := scopeOf(content, doc).alias[nsName]; ok {
		pkgDocs = checker.PackageSource(uri)
	}

	// `this.<member>` inside a method body: enumerate the receiver type's
	// fields, members, and sibling methods.
	if len(pkgDocs) == 0 && nsName == ir.ReceiverParam {
		if items := thisCompletions(doc, line); items != nil {
			return items
		}
	}

	if len(pkgDocs) == 0 {
		return nil
	}

	var items []CompletionItem

	for _, stmt := range pkgStmts(pkgDocs) {
		switch s := stmt.(type) {
		case *ast.ComponentDecl:
			if strings.Contains(s.Name, ".") {
				continue // skip sngl.X overrides
			}
			items = append(items, CompletionItem{
				Label:            s.Name,
				Kind:             CIKClass,
				Detail:           nsName + " component",
				InsertText:       s.Name + "($1)",
				InsertTextFormat: ITFSnippet,
			})
		case *ast.StructDef:
			items = append(items, CompletionItem{
				Label:  s.Name,
				Kind:   CIKStruct,
				Detail: nsName + " struct",
			})
		case *ast.EnumDef:
			items = append(items, CompletionItem{
				Label:  s.Name,
				Kind:   CIKEnum,
				Detail: nsName + " enum",
			})
		}
	}

	return items
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || (ch >= '0' && ch <= '9')
}

// thisCompletions finds the struct/enum body that lexically encloses the
// given line and returns field/member/sibling-method completion items.
// Returns nil if the cursor isn't inside a nested method on a user-defined
// struct or enum.
func thisCompletions(doc *ast.Document, line int) []CompletionItem {
	if doc == nil {
		return nil
	}
	for _, stmt := range doc.Stmts {
		switch d := stmt.(type) {
		case *ast.StructDef:
			if !lineInRange(line, d.Pos.Line, lastBodyLine(d.Body, d.Pos.Line)) {
				continue
			}
			return structThisItems(d)
		case *ast.EnumDef:
			if !lineInRange(line, d.Pos.Line, lastBodyLine(d.Body, d.Pos.Line)) {
				continue
			}
			return enumThisItems(d)
		}
	}
	return nil
}

func lineInRange(line, start, end int) bool {
	return line >= start && line <= end
}

// lastBodyLine returns the last source line covered by any item in a
// sealed-interface body slice (struct or enum). Used as a coarse bound for
// "is the cursor inside this type's body".
func lastBodyLine[T any](body []T, fallback int) int {
	last := fallback
	for _, it := range body {
		switch x := any(it).(type) {
		case *ast.StructField:
			if x.Pos.Line > last {
				last = x.Pos.Line
			}
		case *ast.EnumMember:
			if x.Pos.Line > last {
				last = x.Pos.Line
			}
		case *ast.FuncDef:
			if x.Pos.Line > last {
				last = x.Pos.Line
			}
			if x.Block.IsDefined() {
				for _, s := range x.Block.Stmts {
					if p := s.StmtPos(); p != nil && p.Line > last {
						last = p.Line
					}
				}
			}
		}
	}
	// Add slack for closing brace.
	return last + 1
}

func structThisItems(d *ast.StructDef) []CompletionItem {
	var items []CompletionItem
	for _, f := range d.Fields() {
		for _, name := range f.Names {
			items = append(items, CompletionItem{
				Label:  name,
				Kind:   CIKField,
				Detail: "field on " + d.Name,
			})
		}
	}
	for _, fn := range d.Funcs() {
		items = append(items, CompletionItem{
			Label:            fn.Name,
			Kind:             CIKMethod,
			Detail:           "method on " + d.Name,
			InsertText:       fn.Name + "($1)",
			InsertTextFormat: ITFSnippet,
		})
	}
	return items
}

func enumThisItems(d *ast.EnumDef) []CompletionItem {
	var items []CompletionItem
	for _, fn := range d.Funcs() {
		items = append(items, CompletionItem{
			Label:            fn.Name,
			Kind:             CIKMethod,
			Detail:           "method on " + d.Name,
			InsertText:       fn.Name + "($1)",
			InsertTextFormat: ITFSnippet,
		})
	}
	return items
}

// isWildcardProp reports whether a prop carries #[wildcard(...)].
//
// Matched by the mark's own name, which is all this can do and all it needs:
// completion runs on one parsed document with no checker, so nothing here
// resolves the macro to sngl:macro, applies it, or knows the pattern it
// compiled to. The name survives aliasing (`import p "sngl:macro"` makes
// it `#[p.wildcard]`, same Name), and the cost of a false positive — a user
// macro of that name on an ordinary prop — is one withheld suggestion rather
// than a wrong one.
func isWildcardProp(p ast.Param) bool {
	for _, a := range p.MacroAttrs() {
		if a.Name == "wildcard" {
			return true
		}
	}
	return false
}

// ImportPathCompletions offers the library packages an import may name.
//
// The compiler's own tier is left out: `sngl:internal/<name>` resolves only
// from library source, so offering it to a program is offering an import that
// cannot compile. That is also why this reads PublicPackages rather than
// Packages -- the same list `sngl doc` indexes, and the same rule.
//
// A target's package is not in lib/ at all, so the registered platforms and
// languages contribute theirs by name.
func ImportPathCompletions(content string, line int) []CompletionItem {
	var items []CompletionItem
	for _, p := range lib.PublicPackages() {
		items = append(items, CompletionItem{
			Label:  "sngl:" + p,
			Kind:   CIKModule,
			Detail: "library package",
		})
	}
	for _, name := range codegen.Platforms() {
		items = append(items, CompletionItem{
			Label:  "sngl:platform/" + name,
			Kind:   CIKModule,
			Detail: "platform package",
		})
	}
	for _, name := range codegen.Langs() {
		items = append(items, CompletionItem{
			Label:  "sngl:language/" + name,
			Kind:   CIKModule,
			Detail: "language package",
		})
	}
	return items
}
