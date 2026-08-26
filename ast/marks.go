package ast

// Attributed is a declaration form that can carry `#[...]` marks. The parser
// attaches them to the declaration itself rather than wrapping it, so a marked
// declaration is the same node an unmarked one is and every consumer sees one
// shape. The checker resolves and applies the marks where it registers the
// declaration.
//
// Which forms implement it is the AST's answer to what may be marked: a form
// that does not is refused at parse time, where the position is known, rather
// than by a mark that has no idea what it was written on.
type Attributed interface {
	Stmt
	Marked
	// SetMarkAttrs records them. pos is the first mark's position and becomes
	// the declaration's own, so a doc comment written above the marks still
	// reads as adjacent to the declaration.
	SetMarkAttrs(pos Pos, attrs []MacroAttr)
}

// Marked is anything a mark may be written on. Every Attributed form is one,
// and so is a component prop — which the parser fills in directly rather than
// through SetMarkAttrs, and which is not a Stmt, so it is this narrower
// interface the checker carries a mark's target as.
type Marked interface {
	// MarkAttrs returns the marks written on it, in source order.
	MarkAttrs() []MacroAttr
}

func (s *StructDef) MarkAttrs() []MacroAttr     { return s.Attrs }
func (f *StructField) MarkAttrs() []MacroAttr   { return f.Attrs }
func (f *FuncDef) MarkAttrs() []MacroAttr       { return f.Attrs }
func (c *ComponentDecl) MarkAttrs() []MacroAttr { return c.Attrs }
func (c *ConstDecl) MarkAttrs() []MacroAttr     { return c.Attrs }
func (v *VarDecl) MarkAttrs() []MacroAttr       { return v.Attrs }
func (u *UnitDef) MarkAttrs() []MacroAttr       { return u.Attrs }
func (e *EnumDef) MarkAttrs() []MacroAttr       { return e.Attrs }

// Param is Marked but not Attributed: a component prop carries marks, and a
// function parameter carries the ones the checker refuses.
func (p Param) MarkAttrs() []MacroAttr { return p.Attrs }

func (s *StructDef) SetMarkAttrs(pos Pos, attrs []MacroAttr)     { s.Attrs, s.Pos = attrs, pos }
func (f *StructField) SetMarkAttrs(pos Pos, attrs []MacroAttr)   { f.Attrs, f.Pos = attrs, pos }
func (f *FuncDef) SetMarkAttrs(pos Pos, attrs []MacroAttr)       { f.Attrs, f.Pos = attrs, pos }
func (c *ComponentDecl) SetMarkAttrs(pos Pos, attrs []MacroAttr) { c.Attrs, c.Pos = attrs, pos }
func (c *ConstDecl) SetMarkAttrs(pos Pos, attrs []MacroAttr)     { c.Attrs, c.Pos = attrs, pos }
func (v *VarDecl) SetMarkAttrs(pos Pos, attrs []MacroAttr)       { v.Attrs, v.Pos = attrs, pos }
func (u *UnitDef) SetMarkAttrs(pos Pos, attrs []MacroAttr)       { u.Attrs, u.Pos = attrs, pos }
func (e *EnumDef) SetMarkAttrs(pos Pos, attrs []MacroAttr)       { e.Attrs, e.Pos = attrs, pos }

// MarkName renders an attribute the way it was written, for diagnostics. A
// bare mark has no alias, so the dotted form would print as ".builtin".
func (a MacroAttr) MarkName() string {
	if a.Alias == "" {
		return a.Name
	}
	return a.Alias + "." + a.Name
}
