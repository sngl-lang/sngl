package android

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/internal/androidtc"
	"duckfam.us/sngl/ir"
)

// Config controls code generation. Field names match the SNGL option names
// in android.sngl and lib/options.sngl (camelCase → PascalCase via
// codegen.ApplyOptions).
type Config struct {
	// Source-declared options (android.sngl).
	Package    string // Kotlin package name (default: "test.sngl.app")
	AppName    string // display name override; empty falls back to stdlib Name
	Main       bool   // emit MainActivity.kt + project scaffold
	Gradle     *bool  // use Gradle build system (default: true; nil = use default)
	Icon       string // path to icon file (SVG or PNG); empty falls back to stdlib Icon
	Color      string // theme/icon background color as hex (#RRGGBB)
	ProjectDir string // project root directory (for resolving relative icon paths)
	TestRunner string // "robolectric" (default) or "device"

	// Toolchain selection (see internal/androidtc). Toolchain picks a whole
	// known-good combo by name; the remaining knobs override individual
	// versions within it (an unlisted mix is unvalidated — you're on your own).
	Toolchain     string // combo name, e.g. "stable" (default) or "next"
	Sdk           int    // override compileSdk / targetSdk (0 = combo default)
	MinSdk        int    // override minSdk (0 = default 26)
	GradleVersion string // override Gradle distribution version

	// Stdlib globals (lib/options.sngl).
	Name        string
	Description string
	Version     string

	// Lang globals (lib/language/go/golang.sngl).
	GoVersion string // Go toolchain version for generated go.mod (default: "1.23")

	// Internal — set by Generate(), not from source.
	GoLib bool // true when user funcs live in a Go module (gomobile bind)
	// Nav is set when the program renders a sngl:ui/nav stack, which is a
	// NavHost and wants navigation-compose and kotlinx.serialization.
	Nav bool
	// accent is Color as the program wrote it, before withDefaults fills in
	// the launcher's default: the theme's primary where it is set, and the
	// device's own colors where it is not.
	accent    string
	defaulted bool
}

func (c Config) withDefaults() Config {
	if !c.defaulted {
		c.accent, c.defaulted = c.Color, true
	}
	if c.Package == "" {
		c.Package = "test.sngl.app"
	}
	if c.Color == "" {
		c.Color = "#6750A4"
	}
	if c.AppName == "" {
		c.AppName = c.Name
	}
	if c.GoVersion == "" {
		c.GoVersion = "1.23"
	}
	if c.TestRunner == "" {
		c.TestRunner = "robolectric"
	}
	return c
}

// UseGradle reports whether the gradle build system should be used. Defaults
// to true when the gradle option is unset.
func (c Config) UseGradle() bool {
	if c.Gradle == nil {
		return true
	}
	return *c.Gradle
}

// defaultMinSdk is the minSdk used when the option is unset. It is an app-level
// choice, not a toolchain-compatibility constraint, so it lives outside the
// combo matrix.
const defaultMinSdk = 26

// combo resolves the toolchain combo for this config: the named combo (or the
// default), with per-field overrides applied. An unknown Toolchain name falls
// back to the default.
func (c Config) combo() androidtc.Combo {
	tc, ok := androidtc.ByName(c.Toolchain)
	if !ok {
		tc = androidtc.Default()
	}
	if c.Sdk > 0 {
		tc.CompileSdk = c.Sdk
	}
	if c.GradleVersion != "" {
		tc.Gradle = c.GradleVersion
	}
	return tc
}

// minSdk returns the configured minSdk, or the default.
func (c Config) minSdk() int {
	if c.MinSdk > 0 {
		return c.MinSdk
	}
	return defaultMinSdk
}

// comboFromOpts resolves the toolchain combo from raw build options. Used by
// the launcher paths, which receive *ir.StructLit rather than a Config.
func comboFromOpts(opts *ir.StructLit) androidtc.Combo {
	var cfg Config
	_ = codegen.ApplyOptions(&cfg, opts)
	return cfg.combo()
}

func exportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func pkgToPath(pkg string) string {
	return strings.ReplaceAll(pkg, ".", "/")
}

// CompileIR generates a Kotlin source file from IR using CodegenCtx.
func CompileIR(ctx *codegen.CodegenCtx, cfg Config) ([]byte, error) {
	src, err := compileIR(ctx, cfg)
	return src.bytes(), err
}

func compileIR(ctx *codegen.CodegenCtx, cfg Config) (ktSource, error) {
	cfg = cfg.withDefaults()
	pageTypeMethods(ctx, cfg)
	info := analyzeIR(ctx)
	src, _ := emitIR(info, ctx, cfg, false)
	return src, ctx.Err()
}

// CompileTestIR is the test-runner variant of CompileIR. It emits the
// same Compose source plus a hoisted `MainScreenState` class that
// owns every reactive var as `var x by mutableStateOf(...)`. The
// composable becomes `MainScreen(state: MainScreenState = remember
// { MainScreenState() })`; in-tree references to those vars route
// through `state.<name>`. Tests construct a state instance directly
// (`val state = MainScreenState(); composeTestRule.setContent
// { MainScreen(state) }`) and assert / mutate from outside the
// composition.
func CompileTestIR(ctx *codegen.CodegenCtx, cfg Config) ([]byte, error) {
	src, _, err := compileTestIR(ctx, cfg)
	return src.bytes(), err
}

// compileTestIR also returns what MainScreen spells each state member as --
// `state.<name>` -- so the test lowerer reaches the same members through the
// instance a test holds.
func compileTestIR(ctx *codegen.CodegenCtx, cfg Config) (ktSource, map[string]string, error) {
	cfg = cfg.withDefaults()
	pageTypeMethods(ctx, cfg)
	info := analyzeIR(ctx)
	src, members := emitIR(info, ctx, cfg, true)
	return src, members, ctx.Err()
}

// ktSource is a Kotlin file before a FileEmitter assembles it: its package,
// the imports its body requires, and the body.
type ktSource struct {
	pkg     string
	imports []string
	body    string
}

// write emits src as the file name in sink.
func (src ktSource) write(sink codegen.Sink, name string, lang codegen.LangTranslator, opts codegen.FileOptions) error {
	opts.Name, opts.PackageName = name, src.pkg
	e := lang.NewFileEmitter(sink, opts)
	for _, imp := range src.imports {
		e.RequireImport(imp)
	}
	if _, err := io.WriteString(e, src.body); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}

// bytes is src assembled, with no generated-by header.
func (src ktSource) bytes() []byte {
	const name = "MainScreen.kt"
	mem := codegen.NewMemSink()
	if err := src.write(mem, name, &kotlin.Translator{}, codegen.FileOptions{}); err != nil {
		return nil
	}
	return mem.Files()[name]
}

// irAnalysis is the IR-based replacement for analysisResult.
type irAndroidAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irAndroidBind
	computeds []irAndroidComputed
	// shared names the binds a composable other than MainScreen reads. Those
	// are declared at file level rather than remembered in MainScreen: a
	// component built at run time is a composable of its own, and a local of
	// MainScreen's is nothing it can name.
	shared map[string]bool
}

type irAndroidBind struct {
	name   string
	ktType string
	init   string  // pre-computed literal init (used for simple values)
	initEx ir.Expr // raw IR when init is anything but a scalar literal
	isList bool
	// sealed says the type is a sealed class, an enum carrying payloads,
	// whose initializer is one variant: inferred from it, the state could
	// hold that variant and no other.
	sealed bool
}

// stateTypeArg returns an explicit type argument ("<String?>") for
// mutableStateOf when Kotlin would infer the wrong type from the initializer:
// a nullable var, whatever it starts as, and a `null` initializer. The bind's
// ktType already carries the nullable form (option[T] → "T?").
func stateTypeArg(bind irAndroidBind, initVal string) string {
	// An option var starts from whatever its initializer is, which may be a
	// value: Kotlin infers the state's type from it, and a later write of null
	// does not compile. `datepicker(value=d)` keeps such a cell.
	if strings.HasSuffix(bind.ktType, "?") || bind.sealed {
		return "<" + bind.ktType + ">"
	}
	// A lambda's parameter types are not inferred from the cell it starts.
	if strings.Contains(bind.ktType, "->") {
		return "<" + bind.ktType + ">"
	}
	if strings.TrimSpace(initVal) == "null" {
		t := bind.ktType
		if t == "" {
			t = "Any?"
		}
		if !strings.HasSuffix(t, "?") {
			t += "?"
		}
		return "<" + t + ">"
	}
	return ""
}

// listStateInitKt renders the right-hand side of a list-typed state
// declaration: a SnapshotStateList built from the var's initializer.
func listStateInitKt(kc *kotlin.KtIRContext, bind irAndroidBind, initVal string) string {
	if ll, ok := bind.initEx.(*ir.ListLit); ok && len(ll.Elems) == 0 {
		return kc.Name(ktMutableStateListOf) + "<" + listElementTypeKt(bind.ktType) + ">()"
	}
	elems := initVal
	if strings.HasPrefix(elems, "listOf(") && strings.HasSuffix(elems, ")") {
		elems = elems[len("listOf(") : len(elems)-1]
	} else if bind.initEx != nil && strings.TrimSpace(initVal) != "" {
		return initVal + "." + kc.Name(ktToMutableStateList) + "()"
	}
	if elems == "" {
		return kc.Name(ktMutableStateListOf) + "<" + listElementTypeKt(bind.ktType) + ">()"
	}
	return kc.Name(ktMutableStateListOf) + "(" + elems + ")"
}

// computedCalcKt renders the whole `derivedStateOf(...)` call for a computed,
// using the supplied context (which controls state-var prefixing). `indent` is
// the leading whitespace of the declaration line, so a multi-line body closes
// its brace under the declaration.
//
// A body that is a single `return expr` renders as a lambda holding that
// expression. Anything else is a statement sequence, and a lambda cannot hold
// one that returns: `return` inside the non-inline `derivedStateOf` lambda is
// not a Kotlin expression-value but a non-local return, which is a compile
// error. An anonymous function is the shape that both takes statements and
// keeps `return` local, so a body with a local var, an `if`, a `for` or an
// early return emits as `derivedStateOf(fun(): T { ... })`.
func computedCalcKt(comp irAndroidComputed, cfg Config, kc *kotlin.KtIRContext, indent string) string {
	// Only a computed the gomobile module actually emits is called through it;
	// a component's computed is rendered here from Compose state, because that
	// module has no Model for its body to read component state through.
	if comp.fn == nil || len(comp.fn.Block) == 0 {
		return kc.Name(ktDerivedStateOf) + " { " + ktComputedZero(comp) + " }"
	}
	if len(comp.fn.Block) == 1 {
		if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			if v := kc.EvalExpr(ret.Value); v != "" {
				return kc.Name(ktDerivedStateOf) + " { " + v + " }"
			}
		}
	}
	retType := comp.ktType
	if retType == "" {
		retType = "Any"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s(fun(): %s {\n", kc.Name(ktDerivedStateOf), retType)
	for _, stmt := range comp.fn.Block {
		for _, line := range kc.EvalStmt(stmt) {
			fmt.Fprintf(&b, "%s    %s\n", indent, line)
		}
	}
	fmt.Fprintf(&b, "%s})", indent)
	return b.String()
}

// computedGetterKt declares a computed as a property whose getter runs the
// body, for the state class a test reads it through.
func computedGetterKt(comp irAndroidComputed, kc *kotlin.KtIRContext, indent string) string {
	retType := comp.ktType
	if retType == "" {
		retType = "Any"
	}
	head := fmt.Sprintf("%sval %s: %s get()", indent, comp.name, retType)
	if comp.fn == nil || len(comp.fn.Block) == 0 {
		return head + " = " + ktComputedZero(comp) + "\n"
	}
	if len(comp.fn.Block) == 1 {
		if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			if v := kc.EvalExpr(ret.Value); v != "" {
				return head + " = " + v + "\n"
			}
		}
	}
	var b strings.Builder
	b.WriteString(head + " {\n")
	for _, stmt := range comp.fn.Block {
		for _, line := range kc.EvalStmt(stmt) {
			fmt.Fprintf(&b, "%s    %s\n", indent, line)
		}
	}
	fmt.Fprintf(&b, "%s}\n", indent)
	return b.String()
}

// ktComputedZero is the value for a computed with no body to evaluate — typed
// from its return type, because `derivedStateOf { "" }` on an Int computed is
// a Kotlin type error rather than a wrong number.
func ktComputedZero(comp irAndroidComputed) string {
	if comp.fn != nil {
		return kotlin.KtZeroFor(comp.fn.Return)
	}
	return `""`
}

type irAndroidComputed struct {
	name   string
	ktType string
	fn     *ir.Func
}

// bindForVar describes one reactive var as Compose state. Shared by the main
// component's binds and by a surviving component's own vars, which are the
// same declaration reached through a different scope.
func bindForVar(v *ir.Var) irAndroidBind { return bindFor(v.Name, v.Type, v.Init) }

// bindFor is bindForVar for a binding with no declaration behind it --
// a document's route parameters, which the slot population declares and the
// request fills.
func bindFor(name string, typ *ir.Type, init ir.Expr) irAndroidBind {
	ktType := kotlin.IRTypeToKt(typ)
	initVal := irBindInitKt(typ, init)
	isList := typ != nil && typ.Kind == ir.TypeList
	// Judged on the node, not on the text IRLiteralToKt returned: a composite
	// whose fields all failed to emit still looks emitted.
	var initEx ir.Expr
	if _, isLit := init.(*ir.Literal); init != nil && !isLit {
		initEx = init
	}
	// An empty map arrives as `mapOf()` either way and Kotlin infers
	// Map<Nothing, Nothing> from both spellings -- see #176.
	if isList && (initVal == `""` || initVal == "emptyList()") {
		initVal = ""
	}
	return irAndroidBind{
		name:   name,
		ktType: ktType,
		init:   initVal,
		initEx: initEx,
		isList: isList,
		sealed: ir.PayloadEnum(typ) != nil,
	}
}

// componentOwnedFuncs is the set of funcs declared by a component that
// survived inlining. Each is emitted as a local fun of that component's
// composable rather than beside it: it reads the component's state, and on
// Compose that state is a `remember`ed local, so a top-level fun would name
// what nothing declared.
func componentOwnedFuncs(ctx *codegen.CodegenCtx) map[*ir.Func]bool {
	owned := map[*ir.Func]bool{}
	for _, cc := range ctx.NonRootComponents() {
		for _, fn := range cc.Funcs {
			owned[fn] = true
		}
	}
	return owned
}

func analyzeIR(ctx *codegen.CodegenCtx) *irAndroidAnalysis {
	info := &irAndroidAnalysis{
		CommonAnalysis: ctx.Analysis,
	}

	for _, ov := range ctx.ModelState() {
		if ov.IsConst() {
			continue
		}
		info.binds = append(info.binds, bindFor(ov.Name(), ov.Type(), ov.Init()))
	}
	info.shared = sharedPageState(ctx)

	// A surviving component's own funcs are declared inside its composable,
	// where its state is, so they are not the main composable's to hoist.
	owned := componentOwnedFuncs(ctx)

	allFuncs := ctx.AllFuncs()
	for _, f := range allFuncs {
		if codegen.IsComputed(f) && !owned[f] {
			info.computeds = append(info.computeds, irAndroidComputed{
				name:   f.Name,
				ktType: irFuncReturnKt(f),
				fn:     f,
			})
		}
	}

	return info
}

func emitIR(info *irAndroidAnalysis, ctx *codegen.CodegenCtx, cfg Config, testMode bool) (ktSource, map[string]string) {
	exprCtx := ctx.ScopedExprCtx()
	kc := kotlin.NewIRContext(exprCtx)
	nav, err := collectAndroidNav(ctx)
	if err != nil {
		ctx.Fail(err)
	}
	// The package body is the view, and the Screen in it is the window: a
	// harness that isolated a component has none, and draws that component's
	// body in its place.
	view := viewStmts(ctx)
	mainStacks := nav.stacksIn(view)

	// Collect the non-computed, non-GoLib user funcs we'll emit as
	// MainScreenState members in test mode. Need this list in two
	// places: (1) to register `name → state.name` rewrites so call
	// sites in the composable route through `state`, and (2) to
	// actually emit them inside the class body below.
	// A component's own funcs, which belong inside the composable rather than
	// beside it -- see the emission below.
	mainOwnFuncs := map[*ir.Func]bool{}
	if main := ctx.RootDecl(); main != nil {
		for _, fn := range main.Funcs {
			mainOwnFuncs[fn] = true
		}
	}
	// And the package's own funcs that reach package state. MainScreen holds
	// that state as `remember`ed locals -- ctx.ModelState() is what it
	// declares -- so a func reading one has to be declared inside it; beside
	// it, `log__inst0 = log__inst0 + "!"` names something no file-scope
	// declaration binds.
	// A func of a component composed on its own stays in that composable,
	// beside the component's state, and reaches the page's as the
	// composable does: through the file.
	stateReaching := codegen.PackageStateFuncs(ctx.Pkg)
	composedOwn := componentOwnedFuncs(ctx)
	var atFileLevel map[*ir.Func]bool
	if !testMode {
		atFileLevel = composedCallees(ctx)
	}
	for fn := range stateReaching {
		if composedOwn[fn] || atFileLevel[fn] {
			delete(stateReaching, fn)
			continue
		}
		mainOwnFuncs[fn] = true
	}
	// Two sets, not one: a func belongs inside exactly one composable, and the
	// main one emits only its own. Merging them put every component's func in
	// MainScreen as well as in the composable that owns its state.
	componentOwnFuncs := componentOwnedFuncs(ctx)
	for fn := range mainOwnFuncs {
		componentOwnFuncs[fn] = true
	}

	// The reactive state the class holds: a func reaching one of these is what
	// the class's scope is for.
	stateNames := map[string]struct{}{}
	for _, bind := range info.binds {
		stateNames[bind.name] = struct{}{}
	}
	for _, comp := range info.computeds {
		stateNames[comp.name] = struct{}{}
	}
	scoped := stateScopedFuncs(ctx, stateNames, stateReaching)

	var stateFuncs []*ir.Func
	if testMode && !cfg.GoLib {
		composed := componentOwnedFuncs(ctx)
		allFuncs := ctx.AllFuncs()
		for _, fn := range allFuncs {
			if fn.IsTest || codegen.IsComputed(fn) {
				continue
			}
			// A func of a component composed on its own is a local fun of
			// that composable, beside the state it reads; on the class it
			// named state the class does not hold.
			if composed[fn] && !mainOwnFuncs[fn] {
				continue
			}
			// A method on a struct or an enum is an extension function and
			// belongs nowhere near the state class -- it is called on a value
			// of that type. A *component's* func has a receiver too (the
			// component it was written in), and that one is exactly what the
			// class holds: its state is the class's fields. Excluding both
			// left a component's func emitted by nobody in test mode, which
			// is what `press__inst0` was.
			if fn.OnType() {
				continue
			}
			if !scoped[fn] {
				continue
			}
			stateFuncs = append(stateFuncs, fn)
		}
	}
	stateMembers := make(map[*ir.Func]bool, len(stateFuncs))
	for _, fn := range stateFuncs {
		stateMembers[fn] = true
	}

	var testRewrites map[string]string
	if testMode {
		// Route every reactive var through the hoisted state object
		// so handler bodies, computed expressions, and view-tree
		// reads all produce `state.<name>` references.
		rewrites := map[string]string{}
		for _, bind := range info.binds {
			rewrites[bind.name] = "state." + bind.name
		}
		// Computeds are hoisted onto MainScreenState in test mode (so the test
		// accessor can read `c.<name>`), hence view sites read `state.<name>`.
		for _, comp := range info.computeds {
			rewrites[comp.name] = "state." + comp.name
		}
		// Component-level user funcs live on MainScreenState too —
		// callers in the composable invoke them via `state.<name>()`.
		for _, fn := range stateFuncs {
			rewrites[fn.Name] = "state." + fn.Name
		}
		kc.IdentRewrites = rewrites
		testRewrites = maps.Clone(rewrites)
	}

	// Under --lang go the user's functions are emitted into the gomobile
	// module, exported and capitalized, so a Kotlin call site has to name them
	// there. Without this the module held `func Fib(n int) int` and the Kotlin
	// called a bare `fib(k)` that existed in neither file.
	//
	// The filter is emitGoLibIR's: what that module emits is what can be
	// called through it, and a computed or a receiver-bearing func is not.
	if cfg.GoLib {
		if kc.IdentRewrites == nil {
			kc.IdentRewrites = map[string]string{}
		}
		for _, fn := range ctx.AllFuncs() {
			if !inGoLib(fn) {
				continue
			}
			kc.IdentRewrites[fn.Name] = "golib.Golib." + exportName(fn.Name)
		}
	}

	// Pass 1: what the program declares, ahead of every import, so a Compose
	// name the file imports is aliased around it rather than capturing it.
	reserveDeclared(kc, ctx.Pkg)
	// Conditional imports derived from IR analysis (no code scanning needed).
	if info.NeedsToast {
		kc.RequireImport("android.widget.Toast")
	}
	if cfg.GoLib && len(kc.IdentRewrites) > 0 {
		kc.RequireImport("golib.Golib")
	}
	// i18n import is registered dynamically by kc.RequireImport during
	// body rendering whenever an I18n.* call is emitted.

	// Pass 2: render body. kc.RequireImport fires for any i18n calls.
	var body strings.Builder

	// A multi-base unit is a data class of per-base magnitudes, the same
	// shape Go gives it. A single-base one is a plain number and declares
	// nothing.
	body.WriteString(kotlin.EmitUnitDataClasses(info.Units))

	// Data classes. The built-in `color` is skipped: it is carried as
	// kotlin.ColorDecl below, whose channels are Int rather than the UByte
	// its uint8 fields would give -- which is what the stdlib helpers pass and
	// what Compose's own channel constructor takes. Emitting the declaration
	// too left two `data class Color` in one file.
	declaredStructs := map[string]struct{}{}
	for _, sd := range info.Structs {
		if sd.Builtin == ir.BuiltinColor {
			continue
		}
		declaredStructs[exportName(sd.Name)] = struct{}{}
		// A struct a page's params reach travels in its route.
		if nav.serializable[sd] {
			body.WriteString("@Serializable\n")
		}
		// A data class needs a field, and a struct with none -- `struct
		// Empty {}`, the empty `meta` a nav record carries -- is still a value
		// that compares equal to every other of its type.
		if len(sd.Fields) == 0 {
			name := exportName(sd.Name)
			fmt.Fprintf(&body, "class %s {\n    override fun equals(other: Any?) = other is %s\n    override fun hashCode() = 0\n    override fun toString() = \"%s()\"\n}\n\n", name, name, name)
			continue
		}
		fmt.Fprintf(&body, "data class %s(\n", exportName(sd.Name))
		for i, f := range sd.Fields {
			ktType := kotlin.IRTypeToKt(f.Type)
			def := " = " + kotlin.KtFieldZero(f.Type)
			if f.Default != nil {
				// A literal default is emitted as written, and so is a struct
				// literal of them -- `fill color = color{a=0}` is the one
				// Style writes, and a transparent fill is not the type's
				// zero. Anything else is an expression a data class header
				// cannot hold — `false` is a constant declaration whose own
				// initializer is `0 != 0` — so the field takes the type's
				// Kotlin zero.
				lit := kotlin.KtFieldZero(f.Type)
				switch d := f.Default.(type) {
				case *ir.Literal:
					if s := kotlin.IRLiteralToKt(f.Default); s != "" {
						lit = s
					}
				case *ir.StructLit:
					if s := literalStructLit(d); s != "" {
						lit = s
					}
				}
				def = " = " + lit
			}
			comma := ","
			if i == len(sd.Fields)-1 {
				comma = ""
			}
			fmt.Fprintf(&body, "    var %s: %s%s%s\n", kotlin.SafeIdent(f.Name), ktType, def, comma)
		}
		body.WriteString(")\n\n")
	}

	// The SNGL `color` data class, emitted when the program names the type:
	// a canvas draws with it, and promoteForeignStructs puts it in info.Structs
	// when a surviving signature names it. ComposeColor is the aliased
	// framework color import; Color is our SNGL `color` data class.
	//
	// declaredStructs is keyed by *emitted* name, because that is where the
	// collision is: SNGL's own struct is `color`, so a switch over the source
	// spelling matched nothing and the class was emitted twice.
	hasCanvas := packageHasCanvas(ctx.Canvases)
	if hasCanvas || namesColor(info.Structs) || programNamesColor(ctx.Pkg) {
		body.WriteString(colorKotlinDecl(declaredStructs))
	}

	// Canvas2D stdlib struct (Style). Stdlib-only structs
	// aren't carried on pkg.Structs, so materialize them here (analogous to
	// canvasutil for the Go platforms) whenever the program contains a canvas.
	if hasCanvas {
		body.WriteString(canvasKotlinDecls(kc, declaredStructs))
	}

	// ErrorEvent is emitted when any error-handling construct is present
	// (see bubbletea comment). The stdlib struct is not flowed through
	// user output, so materialize it here.
	if ctx.Pkg.UsesErrorHandling {
		body.WriteString("data class ErrorEvent(val message: String = \"\", val kind: String = \"\")\n\n")
		body.WriteString("class SnglRaise(val event: ErrorEvent) : RuntimeException(event.message)\n\n")
	}

	// string(float) is a helper rather than a method call: Kotlin's own
	// Double.toString writes a fraction a whole number does not have.
	body.WriteString(kotlin.FloatStringDecl)
	body.WriteString("\n")
	body.WriteString(kotlin.SplitDecl)
	body.WriteString("\n")

	// Struct-merge helpers for opaque spreads (flatten_struct_spread lowering).
	if mf := kotlin.EmitMergeFuncs(ctx.Pkg.MergeStructs, ctx.Pkg.MergeSets); mf != "" {
		body.WriteString(mf)
	}

	for _, ed := range info.Enums {
		body.WriteString(kotlin.EnumDecl(ed))
		body.WriteString("\n")
	}

	for _, c := range ctx.Pkg.Consts {
		if c.Init == nil {
			continue
		}
		fmt.Fprintf(&body, "val %s: %s = %s\n\n", c.Name, kotlin.IRTypeToKt(c.Type), kc.EvalExpr(c.Init))
	}

	nav.emitNavDecls(&body, kc)
	if testMode && len(ir.IndexedRefIDs(ctx.Pkg)) > 0 {
		fmt.Fprintf(&body, "val %s = %s<String>(%q)\n\n", kotlin.CopySemanticsKey, kc.Name(ktSemanticsKey), kotlin.CopySemanticsKey)
	}
	var present map[string]bool
	if testMode {
		var keyed map[string]bool
		present, keyed = codegen.NullTestedRefs(ctx.Pkg)
		instances := keyedInstanceIDs(ctx.Pkg)
		for id := range keyed {
			if instances[id] {
				present[id] = true
			}
		}
	}
	if len(present) > 0 {
		fmt.Fprintf(&body, "val %s = %s<Any, String>()\n\n", kotlin.PresentRegistry, kc.Name("androidx.compose.runtime.mutableStateMapOf"))
	}
	var reads []codegen.InstanceRead
	if testMode {
		reads = codegen.InstanceReads(ctx.Pkg)
	}
	if len(reads) > 0 {
		fmt.Fprintf(&body, "val %s = mutableMapOf<String, Map<String, (List<Any?>) -> Any?>>()\n\n", kotlin.InstanceRegistry)
	}

	// State hoisting (test mode): emit a MainScreenState class
	// with the binds as `var x by mutableStateOf(...)`. The
	// composable takes one as a parameter so tests can hold a
	// reference for assertions/mutations from outside the
	// composition.
	if testMode {
		// A composable other than MainScreen that reads the page's state is
		// handed no `state`, so the file holds the one the test made, which
		// each new one replaces. Its reads spell `state.x` as MainScreen's do.
		shared := len(info.shared) > 0
		if shared {
			body.WriteString("lateinit var state: MainScreenState\n\n")
		}
		body.WriteString("class MainScreenState {\n")
		if shared {
			body.WriteString("    init { state = this }\n")
		}
		if len(present) > 0 {
			fmt.Fprintf(&body, "    init { %s.clear() }\n", kotlin.PresentRegistry)
		}
		if len(reads) > 0 {
			fmt.Fprintf(&body, "    init { %s.clear() }\n", kotlin.InstanceRegistry)
		}
		// Init expressions inside the class body resolve sibling state vars
		// via implicit `this`, NOT the `state` parameter (which doesn't exist
		// here). Use a rewrite-free context so e.g. `__ctx_locale__inst1 =
		// __ctx_locale` rather than `state.__ctx_locale`, requiring its
		// imports of the file all the same.
		classKC := kc.Unrewritten()
		for _, bind := range info.binds {
			initVal := bind.init
			if bind.initEx != nil {
				initVal = classKC.EvalExpr(bind.initEx)
			}
			if bind.isList {
				fmt.Fprintf(&body, "    val %s = %s\n", bind.name, listStateInitKt(kc, bind, initVal))
			} else {
				stateDelegate(kc)
				fmt.Fprintf(&body, "    var %s by %s%s(%s)\n", bind.name, kc.Name(ktMutableStateOf), stateTypeArg(bind, initVal), initVal)
			}
		}
		// Getters, not derivedStateOf: one read outside any composition and
		// then invalidated leaves Compose never idle, so waitForIdle hangs.
		for _, comp := range info.computeds {
			body.WriteString(computedGetterKt(comp, classKC, "    "))
		}
		// Component-level user funcs become members of the state
		// class so their bodies resolve reactive vars via implicit
		// `this` rather than the out-of-scope `state` parameter
		// they'd see as top-level functions. Use a fresh context
		// with no IdentRewrites: inside the class, `__ctx_locale`
		// resolves via implicit `this`, not `state.__ctx_locale`.
		if len(stateFuncs) > 0 {
			memberKC := kc.Unrewritten()
			for _, fn := range stateFuncs {
				emitIRKtMemberFunc(&body, fn, memberKC)
			}
		}
		emitNavStateMembers(&body, mainStacks, classKC)
		body.WriteString("}\n\n")
	}

	if !testMode {
		for _, bind := range info.binds {
			if !info.shared[bind.name] {
				continue
			}
			initVal := bind.init
			if bind.initEx != nil {
				initVal = kc.EvalExpr(bind.initEx)
			}
			if bind.isList {
				fmt.Fprintf(&body, "val %s = %s\n\n", bind.name, listStateInitKt(kc, bind, initVal))
			} else {
				stateDelegate(kc)
				fmt.Fprintf(&body, "var %s by %s%s(%s)\n\n", bind.name, kc.Name(ktMutableStateOf), stateTypeArg(bind, initVal), initVal)
			}
		}
	}

	// Main composable
	fmt.Fprintf(&body, "@OptIn(%s::class)\n", kc.Name(ktExperimentalMaterial3Api))
	fmt.Fprintf(&body, "@%s\n", kc.Name(ktComposable))
	if testMode {
		fmt.Fprintf(&body, "fun MainScreen(state: MainScreenState = %s { MainScreenState() }) {\n", kc.Name(ktRemember))
	} else {
		body.WriteString("fun MainScreen() {\n")
	}

	if info.NeedsToast {
		fmt.Fprintf(&body, "    val context = %s.current\n", kc.Name(ktLocalContext))
	}

	// State declarations (skipped in test mode — state lives on
	// the hoisted MainScreenState class).
	if !testMode {
		for _, bind := range info.binds {
			if info.shared[bind.name] {
				continue
			}
			initVal := bind.init
			if bind.initEx != nil {
				// Non-literal init (e.g. i18n.tr call): evaluate via the full IR context.
				initVal = kc.EvalExpr(bind.initEx)
			}
			if bind.isList {
				fmt.Fprintf(&body, "    val %s = %s { %s }\n", bind.name, kc.Name(ktRemember), listStateInitKt(kc, bind, initVal))
			} else {
				stateDelegate(kc)
				fmt.Fprintf(&body, "    var %s by %s { %s%s(%s) }\n", bind.name, kc.Name(ktRemember), kc.Name(ktMutableStateOf), stateTypeArg(bind, initVal), initVal)
			}
		}
	}

	// The stacks come before the computeds and funcs that go to a page or read
	// one's current: Kotlin declares a local where it is written, and a local
	// fun naming a controller declared below it does not compile.
	emitNavState(&body, mainStacks, kc, testMode)

	// Computed state. In test mode computeds live on MainScreenState (emitted
	// above) so the test accessor can read them; here they're locals only in
	// non-test mode.
	if !testMode {
		for _, comp := range info.computeds {
			stateDelegate(kc)
			fmt.Fprintf(&body, "    val %s by %s { %s }\n", comp.name, kc.Name(ktRemember), computedCalcKt(comp, cfg, kc, "    "))
		}
	}

	if len(info.binds) > 0 || len(info.computeds) > 0 {
		body.WriteString("\n")
	}

	// The component's own funcs, as local funs closing over the state
	// declared just above. In test mode they are members of MainScreenState
	// instead, and the call sites are rewritten to reach them there.
	if !testMode && !cfg.GoLib {
		for _, fn := range calleesFirst(ctx.AllFuncs()) {
			if !mainOwnFuncs[fn] || fn.IsTest || codegen.IsComputed(fn) {
				continue
			}
			if !scoped[fn] {
				continue
			}
			var local strings.Builder
			emitIRKtFunc(&local, fn, kc)
			for line := range strings.SplitSeq(strings.TrimRight(local.String(), "\n"), "\n") {
				if line == "" {
					body.WriteString("\n")
					continue
				}
				fmt.Fprintf(&body, "    %s\n", line)
			}
			body.WriteString("\n")
		}
	}

	// The application ends with the composition, when the activity is
	// destroyed. Remembered first, so it is forgotten last: every @unmount
	// and timer below has gone when Teardown releases what lives as long as
	// the application.
	if td := ctx.Pkg.Teardown; td != nil && !cfg.GoLib {
		fmt.Fprintf(&body, "    %s(Unit) {\n", kc.Name(ktDisposableEffect))
		body.WriteString("        onDispose {\n")
		for _, line := range kc.EvalStmt(&ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: td}}) {
			fmt.Fprintf(&body, "            %s\n", line)
		}
		body.WriteString("        }\n")
		body.WriteString("    }\n\n")
	}

	// Visual tree
	cc := &irComposeContext{
		nav:     nav,
		present: present,
		reads:   reads,
		kc:      kc,
		ctx:     ctx,
		buf:     &body,
		indent:  1,
		combo:   cfg.combo(),
		// This one renders the window's own content. A sub-component's
		// composable never does, so the cutout padding belongs here alone.
		atRoot: true,
	}

	screen, err := codegen.SoleScreen(view, intrinsicNS+"Screen", "android", screenPos)
	if err != nil {
		ctx.Fail(err)
	}
	cc.screen = screen
	switch {
	case screen == nil && ctx.Harness() != nil:
		cc.renderContent(view)
	case screen == nil:
		// No window, which only a package that is not a program has: nothing
		// is on screen.
	default:
		for _, s := range view {
			cc.renderStmt(s)
		}
	}

	body.WriteString("}\n")

	// User component composables
	for _, comp := range ctx.NonRootComponents() {
		emitIRComponentComposable(&body, comp, ctx, kc, cfg, cfg.combo(), screen, nav, present, reads)
	}

	// User functions (non-GoLib). In test mode the state class holds them,
	// with one exception: a method on a struct or an enum is an extension
	// function either way -- the class is the component's state and a method
	// on a value of a user type has nothing to do with it. Skipped in both
	// places, `Calc.pending` was declared by nobody while every call site
	// spelled it.
	allFuncs := ctx.AllFuncs()
	for _, fn := range allFuncs {
		if fn.IsTest || codegen.IsComputed(fn) || cfg.GoLib && !composeSide(fn) {
			continue
		}
		if testMode && stateMembers[fn] {
			continue
		}
		// A method on a user type is called as one -- `state.pending()` --
		// so it is an extension function, and the receiver passNoImplicitRecv
		// put in front of the parameters is Kotlin's own `this`. A
		// component's own func is not that: it is called by bare name from
		// the composable, and the component is no Kotlin type to extend.
		if fn.OnType() {
			emitIRKtMethod(&body, fn, kc)
			continue
		}
		// A component's func reads and writes the component's state, and
		// on Compose that state is a `remember`ed local of the composable.
		// So it is a local fun of the composable too; emitted at top level
		// it named a `state` nothing had declared.
		//
		// One that touches no such state is not that, and putting it there
		// anyway is not free: a method on a user type is an extension
		// function at top level, so `Calc.pending` calling the pure
		// `format` found it only inside the composable, or as
		// `state.format` in test mode.
		if componentOwnFuncs[fn] && scoped[fn] {
			continue
		}
		emitIRKtFunc(&body, fn, kc)
	}

	// The imports were collected during pass 1 and dynamically during pass 2;
	// the FileEmitter puts them and the package clause above the body.
	return ktSource{
		pkg:     cfg.Package,
		imports: kc.Imports(),
		body:    kotlin.PruneLibraryDataClasses(body.String(), ctx.Pkg),
	}, testRewrites
}

func emitIRComponentComposable(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, kc *kotlin.KtIRContext, cfg Config, combo androidtc.Combo, screen *codegen.Screen, nav *androidNav, present map[string]bool, reads []codegen.InstanceRead) {
	fmt.Fprintf(b, "\n@%s\n", kc.Name(ktComposable))
	var params []string
	for _, p := range cc.Props {
		ktType := kotlin.IRTypeToKt(p.Type)
		def := ""
		if p.Default != nil {
			if _, ok := p.Default.(*ir.Literal); ok {
				def = " = " + kotlin.IRLiteralToKt(p.Default)
			} else if ir.IsConst(p.Default) && !namesParam(p.Default) {
				// A bodyless declaration's call site omits a defaulted prop and
				// leaves its default to the body, which is this composable.
				def = " = " + kc.EvalExpr(p.Default)
			}
		}
		params = append(params, p.Name+": "+ktType+def)
	}
	scoped := map[*ir.SlotDecl]string{}
	for d, r := range slotReceivers(cc.Body) {
		if r != "" {
			scoped[d] = kc.Name(r)
		}
	}
	params = append(params, slotParams(cc.Component, kc.Name(ktComposable), scoped)...)
	params = append(params, eventParams(cc.Component)...)
	own := readsOf(reads, cc.Component)
	if len(own) > 0 {
		params = append(params, kotlin.InstanceKeyParam+`: String = ""`)
	}
	if takesTestTag(cc.Component) {
		params = append(params, testTagParam+`: String = ""`)
	}
	fmt.Fprintf(b, "fun %s(%s) {\n", exportName(cc.Component.Name), strings.Join(params, ", "))

	compKC := kc.ForComponent(cc.Component)
	for _, p := range cc.Props {
		compKC = compKC.WithLocal(p.Name)
	}

	// The component's own state, computeds and funcs, declared inside the
	// composable. An instance of a component is a place in the composition,
	// and `remember` is what gives that place a cell of its own -- so two
	// rows of a list, or two frames of a recursion, hold two counters. Emitted
	// beside the composable instead, every one of these named a binding
	// nothing had declared.
	decls := 0
	for _, v := range cc.Vars {
		if v.IsConst {
			continue
		}
		bind := bindForVar(v)
		initVal := bind.init
		if bind.initEx != nil {
			initVal = compKC.EvalExpr(bind.initEx)
		}
		if bind.isList {
			fmt.Fprintf(b, "    val %s = %s { %s }\n", bind.name, kc.Name(ktRemember), listStateInitKt(kc, bind, initVal))
		} else {
			stateDelegate(kc)
			fmt.Fprintf(b, "    var %s by %s { %s%s(%s) }\n", bind.name, kc.Name(ktRemember), kc.Name(ktMutableStateOf), stateTypeArg(bind, initVal), initVal)
		}
		decls++
	}
	// Before the computeds and funcs, which may read a stack, as MainScreen's.
	emitNavState(b, nav.stacksIn(cc.Body), compKC, false)
	for _, fn := range cc.Computeds {
		comp := irAndroidComputed{name: fn.Name, ktType: irFuncReturnKt(fn), fn: fn}
		stateDelegate(kc)
		fmt.Fprintf(b, "    val %s by %s { %s }\n", comp.name, kc.Name(ktRemember), computedCalcKt(comp, cfg, compKC, "    "))
		decls++
	}
	for _, fn := range calleesFirst(cc.Funcs) {
		if fn.IsTest || codegen.IsComputed(fn) {
			continue
		}
		var local strings.Builder
		emitIRKtFunc(&local, fn, compKC)
		for line := range strings.SplitSeq(strings.TrimRight(local.String(), "\n"), "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(b, "    %s\n", line)
		}
		decls++
	}
	if decls > 0 {
		b.WriteString("\n")
	}
	emitPropReports(b, cc, ctx.Pkg.PropReports, kc)
	emitInstanceRegistration(b, own, kc)

	vc := &irComposeContext{
		nav:     nav,
		present: present,
		reads:   reads,
		kc:      compKC,
		ctx:     ctx,
		buf:     b,
		indent:  1,
		combo:   combo,
		// The window, where it is under a reactive `if` and so in a
		// composable of its own.
		screen:        screen,
		rootTagged:    takesTestTag(cc.Component),
		slotReceivers: scoped,
	}

	for _, s := range cc.Body {
		vc.renderStmt(s)
	}

	b.WriteString("}\n")
}

// readsOf is each distinct member reads reads or calls through an instance of
// comp.
func readsOf(reads []codegen.InstanceRead, comp *ir.Component) []codegen.InstanceRead {
	var out []codegen.InstanceRead
	seen := map[string]bool{}
	for _, r := range reads {
		if r.Comp != comp || seen[r.Member] {
			continue
		}
		seen[r.Member] = true
		out = append(out, r)
	}
	return out
}

// emitInstanceRegistration has a composable whose members a test reads
// register a closure for each in kotlin.InstanceRegistry while it is
// composed, under the entry its call site hands it: its state is the
// composable's own, which nothing outside the composition can name.
func emitInstanceRegistration(b *strings.Builder, reads []codegen.InstanceRead, kc *kotlin.KtIRContext) {
	if len(reads) == 0 {
		return
	}
	key := kotlin.InstanceKeyParam
	fmt.Fprintf(b, "    %s(%s) {\n", kc.Name(ktDisposableEffect), key)
	fmt.Fprintf(b, "        %s[%s] = mapOf<String, (List<Any?>) -> Any?>(\n", kotlin.InstanceRegistry, key)
	for _, r := range reads {
		// A computed is a derived value the composable remembers, read by
		// name rather than called.
		if r.Func != nil && codegen.IsComputed(r.Func) {
			fmt.Fprintf(b, "            %q to { _ -> %s },\n", r.Member, r.Func.Name)
			continue
		}
		if r.Func != nil {
			var args []string
			i := 0
			for _, p := range r.Func.Params {
				if p.Receiver {
					continue
				}
				args = append(args, fmt.Sprintf("a[%d] as %s", i, kotlin.IRTypeToKt(p.Type)))
				i++
			}
			fmt.Fprintf(b, "            %q to { a -> %s(%s) },\n", r.Member, r.Func.Name, strings.Join(args, ", "))
			continue
		}
		name := r.Member
		if r.Var != nil {
			name = r.Var.Name
		}
		fmt.Fprintf(b, "            %q to { _ -> %s },\n", r.Member, name)
	}
	b.WriteString("        )\n")
	fmt.Fprintf(b, "        onDispose { %s.remove(%s) }\n    }\n\n", kotlin.InstanceRegistry, key)
}

// emitIRKtMemberFunc emits a user func as a method inside a class
// body (Android test mode: members of MainScreenState). The body is
// translated with `kc`'s scope; reads of class members resolve via
// implicit `this` because `kc.IdentRewrites` is intentionally not
// set on the caller-provided context here.
func emitIRKtMemberFunc(b *strings.Builder, fn *ir.Func, kc *kotlin.KtIRContext) {
	// passNoImplicitRecv's receiver param is the component the func was
	// written in, whose state is this class's fields -- so it is `this` here
	// as it is in the composable, and never something a call site passes.
	// Left in, the class declared `fun press__inst0(this: Any, k: Key)`.
	fnParams := fn.Params
	if len(fnParams) > 0 && isReceiverParam(fn, fnParams[0]) {
		fnParams = fnParams[1:]
	}
	params := make([]string, len(fnParams))
	for i, p := range fnParams {
		ktType := kotlin.IRTypeToKt(p.Type)
		params[i] = kotlin.SafeIdent(p.Name) + ": " + ktType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fnParams {
		localKC = localKC.WithLocal(p.Name)
	}
	for _, name := range codegen.BodyLocals(fn.Block) {
		localKC = localKC.WithLocal(name)
	}

	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := localKC.EvalExpr(ret.Value)
			fmt.Fprintf(b, "    %sfun %s(%s)%s = %s\n", ktSuspend(fn), fn.Name, paramStr, retType, body)
			return
		}
	}

	if codegen.HasBody(fn) {
		fmt.Fprintf(b, "    %sfun %s(%s)%s {\n", ktSuspend(fn), fn.Name, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localKC.EvalStmt(stmt) {
				fmt.Fprintf(b, "        %s\n", line)
			}
		}
		b.WriteString("    }\n")
	}
}

// ktSuspend is the modifier an async function is declared with: a call that
// waits is a call of a suspend function, made from another or from a
// coroutine the caller started.
func ktSuspend(fn *ir.Func) string {
	if fn.IsAsync {
		return "suspend "
	}
	return ""
}

// isReceiverParam reports whether p is fn's receiver, which the checker marks
// whatever the declaration called it.
func isReceiverParam(fn *ir.Func, p *ir.Param) bool {
	return fn.IsMethod() && p.Receiver
}

// emitIRKtMethod emits a method on a type as a Kotlin extension
// function. The explicit receiver parameter is dropped: the body already
// names it `this`, which is what an extension's receiver is called.
//
// Skipping every func with a receiver left `Calc.pending` undeclared while
// `state.pending()` was emitted at two call sites.
func emitIRKtMethod(b *strings.Builder, fn *ir.Func, kc *kotlin.KtIRContext) {
	recv := exportName(fn.RecvName())
	if len(fn.Params) > 0 && isReceiverParam(fn, fn.Params[0]) {
		recv = kotlin.IRTypeToKt(fn.Params[0].Type)
	}
	fnCopy := *fn
	fnCopy.Name = recv + "." + fn.Name
	// Dropped here rather than left to emitIRKtFunc, which decides by asking
	// fn.Receiver -- and this copy is about to stop having one.
	localKC := kc
	if len(fnCopy.Params) > 0 && isReceiverParam(fn, fnCopy.Params[0]) {
		// The body names the receiver whatever the declaration called it
		// (`func Op.symbol(o Op)`), and an extension's receiver is `this`.
		localKC = localKC.WithReceiver(fnCopy.Params[0])
		fnCopy.Params = fnCopy.Params[1:]
	}
	fnCopy.Receiver = nil
	emitIRKtFunc(b, &fnCopy, localKC)
}

func emitIRKtFunc(b *strings.Builder, fn *ir.Func, kc *kotlin.KtIRContext) {
	// A declaration that *is* a Kotlin identifier is not emitted: every call
	// to it became a call to that identifier, so a declaration here would be
	// read by nobody -- and it is written as a stub over the zero value, which
	// reads exactly like a real implementation. The same rule JavaScript got
	// for `#[js.native]`.
	if fn.Foreign.Name != "" && !fn.Foreign.Marked {
		return
	}
	// passNoImplicitRecv puts the receiver in front of the parameters as
	// `this`, which is a Kotlin keyword and never what the call site passes:
	// an extension takes it as the receiver, and a component's func is called
	// by name from inside the composable that holds its state.
	fnParams := fn.Params
	if len(fnParams) > 0 && isReceiverParam(fn, fnParams[0]) {
		fnParams = fnParams[1:]
	}
	params := make([]string, len(fnParams))
	for i, p := range fnParams {
		ktType := kotlin.IRTypeToKt(p.Type)
		params[i] = kotlin.SafeIdent(p.Name) + ": " + ktType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeVoid {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fnParams {
		localKC = localKC.WithLocal(p.Name)
	}
	for _, name := range codegen.BodyLocals(fn.Block) {
		localKC = localKC.WithLocal(name)
	}

	// Expression body
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := localKC.EvalExpr(ret.Value)
			fmt.Fprintf(b, "\n%sfun %s(%s)%s = %s\n", ktSuspend(fn), fn.Name, paramStr, retType, body)
			return
		}
	}

	// Block body
	if codegen.HasBody(fn) {
		fmt.Fprintf(b, "\n%sfun %s(%s)%s {\n", ktSuspend(fn), fn.Name, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localKC.EvalStmt(stmt) {
				fmt.Fprintf(b, "    %s\n", line)
			}
		}
		b.WriteString("}\n")
	}
}

// --- helpers ---

func irBindInitKt(typ *ir.Type, init ir.Expr) string {
	if init == nil {
		return ktZeroValue(typ)
	}
	return kotlin.IRLiteralToKt(init)
}

func irFuncReturnKt(f *ir.Func) string {
	if f.Return != nil {
		return kotlin.IRTypeToKt(f.Return)
	}
	if len(f.Block) == 1 {
		if ret, ok := f.Block[0].(*ir.Return); ok && ret.Value != nil {
			if t := ret.Value.ExprType(); t != nil {
				return kotlin.IRTypeToKt(t)
			}
		}
	}
	return "Any"
}

func ktZeroValue(t *ir.Type) string {
	if t == nil {
		return `""`
	}
	switch t.Kind {
	case ir.TypeInt:
		return "0"
	case ir.TypeFloat:
		return "0.0"
	case ir.TypeBool:
		return "false"
	case ir.TypeString:
		return `""`
	case ir.TypeList:
		return ""
	case ir.TypeEnum:
		// The language's zero, which for an enum is its first member. An empty
		// string is not one, and a constructor call carrying it did not
		// type-check.
		return kotlin.KtZeroFor(t)
	default:
		return `""`
	}
}

func listElementTypeKt(ktType string) string {
	if strings.HasPrefix(ktType, "List<") && strings.HasSuffix(ktType, ">") {
		return ktType[5 : len(ktType)-1]
	}
	return "Any"
}

// literalStructLit renders a struct literal whose every field is a literal as
// a Kotlin constructor call, or "" for anything else -- a data class header
// takes an expression naming nothing else this file declares, and only this
// shape is one.
func literalStructLit(n *ir.StructLit) string {
	name := ""
	switch {
	case ir.IsBuilder(n.Type):
		return "StringBuilder()"
	case n.Type != nil && ir.IsColorStruct(n.Type):
		name = "Color"
	case n.Def != nil:
		name = exportName(n.Def.Name)
	default:
		return ""
	}
	var args []string
	for _, f := range n.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok || f.Name == "" {
			return ""
		}
		v := kotlin.IRLiteralToKt(lit)
		if v == "" {
			return ""
		}
		args = append(args, f.Name+" = "+v)
	}
	return name + "(" + strings.Join(args, ", ") + ")"
}

// needsStateScope reports whether a func has to be a member of the hoisted
// state class rather than a top-level function.
//
// A component's own func does: its body names the component's vars. So does a
// func that reads or writes reactive state -- Reads/Writes are transitive, so
// a helper reaching one through another helper is included too.
//
// It is asked on both sides, because a func has to be in exactly one place and
// the two questions are the same one: whether a body owns a func decides where
// it goes, and being *listed* as a body's does not -- every package func is a
// window's by the time this runs.
//
// The body is walked rather than `fn.Reads`/`fn.Writes` asked: those are
// effect analysis's and are empty for a func the lowering synthesized, so
// `step__mark__inst0` -- which assigns the composable's `log__inst0` -- read
// as touching nothing and was emitted beside the composable.
//
// A walk of one body answers for that body and not for what it calls, so the
// transitive half is `reaches` -- codegen.PackageStateFuncs, which closes the
// same question over the call graph. A receiver used to stand in for it: a
// component's func carried one, so `step__inst0`, whose body names no state
// and only calls `step__mark__inst0`, was inside the composable for having a
// receiver rather than for reaching state. The inliner drops that receiver
// when it hoists the clone to the package, and Kotlin then had `step__inst0`
// at file scope calling a local `fun` of MainScreen.
//
// Everything else does not, and hoisting it anyway is not free: a method on a
// user type is an extension function at top level, so a `Calc.pending` calling
// the pure `format` found it only as `state.format`, against a `state` nothing
// in that scope declares.
// stateScopedFuncs is every func that has to be declared where the screen's
// state is in scope: one needsStateScope says reads it, and then, to a fixed
// point, one calling such a func -- which is spelled through the scope as
// `state.f(…)` in test mode and is a local fun of the composable otherwise,
// so a caller declared outside it names something nothing binds there. A
// method on a user type is an extension function at top level and reaches no
// state, so calling one confers nothing.
func stateScopedFuncs(ctx *codegen.CodegenCtx, state map[string]struct{}, reaches map[*ir.Func]bool) map[*ir.Func]bool {
	funcs := ctx.AllFuncs()
	scoped := map[*ir.Func]bool{}
	for _, fn := range funcs {
		if fn.OnType() {
			continue
		}
		if needsStateScope(fn, state, reaches) {
			scoped[fn] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range funcs {
			if scoped[fn] || fn.OnType() {
				continue
			}
			_ = ir.WalkExprs(fn.Block, func(e ir.Expr) error {
				if c, ok := e.(*ir.Call); ok && scoped[c.Func] {
					scoped[fn] = true
				}
				return nil
			})
			if scoped[fn] {
				changed = true
			}
		}
	}
	return scoped
}

func needsStateScope(fn *ir.Func, state map[string]struct{}, reaches map[*ir.Func]bool) bool {
	if fn.OnComponent() || reaches[fn] {
		return true
	}
	// State is matched by name, so what fn binds itself is no read of it: a
	// parameter, a loop's variable or a local named like a var of the page --
	// `display(name string)` beside a page's `name` -- put a pure function
	// on the state class, where a composable built on its own called it
	// through a `state` it does not have.
	own := map[ir.Symbol]bool{}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil {
			own[lv.Sym] = true
		}
		return nil
	})
	found := false
	ir.WalkExprs(fn.Block, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok {
			switch id.Sym.(type) {
			case *ir.Param, *ir.LoopVar:
				return nil
			}
			if own[id.Sym] {
				return nil
			}
			if _, isState := state[id.Name]; isState {
				found = true
			}
		}
		return nil
	})
	return found
}

// sharedPageState is the page's state a composable other than MainScreen reads
// or writes, itself or through a package function it calls (composedCallees),
// and what those vars' initializers read, by name.
func sharedPageState(ctx *codegen.CodegenCtx) map[string]bool {
	page := map[ir.Symbol]bool{}
	for _, ov := range ctx.ModelState() {
		page[ov.Sym] = true
	}
	out := map[string]bool{}
	visit := func(root any) {
		_ = ir.Walk(root, func(nd ir.Node) error {
			if id, ok := nd.(*ir.Ident); ok && id.Sym != nil && page[id.Sym] {
				out[id.Name] = true
			}
			return nil
		})
	}
	for _, root := range composedRoots(ctx) {
		visit(root)
	}
	for fn := range composedCallees(ctx) {
		visit(fn.Block)
	}
	// A file-level declaration is initialized at file level, so what its
	// initializer reads has to be declared there too.
	for changed := true; changed; {
		changed = false
		for _, ov := range ctx.ModelState() {
			if !out[ov.Name()] {
				continue
			}
			n := len(out)
			visit(ov.Init())
			changed = changed || len(out) > n
		}
	}
	return out
}

// emitPropReports marks the reports whose key reads a prop and settles them
// when the prop changes. Elsewhere the prop's setter does (settleOnSet); a
// composable has none, and is called again with the new value, so the change
// is seen when it is applied. The first value is the one the brackets began
// with, and reports nothing, as a setter handed what the prop holds does not.
func emitPropReports(b *strings.Builder, cc *codegen.ComponentCtx, reports map[*ir.Param][]ir.PropReport, kc *kotlin.KtIRContext) {
	for _, p := range cc.Props {
		rs := reports[p.Sym]
		if len(rs) == 0 {
			continue
		}
		seen := "__prop_" + p.Name + "_seen"
		fmt.Fprintf(b, "    val %s = %s { arrayOf<Any?>(%s) }\n", seen, kc.Name(ktRemember), p.Name)
		fmt.Fprintf(b, "    %s(%s) {\n", kc.Name(ktDisposableEffect), p.Name)
		fmt.Fprintf(b, "        if (%s[0] != %s) {\n", seen, p.Name)
		fmt.Fprintf(b, "            %s[0] = %s\n", seen, p.Name)
		var settles []*ir.Func
		for _, r := range rs {
			fmt.Fprintf(b, "            %s = true\n", r.Dirty.Name)
			if !slices.Contains(settles, r.Settle) {
				settles = append(settles, r.Settle)
			}
		}
		for _, settle := range settles {
			fmt.Fprintf(b, "            %s()\n", settle.Name)
		}
		b.WriteString("        }\n        onDispose {}\n    }\n\n")
	}
}

// composedRoots is what the composables other than MainScreen run: their
// bodies, vars' initializers, computeds and funcs.
func composedRoots(ctx *codegen.CodegenCtx) []any {
	var roots []any
	for _, cc := range ctx.NonRootComponents() {
		roots = append(roots, cc.Body)
		for _, v := range cc.Vars {
			if v != nil && v.Init != nil {
				roots = append(roots, v.Init)
			}
		}
		for _, fns := range [][]*ir.Func{cc.Funcs, cc.Computeds} {
			for _, fn := range fns {
				if fn != nil {
					roots = append(roots, fn.Block)
				}
			}
		}
	}
	return roots
}

// composedCallees is every package function a composable other than MainScreen
// calls, and what those call in turn. Such a composable reaches the page only
// through the file, so these are declared there in an app, where they would
// otherwise be local funs of MainScreen, and what they read is shared.
func composedCallees(ctx *codegen.CodegenCtx) map[*ir.Func]bool {
	pkgFunc := map[*ir.Func]bool{}
	for _, fn := range ctx.Pkg.Funcs {
		if fn != nil && !fn.OnType() && !fn.IsTest && !codegen.IsComputed(fn) {
			pkgFunc[fn] = true
		}
	}
	out := map[*ir.Func]bool{}
	var queue []*ir.Func
	collect := func(root any) {
		_ = ir.Walk(root, func(nd ir.Node) error {
			if c, ok := nd.(*ir.Call); ok && pkgFunc[c.Func] && !out[c.Func] {
				out[c.Func] = true
				queue = append(queue, c.Func)
			}
			return nil
		})
	}
	for _, root := range composedRoots(ctx) {
		collect(root)
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		collect(fn.Block)
	}
	return out
}

// pageTypeMethods makes each method on a type that reaches the page's state a
// function of the page, taking its receiver first, and points every call and
// reference at it. MainScreen holds that state as remembered locals, and the
// test build as members of its state class, so the method is declared in
// whichever holds it and called as the page's other functions are, where an
// extension is declared at top level.
func pageTypeMethods(ctx *codegen.CodegenCtx, cfg Config) {
	if cfg.GoLib {
		return
	}
	moved := map[*ir.Func]*ir.Func{}
	reaching := codegen.PackageStateFuncs(ctx.Pkg)
	for i, fn := range ctx.Pkg.Funcs {
		if !fn.OnType() || !reaching[fn] {
			continue
		}
		page := *fn
		page.Name = fn.RecvName() + "_" + fn.Name
		page.Receiver = nil
		moved[fn] = &page
		ctx.Pkg.Funcs[i] = &page
	}
	if len(moved) == 0 {
		return
	}
	_ = ir.Walk(ctx.Pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			if page, ok := moved[x.Func]; ok {
				x.Func = page
			}
		case *ir.Ident:
			if fn, ok := x.Sym.(*ir.Func); ok && moved[fn] != nil {
				x.Sym = moved[fn]
				x.Name = moved[fn].Name
			}
		}
		return nil
	})
}

// calleesFirst orders a composable's local funs so each is declared ahead of
// those that call it, keeping the written order otherwise: a local function
// may not be referenced above its declaration, and a lowering appends what it
// synthesizes -- passEffect's settle and notes, which a handler calls -- after
// the funcs that call it. A cycle cannot be declared locally either way, and
// is left in the order it was met.
func calleesFirst(fns []*ir.Func) []*ir.Func {
	in := make(map[*ir.Func]bool, len(fns))
	for _, fn := range fns {
		in[fn] = true
	}
	out := make([]*ir.Func, 0, len(fns))
	state := map[*ir.Func]int{} // 1 visiting, 2 done
	var visit func(fn *ir.Func)
	visit = func(fn *ir.Func) {
		if state[fn] != 0 {
			return
		}
		state[fn] = 1
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if c, ok := n.(*ir.Call); ok && c.Func != nil && in[c.Func] && c.Func != fn {
				visit(c.Func)
			}
			return nil
		})
		state[fn] = 2
		out = append(out, fn)
	}
	for _, fn := range fns {
		visit(fn)
	}
	return out
}

// namesParam reports whether e names a parameter, which a Kotlin default may
// do only of one declared before it.
func namesParam(e ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if id, ok := x.(*ir.Ident); ok {
			if _, ok := id.Sym.(*ir.Param); ok {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}
