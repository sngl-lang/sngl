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
	Macros  []*Func
	Outputs []*Output
	// BuildConsts are the consts only a build reads: on a target that unrolls
	// its views one document at a time, a const a view loop walks is a value
	// that loop is unrolled against, and no backend declares it.
	BuildConsts []*Var `json:",omitempty"`
	// RootComponent is the component a test harness isolated as the whole
	// program, having cleared the body and the windows around it. Empty for
	// every ordinary build, where a window is the root and a component is
	// only ever a component.
	RootComponent string `json:",omitempty"`
	Contexts      []*Context
	Symbols       *SymbolTable
	// Origin is where a package the program reads by path came from: the
	// program's own, a directory import, a fetched one. Nil for a library
	// package and a native scheme's shell. A build-time function asks it who
	// is reaching the host, and what code a stored answer depends on.
	Origin *PackageOrigin `json:"-"`
	// Schemes are the import schemes the package declares with gen.scheme,
	// taken out of its body where they were written. An importer reaches
	// them, and everything its imports reach, through Imports.
	Schemes []*Scheme `json:"-"`
	// CLinks are the C headers and flags the package's `c.link` directives
	// name: what a cgo preamble includes and links for the `#[cnative]`
	// declarations beside them.
	CLinks []*CLink `json:",omitempty"`

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
	TreeKinds map[*Component]bool `json:"-"`

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
	// MutatedVars records every Var that is written after it is bound --
	// assigned to, assigned through (a field or an element of it), had its
	// address taken, or handed to a call that writes through its argument.
	//
	// What reads it is a backend with value semantics to preserve: Kotlin
	// copies a struct on binding so a mutation cannot reach whoever else holds
	// it, and a binding nothing writes needs no copy. Conservative by
	// construction -- anything that might write marks it -- because a missing
	// copy aliases two names and a spurious one only costs a shallow clone.
	//
	// Filled in lowering, like AddressedVars, and late: a pass that rewrites a
	// body can introduce an assignment, and an answer computed before that
	// would be stale.
	MutatedVars map[*Var]bool

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

	// Teardown is the handler a platform calls when the program is going away,
	// or nil where no effect declared an @unmount. Nothing guarantees it runs
	// -- a killed process and a closed tab both skip it -- so what belongs in
	// it is what a healthy exit should release and never what correctness
	// depends on. An entry point calls what is here rather than looking a name
	// up, because whether there is anything to call is the lowering's answer.
	Teardown *Func `json:"-"`

	// Mounts are the settle handlers the effect lowering appended to the
	// owner's body, in the order the program placed the brackets. The body
	// statement is what most targets run; a target whose body is not
	// executable -- a RenderModel, whose body became a pure View -- drops it,
	// and calls these where its model is built instead. Recorded for the same
	// reason Teardown is: whether there is anything to call, and what it is
	// called, are the lowering's answers and not a name to look up.
	Mounts []*Func `json:"-"`

	// Run is the build's function for the target's `@run` handler: it takes
	// the command line and the host's `run`, and the host's own entry point
	// calls it before anything the program renders exists. Nil where the
	// program wrote none, and the host starts as it always has.
	Run *Func `json:"-"`
}

// RootMounts are the first settles of the brackets written at the root of a
// file: the calls the effect lowering appended to the package body. A target
// that emits its windows from the package body and drops the rest of it --
// fyne and gtk4 -- runs these once its entry window's widgets exist, which is
// where the body would have run them.
func (p *Package) RootMounts() []Stmt {
	if p == nil || len(p.Mounts) == 0 {
		return nil
	}
	var out []Stmt
	for _, st := range p.Body {
		cs, ok := st.(*CallStmt)
		if ok && cs.Call != nil && slices.Contains(p.Mounts, cs.Call.Func) {
			out = append(out, st)
		}
	}
	return out
}

// EntryPoints are the handlers a platform calls from its own scaffolding
// rather than from anything in the IR: the teardown, the store's settle, the
// `@run` function and the effect mounts. Each is recorded rather than named because whether it
// exists and what it is called are the lowering's answers -- which is exactly
// what makes them invisible to any walk that follows calls, the tree-shaker
// included.
//
// Nil entries are skipped, so a caller may walk the result without guarding.
func (p *Package) EntryPoints() []*Func {
	if p == nil {
		return nil
	}
	out := make([]*Func, 0, 3+len(p.Mounts))
	for _, fn := range append([]*Func{p.Teardown, p.RemoteSettle, p.Run}, p.Mounts...) {
		if fn != nil {
			out = append(out, fn)
		}
	}
	return out
}

// AsyncKickerEntry records one async-reactive kicker produced by NoAsyncReactive.
type AsyncKickerEntry struct {
	Func         *Func    // the $compute_X kicker func (async, void)
	OrigComputed string   // name of the original computed func (e.g. "greeting")
	StateVarName string   // name of the synthetic state var (e.g. "__async_greeting")
	Deps         []string // sorted names of reactive state vars whose mutation should re-fire the kicker
}

// RootDecl returns the component RootComponent names, or nil for every
// ordinary build.
func (p *Package) RootDecl() *Component {
	if p == nil || p.RootComponent == "" {
		return nil
	}
	for _, c := range p.Components {
		if c != nil && c.Name == p.RootComponent {
			return c
		}
	}
	return nil
}

// IsProgram reports whether the package is something to build rather than a
// library to import: whether its body renders a node. The package body is the
// application's view, and every node written there is a member of `root` -- a
// window, a root component's instance, a generated family's host -- so a
// package whose body renders nothing has nothing to show, and one whose only
// root component nobody instantiates renders nothing either.
func (p *Package) IsProgram() bool {
	if p == nil {
		return false
	}
	found := false
	_ = WalkStmts(p.Body, func(s Stmt) error {
		if _, ok := s.(*NodeInst); ok {
			found = true
			return SkipAll
		}
		return nil
	})
	return found
}

// usesTreeRole reports whether a member of the tree carrying kind reaches this
// package. Matched on the mark rather than on a name, because a tree is its
// declaration: a program's own `struct shape` is not the one sngl:ui/draw
// paints, and carries no mark saying it is.
func (p *Package) usesTreeRole(kind BuiltinKind) bool {
	if p == nil {
		return false
	}
	for f := range p.TreeKinds {
		if isTreeRole(f, kind) {
			return true
		}
	}
	return false
}

// NoteTreeKind records that a member of a family reaches this package.
func (p *Package) NoteTreeKind(f *Component) {
	if p == nil || f == nil {
		return
	}
	if p.TreeKinds == nil {
		p.TreeKinds = map[*Component]bool{}
	}
	p.TreeKinds[f] = true
}

// IsFamilyValue reports whether a handle to c, a member of a family, is a
// value of the family: a record of the props the family declares, read off
// whichever member it holds. A member declaring the family's props is one;
// a member of a family with props that declares none of them composes
// members rather than being one -- `component extras { nav.page… nav.page…
// }` -- and there is no one value for it to be. Composition itself is not the
// question: a member declaring the props may compose others too, and either
// kind is inlined where its family's members are collected.
func IsFamilyValue(c *Component) bool {
	if c == nil || c.Tree == nil {
		return false
	}
	if len(c.Tree.Props) == 0 {
		return true
	}
	for _, fp := range c.Tree.Props {
		for _, p := range c.Props {
			if p.Name == fp.Name {
				return true
			}
		}
	}
	return false
}

// IsFamily reports whether c declares a family: a component that is itself a
// member of `build.family`. The family of families is the one declaration that
// is a member of itself, which is what its #[builtin("treeFamily")] mark says.
func (c *Component) IsFamily() bool {
	return c != nil && c.Tree != nil && c.Tree.Builtin == BuiltinTreeFamily
}

// isTreeRole reports whether f is the family carrying kind. The roles a phase
// asks after are marked on their declarations (#[marks.builtin]), so nothing
// here spells a package and a name: a family renamed or moved keeps its role,
// and a program declaring a `shape` family of its own does not acquire one.
func isTreeRole(f *Component, kind BuiltinKind) bool {
	return f.IsFamily() && f.Builtin == kind
}

// IsDrawShapeTree reports whether f is the drawing family.
func IsDrawShapeTree(f *Component) bool { return isTreeRole(f, BuiltinTreeShape) }

// IsUITree reports whether f is the widget family.
func IsUITree(f *Component) bool { return isTreeRole(f, BuiltinTreeNode) }

// IsAppRootTree reports whether f is the family a package body accepts.
func IsAppRootTree(f *Component) bool { return isTreeRole(f, BuiltinTreeRoot) }

// IsSegmentedTree reports whether f is a family with its own rendering rules --
// any family but the widget one.
func IsSegmentedTree(f *Component) bool { return f.IsFamily() && !IsUITree(f) }

// TypeFamily is the family t names in a return position or a slot, or nil.
// FamilyArgs is what a member's type, at the type arguments it carries, hands
// its family's type parameters: `page<Pkg, Meta>` is a `_page<Meta>`. A
// member written bare is its defaults, as a struct type is.
func FamilyArgs(m *Component, args []*Type) []*Type {
	if m == nil || len(m.TreeArgs) == 0 {
		return nil
	}
	bindings := map[string]*Type{}
	for i, tp := range m.TypeParams {
		switch {
		case i < len(args):
			bindings[tp.Name] = args[i]
		case tp.Default != nil:
			bindings[tp.Name] = tp.Default
		}
	}
	out := make([]*Type, len(m.TreeArgs))
	for i, a := range m.TreeArgs {
		out[i] = a.Substitute(bindings)
	}
	return out
}

func TypeFamily(t *Type) *Component {
	if t == nil || t.Kind != TypeComponent {
		return nil
	}
	if f, ok := t.Decl.(*Component); ok && f.IsFamily() {
		return f
	}
	return nil
}

// UsesDrawShapes reports whether a member of the drawing tree reaches p.
func (p *Package) UsesDrawShapes() bool { return p.usesTreeRole(BuiltinTreeShape) }

// Import records a resolved import.
type Import struct {
	AST     *ast.Import
	Path    string        // local import path (e.g., "widgets", "go:net/http")
	Alias   string        // effective namespace name
	Replace string        // replacement URL (RHS of =>), empty if not a replace
	Pkg     *Package      // resolved SNGL package (nil for native)
	Native  *NativeImport // non-nil for scheme imports
}

// PackageOrigin is where a package came from.
type PackageOrigin struct {
	// Dir is a directory package's path from the import root, "." for the
	// program's own; it may climb above the root (`../docui`).
	Dir string
	// URI is a fetched package's import, scheme and all.
	URI string
	// Docs are the package's parsed files: what its code is, for a digest of
	// it. Shared, never cloned -- an AST is not edited after it is parsed.
	Docs []*ast.Document
}

// Scheme is an import scheme a package declares: `gen.scheme(name=…,
// @generate(out, importPath) { … })`. Its handler runs in the interpreter when
// a check meets `<name>:<path>`, and what it writes is the package the import
// resolves to.
type Scheme struct {
	Name string
	// Pos is where the gen.scheme is written.
	Pos ast.Pos
	// Handler is the checked @generate handler, and Pkg the checked package
	// it runs in: what the interpreter is built over.
	Handler *EventHandler
	Pkg     *Package
	// Library says the scheme ships in the sngl: tree, compiled into the
	// binary the user chose to run, so its host calls are trusted.
	Library bool
}

// CLink is one `c.link` directive: a header a cgo preamble includes, and the
// compiler and linker flags it needs.
type CLink struct {
	// Include is the header as written between the quotes or brackets of an
	// #include; System says brackets.
	Include string
	System  bool
	CFlags  []string
	LDFlags []string
}

// ReachedCLinks is every c.link pkg and the packages it imports carry, each
// once, in the order an import first reaches it.
func ReachedCLinks(pkg *Package) []*CLink {
	var out []*CLink
	seen := map[*Package]bool{}
	var walk func(p *Package)
	walk = func(p *Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p.CLinks...)
		for _, imp := range p.Imports {
			walk(imp.Pkg)
		}
	}
	walk(pkg)
	return out
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
	// SpecializedFor is the target whose override body was swapped into Block,
	// so a second swap for the same target is a no-op rather than a reset of
	// the block lowering has since rewritten.
	SpecializedFor string `json:",omitempty"`
	// Purity is what the purity fixpoint inferred from the body, and drives
	// the optimizer. Const is what the declaration says: `const func`, a
	// contract the checker holds the body to and callers may rely on. Only
	// Const makes a call a compile-time value (IsConst).
	Purity Purity
	Const  bool `json:",omitempty"`
	IsTest bool
	Reads  []*Var // vars read (directly or via called functions)
	Writes []*Var // vars mutated (directly or via called functions)
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
	CanError       bool // set by effect analysis; true if body raises or calls a CanError func
	IsAsync        bool // for native imports: declared async (e.g. TS Promise<T>). For SNGL funcs: set by effect analysis when body transitively calls an IsAsync func.
	// NativeMethod is the `method` flag on a native mark: the identifier is
	// invoked *on* its first argument rather than passed it. A host API is one
	// shape or the other and the name cannot say which -- cairo takes the
	// context first, a CanvasRenderingContext2D method is called on it.
	NativeMethod bool `json:",omitempty"`
	// NativeNamedArgs is the `named` flag: the call passes its arguments by
	// name, using this declaration's own parameter names. A host function with
	// defaults in the middle of its parameter list needs it -- reaching a
	// later one positionally means supplying every default before it.
	NativeNamedArgs bool `json:",omitempty"`
	// NativeSchedules is the `schedules` flag: the host identifier invokes a
	// callback it is handed from the loop it owns, rather than inline on the
	// caller's thread. On a target that draws on one thread that loop *is* the
	// drawing thread, so such a callback is a body passAsyncOffload has to be
	// able to enter -- see nativeCallbackFuncs.
	//
	// Stated rather than inferred, for the reason a foreign declaration's body
	// is never read: nothing about a call's shape says when the callee runs it.
	NativeSchedules bool   `json:",omitempty"`
	Doc             string // doc comment for scheme-imported decls
	Synthesized     bool   `json:"-"` // true if generated by a lower pass (e.g. passReactivity __renderSlotN)
	// SlotRender says this is a reactive slot's __renderSlot<N>: a body of
	// node operations that renders into the `parent` its one parameter names.
	// Synthesized does not answer that -- an effect's settle halves and the
	// focus-order navigation funcs are synthesized too, and take no parameter
	// at all -- and the platforms that give a slot render a host-typed
	// container parameter were giving those one as well.
	SlotRender bool `json:"-"`
	// AsyncHoist says this is a computed passAsyncReactive synthesized for an
	// async subexpression a view prop read: a zero-argument body returning it,
	// lowered as a named async computed is. Said here rather than read off the
	// `__hoist_` its name is spelled with.
	AsyncHoist bool `json:"-"`
	// Stdlib is true for functions declared in the SNGL standard library
	// (lib/*.sngl). A user declaration may shadow a stdlib method of the same
	// name on the same receiver; two user declarations of it may not.
	Stdlib bool
	// Nested says the declaration was written inside another *function* body
	// and hoisted out of it. A func at the root of a window body is hoisted
	// too and is not this: it is the window's own.
	Nested bool `json:"-"`
	// MutatesReceiver says a call writes through its first argument in place,
	// so reactivity treats a statement-level call as a write to the receiver's
	// variable and a backend emits an in-place mutation.
	MutatesReceiver bool
	// BuildOnly says only the build's evaluator answers this intrinsic: no
	// target emits a call to it, so a call left unfolded is a build error.
	BuildOnly bool
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

	// NodeHandle marks the binding a `#id` on a visual node declares: a handle
	// to the rendered instance, which every target stores wherever it keeps the
	// tree rather than as a local. The marker is the declaration's, because
	// IsElementRef is a fact about a *reference* (ir.Ident) and a read of a
	// program-written `#id` does not carry it -- only the `__nN` handles a
	// lowering pass synthesizes do. Reading the reference is what left a
	// `#[cnative]` method call emitting `C.gtk_progress_bar_pulse(bar)` against
	// a field the same file declares as `m.bar`.
	//
	// It answers *where the handle lives* and nothing about what may be read
	// off it. A prop read still does not compile on fyne or gtk4 -- there is no
	// getter counterpart to the platform hook the write side uses -- so the
	// only read that works on those is a native call's receiver.
	NodeHandle bool `json:"NodeHandle,omitempty"`

	// Cell marks the state lowering gives a two-way prop the call site left
	// unbound (UnboundProps): a var of the component passImplicitState wraps
	// the node in. A read of the prop off the node's `#id` names it, from
	// outside that component, so the inliner repoints the read at the one
	// copy it splices. Copied with the var, so a clone says it too.
	Cell bool `json:"-"`
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
	// The three below travel for the same reason Vars does. BodyDecls is what
	// links a component nested in this body to its owner (ir.BodyOwners).
	BodyDecls []Symbol `json:"-"`
	Funcs     []*Func  `json:"-"`
	// Methods is the receiver's member table, per body rather than merged, so
	// two targets may each write a `func helper`.
	Methods map[string]*Func `json:"-"`
	// Const is the override's own `const` prefix. The component a target
	// renders is const when its base declaration is or this is.
	Const bool `json:",omitempty"`
	// Pkg is the package a program's override is written in, which is who
	// a handler in it runs as at build time -- a family's gen.emit
	// @generate. Nil for a target package's override, which is library
	// source and trusted.
	Pkg *Package `json:"-"`
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
	// Tree is the family this component is a member of, named in its return
	// position: a component that is itself a member of `build.family`. Nil for
	// a component that belongs to no family: it may be placed in any of them
	// and may contain none of their members.
	Tree *Component `json:"-"`
	// TreeArgs are the type arguments the return position hands a generic
	// family, in terms of the member's own type parameters: `page<T, M>`
	// returning `_page<M>` holds M here. Nil for a family that takes none.
	TreeArgs []*Type `json:"-"`
	// Treeless is the #[tree.none] mark: the declaration belongs to no family
	// and says so. Nil Tree without it is a declaration that forgot to name
	// one, which is an error, so the two states are told apart here rather
	// than by the absence of a pointer.
	Treeless bool `json:",omitempty"`
	// Eventless is the #[tree.eventless] mark on a family: its members raise
	// no events, an #[intrinsic] primitive's being the target calling in.
	Eventless bool `json:",omitempty"`
	// TreeParam is the component's own type parameter written in the return
	// position, for a wrapper whose family is whatever it was handed. Nil Tree
	// and a TreeParam is a third state: tree-less at the declaration, and a
	// member of whatever its children turn out to be at each call site.
	TreeParam string `json:",omitempty"`
	// Bodyless is a declaration written with no block at all, as against one
	// written `{}`, which renders nothing. The two are different declarations
	// and only this says which: ast.StmtBlock.IsDefined() reports whether a
	// block came from source, so a component Convert rebuilt has none either
	// way, and reading it as "has a body" printed every empty-bodied component
	// back as a signature.
	Bodyless bool `json:",omitempty"`
	// Const is the `const` prefix: the render depends only on the props. An
	// override's is its base's or its own.
	Const bool `json:",omitempty"`
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
	// RuntimeInstance says the inliner met an instantiation of this component
	// it could not flatten -- one inside reactive control flow, or in a
	// recursive cycle -- so instances of it are built while the program runs.
	//
	// Recorded by the pass that made the decision, because nothing downstream
	// can retell it: a component stays on Package.Components for several
	// reasons, and "still declared" is not the same question as "instantiated
	// at run time". Answering the first for the second gave a factory to every
	// component the page renders as markup.
	RuntimeInstance bool `json:",omitempty"`
	// SpecializedFor is the target whose override body was swapped into Body
	// and Vars, so a second swap for the same target is a no-op instead of a
	// reset of everything lowering has since added.
	SpecializedFor string `json:",omitempty"`
	// DeclaredBody is the body the declaration itself writes, kept the first
	// time an override is swapped into Body: a node a target renders with the
	// declaration's own body rather than its override -- a nav.link in a
	// surface that navigates in place, on a target whose links are addresses
	// -- reads it here. Nil until a swap, when Body still is it.
	DeclaredBody []Stmt `json:"-"`
	// WildcardInto names the prop the matched name binds to, from the mark's
	// second argument. Without it the name a wildcard matched reaches nothing:
	// the component was resolved by a name it has no way to read.
	WildcardInto string `json:",omitempty"`
	// Gen is what the sngl:x/gen marks said about what this declaration
	// generates: a target's build node carries the capabilities of the target,
	// and a platform primitive carries what its own rendered nodes support.
	// Nil for the components that are neither, which is nearly all of them.
	Gen *GenCaps `json:",omitempty"`
	// Pkg is the declaring package URI; see StructDef.Pkg.
	Pkg string
	// TypeParams are the component's generic parameters, in declaration
	// order -- ["T"] for `component effect<T>(on T)`. Bound at the call site
	// from the props supplied there, the same way a func's are bound from its
	// arguments.
	TypeParams []TypeParam `json:",omitempty"`
	Props      []*Prop
	Events     []*EventDecl
	Slots      []*SlotDecl `json:",omitempty"`

	ChildrenType *Type
	Vars         []*Var
	Funcs        []*Func
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
	// BodyDecls are the declarations written inside this component's body: a
	// struct, enum, unit or component. Each is an ordinary member of the
	// package collection its kind lands in; this records which body bound its
	// name, so pass2 can rebind it in the scope pass1 declared it in.
	BodyDecls []Symbol `json:"-"`
}

// RestSlot is the slot the children a caller writes bare go to, or nil for a
// component that accepts none.
func (c *Component) RestSlot() *SlotDecl {
	if c == nil {
		return nil
	}
	for _, s := range c.Slots {
		if s.Rest {
			return s
		}
	}
	return nil
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
	// Construct is the #[macro.construct] mark: the prop is read while the
	// instance is being built and never again, so writing it afterwards would
	// reach nothing. Lowering gives such a prop no setter and rebuilds the
	// instance when its value changes. False for an ordinary prop, which the
	// instance absorbs in place.
	Construct bool `json:",omitempty"`
	// Const is the `const` prefix: the argument at every call site is a
	// compile-time value, and so is the default. Sym.Const mirrors it.
	Const bool `json:",omitempty"`
	// Sym is the Param that Idents referring to this prop inside the
	// component body resolve to. A prop is declared into the body's scope as
	// a parameter; the checker visits a component's bodies more than once, so
	// the symbol is minted once here rather than per pass.
	Sym *Param `json:"-"`
}

// EventDecl is a resolved event declaration on a component.
type EventDecl struct {
	Name string
	// Params are what a handler receives, in order: the FuncSig shape. A
	// handler binds them by position, the way a func literal binds its
	// parameters, and may stop short -- so unlike a slot's, the names here are
	// documentation rather than contract, and an entry with none carries "".
	// `@change T` is one unnamed entry and `@done()` is none. A bare `@tick`
	// is one unnamed `dyn` entry, the loose payload it has always carried.
	Params []*Param `json:",omitempty"`
	// Wildcard is the pattern this event answers to beyond its own name, from
	// #[wildcard]. Empty for an ordinary event.
	Wildcard string `json:",omitempty"`
}

// Payload is the type of the event's only parameter, or nil when it does not
// declare exactly one. A host widget's event -- a DOM click, a Fyne callback,
// a boundary's error -- hands its handler one value, and this is what the code
// dispatching one reads.
func (e *EventDecl) Payload() *Type {
	if e == nil || len(e.Params) != 1 {
		return nil
	}
	return e.Params[0].Type
}

// SlotDecl is a resolved slot declaration: a parameter whose type is a
// component type.
type SlotDecl struct {
	Name string
	// Params are what the slot is invoked with. They carry names as well as
	// types, the way FuncSig.Params does, because a slot's structural match is
	// by name -- so a name written in the type is part of the contract and
	// renaming one is a breaking change, not an edit to a comment. An entry
	// with no name written carries "".
	Params []*Param `json:",omitempty"`
	// Content is what each supplied node must be; Card is how many are
	// accepted. Absent, a slot takes any number of components.
	Content *Type    `json:",omitempty"`
	Card    SlotCard `json:",omitempty"`
	// Rest says the slot was declared `...component`: it collects the children
	// a caller writes bare, rather than being populated by name. At most one
	// per component, and a component without one accepts no children at all.
	Rest bool `json:",omitempty"`
	// Slots are the invocation list's component-typed entries: content an
	// insertion populates by name and a population binds by position. Index
	// is where an entry sits in its slot's invocation list, which Params and
	// Slots each hold only their half of.
	Slots []*SlotDecl `json:",omitempty"`
	Index int         `json:",omitempty"`
	// Const is the `const` prefix on the slot parameter: a population is held
	// to a const component's render rule.
	Const bool `json:",omitempty"`
}

// Arity is how many names a population of the slot binds.
func (s *SlotDecl) Arity() int { return len(s.Params) + len(s.Slots) }

// EntryAt is the component entry at position i of the invocation list, or nil
// when i is a value; value is then that value's index into Params.
func (s *SlotDecl) EntryAt(i int) (entry *SlotDecl, value int) {
	value = i
	for _, e := range s.Slots {
		if e.Index == i {
			return e, -1
		}
		if e.Index < i {
			value--
		}
	}
	return nil, value
}

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

// Timer represents a timer declaration at the component or package level.
// The timer body is a Func so codegen can reuse function transform logic.
// Output is one language/platform pair a build directive names, with the
// options it carries. Only permitted in the program's own package.
//
// It is a projection of the directive's component tree and not the tree
// itself: Options is the three levels of that tree flattened into the one
// record a build reads, which is what every consumer wants. LangComp and
// PlatComp are the two nodes it came from, kept so ir.Convert can put each
// option back at the level it was written -- a target's own props are the only
// record of which level that is.
type Output struct {
	AST      *ast.VisualNode
	Lang     string
	Platform string
	Options  *StructLit
	LangComp *Component `json:"-"`
	PlatComp *Component `json:"-"`
	// Run is the `@run` handler written on the platform's node, when the
	// platform declares one and the program wrote it: the process start,
	// wrapped. The build makes it the package's Run function.
	Run *EventHandler `json:"-"`
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
	// Const is the `const` prefix: the argument is a compile-time value at
	// every call site, so a read of the parameter is one too (IsConst).
	Const bool `json:",omitempty"`
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
	// Anon says the declaration was synthesized for an anonymous struct, one
	// per canonical field signature. It is an ordinary declaration in every
	// other respect — a backend emits it and a value names it — and this only
	// says the program never wrote the Name: a diagnostic and `sngl dump`
	// spell the type structurally rather than leaking the synthesized name.
	Anon bool `json:",omitempty"`
	// BodyOwner names the component or function whose body declared this type,
	// empty for a top-level one. Two bodies may each declare "Local" and the
	// two are distinct types, but both land in this one Package and the host
	// namespace is flat -- so passHoistBodyTypes renames the later one after
	// its owner.
	BodyOwner string `json:",omitempty"`
	// A declaration's members are looked up on the declaration, so this is
	// where every type's methods live — struct, enum, unit and component
	// alike.
	Methods map[string]*Func `json:"-"`
}

func (s *StructDef) SymName() string { return s.Name }

// FieldList is StructDef's half of Fielded. A declaration's members are looked
// up on the declaration, and a field is a member like a method is.
func (s *StructDef) FieldList() []*StructField { return s.Fields }

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
	// DefaultWritten records that the declaration gave this field a default.
	// Normalize otherwise erases the distinction by filling every nil Default
	// with the type's zero, and DeclaredDefault needs it: a field the
	// declaration said nothing about has to stay absent, so that "unset" goes
	// on meaning unset to a backend that reads presence.
	DefaultWritten bool `json:",omitempty"`
	Foreign        `json:"Foreign,omitzero"`
}

// EnumDef is a resolved enum type declaration.
type EnumDef struct {
	AST       *ast.EnumDef
	Name      string
	Members   []*EnumMember
	Foreign   `json:"Foreign,omitzero"`
	Pkg       string           // declaring package URI; see StructDef.Pkg
	Doc       string           // doc comment for scheme-imported decls
	BodyOwner string           `json:",omitempty"` // see StructDef.BodyOwner
	Methods   map[string]*Func `json:"-"`
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
	// Fields are the unit's members, looked up the way a struct's are: one per
	// base suffix, because a unit value is a magnitude per base and that is
	// what every backend emits it as. A single-base unit has none -- its value
	// is a plain number on every target, with nothing to select -- and
	// float(x)/int(x) is how the magnitude is read there.
	Fields    []*StructField
	Pkg       string           // declaring package URI; see StructDef.Pkg
	Builtin   BuiltinKind      // compiler built-in marker; BuiltinNone otherwise
	BodyOwner string           `json:",omitempty"` // see StructDef.BodyOwner
	Methods   map[string]*Func `json:"-"`
}

func (u *UnitDef) SymName() string           { return u.Name }
func (u *UnitDef) FieldList() []*StructField { return u.Fields }
func (u *UnitDef) SymType() *Type            { return &Type{Kind: TypeUnit, Decl: u} }

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
