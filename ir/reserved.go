package ir

// The props a build-target node declares its reserved names in: a language
// node the words its declarations cannot take, a platform node the
// identifiers its own generated code introduces beside a program's. Two names
// rather than one because a build's options are one record, in which a
// language's prop and a platform's of the same name would be one field.
const (
	ReservedWordsProp = "reservedWords"
	ReservedNamesProp = "reservedNames"
)

// TargetReserved is what a build-target node reserves by default: its
// reservedWords or reservedNames prop's default, read as constant strings.
func TargetReserved(c *Component) []string {
	if c == nil {
		return nil
	}
	for _, p := range c.Props {
		if p.Name == ReservedWordsProp || p.Name == ReservedNamesProp {
			ss, _ := ConstStrings(p.Default)
			return ss
		}
	}
	return nil
}

// ConstStrings reads e as a constant list of strings: a list literal of
// string literals, whose elements may spread another such list, or a const
// holding one.
func ConstStrings(e Expr) ([]string, bool) {
	switch x := e.(type) {
	case *ListLit:
		var out []string
		for _, el := range x.Elems {
			if sp, ok := el.(*Spread); ok {
				ss, ok := ConstStrings(sp.Operand)
				if !ok {
					return nil, false
				}
				out = append(out, ss...)
				continue
			}
			lit, ok := el.(*Literal)
			if !ok {
				return nil, false
			}
			out = append(out, lit.Value)
		}
		return out, true
	case *Ident:
		if v, ok := x.Sym.(*Var); ok && v.IsConst {
			return ConstStrings(v.Init)
		}
	case *Select:
		// `js.words`: a const another package declares, reached through the
		// alias its package is imported under.
		id, ok := x.Operand.(*Ident)
		if !ok {
			return nil, false
		}
		ns, ok := id.Sym.(*Namespace)
		if !ok || ns.Pkg == nil || ns.Pkg.Symbols == nil {
			return nil, false
		}
		if m, found := ns.Pkg.Symbols.LookupMember(x.Field); found {
			if v, ok := m.(*Var); ok && v.IsConst {
				return ConstStrings(v.Init)
			}
		}
	}
	return nil, false
}
