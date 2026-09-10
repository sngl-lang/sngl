package ast

import (
	"fmt"
	"strings"
)

// FileAsset records a file that needs to be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute source path
	OutPath string // output path relative to output dir
	URL     string // URL path for use in HTML
}

// Pos records the source position of an AST node.
type Pos struct {
	File   string // source filename (empty when unknown)
	Line   int    // 1-based line number
	Column int    // 1-based column number
}

func (p Pos) IsSet() bool   { return p != Pos{} }
func (p Pos) IsValid() bool { return p.Line > 0 }

func (p Pos) String() string {
	if p.Line == 0 {
		return ""
	}
	if p.File != "" {
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Comment is a source comment preserved for formatting.
type Comment struct {
	Pos    Pos
	Text   string // includes delimiters (// or /* */)
	Block  bool   // true for /* */ comments
	Inline bool   // true for trailing same-line comments
}

// DisabledDecl wraps any statement prefixed with slashdash (/-).
type DisabledDecl struct {
	Pos   Pos
	Inner Stmt
}

// MacroAttr is a single macro attribute: #[alias.name(args)].
type MacroAttr struct {
	Pos   Pos
	Alias string // import alias, e.g. "canvas" in #[canvas.shape]
	Name  string // macro name, e.g. "shape" in #[canvas.shape]
	Args  []Expr // optional arguments
}

// Document is the top-level container for a .sngl file.
type Document struct {
	Stmts []Stmt
	// BlankLines holds the 1-based lines the source left empty. It is what
	// lets the formatter keep the spacing an author chose without deducing
	// where each statement ended — a question no AST node answers, and one
	// every multi-line literal, argument list and nested block got wrong.
	// A document the compiler synthesized has none, and formats unspaced.
	BlankLines map[int]bool `json:"-"`
}

// BlankBefore reports whether the source left the line above line empty.
func (d *Document) BlankBefore(line int) bool {
	return line > 1 && d.BlankLines[line-1]
}

// --- Type declarations ---

// EnumMember is a single value in an enum declaration.
type EnumMember struct {
	Pos   Pos
	Name  string
	Value Expr // nil for bare members
}

// EnumBodyItem is one element inside an enum body — a member or a nested func.
type EnumBodyItem interface {
	enumBodyItem()
}

func (*EnumMember) enumBodyItem() {}
func (*FuncDef) enumBodyItem()    {}

// A comment written inside an enum body is one of its items: there is nowhere
// else for it to live, and moving it out of the braces on a format would be
// moving it away from what it describes.
func (*Comment) enumBodyItem() {}

// EnumDef declares an enum type. Name is empty for anonymous enum types.
type EnumDef struct {
	Pos         Pos
	Name        string
	Body        []EnumBodyItem // members and nested funcs, in source order
	IsMultiline bool
	Attrs       []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// Members returns just the *EnumMember items from Body, in source order.
// Read-only iteration helper for callers that don't care about funcs.
func (e *EnumDef) Members() []*EnumMember {
	out := make([]*EnumMember, 0, len(e.Body))
	for _, it := range e.Body {
		if m, ok := it.(*EnumMember); ok {
			out = append(out, m)
		}
	}
	return out
}

// Funcs returns just the *FuncDef items from Body, in source order.
func (e *EnumDef) Funcs() []*FuncDef {
	out := make([]*FuncDef, 0, len(e.Body))
	for _, it := range e.Body {
		if f, ok := it.(*FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}

// StructBodyItem is one element inside a struct body — a field or a nested func.
type StructBodyItem interface {
	structBodyItem()
}

func (*StructField) structBodyItem() {}
func (*FuncDef) structBodyItem()     {}
func (*Comment) structBodyItem()     {}

// TypeParam is one generic parameter of a declaration. Default is what an
// argument list that stops short falls back to.
type TypeParam struct {
	Pos     Pos `json:"-"`
	Name    string
	Default TypeExpr `json:",omitempty"`
}

// ParamName and ParamPos let one helper walk either package's type parameters
// -- ir.TypeParam answers to them too. The interface they satisfy belongs to
// the checker, which is the only caller.
func (p TypeParam) ParamName() string { return p.Name }
func (p TypeParam) ParamPos() Pos     { return p.Pos }

// StructDef declares a struct type. Name is empty for anonymous struct types.
type StructDef struct {
	Pos         Pos
	Name        string
	TypeParams  []TypeParam // generic parameters: ["T"] for `struct list<T> {}`
	Body        []StructBodyItem
	IsMultiline bool
	Attrs       []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// Fields returns just the *StructField items from Body, in source order.
func (s *StructDef) Fields() []*StructField {
	out := make([]*StructField, 0, len(s.Body))
	for _, it := range s.Body {
		if f, ok := it.(*StructField); ok {
			out = append(out, f)
		}
	}
	return out
}

// Funcs returns just the *FuncDef items from Body, in source order.
func (s *StructDef) Funcs() []*FuncDef {
	out := make([]*FuncDef, 0, len(s.Body))
	for _, it := range s.Body {
		if f, ok := it.(*FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}

// StructField is a field in a struct declaration.
// Names groups comma-separated fields sharing a type (e.g. `a, b int`);
// a default applies to every name.
type StructField struct {
	Pos           Pos
	Names         []string
	NamePositions []Pos // parallel to Names; per-name source positions
	Type          TypeExpr
	Default       Expr
	Attrs         []MacroAttr `json:",omitempty"` // the #[...] marks written on the field
}

// UnitDef declares a unit type with named suffixes.
// Name is empty for anonymous unit types.
type UnitDef struct {
	Pos         Pos
	Name        string
	Body        []UnitBodyItem // suffixes and comments, in source order
	IsMultiline bool
	Attrs       []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// UnitBodyItem is what may appear between a unit's braces.
type UnitBodyItem interface {
	unitBodyItem()
}

func (*UnitSuffix) unitBodyItem() {}
func (*Comment) unitBodyItem()    {}

// Suffixes returns just the *UnitSuffix items from Body, in source order.
func (u *UnitDef) Suffixes() []*UnitSuffix {
	out := make([]*UnitSuffix, 0, len(u.Body))
	for _, it := range u.Body {
		if s, ok := it.(*UnitSuffix); ok {
			out = append(out, s)
		}
	}
	return out
}

// UnitSuffix defines a single suffix within a unit declaration.
type UnitSuffix struct {
	Pos    Pos
	Name   string // "ms", "px"
	Factor Expr   // nil for bare suffixes
}

// --- Variables and constants ---

// VarSpec is a single binding in a const or var declaration.
// NamePositions runs parallel to Names: NamePositions[i] is the source
// position of Names[i]. Empty when the parser didn't track positions
// (older fixtures, synthesized specs).
type VarSpec struct {
	Pos           Pos
	Names         []string
	NamePositions []Pos
	Type          TypeExpr
	Default       Expr
	Handlers      []EventHandler
	Leading       []*Comment `json:",omitempty"` // comments written above it in a group
	Trailing      *Comment   `json:",omitempty"` // the comment written after it on its line
}

// ConstDecl declares one or more constants.
type ConstDecl struct {
	Pos       Pos
	EndPos    Pos // the closing paren of a group, which bounds the comments inside it
	IsGrouped bool
	Specs     []VarSpec
	Tail      []*Comment  `json:",omitempty"` // comments after the last spec, still inside the group
	Attrs     []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// VarDecl declares one or more variables.
type VarDecl struct {
	Pos       Pos
	EndPos    Pos // the closing paren of a group, which bounds the comments inside it
	IsGrouped bool
	Specs     []VarSpec
	Tail      []*Comment  `json:",omitempty"` // comments after the last spec, still inside the group
	Attrs     []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// --- Imports ---

// Import declares a module import, optionally aliased and/or replaced.
type Import struct {
	Pos     Pos
	Path    string // local import path (LHS of =>, or the bare string)
	Alias   string // Alias holds the identifier to reference the import. "." for imports that are spread into the file's scope.
	Replace string // replacement URL (RHS of =>), "" if no replace
}

// IsDot reports whether this is a dot import (`import . "p"`). "." cannot
// collide with a real alias because it is not a legal identifier.
func (i *Import) IsDot() bool { return i.Alias == "." }

// --- Parameters ---

// Param is a parameter in a function, component, or lambda definition.
type Param struct {
	Pos           Pos
	Name          string
	Type          TypeExpr
	Default       Expr
	Bidirectional bool // :name — component binding param
	// Attrs are the #[...] macro attributes written before the parameter. The
	// grammar accepts them anywhere a parameter is written; which of them mean
	// anything there is the checker's to say, and it refuses the rest where it
	// registers the parameter.
	Attrs []MacroAttr `json:",omitempty"`
	// Leading and Trailing are the comments written above the parameter and
	// after it on its line. A parameter is not a statement, so its comments
	// have no statement list to live in; only a list written across lines can
	// hold them.
	Leading  []*Comment `json:",omitempty"`
	Trailing *Comment   `json:",omitempty"`
}

// ParamList is an ordered list of parameters.
type ParamList struct {
	Pos         Pos
	IsMultiline bool
	Params      []Param
}

// --- Functions ---

// FuncDef declares a named function.
// Exactly one of Body or Block is set.
type FuncDef struct {
	Pos            Pos
	Name           string
	TypeParams     []TypeParam // method-level generic parameters, e.g. ["T", "U"]
	RecvTypeParams []TypeParam // receiver-level: ["T"] for func list<T>.length()
	Params         ParamList
	// Target is the `[expr]` index on the declaration name: the build target
	// this declaration implements, written by whoever overrides one. nil on an
	// ordinary declaration. See ComponentDecl.Target.
	Target     Expr
	ReturnType TypeExpr    // nil for void/action functions
	Body       Expr        // single-expression form (=> expr)
	Block      StmtBlock   // block form ({ ... })
	Attrs      []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// IsTest returns true if this function is a test function.
func (f *FuncDef) IsTest() bool { return strings.HasPrefix(f.Name, "test") }

// TestFuncs returns all document-level test functions.
func (d *Document) TestFuncs() []*FuncDef {
	var out []*FuncDef
	for _, s := range d.Stmts {
		if fn, ok := s.(*FuncDef); ok && fn.IsTest() {
			out = append(out, fn)
		}
	}
	return out
}

// SplitMethodName splits "int.sqrt" into ("int", "sqrt", true).
func SplitMethodName(name string) (typeName, method string, ok bool) {
	if before, after, ok0 := strings.Cut(name, "."); ok0 {
		return before, after, true
	}
	return "", name, false
}

// --- Components ---

// ComponentDecl declares a component with props and a body.
type ComponentDecl struct {
	Pos  Pos
	Name string
	// Target is the `[expr]` index on the declaration name --
	// `component sngl.button[html.platform](text) { ... }`. It names the build
	// target this declaration implements, and is nil on an ordinary one. The
	// expression must fold to a target identity, which only a target's own
	// package supplies, so the target cannot be misspelled into an override
	// nothing ever selects.
	Target Expr
	// TypeParams are the generic parameters written after the name --
	// ["T"] for `component effect<T>(on T)`. Bound at the call site from the
	// props supplied there, the same way a func's are bound from its
	// arguments.
	TypeParams   []TypeParam `json:",omitempty"`
	Props        PropList
	HasParens    bool // true if declaration was written with `()` (even empty)
	ChildrenType TypeExpr
	Body         StmtBlock
	Attrs        []MacroAttr `json:",omitempty"` // the #[...] marks written on the declaration
}

// PropList is the parameter list of a component declaration.
type PropList struct {
	Pos         Pos
	IsMultiline bool
	Props       []ParamOrEventDecl
}

// ParamOrEventDecl is a component parameter or event declaration.
type ParamOrEventDecl interface {
	paramOrEventDecl()
}

func (Param) paramOrEventDecl()     {}
func (EventDecl) paramOrEventDecl() {}

// EventDecl declares an event on a component: @click, @change Type.
type EventDecl struct {
	Pos  Pos
	Name string
	Type TypeExpr // optional type annotation
	// Attrs are the #[...] macro attributes written before the event, read
	// the same way a Param's are.
	Attrs    []MacroAttr `json:",omitempty"`
	Leading  []*Comment  `json:",omitempty"`
	Trailing *Comment    `json:",omitempty"`
}

// EventHandler is an event handler: @name[(params)] { body }.
type EventHandler struct {
	Pos    Pos
	Name   string
	Params ParamList
	Body   StmtBlock
}

// --- Visual nodes ---

// VisualNode is a visual element instantiation with optional args and body.
type VisualNode struct {
	Pos    Pos
	Target TargetExpr
	Args   ArgList
	ID     string // element ID from #id syntax
	Block  StmtBlock
	// HasParens records that the node was written with `()`, which an empty
	// argument list cannot say on its own — `vbox()` and `vbox` are the same
	// node, and the parens are the author's.
	HasParens bool
}

// TargetName is the node's target as written: a bare name, or a name
// qualified by one namespace. Empty for anything else, which is what a
// caller reads as "not a name" rather than as a spelling it could quote.
//
// It lives here rather than in the checker because a diagnostic raised after
// the checker -- codegen declining a node kind it cannot emit -- has only the
// AST left to say what the program wrote.
func (vn *VisualNode) TargetName() string {
	if vn == nil || vn.Target == nil {
		return ""
	}
	switch t := vn.Target.(type) {
	case *IdentExpr:
		return t.Name
	case *SelectExpr:
		if id, ok := t.Operand.(*IdentExpr); ok {
			return id.Name + "." + t.Field
		}
	}
	return ""
}

// --- Control flow ---

// IfStmt: if cond { body } [else { alt }].
type IfStmt struct {
	Pos  Pos
	Cond Expr
	Body StmtBlock
	Else StmtBlock // zero value if no else
}

// ForStmt: for [var key [, value] =] [iter] { body } [else { alt }].
//
// Iter is nil for `for { }`, the loop with no head at all. Everything else
// carries one, and what it evaluates to is what the loop does: an iterable is
// walked, a bool is a condition tested before each iteration.
type ForStmt struct {
	Pos      Pos
	Key      string // iterator variable
	KeyRef   bool   // Key was &-prefixed: `for var &t = ...` (ref<T> element binding)
	Value    string // optional second variable (empty if single-var form)
	ValueRef bool   // Value was &-prefixed: `for var i, &t = ...`
	Iter     Expr
	Body     StmtBlock
	Else     StmtBlock // zero value if no else
}

// --- StmtPos implementations ---

func (s *StructDef) StmtPos() *Pos { return &s.Pos }

// A field is a Stmt only so it can carry marks; nothing executes it.
func (f *StructField) StmtPos() *Pos { return &f.Pos }

func (e *EnumDef) StmtPos() *Pos       { return &e.Pos }
func (u *UnitDef) StmtPos() *Pos       { return &u.Pos }
func (c *ConstDecl) StmtPos() *Pos     { return &c.Pos }
func (c *VarDecl) StmtPos() *Pos       { return &c.Pos }
func (f *FuncDef) StmtPos() *Pos       { return &f.Pos }
func (i *Import) StmtPos() *Pos        { return &i.Pos }
func (c *ComponentDecl) StmtPos() *Pos { return &c.Pos }
func (vn *VisualNode) StmtPos() *Pos   { return &vn.Pos }
func (s *IfStmt) StmtPos() *Pos        { return &s.Pos }
func (s *ForStmt) StmtPos() *Pos       { return &s.Pos }
func (c *Comment) StmtPos() *Pos       { return &c.Pos }
func (d *DisabledDecl) StmtPos() *Pos  { return &d.Pos }
