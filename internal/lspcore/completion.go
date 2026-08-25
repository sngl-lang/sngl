package lspcore

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
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
		return ComponentKeywords()
	case CtxVisualNode:
		return ComponentNameCompletions(doc)
	case CtxPropList:
		return PropListCompletions(content, doc, line, col)
	case CtxStyleProp:
		return StylePropCompletions()
	case CtxEventHandler:
		return EventCompletions()
	case CtxOutputOpts:
		return OutputOptsCompletions(content, line)
	case CtxOutputTarget:
		return OutputTargetCompletions(content, line)
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
)

func CompletionContext(content string, line, col int) CompletionCtx {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return CtxTopLevel
	}
	l := strings.TrimSpace(lines[line-1])

	// Output line detection — must be before brace depth check since outputs are top-level
	if strings.HasPrefix(l, "output ") || l == "output" {
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
	kws := []string{"import", "output", "struct", "enum", "unit", "const", "style", "styles", "component"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	return items
}

func ComponentKeywords() []CompletionItem {
	kws := []string{"var", "const", "if", "for"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	// TODO: add stdlib component items (LoadStdlib removed in v2)
	return items
}

func ComponentNameCompletions(doc *ast.Document) []CompletionItem {
	// TODO: add stdlib component items (LoadStdlib removed in v2)
	var items []CompletionItem
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

func EventCompletions() []CompletionItem {
	// TODO: restore event completions from stdlib (LoadStdlib removed in v2)
	return nil
}

func StylePropCompletions() []CompletionItem {
	// TODO: restore style prop completions from stdlib (LoadStdlib removed in v2)
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

	// Look up user-defined components
	var props []ast.Param
	var events []ast.EventDecl
	if doc != nil {
		for _, stmt := range doc.Stmts {
			if c, ok := stmt.(*ast.ComponentDecl); ok && c.Name == compName {
				for _, p := range c.Props.Props {
					switch pd := p.(type) {
					case ast.Param:
						props = append(props, pd)
					case ast.EventDecl:
						events = append(events, pd)
					}
				}
				break
			}
		}
	}

	// TODO: look up stdlib component schemas (LoadStdlib removed in v2)

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

	// Style prop
	if !used["style"] {
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
// inside the parenthesized options of an output declaration. The candidate
// set is the union of stdlib, language, and platform Options structs.
func OutputOptsCompletions(content string, line int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	l := strings.TrimSpace(lines[line-1])

	platformName := extractOutputPlatform(l)
	langName := extractOutputLang(l)

	// Build options are directive surface, not declarations the file imports:
	// `output { none { html(name="X") } }` checks with no import at all, so
	// these are read from the package that declares them rather than from
	// whatever the file has in scope.
	sources := checker.PackageDocsFor("std")
	if langName != "" {
		sources = append(sources, codegen.LangDocs(codegen.LookupLang(langName))...)
	}
	if platformName != "" {
		sources = append(sources, codegen.PlatformDocs(codegen.LookupPlatform(platformName))...)
	}

	seen := map[string]bool{}
	var items []CompletionItem
	for _, doc := range sources {
		if doc == nil {
			continue
		}
		for _, stmt := range doc.Stmts {
			s, ok := stmt.(*ast.StructDef)
			if !ok || !s.Options {
				continue
			}
			for _, f := range s.Fields() {
				for _, name := range f.Names {
					if seen[name] {
						continue
					}
					seen[name] = true
					items = append(items, CompletionItem{
						Label:      name,
						Kind:       CIKProperty,
						Detail:     typeExprString(f.Type),
						InsertText: name + "=",
					})
				}
			}
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

	// Also check imported namespaces in the document
	if pkgDoc == nil && doc != nil {
		for _, stmt := range doc.Stmts {
			if imp, ok := stmt.(*ast.Import); ok && imp.Alias == nsName {
				// Imported package — we don't have resolved components here,
				// so return empty for now.
				// TODO: resolve imported components for namespace completions
				return nil
			}
		}
	}

	// `this.<member>` inside a method body: enumerate the receiver type's
	// fields, members, and sibling methods.
	if pkgDoc == nil && nsName == ir.ReceiverParam {
		if items := thisCompletions(doc, line); items != nil {
			return items
		}
	}

	if pkgDoc == nil {
		return nil
	}

	var items []CompletionItem

	for _, stmt := range pkgDoc.Stmts {
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
