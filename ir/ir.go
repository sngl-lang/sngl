package ir

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// isExportedName reports whether name refers to an exported identifier
// under the current export rule. Callers outside this package should use
// the IsExported method on the concrete decl instead — the rule may
// grow beyond a pure name check (e.g. visibility annotations) and
// callers should not bake the current rule into their own logic.
//
// Today: a name is unexported if any dotted segment begins with an
// underscore. "vbox" and "color.rgb" are exported; "_example_vbox",
// "_Test.assert", and "Foo._helper" are not.
func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	for part := range strings.SplitSeq(name, ".") {
		if part == "" || part[0] == '_' {
			return false
		}
	}
	return true
}

// Package is the top-level IR for a checked .sngl package.
type Package struct {
	Imports    []*Import
	Structs    []*StructDef
	Enums      []*EnumDef
	Units      []*UnitDef
	Consts     []*Var
	Vars       []*Var
	Funcs      []*Func
	Components []*Component
	Windows    []*Window
	Timers     []*Timer
	Outputs    []*Output
	Symbols    *SymbolTable

	// LiftedCaptures records, for every lifted closure Func produced by
	// NoLambda, the mapping from each captured Symbol to the synthesized
	// state-struct field name that aliases it. NoReactivity reads this
	// map to resolve `*state.fieldName` mutation sites back to the
	// underlying captured Var. Empty map (not nil) when no lifts have
	// happened.
	LiftedCaptures map[*Func]map[Symbol]string

	// AddressedVars records every Var whose address is taken anywhere in the
	// package — by user code via `&v`, or by NoLambda's lifter when emitting
	// mutable-capture init. NoRef reads this set to decide which Vars to box.
	AddressedVars map[*Var]bool

	// AsyncKickers is populated by the NoAsyncReactive lowering pass. Each
	// entry records a synthetic async kicker func and the original computed
	// name it fires on behalf of. Tasks 8b and 8c use this to wire kickers
	// to reactivity deps and startup calls.
	// Nil (not set) when NoAsyncReactive has not run or found no candidates.
	AsyncKickers []AsyncKickerEntry

	// PointsTo holds funcvar points-to analysis results from
	// analyzePointsTo. Nil before that pass runs; populated afterward.
	PointsTo *PointsToInfo
}

// AsyncKickerEntry records one async-reactive kicker produced by NoAsyncReactive.
type AsyncKickerEntry struct {
	Func         *Func    // the $compute_X kicker func (async, void)
	OrigComputed string   // name of the original computed func (e.g. "greeting")
	StateVarName string   // name of the synthetic state var (e.g. "__async_greeting")
	Deps         []string // sorted names of reactive state vars whose mutation should re-fire the kicker
}

func (p *Package) IsMain() bool {
	return slices.ContainsFunc(p.Components, func(c *Component) bool { return c.Name == "main" })
}

// Import records a resolved import.
type Import struct {
	AST     *ast.Import
	Path    string        // local import path (e.g., "widgets", "go://net/http")
	Alias   string        // effective namespace name
	Replace string        // replacement URL (RHS of =>), empty if not a replace
	Pkg     *Package      // resolved SNGL package (nil for native)
	Native  *NativeImport // non-nil for scheme imports
}

func (i *Import) SymName() string { return i.Alias }
func (i *Import) SymType() *Type  { return nil }

// NativeImport holds declarations from a scheme import (go://, ts://, etc.).
type NativeImport struct {
	ImportPath string
	Structs    []*StructDef
	Enums      []*EnumDef
	Funcs      []*Func
	Vars       []*Var
	// LinkFlags holds linker flags for C imports (e.g. pkg-config --libs output).
	// Empty for non-C imports.
	LinkFlags []string
}

// Func represents any function: top-level, type-attached method, or lambda.
//
// NativePkg/NativeName are set when the function originates from a scheme
// import (e.g. "go://fmt"); codegen reads them to emit the correct import
// and call. HasContextArg / HasErrorReturn describe shape adapter wrapping
// applied by the importer (leading context.Context stripped; trailing error
// unwrapped). Unusable is non-empty when SNGL cannot model the function
// precisely; the checker rejects any reference to such a Func.
type Func struct {
	AST            *ast.FuncDef // nil for lambdas and event handlers
	Name           string       // empty for lambdas and event handlers
	Receiver       string       // "int" for int.abs (empty for plain funcs)
	TypeParams     []string
	RecvTypeParams []string // receiver-level type parameters: ["T"] for func list<T>.length()
	Params         []*Param
	Return         *Type
	Block          []Stmt // type-checked statements (expression bodies become a single Return)
	Purity         Purity
	IsTest         bool
	Reads          []*Var // vars read (directly or via called functions)
	Writes         []*Var // vars mutated (directly or via called functions)
	Intrinsic      string // non-empty = intrinsic ID (e.g. "StrIndexOf"); codegen must provide native impl
	NativePkg      string // scheme-import package path (e.g. "fmt")
	NativeName     string // qualified native ref to emit (e.g. "fmt.Sprintf")
	HasContextArg  bool
	HasErrorReturn bool
	CanError       bool // set by effect analysis; true if body raises or calls a CanError func
	IsAsync        bool // for native imports: declared async (e.g. TS Promise<T>). For SNGL funcs: set by effect analysis when body transitively calls an IsAsync func.
	Unusable       string
	Doc            string // doc comment for scheme-imported decls
}

func (f *Func) SymName() string { return f.Name }
func (f *Func) SymType() *Type  { return &Type{Kind: TypeFunc, Sig: f.FuncSig()} }

// IsExported reports whether the function is part of its package's public
// API. Both the receiver (if any) and the method name must be exported.
func (f *Func) IsExported() bool {
	if f.Receiver != "" && !isExportedName(f.Receiver) {
		return false
	}
	return isExportedName(f.Name)
}

// FuncSig builds the FuncSig for this function.
func (f *Func) FuncSig() *FuncSig {
	return &FuncSig{
		Params:         f.Params,
		Return:         f.Return,
		TypeParams:     f.TypeParams,
		RecvTypeParams: f.RecvTypeParams,
		Purity:         f.Purity,
	}
}

// Var represents a constant or variable declaration.
//
// NativePkg/NativeName are set when the var originates from a scheme import.
// Unusable is non-empty when the var's type cannot be modelled precisely.
type Var struct {
	AST        ast.Stmt // original ConstDecl or VarDecl
	Name       string
	Type       *Type
	Init       Expr // checked initializer (nil if none)
	IsConst    bool
	Handlers   []*EventHandler
	NativePkg  string
	NativeName string
	Unusable   string
	Doc        string // doc comment for scheme-imported decls
}

func (v *Var) SymName() string { return v.Name }
func (v *Var) SymType() *Type  { return v.Type }

// IsExported reports whether the var/const is part of its package's
// public API.
func (v *Var) IsExported() bool { return isExportedName(v.Name) }

// Component represents a resolved component declaration.
type Component struct {
	AST          *ast.ComponentDecl
	Name         string
	Props        []*Prop
	Events       []*EventDecl
	ChildrenType *Type
	Vars         []*Var
	Funcs        []*Func
	Timers       []*Timer
	Body         []Stmt // type-checked body statements
}

func (c *Component) SymName() string { return c.Name }
func (c *Component) SymType() *Type  { return &Type{Kind: TypeComponent, Decl: c} }

// IsExported reports whether the component is part of its package's
// public API.
func (c *Component) IsExported() bool { return isExportedName(c.Name) }

// Prop is a resolved component property.
type Prop struct {
	Name          string
	Type          *Type
	Default       Expr // nil if no default
	Bidirectional bool
}

// EventDecl is a resolved event declaration on a component.
type EventDecl struct {
	Name string
	Type *Type // payload type; nil for void events
}

// EventHandler is a resolved event handler. The handler body is represented
// as a Func so codegen can reuse its function transform logic.
type EventHandler struct {
	AST  *ast.EventHandler
	Name string
	Func *Func
	// CanError is set by effect analysis when the handler body may raise
	// or calls a function that may raise. Codegen uses this to decide
	// whether to emit error-propagation scaffolding for this handler.
	CanError bool
}

// Window represents a window declaration at the root or component level.
type Window struct {
	AST          *ast.VisualNode
	Name         string
	Typ          *Type // *Type{Kind:TypeStruct, Decl: stdlib's Window struct}; nil if unresolved
	Href         Expr  // checked href expression (folded during optimization)
	Title        Expr  // checked title expression
	Favicon      Expr  // checked favicon expression
	Vars         []*Var
	Funcs        []*Func
	Body         []Stmt        // type-checked body statements
	Checked      bool          // true if body was already checked in context (e.g., inside a for-loop)
	ErrorHandler *EventHandler // optional @error handler; outermost error boundary for this window
}

func (w *Window) SymName() string { return w.Name }
func (w *Window) SymType() *Type  { return w.Typ }
func (w *Window) stmtNode()       {} // Window can appear as a statement in for-loop bodies

// Timer represents a timer declaration at the component or package level.
// The timer body is a Func so codegen can reuse function transform logic.
type Timer struct {
	AST      *ast.VisualNode
	Interval Expr // checked interval expression (e.g., 500ms)
	Enabled  Expr // optional bool expression gating the timer
	Handler  *Func
}

// Output represents a resolved output directive. Only permitted in the main file.
type Output struct {
	AST      *ast.VisualNode
	Lang     string
	Platform string
	Options  *StructLit
}

// Param is a resolved function or component parameter.
type Param struct {
	Name    string
	Type    *Type
	Default Expr // nil if no default
}

func (p *Param) SymName() string { return p.Name }
func (p *Param) SymType() *Type  { return p.Type }

// StructDef is a resolved struct type declaration.
type StructDef struct {
	AST        *ast.StructDef
	Name       string
	TypeParams []string // generic type parameters, e.g. ["T"] for list<T>, ["K","V"] for map<K,V>
	Fields     []*StructField
	Native     string // qualified native-language name (e.g. "ast.File"); empty for user-defined
	Doc        string // doc comment for scheme-imported decls; empty for SNGL-sourced
}

func (s *StructDef) SymName() string { return s.Name }

// IsExported reports whether the struct is part of its package's public API.
func (s *StructDef) IsExported() bool { return isExportedName(s.Name) }

func (s *StructDef) SymType() *Type {
	t := &Type{Kind: TypeStruct, Decl: s}
	if s.Native != "" {
		t.Meta = s.Native
	}
	return t
}

// StructField is a resolved field in a struct.
//
// NativeName is the source-language field name (e.g. Go's "Decls" for SNGL
// "decls") when the field comes from a scheme import. Unusable is set when
// the field's native type cannot be modelled; reads/writes are rejected.
type StructField struct {
	Name       string
	Type       *Type
	Default    Expr // nil if no default
	NativeName string
	Unusable   string
}

// EnumDef is a resolved enum type declaration.
type EnumDef struct {
	AST     *ast.EnumDef
	Name    string
	Members []*EnumMember
	Doc     string // doc comment for scheme-imported decls
}

func (e *EnumDef) SymName() string { return e.Name }
func (e *EnumDef) SymType() *Type  { return &Type{Kind: TypeEnum, Decl: e} }

// IsExported reports whether the enum is part of its package's public API.
func (e *EnumDef) IsExported() bool { return isExportedName(e.Name) }

// EnumMember is a single value in an enum.
type EnumMember struct {
	Name  string
	Value Expr // nil for bare members
}

// UnitDef is a resolved unit type declaration with conversion table.
type UnitDef struct {
	AST      *ast.UnitDef
	Name     string
	Suffixes []*UnitSuffix
}

func (u *UnitDef) SymName() string { return u.Name }
func (u *UnitDef) SymType() *Type  { return &Type{Kind: TypeUnit, Decl: u} }

// IsExported reports whether the unit is part of its package's public API.
func (u *UnitDef) IsExported() bool { return isExportedName(u.Name) }

// UnitSuffix is a resolved suffix within a unit declaration.
type UnitSuffix struct {
	Name     string
	Factor   float64 // multiplier reducing this suffix into BaseName (1.0 for bases)
	BaseName string  // the base suffix this one reduces to (== Name when this *is* a base)
}

// IsBase reports whether this suffix is itself a base of its unit
// (i.e. has no `= factor` clause).
func (s *UnitSuffix) IsBase() bool {
	return s != nil && s.BaseName == s.Name
}

// Diagnostic is a structured error or warning with position.
type Diagnostic struct {
	Pos      ast.Pos
	Msg      string
	Severity Severity
}

func (d Diagnostic) Error() string {
	if d.Pos.IsValid() {
		return d.Pos.String() + ": " + d.Msg
	}
	return d.Msg
}

//go:generate go tool stringer -type=Severity

// Severity classifies a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Language is a Target for a registered language translator.
type Language interface {
	LanguageIdentifier() string
	Description() string              // short one-line summary for reference docs
	Package() []*ast.Document         // parsed .sngl API docs (includes Options struct)
	Resolve(identifier string) Symbol // dynamic identifiers (e.g., html.div); nil if unknown
}

// Platform is a Target for a registered platform generator.
type Platform interface {
	PlatformIdentifier() string
	Description() string              // short one-line summary for reference docs
	Package() []*ast.Document         // parsed .sngl API docs (includes Options struct)
	Resolve(identifier string) Symbol // dynamic identifiers (e.g., html.div); nil if unknown
	IsLanguageSupported(Language) bool
}

// StaticTarget identifies the compile target by name.
type StaticTarget struct {
	Platform string
	Language string
}
