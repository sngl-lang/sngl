package lsp

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// lookupStdlibSymbol walks pkg.Imports looking for a func, component,
// struct, or unit named `name`. Returns formatted markdown on match.
func lookupStdlibSymbol(pkg *ir.Package, name string) (string, bool) {
	if pkg == nil {
		return "", false
	}
	// The checker registers stdlib decls in the package's SymbolTable
	// (Comps/Types) alongside user-code decls. User-code names are
	// intercepted by hoverInStmts before this callback fires, so
	// querying the merged symbol table here returns stdlib decls.
	if pkg.Symbols != nil {
		if sym, ok := pkg.Symbols.LookupComponent(name); ok {
			if c, ok := sym.(*ir.Component); ok {
				return formatStdlibComponent(c), true
			}
		}
		if sym, ok := pkg.Symbols.LookupType(name); ok {
			switch t := sym.(type) {
			case *ir.StructDef:
				return formatStdlibStruct(t), true
			case *ir.UnitDef:
				return formatStdlibUnit(t), true
			}
		}
		// Receiver-qualified method lookup: "color.rgb" → method "rgb"
		// on receiver "color".
		if recv, method, hasDot := splitMethod(name); hasDot {
			if fn, ok := pkg.Symbols.LookupMethod(recv, method); ok {
				return formatStdlibFunc(fn), true
			}
		}
		// Plain func lookup via scope (covers stdlib free funcs registered
		// at the root scope).
		if pkg.Symbols.Root != nil {
			if sym, ok := pkg.Symbols.Root.Lookup(name); ok {
				if fn, ok := sym.(*ir.Func); ok {
					return formatStdlibFunc(fn), true
				}
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if matchesFuncName(fn, name) {
			return formatStdlibFunc(fn), true
		}
	}
	// Also walk explicit imports' sub-packages (e.g. user `import widgets`).
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Pkg == nil {
			continue
		}
		sub := imp.Pkg
		for _, fn := range sub.Funcs {
			if matchesFuncName(fn, name) {
				return formatStdlibFunc(fn), true
			}
		}
		for _, c := range sub.Components {
			if c.Name == name {
				return formatStdlibComponent(c), true
			}
		}
		for _, sd := range sub.Structs {
			if sd.Name == name {
				return formatStdlibStruct(sd), true
			}
		}
		for _, u := range sub.Units {
			if u.Name == name {
				return formatStdlibUnit(u), true
			}
		}
	}
	return "", false
}

// matchesFuncName handles plain ("rgb") and receiver-qualified
// ("color.rgb") lookups.
func matchesFuncName(fn *ir.Func, query string) bool {
	if fn.Name == query {
		return true
	}
	if fn.Receiver != "" && (fn.Receiver+"."+fn.Name) == query {
		return true
	}
	return false
}

func splitMethod(name string) (recv, method string, ok bool) {
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return name[:i], name[i+1:], true
		}
	}
	return "", name, false
}

func typeString(t *ir.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

func formatStdlibFunc(fn *ir.Func) string {
	var sb strings.Builder
	sb.WriteString("```sngl\nfunc ")
	if fn.Receiver != "" {
		sb.WriteString(fn.Receiver)
		sb.WriteByte('.')
	}
	sb.WriteString(fn.Name)
	sb.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.Name)
		if t := typeString(p.Type); t != "" {
			sb.WriteByte(' ')
			sb.WriteString(t)
		}
	}
	sb.WriteByte(')')
	if rt := typeString(fn.Return); rt != "" {
		sb.WriteByte(' ')
		sb.WriteString(rt)
	}
	sb.WriteString("\n```\n")
	if fn.Doc != "" {
		sb.WriteByte('\n')
		sb.WriteString(fn.Doc)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n> stdlib\n")
	return sb.String()
}

func formatStdlibComponent(c *ir.Component) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\ncomponent %s\n```\n", c.Name)
	if len(c.Props) > 0 {
		sb.WriteString("\n**Props:**\n")
		for _, p := range c.Props {
			t := typeString(p.Type)
			if t == "" {
				t = "dyn"
			}
			if p.Default == nil {
				fmt.Fprintf(&sb, "- `%s` %s (required)\n", p.Name, t)
			} else {
				fmt.Fprintf(&sb, "- `%s` %s\n", p.Name, t)
			}
		}
	}
	sb.WriteString("\n> stdlib\n")
	return sb.String()
}

func formatStdlibStruct(sd *ir.StructDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nstruct %s {\n", sd.Name)
	for _, f := range sd.Fields {
		fmt.Fprintf(&sb, "    %s %s\n", f.Name, typeString(f.Type))
	}
	sb.WriteString("}\n```\n")
	if sd.Doc != "" {
		sb.WriteByte('\n')
		sb.WriteString(sd.Doc)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n> stdlib\n")
	return sb.String()
}

func formatStdlibUnit(u *ir.UnitDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nunit %s {\n", u.Name)
	for _, s := range u.Suffixes {
		fmt.Fprintf(&sb, "    %s\n", s.Name)
	}
	sb.WriteString("}\n```\n")
	sb.WriteString("\n> stdlib\n")
	return sb.String()
}

// lookupComponentProp finds the prop named propName on the component
// identified by componentName (user or stdlib).
func lookupComponentProp(pkg *ir.Package, componentName, propName string) (string, bool) {
	comp := findComponent(pkg, componentName)
	if comp == nil {
		return "", false
	}
	for _, p := range comp.Props {
		if p.Name == propName {
			return formatProp(comp, p), true
		}
	}
	return "", false
}

func findComponent(pkg *ir.Package, name string) *ir.Component {
	if pkg == nil {
		return nil
	}
	if pkg.Symbols != nil {
		if sym, ok := pkg.Symbols.LookupComponent(name); ok {
			if c, ok := sym.(*ir.Component); ok {
				return c
			}
		}
	}
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c.Name == name {
				return c
			}
		}
	}
	return nil
}

func formatProp(comp *ir.Component, p *ir.Prop) string {
	t := typeString(p.Type)
	if t == "" {
		t = "dyn"
	}
	required := "required"
	if p.Default != nil {
		required = "optional"
	}
	return fmt.Sprintf("```sngl\n%s.%s: %s\n```\n\n%s prop on `component %s`.\n", comp.Name, p.Name, t, required, comp.Name)
}

// lookupStructFieldType finds field on the struct named structName
// (user or stdlib). Returns markdown describing the field's type.
func lookupStructFieldType(pkg *ir.Package, structName, fieldName string) (string, bool) {
	sd := findStruct(pkg, structName)
	if sd == nil {
		return "", false
	}
	for _, f := range sd.Fields {
		if f.Name != fieldName {
			continue
		}
		t := "dyn"
		if f.Type != nil {
			t = f.Type.String()
		}
		return fmt.Sprintf("```sngl\n%s.%s: %s\n```\n\nfield on `struct %s`.\n", sd.Name, f.Name, t, sd.Name), true
	}
	return "", false
}

func findStruct(pkg *ir.Package, name string) *ir.StructDef {
	if pkg == nil {
		return nil
	}
	if pkg.Symbols != nil {
		if sym, ok := pkg.Symbols.LookupType(name); ok {
			if sd, ok := sym.(*ir.StructDef); ok {
				return sd
			}
		}
	}
	for _, sd := range pkg.Structs {
		if sd.Name == name {
			return sd
		}
	}
	for _, imp := range pkg.Imports {
		if imp.Pkg == nil {
			continue
		}
		for _, sd := range imp.Pkg.Structs {
			if sd.Name == name {
				return sd
			}
		}
	}
	return nil
}
