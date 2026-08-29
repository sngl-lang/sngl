package lookup

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// renderDeclBody returns rendered HTML for the given lookup result. Used to
// pre-bake DeclPage.Body so the website can drop the page contents in via
// docui.Markdown without depending on the optimizer to fold a runtime
// lookup.Resolve call. `pkg` lets renderers cross-link to sibling decl pages
// (e.g. methods → method detail pages) using the same URL scheme as
// declPageHref.
func renderDeclBody(pkg string, res Result) string {
	md := buildDeclMarkdown(pkg, res)
	if md == "" {
		return ""
	}
	html, err := docsite.RenderMarkdown([]byte(md))
	if err != nil {
		return md
	}
	return string(html)
}

func buildDeclMarkdown(pkg string, res Result) string {
	switch res.Kind {
	case KindComponent:
		return componentMD(res.Component)
	case KindType:
		return typeMD(pkg, res.Type)
	case KindEnum:
		return enumMD(pkg, res.Enum)
	case KindFunc:
		return funcMD(res.Func)
	case KindValue:
		return valueMD(res.Value)
	case KindMember:
		return memberMD(res.Member)
	case KindField:
		return fieldMD(res.Field)
	case KindProp:
		return propMD(res.Prop)
	}
	return ""
}

func componentMD(c *ComponentDetail) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	if c.Doc != "" {
		sb.WriteString(c.Doc)
		sb.WriteString("\n\n")
	}
	if c.Schema != nil {
		if len(c.Schema.Props) > 0 {
			sb.WriteString("## Properties\n\n")
			sb.WriteString("| Name | Type | Description |\n")
			sb.WriteString("|------|------|-------------|\n")
			names := make([]string, 0, len(c.Schema.Props))
			for n := range c.Schema.Props {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				ps := c.Schema.Props[n]
				typeStr := (&ps.Type).String()
				if len(ps.Enum) > 0 {
					typeStr += " (" + strings.Join(ps.Enum, ", ") + ")"
				}
				sb.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", n, typeStr, escapeMD(ps.Doc)))
			}
			sb.WriteString("\n")
		}
		if len(c.Schema.Events) > 0 {
			sb.WriteString("## Events\n\n")
			sb.WriteString("| Name | Payload |\n")
			sb.WriteString("|------|---------|\n")
			enames := make([]string, 0, len(c.Schema.Events))
			for n := range c.Schema.Events {
				enames = append(enames, n)
			}
			sort.Strings(enames)
			for _, n := range enames {
				sb.WriteString(fmt.Sprintf("| `@%s` | %s |\n", n, c.Schema.Events[n]))
			}
			sb.WriteString("\n")
		}
		if c.Schema.Children != nil {
			sb.WriteString(fmt.Sprintf("**Children:** %s\n\n", docsite.ChildPolicyString(c.Schema.Children)))
		}
	}
	return sb.String()
}

func typeMD(pkg string, t *TypeDetail) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	if t.Native != nil && t.Native.Foreign.Name != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", t.Native.Foreign.Name))
	}
	if t.Doc != "" {
		sb.WriteString(t.Doc)
		sb.WriteString("\n\n")
	}
	// Fields (struct or native struct).
	if t.Struct != nil && len(t.Struct.Fields()) > 0 {
		sb.WriteString("## Fields\n\n```\n")
		for _, f := range t.Struct.Fields() {
			tstr := parser.FormatType(f.Type)
			for _, name := range f.Names {
				sb.WriteString(fmt.Sprintf("%-16s %s\n", name, tstr))
			}
		}
		sb.WriteString("```\n\n")
	}
	if t.Native != nil && len(t.Native.Fields) > 0 {
		sb.WriteString("## Fields\n\n```\n")
		for _, f := range t.Native.Fields {
			line := fmt.Sprintf("%-20s %s", f.Name, f.Type.String())
			if f.Unusable != "" {
				line += "  (unusable)"
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("```\n\n")
	}
	if len(t.Methods) > 0 {
		sb.WriteString("## Methods\n\n")
		for _, m := range t.Methods {
			href := declPageHref(pkg, "types", t.Name, m.ShortName)
			if blurb := FirstSentence(m.Doc); blurb != "" {
				sb.WriteString(fmt.Sprintf("- [`%s`](%s) — %s\n", m.ShortName, href, blurb))
			} else {
				sb.WriteString(fmt.Sprintf("- [`%s`](%s)\n", m.ShortName, href))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func enumMD(pkg string, e *EnumDetail) string {
	if e == nil {
		return ""
	}
	var sb strings.Builder
	if e.Doc != "" {
		sb.WriteString(e.Doc)
		sb.WriteString("\n\n")
	}
	var members []string
	if e.AST != nil {
		for _, m := range e.AST.Members() {
			members = append(members, m.Name)
		}
	}
	if e.Native != nil {
		for _, m := range e.Native.Members {
			members = append(members, m.Name)
		}
	}
	if len(members) > 0 {
		sb.WriteString("## Members\n\n")
		for _, m := range members {
			href := declPageHref(pkg, "enums", e.Name, m)
			sb.WriteString(fmt.Sprintf("- [`%s`](%s)\n", m, href))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func funcMD(f *FuncDetail) string {
	if f == nil {
		return ""
	}
	var sb strings.Builder
	if f.Native != nil {
		parts := make([]string, 0, len(f.Native.Params))
		for _, p := range f.Native.Params {
			parts = append(parts, fmt.Sprintf("%s %s", p.Name, p.Type.String()))
		}
		sig := fmt.Sprintf("func %s(%s)", f.Native.Name, strings.Join(parts, ", "))
		if f.Native.Return != nil {
			sig += " " + f.Native.Return.String()
		}
		sb.WriteString("```\n" + sig + "\n```\n\n")
		if f.Native.Foreign.Name != "" {
			sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", f.Native.Foreign.Name))
		}
	} else if f.AST != nil && checker.IsMacroDecl(f.AST) {
		// A macro is written as a mark, so the mark form is its signature; the
		// declaration it is spelled as would only invite a call.
		sb.WriteString("```\n" + MarkSignature(f.Name, f.AST) + "\n```\n\n")
	} else if f.AST != nil {
		sb.WriteString("```\n")
		sb.WriteString(snglSignatureWithInferred(f.Name, f.AST))
		sb.WriteString("\n```\n\n")
	}
	if f.Doc != "" {
		sb.WriteString(f.Doc)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// MarkSignature renders a macro declaration the way it is written: as a
// `#[...]` mark. Parameters become names only — "..." for the list parameter
// that stands in for the bare-identifier flags SNGL has no variadic for, and
// brackets for one with a default, since a mark's arguments are positional.
// A macro taking none renders as `#[options]`.
func MarkSignature(name string, f *ast.FuncDef) string {
	return "#[" + name + markParams(f) + "]"
}

func markParams(f *ast.FuncDef) string {
	var parts []string
	for _, p := range f.Params.Params {
		name := p.Name
		if nt, ok := p.Type.(*ast.NamedType); ok && nt.Name == "list" {
			name += "..."
		}
		if p.Default != nil {
			name = "[" + name + "]"
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// snglSignatureWithInferred renders a SNGL func signature, falling back to
// the type-checked IR package's resolved Return type when the AST has no
// explicit annotation (typical for `=> expr` shorthand bodies). Stdlib IR
// funcs split a "Receiver.Name" decl into separate Receiver + Name fields,
// so we match on the (receiver, simple-name) pair derived from `fullName`.
func snglSignatureWithInferred(fullName string, f *ast.FuncDef) string {
	sig := snglSignature(f)
	if f.ReturnType != nil {
		return sig
	}
	pkg := checker.StdlibIRPackage()
	if pkg == nil {
		return sig
	}
	receiver, simple := "", fullName
	if i := strings.Index(fullName, "."); i > 0 {
		receiver = fullName[:i]
		simple = fullName[i+1:]
	}
	for _, irFunc := range pkg.Funcs {
		if irFunc.Receiver != receiver || irFunc.Name != simple {
			continue
		}
		if irFunc.Return != nil {
			return sig + " " + irFunc.Return.String()
		}
	}
	return sig
}

func valueMD(v *ValueDetail) string {
	if v == nil {
		return ""
	}
	var sb strings.Builder
	if v.Native != nil && v.Native.Type != nil {
		sb.WriteString(fmt.Sprintf("_Type:_ `%s`\n\n", v.Native.Type.String()))
		if v.Native.Foreign.Name != "" {
			sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", v.Native.Foreign.Name))
		}
	}
	if v.Doc != "" {
		sb.WriteString(v.Doc)
		sb.WriteString("\n")
	}
	return sb.String()
}

func memberMD(m *MemberDetail) string {
	if m == nil {
		return ""
	}
	var sb strings.Builder
	if m.Doc != "" {
		sb.WriteString(m.Doc)
		sb.WriteString("\n")
	}
	return sb.String()
}

func fieldMD(f *FieldDetail) string {
	if f == nil {
		return ""
	}
	var sb strings.Builder
	if f.Expr != nil {
		sb.WriteString(fmt.Sprintf("_Type:_ `%s`\n\n", parser.FormatType(f.Expr)))
	}
	if f.Doc != "" {
		sb.WriteString(f.Doc)
		sb.WriteString("\n")
	}
	return sb.String()
}

func propMD(p *PropDetail) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	if p.Schema != nil {
		sb.WriteString(fmt.Sprintf("_Type:_ `%s`\n\n", (&p.Schema.Type).String()))
		if len(p.Schema.Enum) > 0 {
			sb.WriteString("_Values:_ " + strings.Join(p.Schema.Enum, ", ") + "\n\n")
		}
		if p.Schema.Doc != "" {
			sb.WriteString(p.Schema.Doc)
			sb.WriteString("\n")
		}
		return sb.String()
	}
	if p.Event != "" {
		sb.WriteString(fmt.Sprintf("_Event payload:_ `%s`\n", p.Event))
	}
	return sb.String()
}

func snglSignature(f *ast.FuncDef) string {
	var sb strings.Builder
	sb.WriteString("func " + f.Name)
	sb.WriteString(parser.FormatTypeParams(f.TypeParams))
	sb.WriteString("(")
	for i, p := range f.Params.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.Name)
		if p.Type != nil {
			sb.WriteString(" " + parser.FormatType(p.Type))
		}
	}
	sb.WriteString(")")
	if f.ReturnType != nil {
		sb.WriteString(" " + parser.FormatType(f.ReturnType))
	}
	return sb.String()
}

// escapeMD converts whitespace runs in a doc string to single spaces so it
// flows into a markdown table cell without breaking the row.
func escapeMD(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
