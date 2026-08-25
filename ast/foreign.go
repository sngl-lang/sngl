package ast

// ForeignMark is what #[foreign("scheme://pkg#Name")] records: the language
// whose name this is, the package it lives in there, and the name to emit.
// A mark that gives a bare name leaves Scheme and Pkg empty — a struct field
// has no package of its own, and neither does a declaration whose target
// language spells it without qualification.
//
// It is not a foreign declaration's identity: several SNGL types map to one
// target-language type, so a mark is a codegen fact and nothing more. The
// zero value means "not foreign".
type ForeignMark struct{ Scheme, Pkg, Name string }

// ForeignTaggable is a declaration form that can carry #[foreign(...)]. As
// with BuiltinTaggable, the macro asserts this interface rather than switching
// on the declaration kind, so which forms can carry a mark is a property of
// the AST.
type ForeignTaggable interface {
	SetForeign(ForeignMark)
}

func (s *StructDef) SetForeign(m ForeignMark)   { s.Foreign = m }
func (f *StructField) SetForeign(m ForeignMark) { f.Foreign = m }
func (e *EnumDef) SetForeign(m ForeignMark)     { e.Foreign = m }
func (f *FuncDef) SetForeign(m ForeignMark)     { f.Foreign = m }
func (c *ConstDecl) SetForeign(m ForeignMark)   { c.Foreign = m }
func (v *VarDecl) SetForeign(m ForeignMark)     { v.Foreign = m }
