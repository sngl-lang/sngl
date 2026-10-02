package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// markImpls binds each macro the compiler implements to the code that runs
// for it, and says nothing else about one. A declaration with no entry here is
// a mark that would resolve and then do nothing.
var markImpls = map[markKey]markImpl{
	{"internal/marks", "builtin"}:   markBuiltin,
	{"internal/marks", "intrinsic"}: markIntrinsic,
	{"tree", "none"}:                markTreeNone,
	{"tree", "crosses"}:             markTreeCrosses,
	{"macro", "wildcard"}:           markWildcard,
	{"macro", "construct"}:          markConstruct,
	{"macro", "foreign"}:            markForeign,
	{"macro", "cnative"}:            markCNative,
	{"macro", "unusable"}:           markUnusable,
	{"language/kotlin", "native"}:   markKotlinNative,
	{"language/go", "native"}:       markGoNative,
	{"language/go", "async"}:        markGoAsync,
	{"language/js", "native"}:       markJSNative,
	{"x/gen", "can"}:                markGenCan,
	{"x/gen", "cannot"}:             markGenCannot,
	{"x/gen", "wants"}:              markGenWants,
	{"x/gen", "renders"}:            markGenRenders,
	{"x/gen", "name"}:               markGenName,
	{"ui/markup/md", "order"}:       markMdOrder,
}

// markMdOrder implements #[md.order]. The md: importer reads it from source
// and sorts by it; what is checked here is only what that reading assumes.
func markMdOrder(m *mark) error {
	f, ok := m.sym.(*ir.StructField)
	if !ok {
		return fmt.Errorf("#[md.order] cannot mark %s; only a Frontmatter field is a key pages are sorted by", ast.DeclFormName(m.decl))
	}
	if n := markedNames(m.decl); n > 1 {
		return fmt.Errorf("#[md.order] marks %d fields at once; pages are sorted by one key", n)
	}
	switch f.Type.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeString:
		return nil
	}
	return fmt.Errorf("#[md.order] on %q: a sort key is an int, a float or a string, not %s", f.Name, f.Type)
}

// markGoAsync implements #[go.async]: a call to this function blocks.
//
// It lands on the same field #[foreign(..., async)] sets, because the two say
// the same thing about a *caller* — a function that calls one does not complete
// now either — and the fixpoint that propagates it is already written against
// that field. What they say about the declaration differs, which is why this is
// its own mark: the foreign flag reports a promise the call site awaits, and a
// blocking Go function returns its value directly.
func markGoAsync(m *mark) error {
	d, ok := m.sym.(*ir.Func)
	if !ok {
		return fmt.Errorf("#[go.async] cannot mark %s; only a function has a call that blocks", ast.DeclFormName(m.decl))
	}
	d.IsAsync = true
	return nil
}

// markGoNative implements #[go.native("path", "Name")]: the declaration *is*
// that Go package-level identifier rather than corresponding to one.
//
// Which is the whole difference from #[foreign], and why Foreign.Marked is
// where it lands. Marked says "the backend emits this declaration and the name
// is one to spell beside it"; unset says "the identifier already exists and a
// call becomes a call to it" — the shape the Go renderer has always read for an
// imported declaration, requiring the import at the call site.
//
// That last part is the point. An import written at the top of a target's
// library package is resolved by every program built for that language, so a
// transport nothing fetches with still had to be on the machine. A native
// declaration costs nothing until something calls it.
//
// It lives in sngl:language/go because a Go path and a Go identifier are Go's
// to describe, and a package may use a macro it declares.
func markGoNative(m *mark) error {
	path, name := m.args.String("path"), m.args.String("name")
	if !m.args.Has("name") {
		// One argument is the identifier alone, which is all a struct field
		// can say: a field has no package of its own.
		path, name = "", path
	}
	if name == "" {
		return fmt.Errorf("#[go.native]: an identifier is required")
	}
	if _, isField := m.sym.(*ir.StructField); !isField && path == "" {
		return fmt.Errorf("#[go.native(%q)]: a package path is required; only a struct field may name an identifier alone", name)
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[native(%q)]", name))
	if err != nil {
		return err
	}
	fm := ir.Foreign{Scheme: "go", Path: path, Name: name}
	switch d := m.sym.(type) {
	case *ir.Func:
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
		// The same fact the Go importer reads off a signature it sees. A
		// declaration here has no signature to read, so the mark is where it
		// is said.
		d.HasErrorReturn = slices.Contains(flags, flagFails)
		d.NativeMethod = slices.Contains(flags, flagMethod)
		d.NativeSchedules = slices.Contains(flags, flagSchedules)
		d.HasContextArg = slices.Contains(flags, flagContext)
	case *ir.StructDef:
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a struct has none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.StructField:
		// The Go spelling of a field SNGL writes differently -- `ID` against
		// `id`. There is no call and no package, so no flag applies and the
		// path is the one this form leaves empty.
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a struct field has none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.Var:
		// A const or var names a host value: `math.Pi` is a constant, Kotlin's
		// `StrokeCap.Round` a property, `os.Args` a package var. None has a
		// call form, and a declaration that had to be a func to be readable
		// said so only through a flag nothing else could check.
		//
		// Which of the two is the host's distinction rather than ours: a const
		// is what the program only reads, a var a global it may assign to.
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a const or var names a value and makes none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("#[go.native] cannot mark %s; only a func, a struct, a const or a var names a Go identifier", ast.DeclFormName(m.decl))
	}
	return nil
}

// markKotlinNative implements #[kotlin.native("name")] and the module form.
//
// The same shape as the other two. A receiverless declaration is what a
// Compose `DrawScope` function needs: the scope is the receiver and the call
// carries none.
func markKotlinNative(m *mark) error {
	name, module := m.args.String("name"), m.args.String("module")
	if name == "" {
		return fmt.Errorf("#[kotlin.native]: an identifier is required")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[native(%q)]", name))
	if err != nil {
		return err
	}
	fm := ir.Foreign{Scheme: "kotlin", Path: module, Name: name}
	switch d := m.sym.(type) {
	case *ir.Func:
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
		d.NativeMethod = slices.Contains(flags, flagMethod)
		d.NativeNamedArgs = slices.Contains(flags, flagNamed)
	case *ir.StructDef:
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a struct has none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.Var:
		// A const or var names a host value: `math.Pi` is a constant, Kotlin's
		// `StrokeCap.Round` a property, `os.Args` a package var. None has a
		// call form, and a declaration that had to be a func to be readable
		// said so only through a flag nothing else could check.
		//
		// Which of the two is the host's distinction rather than ours: a const
		// is what the program only reads, a var a global it may assign to.
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a const or var names a value and makes none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("#[kotlin.native] cannot mark %s; only a func, a struct, a const or a var names a Kotlin identifier", ast.DeclFormName(m.decl))
	}
	return nil
}

// markCNative implements #[cnative("name")]: the declaration is that C
// identifier.
//
// The scheme is "c" rather than a language's, because C is an ABI and not a
// SNGL target: a platform wrapping a C library describes it once and any
// backend that can call C reads the same declarations. The path is "C", which
// is what the Go renderer already keys its cgo spelling on.
func markCNative(m *mark) error {
	name := m.args.String("name")
	if name == "" {
		return fmt.Errorf("#[cnative]: a C identifier is required")
	}
	fm := ir.Foreign{Scheme: "c", Path: "C", Name: name}
	switch d := m.sym.(type) {
	case *ir.Func:
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.StructDef:
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("#[cnative] cannot mark %s; only a function or a struct names a C identifier", ast.DeclFormName(m.decl))
	}
	return nil
}

// markJSNative implements #[js.native("name")] and #[js.native("name",
// "module")]: the declaration *is* that JavaScript identifier.
//
// The same shape as #[go.native], with the one difference JavaScript forces.
// A Go identifier is always package-qualified, so that mark can require a
// path; `setInterval` is a property of globalThis and has no path to name, so
// the module is optional here and the name comes first. An empty module is a
// global, and the emitter renders it as a bare call.
//
// Foreign.Marked stays unset for the reason it does there: the identifier
// already exists, and a call becomes a call to it rather than to a declaration
// this build emits.
func markJSNative(m *mark) error {
	name, module := m.args.String("name"), m.args.String("module")
	if name == "" {
		return fmt.Errorf("#[js.native]: an identifier is required")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[native(%q)]", name))
	if err != nil {
		return err
	}
	fm := ir.Foreign{Scheme: "js", Path: module, Name: name}
	switch d := m.sym.(type) {
	case *ir.Func:
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
		// The same fact the TypeScript importer reads off a `Promise<T>`
		// return. A declaration here has no signature to read, so the mark is
		// where it is said, and the emitter awaits the call.
		d.IsAsync = slices.Contains(flags, flagAsync)
		d.NativeMethod = slices.Contains(flags, flagMethod)
	case *ir.StructDef:
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a struct has none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.Var:
		// A const or var names a host value: `math.Pi` is a constant, Kotlin's
		// `StrokeCap.Round` a property, `os.Args` a package var. None has a
		// call form, and a declaration that had to be a func to be readable
		// said so only through a flag nothing else could check.
		//
		// Which of the two is the host's distinction rather than ours: a const
		// is what the program only reads, a var a global it may assign to.
		if len(flags) > 0 {
			return fmt.Errorf("#[native(%q)] carries %s, which describes a call; a const or var names a value and makes none", name, flags[0])
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("#[js.native] cannot mark %s; only a func, a struct, a const or a var names a JavaScript identifier", ast.DeclFormName(m.decl))
	}
	return nil
}

// markBuiltin implements #[builtin("kind")], the mark that names the IR
// construct a declaration dispatches to.
//
// It does not decide which kinds go on which declaration forms; it stamps the
// kind on whichever IR the declaration became. What a given kind then requires
// — that a node kind names a component, that a const kind names a const — is
// checked by bindBuiltinRole, where the compiler stores the reference, because
// that is where the requirement comes from.
func markBuiltin(m *mark) error {
	raw := m.args.String("kind")
	kind := ir.BuiltinKind(raw)
	if !kind.Valid() {
		return fmt.Errorf("unknown builtin kind %q (valid: %s)", raw, strings.Join(builtinKindNames(), ", "))
	}
	switch d := m.sym.(type) {
	case *ir.Component:
		d.Builtin = kind
	case *ir.StructDef:
		d.Builtin = kind
	case *ir.Var:
		d.Builtin = kind
	case *ir.UnitDef:
		d.Builtin = kind
	default:
		return fmt.Errorf("#[builtin(%q)] cannot mark %s", raw, ast.DeclFormName(m.decl))
	}
	m.c.bindBuiltinRole(kind, m.sym.(ir.Symbol))
	return nil
}

func builtinKindNames() []string {
	all := ir.AllBuiltinKinds()
	names := make([]string, len(all))
	for i, k := range all {
		names[i] = string(k)
	}
	return names
}

// The flags #[intrinsic] accepts after the id, declared as ir.IntrinsicFlag in
// lib/internal/ir. The checker validates them against that enum; these are the
// members it acts on.
const (
	flagMutates         = "mutates"
	flagReadonly        = "readonly"
	flagMutatesReceiver = "mutatesReceiver"
)

// markIntrinsic implements #[intrinsic("Id", flags...)], stamping the id and
// what the compiler needs to know about a call onto the function — or, on a
// component, the id alone: a component is emitted by the platform codegen
// that answers to the id, and no call cost follows from that.
func markIntrinsic(m *mark) error {
	id := m.args.String("id")
	if id == "" {
		return fmt.Errorf("#[intrinsic] requires a non-empty id")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[intrinsic(%q)]", id))
	if err != nil {
		return err
	}
	if slices.Contains(flags, flagMutates) && slices.Contains(flags, flagReadonly) {
		return fmt.Errorf("#[intrinsic(%q)] is both %s and %s; a call either has an effect or only reads host state",
			id, flagMutates, flagReadonly)
	}
	if comp, ok := m.sym.(*ir.Component); ok {
		// A component has no signature, so the flags — which all describe
		// what a call costs — have nothing to describe.
		if len(flags) > 0 {
			return fmt.Errorf("#[intrinsic(%q)] carries %s, which describes a call; a component has none", id, flags[0])
		}
		if comp.Intrinsic != "" {
			return fmt.Errorf("#[intrinsic(%q)]: already an intrinsic (%q)", id, comp.Intrinsic)
		}
		comp.Intrinsic = id
		return nil
	}
	fn, ok := m.sym.(*ir.Func)
	if !ok {
		return fmt.Errorf("#[intrinsic(%q)] cannot mark %s", id, ast.DeclFormName(m.decl))
	}
	if fn.Intrinsic != "" {
		return fmt.Errorf("#[intrinsic(%q)]: already an intrinsic (%q)", id, fn.Intrinsic)
	}
	fn.Intrinsic = id
	fn.MutatesReceiver = slices.Contains(flags, flagMutatesReceiver)
	switch {
	case slices.Contains(flags, flagMutates):
		fn.Purity = ir.PurityMutates
	case slices.Contains(flags, flagReadonly):
		fn.Purity = ir.PurityReadonly
	}
	if fn.Const && fn.Purity > ir.PurityPure {
		return fmt.Errorf("#[intrinsic(%q)] on const func %s says it reads or writes state, which a const func does not", id, fn.Name)
	}
	return nil
}

// The flags #[foreign] accepts after the name, declared as ir.ForeignFlag.
const (
	flagAsync  = "async"
	flagNative = "native"
)

// The flag #[native] accepts after the name, declared as go.NativeFlag.
const flagFails = "fails"

// flagMethod says the native identifier is called on its first argument, not
// handed it. Both languages accept it: the choice is the host API's, not the
// language's.
const flagMethod = "method"

// flagNamed says a native call passes its arguments by name, using the
// declaration's own parameter names.
const flagNamed = "named"

// flagSchedules says the host identifier runs a callback it is handed from the
// loop it owns rather than inline. It describes *when* a call's argument runs,
// which is the one thing about a native nothing else can find out.
const flagSchedules = "schedules"

// flagContext says the host function takes a leading context.Context the SNGL
// signature does not, so the call site supplies one. An importer reads it off
// the Go signature; a hand-written declaration has no signature to read.
const flagContext = "context"

// markForeign implements #[foreign("scheme://path", "Name", flags...)].
//
// Two arguments are the import path the declaration comes from and its name
// there, written the way a program's own import line writes them. One argument
// is the name alone: that is all a struct field can say, since a field has no
// package of its own. Nothing here inspects either string — the path is not
// resolved and the name is not checked to exist, because a plugin's output is
// trusted.
//
// The mark never confers type identity. ir.Foreign.Origin is left nil: Origin
// is a scheme importer's own key for a declaration it read, and it is what
// makes two declarations the same type. A marked declaration unifies with
// nothing.
func markForeign(m *mark) error {
	path, name := m.args.String("path"), m.args.String("name")
	if !m.args.Has("name") {
		path, name = "", path
	}
	if name == "" {
		return fmt.Errorf("#[foreign] requires a non-empty name")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[foreign(%q)]", name))
	if err != nil {
		return err
	}
	// `async` describes a call, so only a function carries it.
	// `native` describes the declaration itself -- it says the host already
	// has this, and a struct can say that as readily as a function.
	if _, isFunc := m.sym.(*ir.Func); !isFunc {
		for _, f := range flags {
			if f != flagNative {
				return fmt.Errorf("#[foreign(%q)] carries %s, which describes a call; %s has none", name, f, ast.DeclFormName(m.decl))
			}
		}
	}
	if n := markedNames(m.decl); n > 1 {
		return fmt.Errorf("#[foreign(%q)] marks %d names at once; one foreign name cannot stand for several declarations", name, n)
	}
	scheme, pkgPath := imports.ParseScheme(path)
	fm := ir.Foreign{Scheme: scheme, Path: pkgPath, Name: name, Marked: !slices.Contains(flags, flagNative)}
	switch d := m.sym.(type) {
	case *ir.StructDef:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.StructField:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
	case *ir.Func:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		if err := claimForeign(&d.Foreign, fm, name); err != nil {
			return err
		}
		// A foreign function's body describes the declaration rather than
		// implementing it, so what a call costs is what the declaration says:
		// `const func` for a pure one. Purity is otherwise left unknown:
		// inferring it from the body would fold a stub's result into the
		// program in place of the call.
		d.IsAsync = slices.Contains(flags, flagAsync)
		if d.Const {
			d.Purity = ir.PurityPure
		}
	default:
		return fmt.Errorf("#[foreign(%q)] cannot mark %s", name, ast.DeclFormName(m.decl))
	}
	return nil
}

// markUnusable implements #[unusable(reason = "...")].
//
// The declaration is registered like any other and then refused at every use.
// That is the point of it: an importer that dropped what it could not model
// would leave a program naming it with `undefined`, which says nothing about
// the foreign thing or about why SNGL cannot reach it. Resolving to a
// declaration that carries the reason turns that into a sentence.
//
// Language-neutral, because the wall is. Go returning two non-error values, a
// C type nothing models and a JavaScript signature nothing describes are one
// condition met by three importers.
func markUnusable(m *mark) error {
	reason := m.args.String("reason")
	if reason == "" {
		return fmt.Errorf("#[unusable] requires a reason: it is what a program naming this declaration is told")
	}
	switch d := m.sym.(type) {
	case *ir.StructDef:
		return claimUnusable(&d.Foreign, reason)
	case *ir.StructField:
		return claimUnusable(&d.Foreign, reason)
	case *ir.Func:
		return claimUnusable(&d.Foreign, reason)
	case *ir.Var:
		return claimUnusable(&d.Foreign, reason)
	}
	return fmt.Errorf("#[unusable] cannot mark %s", ast.DeclFormName(m.decl))
}

// claimUnusable refuses a second reason rather than overwriting the first, the
// rule every other mark is held to: two reasons for one declaration is a
// question about which is true, and silence picks one.
func claimUnusable(f *ir.Foreign, reason string) error {
	if f.Unusable != "" {
		return fmt.Errorf("#[unusable]: already unusable (%s)", f.Unusable)
	}
	f.Unusable = reason
	return nil
}

// markedNames counts the names one declaration binds. A foreign name stands
// for one declaration, so a grouped one cannot carry the mark.
func markedNames(decl markTarget) int {
	switch d := decl.(type) {
	case *ast.StructField:
		return len(d.Names)
	case *ast.ConstDecl:
		return specNameCount(d.Specs)
	case *ast.VarDecl:
		return specNameCount(d.Specs)
	}
	return 1
}

func specNameCount(specs []ast.VarSpec) int {
	n := 0
	for _, s := range specs {
		n += len(s.Names)
	}
	return n
}

// uniqueFlags rejects a repeated flag. The enum membership of each one is
// already checked against the declared parameter type.
func uniqueFlags(flags []string, mark string) ([]string, error) {
	for i, f := range flags {
		if slices.Contains(flags[:i], f) {
			return nil, fmt.Errorf("%s repeats flag %s", mark, f)
		}
	}
	return flags, nil
}

// markTreeNone implements #[tree.none]: this component belongs to no family.
//
// It has to be written rather than left out, because leaving the return
// position out is now the error that drove every declaration to name `ui`.
// The mark is the difference between "renders nothing, so it goes anywhere"
// and "somebody forgot".
func markTreeNone(m *mark) error {
	comp, ok := m.sym.(*ir.Component)
	if !ok {
		return fmt.Errorf("#[tree.none] cannot mark %s; only a component belongs to a tree", ast.DeclFormName(m.decl))
	}
	comp.Treeless = true
	return nil
}

// markTreeCrosses implements #[tree.crosses]: this placement may stand where
// its family is not the one accepted. It marks the placement and never the
// declaration, so a platform says exactly which use of a node it permits.
func markTreeCrosses(m *mark) error {
	switch s := m.sym.(type) {
	case *ir.NodeInst:
		s.Crosses = true
		return nil
	case *ir.SlotInst:
		s.Crosses = true
		return nil
	}
	form := ast.DeclFormName(m.decl)
	switch m.sym.(type) {
	case *ir.ErrorBoundary:
		form = "a boundary"
	case *ir.ContextProvider:
		form = "a context override"
	}
	return fmt.Errorf("#[tree.crosses] cannot mark %s; only a node or a slot insertion in a view is placed in a tree", form)
}

// markWildcard implements #[macro.wildcard("pattern")], which says what a
// name nobody declared resolves to: a component reached by any matching name
// in its package's namespace, or a prop bound by any matching prop name.
//
// The pattern is anchored to the whole name, so a partial match is not one.
// It is compiled here, where the mark is written, so a pattern RE2 cannot
// parse is reported at its own position rather than at the first call site
// that would have matched it.
func markWildcard(m *mark) error {
	pat := m.args.String("pattern")
	if pat == "" {
		return fmt.Errorf("#[wildcard] requires a non-empty pattern")
	}
	if err := compileWildcard(pat); err != nil {
		return fmt.Errorf("#[wildcard(%q)]: %w", pat, err)
	}
	switch d := m.sym.(type) {
	case *ir.Component:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		d.Wildcard, d.WildcardInto = pat, m.args.String("into")
		// The prop it names is checked by finishWildcardMarks: a component's
		// marks run before its props are built, so there is nothing to look
		// the name up in yet.
	case *ir.Prop:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		if m.args.Has("into") {
			return fmt.Errorf("#[wildcard(%q)]: `into` names where a matched name is bound, which only a component's mark has to say; a prop's matched names are its own keys", pat)
		}
		if err := checkWildcardPropType(d); err != nil {
			return fmt.Errorf("#[wildcard(%q)] on prop %q: %w", pat, d.Name, err)
		}
		d.Wildcard = pat
	case *ir.EventDecl:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		if m.args.Has("into") {
			return fmt.Errorf("#[wildcard(%q)]: `into` names where a matched name is bound, which only a component's mark has to say; a handler already carries the name it was written under", pat)
		}
		// No map, unlike a prop: a handler is bound under its own name in the
		// IR, so the names a wildcard event matched are already distinct
		// without being collected anywhere.
		d.Wildcard = pat
	default:
		return fmt.Errorf("#[wildcard(%q)] cannot mark %s; only a component, one of its props or one of its events stands for names nobody declared", pat, ast.DeclFormName(m.decl))
	}
	return nil
}

// checkWildcardPropType requires a wildcard prop to be a map keyed by string.
// A wildcard prop stands for many names at once, so one call site can bind it
// many times; a scalar has room for one of them, and the rest were being
// accepted and then dropped on the floor.
func checkWildcardPropType(p *ir.Prop) error {
	t := p.Type
	if t == nil || t.Kind != ir.TypeMap || len(t.Elems) != 2 || t.Elems[0].Kind != ir.TypeString {
		got := "no type"
		if t != nil {
			got = t.String()
		}
		return fmt.Errorf("a wildcard prop collects every name it matches, so it is a map keyed by that name; declare it map<string, V> rather than %s", got)
	}
	return nil
}

// finishWildcardMarks validates the `into` prop a component's wildcard mark
// names, once its props exist. The matched name is a name, so the prop it
// binds to holds a string.
func (c *checker) finishWildcardMarks(pos ast.Pos, comp *ir.Component) {
	if comp == nil || comp.WildcardInto == "" {
		return
	}
	for _, p := range comp.Props {
		if p.Name != comp.WildcardInto {
			continue
		}
		if p.Type == nil || p.Type.Kind != ir.TypeString {
			c.error(pos, "#[wildcard(..., %q)] on component %s: the matched name is a name, so %q holds a string", comp.WildcardInto, comp.Name, p.Name)
		}
		return
	}
	c.error(pos, "#[wildcard(..., %q)] on component %s: no prop %q to bind the matched name to", comp.WildcardInto, comp.Name, comp.WildcardInto)
}

// markConstruct implements #[macro.construct], which says a prop is read while
// its instance is being built and never again.
//
// Only a prop, because the mark is about a value arriving from outside: a `var`
// is written by definition, and a declaration with no instance behind it has
// nothing to rebuild.
//
// The type is the mark's one requirement, and it is the same one an effect's
// `on` carries (checkEffectKey): the value the instance was built from is kept
// and compared against what the next render describes, so every target has to
// agree on when two of them are the same value. A type that does not compare
// alike -- a list, a map, an option, an opaque `date` -- leaves the site
// rebuilding its instance on every render, which throws away exactly the state
// the mark exists to preserve. Refused where the mark is written rather than
// lowered into a program whose instances live different lengths on each target.
//
// The judgement itself waits: a struct is comparable exactly when its fields
// are, and pass1 registers components before it resolves struct bodies, so a
// struct named here still has no fields to walk. The mark records where it was
// written and checkConstructProps answers once every shell is filled.
func markConstruct(m *mark) error {
	p, ok := m.sym.(*ir.Prop)
	if !ok {
		return fmt.Errorf("#[construct] cannot mark %s; only a component prop is read once while its instance is built", ast.DeclFormName(m.decl))
	}
	if p.Construct {
		return fmt.Errorf("#[construct]: prop %q is already construct-only", p.Name)
	}
	p.Construct = true
	m.c.pendingConstruct = append(m.c.pendingConstruct, constructMark{pos: m.attr.Pos, prop: p})
	return nil
}

// constructMark is one #[construct] whose prop type is not yet complete enough
// to judge: the position the mark was written at, and the prop it marked.
type constructMark struct {
	pos  ast.Pos
	prop *ir.Prop
}

// checkConstructProps refuses a #[construct] prop whose type does not compare
// alike everywhere. See markConstruct for why, and for why it is not answered
// there; the diagnostic mirrors checkEffectKey's, which is the same rule for
// the same reason, down to naming the offending field of a struct rather than
// the struct.
func (c *checker) checkConstructProps() {
	pending := c.pendingConstruct
	c.pendingConstruct = nil
	for _, m := range pending {
		why := ir.RebuildIncomparable(m.prop.Type)
		if why == "" {
			continue
		}
		// The mark is refused, so the prop is an ordinary one: leaving
		// Construct set would have lowering build the memory and the
		// comparison for a value it has just been told it cannot compare.
		m.prop.Construct = false
		c.error(m.pos, "#[construct] on prop %q: the instance is rebuilt when this value changes, "+
			"so it must compare alike on every target, and %s does not", m.prop.Name, why)
	}
}

// claimForeign records fm on a declaration that has no host identity yet.
//
// A second native mark is refused rather than overwriting the first: ir.Foreign
// holds one scheme and one name, so `#[go.native]` and `#[js.native]` written
// on one declaration kept whichever ran last, silently, and the *Go* output
// said `Math.PI`. A value each language spells differently is an
// `#[marks.intrinsic]`, which is what `float.sin` already is.
func claimForeign(dst *ir.Foreign, fm ir.Foreign, name string) error {
	if dst.Name != "" {
		return fmt.Errorf("#[native(%q)] is a second host identity; %q already names this declaration's, and a declaration names one identifier", name, dst.Name)
	}
	*dst = fm
	return nil
}

// markGenCan, markGenCannot and markGenWants implement the sngl:x/gen marks:
// what a target emits natively, what it refuses, and which lowering passes it
// asks for.
//
// Three marks rather than one with a polarity argument, because they are three
// different statements and a reader of a target package should not have to
// decode which one is being made. The names are stored as written -- the enum
// they were checked against is the vocabulary, and what each one gates is the
// lowering's to know.
func markGenCan(m *mark) error     { return markGen(m, "can") }
func markGenCannot(m *mark) error  { return markGen(m, "cannot") }
func markGenWants(m *mark) error   { return markGen(m, "wants") }
func markGenRenders(m *mark) error { return markGen(m, "renders") }

// markGenName implements #[gen.name]: the string a build-target node is known
// by outside SNGL. Where it may be written -- a build-target node, and only
// one -- is finishTreeMarks' question, for the reason the other gen marks'
// placement is.
func markGenName(m *mark) error {
	comp, ok := m.sym.(*ir.Component)
	if !ok {
		return fmt.Errorf("#[gen.name] cannot mark %s; it belongs on the build-tree node a target package declares", ast.DeclFormName(m.decl))
	}
	name := m.args.String("target")
	if name == "" {
		return fmt.Errorf("#[gen.name] requires a non-empty name")
	}
	if comp.Gen == nil {
		comp.Gen = &ir.GenCaps{}
	}
	if comp.Gen.TargetName != "" {
		return fmt.Errorf("#[gen.name(%q)]: already named %q", name, comp.Gen.TargetName)
	}
	comp.Gen.TargetName = name
	return nil
}

func markGen(m *mark, kind string) error {
	comp, ok := m.sym.(*ir.Component)
	if !ok {
		return fmt.Errorf("#[gen.%s] cannot mark %s; it belongs on the build-tree node a target package declares, or on an #[intrinsic] primitive", kind, ast.DeclFormName(m.decl))
	}
	// Which component it may be written on is checked in finishTreeMarks, not
	// here: a mark applies as the declaration registers, and the return
	// position it has to be measured against is read after that.
	arg := "caps"
	switch kind {
	case "wants":
		arg = "passes"
	case "renders":
		arg = "these"
	}
	names := m.args.Idents(arg)
	if len(names) == 0 {
		return fmt.Errorf("#[gen.%s] names nothing", kind)
	}
	if comp.Gen == nil {
		comp.Gen = &ir.GenCaps{}
	}
	switch kind {
	case "can":
		comp.Gen.Can = append(comp.Gen.Can, names...)
	case "cannot":
		comp.Gen.Cannot = append(comp.Gen.Cannot, names...)
	case "wants":
		comp.Gen.Wants = append(comp.Gen.Wants, names...)
	case "renders":
		comp.Gen.Renders = append(comp.Gen.Renders, names...)
	}
	// Both ways on one declaration is an error rather than a precedence rule
	// nobody can remember. Across declarations it is the whole point: a
	// platform's `cannot` overrules the language's `can`.
	for _, n := range comp.Gen.Can {
		if slices.Contains(comp.Gen.Cannot, n) {
			return fmt.Errorf("#[gen] names %q both ways on one declaration", n)
		}
	}
	for _, set := range [][]string{comp.Gen.Can, comp.Gen.Cannot, comp.Gen.Wants, comp.Gen.Renders} {
		if n, dup := firstDuplicate(set); dup {
			return fmt.Errorf("#[gen] names %q twice", n)
		}
	}
	return nil
}

func firstDuplicate(names []string) (string, bool) {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			return n, true
		}
		seen[n] = true
	}
	return "", false
}
