package ast

import (
	"fmt"
	"strings"
)

// Pos records the source position of an AST node.
type Pos struct {
	Line   int // 1-based line number
	Column int // 1-based column number
}

func (p Pos) String() string {
	if p.Line == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// IsValid reports whether the position has been set.
func (p Pos) IsValid() bool { return p.Line > 0 }

// TypeInfo holds resolved type information populated by the checker.
type TypeInfo struct {
	Type     string // SNGL type: "int", "float", "bool", "string", "dyn", struct name
	IsList   bool   // true for list<T>
	ElemType string // element type for lists

	// Foreign type info (for go://, kt://, etc. imports)
	NativePkg  string // import path: "go/ast"
	NativeType string // qualified type: "ast.File"
}

type Output struct {
	Pos      Pos
	Lang     string            // "go"
	Platform string            // "bubbletea"
	Options  map[string]string // {"package": "main"}
	LangLine int               // source line of the lang keyword (for one-liner detection)
}

// Decl is the interface for top-level and component-level declarations
// that can appear in a Decls slice (including interleaved comments).
type Decl interface {
	DeclPos() Pos
}

// Comment is a source comment preserved for formatting.
type Comment struct {
	Pos    Pos
	Text   string // includes delimiters (// or /* */)
	Block  bool   // true for /* */ comments
	Inline bool   // true for trailing same-line comments
}

// DeclPos implements the Decl interface for Comment.
func (c *Comment) DeclPos() Pos { return c.Pos }

type Document struct {
	OutputDefaults     map[string]string // key=value from output(...) defaults
	Outputs            []*Output
	Structs            []*StructDef
	Enums              []*EnumDef
	Imports            []*Import
	Units              []*UnitDef
	Consts             []*Const
	Data               []*Data
	Functions          []*FuncDef
	Components         []*Component
	ImportedComponents []*Component // populated by checker; qualified-name keyed
	AbstractComponents []*Component // stdlib components with default bodies; populated by checker
	Timers             []*Timer
	Styles []*StyleDecl
	App    *App
	Comments           []Comment // all comments, ordered by position
	Decls              []Decl    // ordered declarations including interleaved comments
}

// FindComponent looks up a component by name, searching local components first,
// then imported components. For qualified names like "widgets.Counter", only
// imported components are searched (by their unqualified name).
func (d *Document) FindComponent(name string) *Component {
	if ns, local, ok := strings.Cut(name, "."); ok {
		_ = ns
		for _, c := range d.ImportedComponents {
			if c.Name == local {
				return c
			}
		}
		return nil
	}
	// User components take priority over abstract stdlib components
	for _, c := range d.Components {
		if c.Name == name {
			return c
		}
	}
	for _, c := range d.AbstractComponents {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// AllComponents returns local, imported, and abstract components combined.
func (d *Document) AllComponents() []*Component {
	all := make([]*Component, 0, len(d.Components)+len(d.ImportedComponents)+len(d.AbstractComponents))
	all = append(all, d.Components...)
	all = append(all, d.ImportedComponents...)
	all = append(all, d.AbstractComponents...)
	return all
}


// UnitDef declares a unit type with named suffixes.
type UnitDef struct {
	Pos      Pos
	Name     string        // "duration", "measurement"
	Suffixes []*UnitSuffix // all suffixes in a single group
}

// UnitSuffix defines a single suffix within a unit declaration.
// A bare suffix (Factor == nil) is an independent base.
// A suffix with a factor (e.g., rem = 16em) is related to another suffix.
type UnitSuffix struct {
	Pos    Pos
	Name   string // "ms", "px"
	Factor Node   // nil for bare suffixes, expression for related suffixes
}

// Const is an immutable named value.
type Const struct {
	Pos          Pos
	Name         string
	Init         Expr
	Disabled     bool
	Grouped      bool // parsed from const(...) grouped declaration
	ExplicitType bool // true when a type was written between name and =
}

type EnumDef struct {
	Pos      Pos
	Name     string
	Values   []string
	Disabled bool
}

type StructDef struct {
	Pos      Pos
	Name     string
	Fields   []*StructField
	Disabled bool
}

type StructField struct {
	Pos      Pos
	Name     string
	Type     string // type hint: "string", "bool", "int", etc.
	Default  Expr
	Resolved *TypeInfo // populated by checker
}

type Import struct {
	Pos       Pos
	Path      string
	Scheme    string // "go", "ts", "proto", "" for directory imports
	Namespace string // last path segment, e.g. "widgets" from "lib/widgets"
	Disabled  bool
}

// NativeDecls holds SNGL-compatible declarations resolved from a native import.
type NativeDecls struct {
	Structs    []*StructDef
	Enums      []*EnumDef
	Data       []*Data // extern funcs and vars
	ImportPath string  // e.g., "go/ast" for go:// imports
}

type Data struct {
	Pos          Pos
	Name         string
	Init         Expr
	Extern       bool     // "extern" positional arg present
	IsFunc       bool     // TypeHint starts with "func"
	ParamTypes   []string // parsed func params (e.g., ["string", "int"])
	ReturnType   string   // parsed func return type, "" for void
	Trigger      string   // resolved trigger function name, "" for none
	Disabled     bool
	Grouped      bool      // parsed from var(...) grouped declaration
	ExplicitType bool      // true when a type was written between name and =
	Resolved     *TypeInfo // populated by checker
}

type StyleDecl struct {
	Pos       Pos
	Name      string
	Props     map[string]Expr
	PropOrder []string // insertion order of property names from source
}

type PropDecl struct {
	Pos           Pos
	Name          string
	TypeHint      string   // "string", "bool", "int", "float", "dyn"
	Enum          []string // optional enum constraints
	Bidirectional bool     // :name — desugars to prop + change event
}

type EventDecl struct {
	Pos         Pos
	Name        string
	PayloadType string // "ClickEvent", "InputEvent", etc.
}

// FuncParam is a parameter in a function definition.
type FuncParam struct {
	Pos      Pos
	Name     string
	Type     string    // type hint: "int", "string", "User", etc.
	Resolved *TypeInfo // populated by checker
}

// FuncDef declares a named function.
// Exactly one of Body or Block is set.
type FuncDef struct {
	Pos        Pos
	EndLine    int // line of closing } for block-form funcs (set by parser)
	BraceCol   int // column of opening { for block-form funcs (set by parser, for comment filtering)
	Name       string
	TypeParams []string   // generic type parameters, e.g., ["T", "U"]
	Params     []*FuncParam
	ReturnType string     // "" for void/action functions
	Body       Expr       // single-expression form (= expr)
	Block      *FuncBlock // block form ({ ... }), nil for expression form
	IsStdlib   bool       // true for stdlib-provided functions (codegens use native implementations)
	Disabled   bool
	HasParens  bool       // true when () was explicit in source (for zero-param expression funcs)
}

// IsTest returns true if this function is a test function (name starts with "test").
func (f *FuncDef) IsTest() bool { return strings.HasPrefix(f.Name, "test") }

// TestFuncs returns all document-level functions that are test functions.
func (d *Document) TestFuncs() []*FuncDef {
	var out []*FuncDef
	for _, fn := range d.Functions {
		if fn.IsTest() {
			out = append(out, fn)
		}
	}
	return out
}

// FuncBlock is the body of a block-form function.
type FuncBlock struct {
	Stmts  []Node // VarStmt, AssignStmt, ToggleStmt, EmitStmt, CallStmt, etc.
	Return Node   // return expression (nil for void functions)
}

// SplitMethodName splits a dotted function name into type and method parts.
// Returns ("int", "sqrt", true) for "int.sqrt", or ("", "add", false) for plain names.
func SplitMethodName(name string) (typeName, method string, ok bool) {
	if before, after, ok0 := strings.Cut(name, "."); ok0 {
		return before, after, true
	}
	return "", name, false
}

type Component struct {
	Pos            Pos
	EndLine        int    // line of closing }, for comment filtering
	BraceCol       int    // column of opening {, for comment filtering
	BraceLine      int    // line of opening {, for comment filtering
	Name           string
	Disabled       bool
	Params         []*Param                // params/props declared in ()
	Consts         []*Const                // const declarations
	Data           []*Data                 // var declarations (component-scoped state)
	Functions      []*FuncDef              // func declarations
	Timers         []*Timer                // timer declarations
	EventDecls     []*EventDecl            // @event declarations in ()
	ChildrenType   string                  // return-type position: "", "component", "list<component>", "option<component>", etc.
	Body           []*VisualNode           // default body (or only body for user components)
	PlatformBodies map[string][]*VisualNode // platform-conditional bodies: platform name → visual nodes
	Decls          []Decl                  // ordered declarations including interleaved comments
}

type Param struct {
	Pos           Pos
	Name          string
	Default       Expr
	Required      bool
	Disabled      bool
	Bidirectional bool      // :name — desugars to param + change event
	Enum          []string  // optional enum constraints (e.g., enum(text, password, number))
	Resolved      *TypeInfo // populated by checker
}

type App struct {
	Pos      Pos
	Children []*VisualNode
}

// Timer declares a recurring interval that executes statements while active.
type Timer struct {
	Pos      Pos
	Interval Expr   // duration literal (e.g., 100ms, 1s)
	Active   string // name of bool var controlling start/stop
	Body     Node   // StmtBlock of mutation statements
	Disabled bool
}

type VisualNode struct {
	Pos        Pos
	EndLine    int // line of closing } (set by parser)
	BraceCol   int // column of opening { (set by parser, for comment filtering)
	Component  string
	HasBody    bool // true when { } was present in source (even if empty)
	HasProps       bool // true when () was present in source (even if no props)
	MultilineProps bool // true when prop list spans multiple lines
	Disabled   bool
	ID         string // element ID from #id syntax (empty = no ID)
	Key        *Expr
	Class      *Expr
	If         *Expr
	For        *ForClause
	Ref        *Expr
	Props      map[string]Expr
	Events     map[string]Expr
	Bindings   map[string]Expr // :name=var — bidirectional binding (desugars to prop + event)
	PropOrder  []string        // insertion order of props/events/bindings (prefix: @=event, :=binding)
	Children []*VisualNode
}

// DeclPos implementations for types that can appear in Decls slices.
func (s *StructDef) DeclPos() Pos  { return s.Pos }
func (e *EnumDef) DeclPos() Pos    { return e.Pos }
func (u *UnitDef) DeclPos() Pos    { return u.Pos }
func (s *StyleDecl) DeclPos() Pos  { return s.Pos }
func (c *Const) DeclPos() Pos      { return c.Pos }
func (d *Data) DeclPos() Pos       { return d.Pos }
func (f *FuncDef) DeclPos() Pos    { return f.Pos }
func (t *Timer) DeclPos() Pos      { return t.Pos }
func (i *Import) DeclPos() Pos     { return i.Pos }
func (o *Output) DeclPos() Pos     { return o.Pos }
func (c *Component) DeclPos() Pos  { return c.Pos }
func (vn *VisualNode) DeclPos() Pos { return vn.Pos }
func (a *App) DeclPos() Pos         { return a.Pos }


// StyleFields extracts style attributes from Props["style"] if it exists and is a StructExpr.
// Returns nil if no style prop or if it's not an anonymous struct literal.
func (vn *VisualNode) StyleFields() map[string]Expr {
	if vn.Props == nil {
		return nil
	}
	styleProp, ok := vn.Props["style"]
	if !ok {
		return nil
	}
	se, ok := styleProp.SNGL.(*StructExpr)
	if !ok || se == nil {
		return nil
	}
	m := make(map[string]Expr, len(se.Fields))
	for _, f := range se.Fields {
		if !f.Spread {
			m[f.Name] = Expr{SNGL: f.Value}
		}
	}
	return m
}

