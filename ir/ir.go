package ir

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
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
	// Macros are the package's `func X(...) Macro` declarations. A macro is
	// not a function — it is written as a `#[...]` mark and never called — so
	// it is kept here rather than bound in scope. The declaration is what says
	// a macro exists, what arguments it takes and what it does; the compiler
	// adds only an implementation for the ones it implements.
	Macros   []*Func
	Windows  []*Window
	Timers   []*Timer
	Outputs  []*Output
	Contexts []*Context
	Symbols  *SymbolTable

	// Body is what the package itself renders: visual nodes written at the top
	// level, outside any component or window. The package is then a state
	// owner like the other two -- Vars is the state this body reads, the way
	// Component.Vars is for Component.Body.
	//
	// Nothing fills it yet. It exists so the owners are three of a kind before
	// the syntax that populates it lands, because every consumer that asks
	// "which declarations own state" has to be able to name the package
	// without a special case (#135).
	Body []Stmt `json:",omitempty"`

	// TreeKinds records the segmented trees whose members
	// this package declares or imports. The lowering pass for a tree gates on
	// it: an import of sngl:ui/draw is neither necessary (a package may declare
	// its own shapes) nor sufficient (inlining flattens a canvas out of the
	// package that imported it), so the declarations are the only honest
	// signal.
	TreeKinds map[*StructDef]bool `json:"-"`

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
	// UsesRemote says the program holds a query box. Such a box settles after
	// the render that started it, and tells the store when it does, so an entry
	// point that never asks shows the fetch starting and nothing after.
	UsesRemote bool

	// RemoteSettle is the handler the reactivity lowering left for a store to
	// call, or nil where it left none -- a target with native reactivity has no
	// updaters to re-run, and one that never fetches has nothing to re-run them
	// for. An entry point subscribes what is here rather than looking a name up,
	// because whether the handler exists is the lowering's answer and so is what
	// it is called.
	RemoteSettle *Func `json:"-"`
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

// UsesTree reports whether a member of the tree that pkg declares as name
// reaches this package. Matched on the declaring package as well as the name,
// because a tree is its declaration: a program's own `struct shape` is not the
// one sngl:ui/draw paints.
func (p *Package) usesTree(pkg, name string) bool {
	if p == nil {
		return false
	}
	for sd := range p.TreeKinds {
		if sd.Pkg == pkg && sd.Name == name {
			return true
		}
	}
	return false
}

// NoteTreeKind records that a member of a tree reaches this package.
func (p *Package) NoteTreeKind(sd *StructDef) {
	if p == nil || sd == nil {
		return
	}
	if p.TreeKinds == nil {
		p.TreeKinds = map[*StructDef]bool{}
	}
	p.TreeKinds[sd] = true
}

// isTreeNamed reports whether sd is the tree that pkg declares as name.
func isTreeNamed(sd *StructDef, pkg, name string) bool {
	return sd != nil && sd.IsTree && sd.Pkg == pkg && sd.Name == name
}

// The drawing tree is sngl:ui/draw's `shape`, and this is the only place the
// compiler spells it. passCanvas emits that package's own primitives, so it is
// the one tree there are rules about; a tree that carried its own would need
// none of this.
const (
	drawPkg   = "sngl:ui/draw"
	shapeTree = "shape"
)

// IsDrawShapeTree reports whether sd is the drawing tree.
func IsDrawShapeTree(sd *StructDef) bool { return isTreeNamed(sd, drawPkg, shapeTree) }

// UsesDrawShapes reports whether a member of the drawing tree reaches p.
func (p *Package) UsesDrawShapes() bool { return p.usesTree(drawPkg, shapeTree) }

// Import records a resolved import.
type Import struct {
	AST     *ast.Import
	Path    string        // local import path (e.g., "widgets", "go:net/http")
	Alias   string        // effective namespace name
	Replace string        // replacement URL (RHS of =>), empty if not a replace
	Pkg     *Package      // resolved SNGL package (nil for native)
	Native  *NativeImport // non-nil for scheme imports
}

func (i *Import) SymName() string { return i.Alias }
func (i *Import) SymType() *Type  { return nil }

// NativeImport holds declarations from a scheme import (go:, ts://, etc.).
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

// NativeDeclRef names a foreign declaration the way an encoded value does:
// the scheme it was imported under, the package that declares it, and its name
// there. That triple is what a runtime can say about a value — Go's
// reflect.Type gives the last two — and what a scheme importer keys its
// declarations by.
type NativeDeclRef struct{ Scheme, Path, Name string }

// NativeDecls indexes every foreign declaration a package graph imported, so a
// value that names one can be given the declaration rather than a name to
// match against. A ref that is absent names a type this program never imported
// — not an error, only the absence of a declaration.
type NativeDecls map[NativeDeclRef]Symbol

// IndexNativeDecls builds that index over p and the packages it imports.
func IndexNativeDecls(p *Package) NativeDecls {
	out := NativeDecls{}
	seen := map[*Package]bool{}
	var walk func(*Package)
	walk = func(p *Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		for _, imp := range p.Imports {
			walk(imp.Pkg)
			if imp.Native == nil {
				continue
			}
			scheme, _ := imports.ParseScheme(imp.Path)
			if scheme == "" {
				continue
			}
			for _, sd := range imp.Native.Structs {
				out[NativeDeclRef{scheme, imp.Native.ImportPath, sd.Name}] = sd
			}
			for _, ed := range imp.Native.Enums {
				out[NativeDeclRef{scheme, imp.Native.ImportPath, ed.Name}] = ed
			}
		}
	}
	walk(p)
	return out
}

// Foreign records what a declaration corresponds to outside SNGL: the package
// it lives in there, the name to emit for it, and whether SNGL can model it at
// all. A declaration with an empty Foreign is SNGL's own.
type Foreign struct {
	Path string // scheme-import package path (e.g. "fmt")
	// Name is the reference to emit, spelled the way the target language
	// spells it where it is used — qualified when the importer knows the
	// qualifier ("fmt.Sprintf", "api.Item"), bare otherwise.
	Name string
	// Scheme names the language the Name belongs to, for a correspondence a
	// #[foreign] mark declared. An importer answers the same question with
	// Origin's own type and leaves this empty.
	Scheme string
	// Origin identifies the foreign declaration this was read from, in a type
	// the importer defines. Two files that each resolve the same package get
	// their own *StructDef for one type, and comparing Origin is what makes
	// them the same type again. The importer's own struct type is the key:
	// two importers cannot collide however they spell a name, which a shared
	// string could not promise. Must be comparable — it is compared with ==.
	Origin any
	// Marked says a #[foreign] mark asserted this rather than an importer
	// reading it. The declaration is then still the program's own: a backend
	// emits it, so Name is a name to spell alongside that declaration and
	// never a reference redirecting to one the backend did not emit.
	Marked bool
	// Unusable is non-empty when SNGL cannot model the declaration precisely;
	// the checker rejects any reference to it. Only a foreign declaration can
	// be one: SNGL's own syntax cannot express a type SNGL has no name for.
	Unusable string
}

// Func represents any function: top-level, type-attached method, or lambda.
//
// Foreign is set when the function originates from a scheme import (e.g.
// "go:fmt"); codegen reads it to emit the correct import and call.
// HasContextArg / HasErrorReturn describe shape adapter wrapping applied by
// the importer (leading context.Context stripped; trailing error unwrapped).
type Func struct {
	AST  *ast.FuncDef // nil for lambdas and event handlers
	Name string       // empty for lambdas and event handlers
	// Pkg is the declaring package URI, as StructDef.Pkg is. Empty for a
	// program's own declarations, whose names mean nothing outside a build.
	//
	// A macro needs it: a mark is bound to the declaration it was written from,
	// and that binding is (package, name).
	Pkg      string
	Receiver string // "int" for int.abs (empty for plain funcs)
	// RecvParam is the implicit `this` of a method on a generic built-in whose
	// body is an expression -- `func list<T>.push(item T) => …ListPush(this,
	// item)`. The checker binds it into the body's scope without putting it in
	// Params, because no call site passes one: the receiver arrives as
	// Call.Receiver. Anything substituting arguments for parameters has to bind
	// this one too, and needs a handle on it to do so.
	RecvParam      *Param `json:"-"`
	TypeParams     []TypeParam
	RecvTypeParams []TypeParam // receiver-level: ["T"] for func list<T>.length()
	Params         []*Param
	Return         *Type
	Block          []Stmt // type-checked statements (expression bodies become a single Return)
	// PlatformOverrides holds the body each target implements this function
	// with, keyed by platform. The checker collects every registered target's;
	// ir.SpecializeForTarget swaps the chosen one into Block. Empty for a
	// function nobody overrides. A function has no state of its own, so the
	// Body's Vars are always empty -- it shares the type so one collapse rule
	// covers a component and a function alike.
	PlatformOverrides map[string]Body `json:",omitempty"`
	// LanguageOverrides is the same, keyed by language. A declaration may be
	// overridden on either axis; the platform's wins where both apply.
	LanguageOverrides map[string]Body `json:",omitempty"`
	Purity            Purity
	IsTest            bool
	Reads             []*Var // vars read (directly or via called functions)
	Writes            []*Var // vars mutated (directly or via called functions)
	// Intrinsic is the id a backend implements (e.g. "string.indexOf"). A
	// declaration carrying one and no body is a signature every backend must
	// implement; one carrying a body asserts that the body computes the same
	// answer, so a backend without the id may emit it instead. The body's
	// presence is the whole of that distinction — there is no flag, because a
	// flag beside a body is two records of one fact and they drifted.
	Intrinsic string
	// The tag is load-bearing: without it Foreign.Name and Func.Name collide
	// in the encoder and neither is written.
	Foreign        `json:"Foreign,omitzero"`
	HasContextArg  bool
	HasErrorReturn bool
	CanError       bool   // set by effect analysis; true if body raises or calls a CanError func
	IsAsync        bool   // for native imports: declared async (e.g. TS Promise<T>). For SNGL funcs: set by effect analysis when body transitively calls an IsAsync func.
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
	// LoweredFromTag and LoweredFromEvent record the originating
	// component tag and event name when passDeclarative promotes an
	// inline node-attached handler into a top-level Func. Platforms
	// consume these to rewrite the handler signature/body to match
	// the host widget's callback shape (e.g. fyne's Entry.OnChanged
	// takes a single string param rather than the SNGL InputEvent).
	LoweredFromTag   string `json:"-"`
	LoweredFromEvent string `json:"-"`
	// LoweredFromComponentEvent is the event a *program* wrote, when this
	// handler serves one. A platform override subscribes to its host widget's
	// event and re-raises the component's -- gtk4's button is
	// `@clicked { click() }` -- so by the time a handler is promoted its name
	// is the host's ("clicked") and the name a test can write ("click") is
	// gone. Recorded where the two are still both visible: the substitution
	// that replaces the `click()` emit with the program's own block.
	LoweredFromComponentEvent string `json:"-"`
	// LoweredFromNode is the synthesized node id (`__nN`) the handler was
	// attached to. A tag identifies the *kind* of node and no more, so a
	// platform whose components all lower to a handful of primitives has to
	// ask about the instance to learn anything about it.
	LoweredFromNode string `json:"-"`
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
// Foreign is set when the var originates from a scheme import.
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
	Builtin     BuiltinKind
	Foreign     `json:"Foreign,omitzero"`
	Doc         string // doc comment for scheme-imported decls
	Synthesized bool   `json:"-"` // true if generated by a lower pass (e.g. passReactivity __slotN)
}

func (v *Var) SymName() string { return v.Name }
func (v *Var) SymType() *Type  { return v.Type }

// IsExported reports whether the var/const is part of its package's
// public API.
func (v *Var) IsExported() bool { return isExportedName(v.Name) }

// Body is what a declaration renders: its statements and the state they read.
// The two travel together -- a var belongs to the body that declares it, and a
// body swapped in without its vars reads names nothing declared -- so an
// override carries one of these rather than an entry in each of two maps that
// have to be kept in step.
type Body struct {
	Vars  []*Var
	Stmts []Stmt
}

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
	Builtin BuiltinKind
	// Tree is the segmented tree this component is a member of, named in its
	// return position. Nil for a member of the default tree — an ordinary
	// component, interchangeable with any other.
	Tree *StructDef `json:"-"`
	// Intrinsic is the id from #[intrinsic] on a component: this component is
	// emitted by the platform codegen that answers to the id, not by
	// inlining a body. It is what tells the inliner to leave the component
	// standing — a bodyless component the wrappers lower *to*, rather than a
	// wrapper over one.
	Intrinsic string `json:",omitempty"`
	// Wildcard is the RE2 pattern from #[platforms.wildcard]: a name nobody
	// declared in this component's package namespace resolves to this
	// component when the pattern matches the whole name. Empty for an
	// ordinary component, which only its own name reaches.
	Wildcard string `json:",omitempty"`
	// WildcardInto names the prop the matched name binds to, from the mark's
	// second argument. Without it the name a wildcard matched reaches nothing:
	// the component was resolved by a name it has no way to read.
	WildcardInto string `json:",omitempty"`
	// Pkg is the declaring package URI; see StructDef.Pkg.
	Pkg          string
	Props        []*Prop
	Events       []*EventDecl
	Slots        []*SlotDecl `json:",omitempty"`
	ChildrenType *Type
	Vars         []*Var
	Funcs        []*Func
	Timers       []*Timer
	Body         []Stmt // type-checked body statements
	// PlatformOverrides holds the body each target implements this component
	// with, keyed by platform, and LanguageOverrides the same keyed by
	// language --
	// a declaration may be overridden on either axis, and the platform's wins
	// where both apply.
	//
	// The checker fills these for every registered target, because it does not
	// know which one a build picks; ir.SpecializeForTarget swaps the chosen
	// one into the declaration before the passes that read a body run. nil for
	// a component nobody overrides.
	PlatformOverrides map[string]Body `json:"-"`
	LanguageOverrides map[string]Body `json:"-"`
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
	// Wildcard is the RE2 pattern from #[platforms.wildcard] written on the
	// prop: a prop name nobody declared is accepted at a call site when the
	// pattern matches it whole, and its value checked against Type. Empty for
	// an ordinary prop, which only its own name binds.
	Wildcard string `json:",omitempty"`
	// Sym is the Param that Idents referring to this prop inside the
	// component body resolve to. A prop is declared into the body's scope as
	// a parameter; the checker visits a component's bodies more than once, so
	// the symbol is minted once here rather than per pass.
	Sym *Param `json:"-"`
}

// EventDecl is a resolved event declaration on a component.
type EventDecl struct {
	Name string
	Type *Type // payload type; nil for void events
	// Wildcard is the pattern this event answers to beyond its own name, from
	// #[wildcard]. Empty for an ordinary event.
	Wildcard string `json:",omitempty"`
}

// SlotDecl is a resolved named-slot declaration. Params are types only, so an
// insertion matches them by position.
type SlotDecl struct {
	Name   string
	Params []*Type `json:",omitempty"`
	// Content is what each supplied node must be; Card is how many are
	// accepted. Absent, a slot takes any number of components.
	Content *Type    `json:",omitempty"`
	Card    SlotCard `json:",omitempty"`
}

// DefaultSlot is the name of the slot a caller fills with ordinary children.
// It is a slot like any other; only its content arrives as sugar, which is why
// it cannot be populated by name.
const DefaultSlot = "_"

// SlotCard is how many nodes a slot accepts.
type SlotCard string

const (
	SlotAny      SlotCard = ""         // any number; the default
	SlotOne      SlotCard = "one"      // exactly one, from tree.one<T>
	SlotOptional SlotCard = "optional" // none or one, from option<T>
)

// EventHandler is a resolved event handler. The handler body is represented
// as a Func so codegen can reuse its function transform logic.
type EventHandler struct {
	AST  *ast.EventHandler
	Name string
	Func *Func
	// ComponentEvent is the event a program wrote that this handler ends up
	// serving, when it is a platform override's subscription to its host
	// widget's event. See Func.LoweredFromComponentEvent, which it feeds.
	ComponentEvent string `json:"-"`
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

// TypeParam is one generic parameter of a declaration. Default is what an
// argument list that stops short falls back to.
type TypeParam struct {
	Pos     ast.Pos `json:"-"`
	Name    string
	Default *Type `json:"-"`
}

// ParamName and ParamPos mirror ast.TypeParam's; see the note there.
func (p TypeParam) ParamName() string { return p.Name }
func (p TypeParam) ParamPos() ast.Pos { return p.Pos }

// StructDef is a resolved struct type declaration.
type StructDef struct {
	// IsTree is set by #[tree.kind]: this struct names a segmented tree rather
	// than describing a value, and components name it to say they are members.
	IsTree     bool `json:",omitempty"`
	AST        *ast.StructDef
	Name       string
	TypeParams []TypeParam // generic parameters, e.g. ["T"] for list<T>, ["K","V"] for map<K,V>
	Fields     []*StructField
	Foreign    `json:"Foreign,omitzero"`
	// Pkg is the URI of the package that declared this type, for a package
	// whose identity is global — today the embedded library's "sngl:ui",
	// "sngl:builtin", "sngl:ui/draw". Empty for a program's own
	// declarations, whose names are only meaningful relative to a build.
	// See sameDecl: Pkg and Name are a named type's identity.
	Pkg     string
	Doc     string      // doc comment for scheme-imported decls; empty for SNGL-sourced
	Builtin BuiltinKind // compiler built-in marker (string-repr value type or generic constructor); BuiltinNone otherwise
	// Options is the #[options] mark: this struct is a target's build-option
	// schema. Every lookup of an option schema keys on the mark, so the
	// declaration's name carries no meaning.
	Options bool `json:",omitempty"`
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
	if s.Foreign.Name != "" {
		t.Meta = s.Foreign.Name
	}
	return t
}

// StructField is a resolved field in a struct.
//
// Foreign.Name is the source-language field name (e.g. Go's "Decls" for SNGL
// "decls") when the field comes from a scheme import.
type StructField struct {
	Name    string
	Type    *Type
	Default Expr // nil if no default
	Foreign `json:"Foreign,omitzero"`
}

// EnumDef is a resolved enum type declaration.
type EnumDef struct {
	AST     *ast.EnumDef
	Name    string
	Members []*EnumMember
	Foreign `json:"Foreign,omitzero"`
	Pkg     string           // declaring package URI; see StructDef.Pkg
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
	Pkg      string           // declaring package URI; see StructDef.Pkg
	Builtin  BuiltinKind      // compiler built-in marker; BuiltinNone otherwise
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
	Description() string // short one-line summary for reference docs
}

// Platform is a Target for a registered platform generator.
type Platform interface {
	PlatformIdentifier() string
	Description() string // short one-line summary for reference docs
}

// StaticTarget identifies the compile target by name.
type StaticTarget struct {
	Platform string
	Language string
}

// WildcardPattern implements ir.WildcardSymbol: a component carrying
// #[wildcard] answers to every name its pattern matches.
func (c *Component) WildcardPattern() string { return c.Wildcard }
