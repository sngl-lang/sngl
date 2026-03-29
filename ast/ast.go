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

type Output struct {
	Pos      Pos
	Lang     string            // "go"
	Platform string            // "bubbletea"
	Options  map[string]string // {"package": "main"}
}

type Document struct {
	Outputs            []*Output
	Structs            []*StructDef
	Enums              []*EnumDef
	Imports            []*Import
	Units              []*UnitDef
	Consts             []*Const
	Data               []*Data
	Computeds          []*Computed
	Functions          []*FuncDef
	Components         []*Component
	ImportedComponents []*Component // populated by checker; qualified-name keyed
	Timers             []*Timer
	Styles             []*StyleDecl
	StyleDefs          []*StylePropDef // from "styles" top-level node
	App                *App
	Tests              []*TestDef
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
	for _, c := range d.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// AllComponents returns local and imported components combined.
func (d *Document) AllComponents() []*Component {
	all := make([]*Component, 0, len(d.Components)+len(d.ImportedComponents))
	all = append(all, d.Components...)
	all = append(all, d.ImportedComponents...)
	return all
}

// TestDef declares a test block targeting a component.
// Top-level tests specify a Component name; nested subtests inherit it.
type TestDef struct {
	Pos       Pos
	Component string     // component under test (top-level only)
	Desc      string     // test description
	Body      []Node     // statements: assign, toggle, emit, call (assert), expressions
	Subtests  []*TestDef // nested test blocks
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
	Pos  Pos
	Name string
	Init Expr
}

type EnumDef struct {
	Pos    Pos
	Name   string
	Values []string
}

type StructDef struct {
	Pos    Pos
	Name   string
	Fields []*StructField
}

type StructField struct {
	Pos     Pos
	Name    string
	Type    string // type hint: "string", "bool", "int", etc.
	Default Expr
}

type Import struct {
	Pos       Pos
	Path      string
	Scheme    string // "go", "ts", "proto", "" for directory imports
	Namespace string // last path segment, e.g. "widgets" from "lib/widgets"
}

// NativeDecls holds SNGL-compatible declarations resolved from a native import.
type NativeDecls struct {
	Structs []*StructDef
	Enums   []*EnumDef
	Data    []*Data // extern funcs and vars
}

type Data struct {
	Pos        Pos
	Name       string
	Init       Expr
	Extern     bool     // "extern" positional arg present
	IsFunc     bool     // TypeHint starts with "func"
	ParamTypes []string // parsed func params (e.g., ["string", "int"])
	ReturnType string   // parsed func return type, "" for void
	Trigger    string   // resolved trigger function name, "" for none
}

type Computed struct {
	Pos  Pos
	Name string
	Expr Expr
}

type StyleDecl struct {
	Pos   Pos
	Name  string
	Props map[string]Expr
}

type PropDecl struct {
	Pos      Pos
	Name     string
	TypeHint string   // "string", "bool", "int", "float", "dyn"
	Enum     []string // optional enum constraints
}

type EventDecl struct {
	Pos         Pos
	Name        string
	PayloadType string // "ClickEvent", "InputEvent", etc.
}

type StylePropDef struct {
	Pos      Pos
	Name     string
	TypeHint string
	Enum     []string // optional enum constraints
}

// FuncParam is a parameter in a function definition.
type FuncParam struct {
	Pos  Pos
	Name string
	Type string // type hint: "int", "string", "User", etc.
}

// FuncDef declares a named function.
// Exactly one of Body or Block is set.
type FuncDef struct {
	Pos        Pos
	Name       string
	Params     []*FuncParam
	ReturnType string     // "" for void/action functions
	Body       Expr       // single-expression form (= expr)
	Block      *FuncBlock // block form ({ ... }), nil for expression form
	IsStdlib   bool       // true for stdlib-provided functions (codegens use native implementations)
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
	Pos         Pos
	Name        string
	Params      []*Param     // @param (user-defined components)
	Consts      []*Const     // const declarations
	Data        []*Data      // var declarations (component-scoped state)
	Computeds   []*Computed  // computed declarations
	Functions   []*FuncDef   // func declarations
	Timers      []*Timer     // timer declarations
	PropDecls   []*PropDecl  // @prop (stdlib schemas)
	EventDecls  []*EventDecl // @event (stdlib schemas)
	ChildPolicy string       // @children value: "none"/"one"/"many"/""
	Body        []*VisualNode
}

type Param struct {
	Pos      Pos
	Name     string
	Default  Expr
	Required bool
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
}

type VisualNode struct {
	Pos        Pos
	Component  string
	ID         string // element ID from #id syntax (empty = no ID)
	Key        *Expr
	Class      *Expr
	If         *Expr
	For        *ForClause
	Ref        *Expr
	Props      map[string]Expr
	Events     map[string]Expr
	StyleAttrs map[string]Expr
	StyleBlock map[string]Expr
	AttrNodes  map[string]*AttrNode
	Children   []*VisualNode
}

type AttrNode struct {
	Pos   Pos
	Name  string
	Props map[string]Expr
}
