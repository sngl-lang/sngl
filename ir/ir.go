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
	Contexts   []*Context
	Symbols    *SymbolTable

	// UsesShapes records that this package resolved a list<shape> children
	// type. The canvas passes gate on it: an import of sngl://draw is neither
	// necessary (list<shape> is resolved by the compiler, not by draw) nor
	// sufficient (inlining flattens a canvas out of the package that imported
	// it), so the construct is the only honest signal.
	UsesShapes bool

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

	// MergeStructs lists struct types that need a generated __merge_<Struct>
	// runtime function, recorded by the flatten_struct_spread lowering pass
	// when it rewrites an opaque (non-literal) spread. Deduped by *StructDef.
	MergeStructs []*StructDef

	// SourcePath is the absolute path of the .sngl fixture this package was
	// built from. Set by the CLI test driver so the headless (`none`) test
	// runner can resolve sibling `<fixture>.snapshots/<name>.sngl` goldens
	// for t.snapshot(). Empty when the package was built from merged input or
	// in contexts where no single source file applies.
	SourcePath string

	// UsesI18n, UsesAlert, and UsesErrorHandling are stamped by the
	// passStampUsage lowering pass (always-on, runs last) so codegen reads a
	// field instead of re-walking the whole package. They reflect the
	// fully-lowered IR for the active target — i18n in particular must be
	// computed post-inlining, which the final-pass timing guarantees. Zero
	// values (all false) hold until lowering runs; every codegen path lowers
	// before generating, so readers never observe the unstamped state.
	UsesI18n          bool
	UsesAlert         bool
	UsesErrorHandling bool
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
	Intrinsic      string // non-empty = intrinsic ID (e.g. "string.indexOf"); a backend must implement it unless IntrinsicBodyUsable
	NativePkg      string // scheme-import package path (e.g. "fmt")
	NativeName     string // qualified native ref to emit (e.g. "fmt.Sprintf")
	HasContextArg  bool
	HasErrorReturn bool
	CanError       bool // set by effect analysis; true if body raises or calls a CanError func
	IsAsync        bool // for native imports: declared async (e.g. TS Promise<T>). For SNGL funcs: set by effect analysis when body transitively calls an IsAsync func.
	Unusable       string
	Doc            string // doc comment for scheme-imported decls
	Synthesized    bool   `json:"-"` // true if generated by a lower pass (e.g. passReactivity __renderSlotN)
	// Stdlib is true for functions declared in the SNGL standard library
	// (lib/*.sngl). A user declaration may shadow a stdlib method of the same
	// name on the same receiver; two user declarations of it may not.
	Stdlib bool
	// MutatesReceiver says a call writes through its first argument in place,
	// so reactivity treats a statement-level call as a write to the receiver's
	// variable and a backend emits an in-place mutation.
	MutatesReceiver bool
	// IntrinsicBodyUsable says this function's SNGL body computes the same
	// result the native implementation of Intrinsic would, so a backend that
	// does not implement the id may emit the body. Without it the declaration
	// is a signature only: a backend that cannot emit the id must say so
	// rather than emit a call to something that does not exist.
	IntrinsicBodyUsable bool
	// LoweredFromTag and LoweredFromEvent record the originating
	// component tag and event name when passDeclarative promotes an
	// inline node-attached handler into a top-level Func. Platforms
	// consume these to rewrite the handler signature/body to match
	// the host widget's callback shape (e.g. fyne's Entry.OnChanged
	// takes a single string param rather than the SNGL InputEvent).
	LoweredFromTag   string `json:"-"`
	LoweredFromEvent string `json:"-"`
	// LocalRefs is populated by lower's passNodeEscape (MutationModel
	// platforms only): the set of synthesized widget ref ids (__nN)
	// created in this function's Block that do NOT escape to any other
	// scope. Codegen translators emit these as function-local variables
	// rather than shared Model fields. nil when the pass did not run.
	// See internal/lower/node_escape.go and Component.LocalRefs.
	LocalRefs map[string]bool `json:"-"`
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
	AST      ast.Stmt // original ConstDecl or VarDecl
	Name     string
	Type     *Type
	Init     Expr // checked initializer (nil if none)
	IsConst  bool
	Handlers []*EventHandler
	// Builtin is set by the #[builtin] macro on a predeclared constant
	// (null, PLATFORM, LANGUAGE). The compiler supplies the type and value;
	// the written ones are placeholders.
	Builtin     ast.BuiltinKind
	NativePkg   string
	NativeName  string
	Unusable    string
	Doc         string // doc comment for scheme-imported decls
	Synthesized bool   `json:"-"` // true if generated by a lower pass (e.g. passReactivity __slotN)
}

func (v *Var) SymName() string { return v.Name }
func (v *Var) SymType() *Type  { return v.Type }

// IsExported reports whether the var/const is part of its package's
// public API.
func (v *Var) IsExported() bool { return isExportedName(v.Name) }

// Component represents a resolved component declaration.
type Component struct {
	AST  *ast.ComponentDecl
	Name string
	// Stdlib is true for components declared in the SNGL standard library
	// (lib/*.sngl). Stdlib component props without explicit defaults are
	// optional (rendered as zero-values by the platform); only user-defined
	// component props without defaults are required at call sites.
	Stdlib bool
	// Builtin carries the #[builtin("window")] mark from the declaration, so the
	// checker can recognise a built-in visual node (window) by tag rather than
	// by name. Copied from ComponentDecl.Builtin at registration.
	Builtin      ast.BuiltinKind
	Props        []*Prop
	Events       []*EventDecl
	ChildrenType *Type
	Vars         []*Var
	Funcs        []*Func
	Timers       []*Timer
	Body         []Stmt // type-checked body statements
	// PlatformBodies holds checked IR bodies for `component sngl.X`
	// extensions, keyed by platform name (the identifier from
	// PlatformGenerator.PlatformIdentifier). Populated by the checker's
	// mergePlatformExtensions across *all* registered platforms; consumed
	// by lower's passPlatformExtensionBody, which swaps the active
	// platform's body into Component.Body before remaining lowering
	// passes run. nil for components with no extension declarations.
	PlatformBodies map[string][]Stmt `json:"-"`
	// Native carries platform-provided metadata for components that
	// resolve through a platform's Resolve() (e.g. GIR-loaded GTK
	// widgets). nil for user-defined and stdlib components. Opaque to
	// the checker — consumers cast to a platform-specific shape.
	Native any `json:"-"`
	// LocalRefs is populated by lower's passNodeEscape (MutationModel
	// platforms only): the set of synthesized widget ref ids (__nN)
	// created in this component's Body that do NOT escape to any other
	// scope (updater/handler/slot func). Codegen translators emit these
	// as function-local variables rather than shared Model fields, so a
	// recursive component's render method gets fresh locals per frame.
	// nil when the pass did not run. See internal/lower/node_escape.go.
	LocalRefs map[string]bool  `json:"-"`
	Methods   map[string]*Func `json:"-"`
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
	// Sym is the Param that Idents referring to this prop inside the
	// component body resolve to. A prop is declared into the body's scope as
	// a parameter; the checker visits a component's bodies more than once, so
	// the symbol is minted once here rather than per pass.
	Sym *Param `json:"-"`
	// NativeSetter is the platform-provided setter for this prop
	// (e.g. "gtk_label_set_text" for GtkLabel.label). Empty for props
	// without a platform binding. Opaque to the checker.
	NativeSetter string `json:"-"`
	// NativeReceiverType is the platform cast type the setter's first
	// argument expects (e.g. "GtkEditable" for the gtk_editable_set_text
	// setter on a GtkEntry widget). Empty → use the widget's own type.
	NativeReceiverType string `json:"-"`
	// NativeValueType is the platform value type the setter's value
	// argument expects (e.g. "GtkOrientation" for gtk_orientable_set_orientation).
	// Empty → infer from the SNGL value type.
	NativeValueType string `json:"-"`
}

// EventDecl is a resolved event declaration on a component.
type EventDecl struct {
	Name string
	Type *Type // payload type; nil for void events
	// NativeSignal is the platform-provided signal name (e.g.
	// "clicked" for GtkButton's click event). Empty for events
	// without a platform binding.
	NativeSignal string `json:"-"`
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
	Typ          *Type // instance type of the #[builtin("window")] component; nil if unresolved
	Href         Expr  // checked href expression (folded during optimization)
	Title        Expr  // checked title expression
	Favicon      Expr  // checked favicon expression
	Vars         []*Var
	Funcs        []*Func
	Body         []Stmt        // type-checked body statements
	Checked      bool          // true if body was already checked in context (e.g., inside a for-loop)
	ErrorHandler *EventHandler // optional @error handler; outermost error boundary for this window
	// LocalRefs is populated by lower's passNodeEscape (MutationModel
	// platforms only): the set of synthesized widget ref ids (__nN)
	// created in this window's Body that do NOT escape to any other
	// scope. See internal/lower/node_escape.go and Component.LocalRefs.
	LocalRefs map[string]bool `json:"-"`
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

// ReceiverParam is the surface name of the implicit method receiver (SNGL's
// `this`). The checker binds the desugared receiver under this name so that
// `this` written in a method body resolves to it. It is NOT reserved, so code
// must never use a name match to *identify* the synthetic receiver — test
// Param.Receiver instead (a user may legitimately declare `func f(this int)`).
const ReceiverParam = "this"

// Param is a resolved function or component parameter.
type Param struct {
	Name    string
	Type    *Type
	Default Expr // nil if no default
	// Receiver marks the synthetic first parameter the checker prepends when
	// desugaring a nested method (`func Type.m()`) or a bare component func
	// into top-level form. Codegen/interp/lowering test this flag to recognise
	// the implicit receiver structurally, rather than matching its name.
	Receiver bool
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
	// Origin identifies the foreign declaration this was read from, in a type
	// the importer defines. Two files that each resolve the same package get
	// their own *StructDef for one type, and comparing Origin is what makes
	// them the same type again. The importer's own struct type is the key:
	// two importers cannot collide however they spell a name, which a shared
	// string could not promise. Must be comparable — it is compared with ==.
	Origin  any
	Doc     string          // doc comment for scheme-imported decls; empty for SNGL-sourced
	Builtin ast.BuiltinKind // compiler built-in marker (string-repr value type or generic constructor); BuiltinNone otherwise
	// A declaration's members are looked up on the declaration, so this is
	// where every type's methods live — struct, enum, unit and component
	// alike.
	Methods map[string]*Func `json:"-"`
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
	Doc     string           // doc comment for scheme-imported decls
	Methods map[string]*Func `json:"-"`
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
	Methods  map[string]*Func `json:"-"`
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
}

// StaticTarget identifies the compile target by name.
type StaticTarget struct {
	Platform string
	Language string
}
