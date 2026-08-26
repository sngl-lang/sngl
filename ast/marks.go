package ast

// Attributed is a declaration form that can carry `#[...]` macro attributes.
// The parser attaches them to the declaration itself rather than wrapping it,
// so an attributed declaration is the same node an unattributed one is and
// every consumer sees one shape.
//
// What an attribute means is not the AST's to know: it is syntax for reaching
// past the syntax, and the checker is where one becomes a mark on the
// declaration it annotates. Which forms implement this is the AST's whole
// answer — a form that does not is refused at parse time, where the position
// is known.
type Attributed interface {
	Stmt
	// MacroAttrs returns the attributes written on the declaration, in source
	// order.
	MacroAttrs() []MacroAttr
	// SetMacroAttrs records them. pos is the first attribute's position and
	// becomes the declaration's own, so a doc comment written above them still
	// reads as adjacent to the declaration.
	SetMacroAttrs(pos Pos, attrs []MacroAttr)
}

func (s *StructDef) MacroAttrs() []MacroAttr     { return s.Attrs }
func (f *StructField) MacroAttrs() []MacroAttr   { return f.Attrs }
func (f *FuncDef) MacroAttrs() []MacroAttr       { return f.Attrs }
func (c *ComponentDecl) MacroAttrs() []MacroAttr { return c.Attrs }
func (c *ConstDecl) MacroAttrs() []MacroAttr     { return c.Attrs }
func (v *VarDecl) MacroAttrs() []MacroAttr       { return v.Attrs }
func (u *UnitDef) MacroAttrs() []MacroAttr       { return u.Attrs }
func (e *EnumDef) MacroAttrs() []MacroAttr       { return e.Attrs }

// A Param is not Attributed — the parser fills its attributes in directly, and
// it is not a Stmt — but it reads them out the same way, so that whatever
// accepts attributes from a declaration accepts them from a parameter too.
func (p Param) MacroAttrs() []MacroAttr { return p.Attrs }

func (s *StructDef) SetMacroAttrs(pos Pos, attrs []MacroAttr)     { s.Attrs, s.Pos = attrs, pos }
func (f *StructField) SetMacroAttrs(pos Pos, attrs []MacroAttr)   { f.Attrs, f.Pos = attrs, pos }
func (f *FuncDef) SetMacroAttrs(pos Pos, attrs []MacroAttr)       { f.Attrs, f.Pos = attrs, pos }
func (c *ComponentDecl) SetMacroAttrs(pos Pos, attrs []MacroAttr) { c.Attrs, c.Pos = attrs, pos }
func (c *ConstDecl) SetMacroAttrs(pos Pos, attrs []MacroAttr)     { c.Attrs, c.Pos = attrs, pos }
func (v *VarDecl) SetMacroAttrs(pos Pos, attrs []MacroAttr)       { v.Attrs, v.Pos = attrs, pos }
func (u *UnitDef) SetMacroAttrs(pos Pos, attrs []MacroAttr)       { u.Attrs, u.Pos = attrs, pos }
func (e *EnumDef) SetMacroAttrs(pos Pos, attrs []MacroAttr)       { e.Attrs, e.Pos = attrs, pos }

// MarkName renders an attribute the way it was written, for diagnostics. A
// bare mark has no alias, so the dotted form would print as ".builtin".
func (a MacroAttr) MarkName() string {
	if a.Alias == "" {
		return a.Name
	}
	return a.Alias + "." + a.Name
}
