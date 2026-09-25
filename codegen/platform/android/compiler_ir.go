package android

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/ir"
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

	// Lang globals (codegen/lang/golang/golang.sngl).
	GoVersion string // Go toolchain version for generated go.mod (default: "1.23")

	// Internal — set by Generate(), not from source.
	GoLib bool // true when user funcs live in a Go module (gomobile bind)
}

func (c Config) withDefaults() Config {
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
	cfg = cfg.withDefaults()
	info := analyzeIR(ctx)
	src := emitIR(info, ctx, cfg, false)
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
	cfg = cfg.withDefaults()
	info := analyzeIR(ctx)
	src := emitIR(info, ctx, cfg, true)
	return src, ctx.Err()
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
}

// stateTypeArg returns an explicit type argument ("<String?>") for
// mutableStateOf when Kotlin can't infer the element type from the
// initializer — i.e. when the init is `null`. The bind's ktType already
// carries the nullable form (option<T> → "T?", dyn → "Any?").
func stateTypeArg(bind irAndroidBind, initVal string) string {
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
func listStateInitKt(bind irAndroidBind, initVal string) string {
	elems := initVal
	if strings.HasPrefix(elems, "listOf(") && strings.HasSuffix(elems, ")") {
		elems = elems[len("listOf(") : len(elems)-1]
	} else if bind.initEx != nil && strings.TrimSpace(initVal) != "" {
		return initVal + ".toMutableStateList()"
	}
	if elems == "" {
		return "mutableStateListOf<" + listElementTypeKt(bind.ktType) + ">()"
	}
	return "mutableStateListOf(" + elems + ")"
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
		return "derivedStateOf { " + ktComputedZero(comp) + " }"
	}
	if len(comp.fn.Block) == 1 {
		if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			if v := kc.EvalExpr(ret.Value); v != "" {
				return "derivedStateOf { " + v + " }"
			}
		}
	}
	retType := comp.ktType
	if retType == "" {
		retType = "Any"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "derivedStateOf(fun(): %s {\n", retType)
	for _, stmt := range comp.fn.Block {
		for _, line := range kc.EvalStmt(stmt) {
			fmt.Fprintf(&b, "%s    %s\n", indent, line)
		}
	}
	fmt.Fprintf(&b, "%s})", indent)
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

// bindFor is bindForVar for a binding with no declaration behind it -- a
// window's route parameters, which the slot population declares and the
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

func emitIR(info *irAndroidAnalysis, ctx *codegen.CodegenCtx, cfg Config, testMode bool) []byte {
	exprCtx := ctx.ScopedExprCtx()
	kc := kotlin.NewIRContext(exprCtx)

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
	//
	// This used to be every window's funcs, which reached the same set from
	// the other side: a window body's func, and every clone the inliner
	// hoisted into one, was a window's. A window owns nothing now, so the
	// question is asked of the body instead of of the list -- which is also
	// what ctx.RootDecl() stopped answering for an ordinary program, `main`
	// having lost its harness convention.
	stateReaching := codegen.PackageStateFuncs(ctx.Pkg)
	for fn := range stateReaching {
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

	var stateFuncs []*ir.Func
	if testMode && !cfg.GoLib {
		allFuncs := ctx.AllFuncs()
		for _, fn := range allFuncs {
			if fn.IsTest || codegen.IsComputed(fn) {
				continue
			}
			// A method on a struct or an enum is an extension function and
			// belongs nowhere near the state class -- it is called on a value
			// of that type. A *component's* func has a receiver too (the
			// component it was written in), and that one is exactly what the
			// class holds: its state is the class's fields. Excluding both
			// left a component's func emitted by nobody in test mode, which
			// is what `press__inst0` was.
			if fn.Receiver != "" && kotlin.ReceiverIsUserType(ctx.Pkg, fn.Receiver) {
				continue
			}
			if !needsStateScope(fn, stateNames, stateReaching) {
				continue
			}
			if fn.Return != nil && fn.Return.Kind == ir.TypeDyn {
				continue
			}
			stateFuncs = append(stateFuncs, fn)
		}
	}
	stateMembers := make(map[*ir.Func]bool, len(stateFuncs))
	for _, fn := range stateFuncs {
		stateMembers[fn] = true
	}

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
	}

	// Under --lang go the user's functions are emitted into the gomobile
	// module, exported and capitalised, so a Kotlin call site has to name them
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
			if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
				continue
			}
			if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
				continue
			}
			kc.IdentRewrites[fn.Name] = "golib.Golib." + exportName(fn.Name)
		}
	}

	// Pass 1: register all known imports before rendering the body.
	// Structural Compose imports are always required for any Compose UI.
	kc.RequireImport("androidx.compose.foundation.background")
	kc.RequireImport("androidx.compose.foundation.clickable")
	kc.RequireImport("androidx.compose.foundation.layout.*")
	kc.RequireImport("androidx.compose.foundation.rememberScrollState")
	kc.RequireImport("androidx.compose.foundation.verticalScroll")
	kc.RequireImport("androidx.compose.material3.*")
	kc.RequireImport("androidx.compose.material3.pulltorefresh.PullToRefreshBox")
	kc.RequireImport("androidx.compose.runtime.*")
	kc.RequireImport("androidx.compose.ui.Alignment")
	kc.RequireImport("androidx.compose.ui.Modifier")
	kc.RequireImport("androidx.compose.ui.platform.testTag")
	kc.RequireImport("androidx.compose.ui.draw.alpha")
	kc.RequireImport("androidx.compose.ui.draw.clip")
	// Aliased to ComposeColor so the SNGL stdlib `color` struct (emitted as
	// a `Color` data class for Canvas2D) doesn't collide with the framework
	// graphics Color. All framework-color uses below say ComposeColor.
	kc.RequireImport("androidx.compose.ui.graphics.Color as ComposeColor")
	kc.RequireImport("androidx.compose.ui.text.TextStyle")
	kc.RequireImport("androidx.compose.ui.text.font.FontWeight")
	kc.RequireImport("androidx.compose.ui.text.style.TextAlign")
	kc.RequireImport("androidx.compose.ui.unit.dp")
	kc.RequireImport("androidx.compose.ui.unit.sp")
	kc.RequireImport("androidx.compose.ui.window.Dialog")
	// Conditional imports derived from IR analysis (no code scanning needed).
	if len(info.Timers) > 0 {
		kc.RequireImport("kotlinx.coroutines.delay")
	}
	if info.NeedsToast {
		kc.RequireImport("android.widget.Toast")
		kc.RequireImport("androidx.compose.ui.platform.LocalContext")
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
		fmt.Fprintf(&body, "data class %s(\n", exportName(sd.Name))
		for i, f := range sd.Fields {
			ktType := kotlin.IRTypeToKt(f.Type)
			def := ""
			if f.Default != nil {
				// A literal default is emitted as written, and so is a struct
				// literal of them -- `fill color = color{a=0}` is the one
				// CanvasStyle writes, and a transparent fill is not the type's
				// zero. Anything else is an expression a data class header
				// cannot hold — `false` is a constant declaration whose own
				// initializer is `0 != 0` — so the field takes the type's
				// Kotlin zero.
				lit := kotlin.KtZeroFor(f.Type)
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
			fmt.Fprintf(&body, "    var %s: %s%s%s\n", f.Name, ktType, def, comma)
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
	if hasCanvas || namesColor(info.Structs) {
		body.WriteString(colorKotlinDecl(declaredStructs))
	}

	// Canvas2D stdlib structs (CanvasStyle/PathCmd). Stdlib-only structs
	// aren't carried on pkg.Structs, so materialize them here (analogous to
	// canvasutil for the Go platforms) whenever the program contains a canvas.
	if hasCanvas {
		body.WriteString(canvasKotlinDecls(declaredStructs))
	}

	// ErrorEvent is emitted when any error-handling construct is present
	// (see bubbletea comment). The stdlib struct is not flowed through
	// user output, so materialise it here.
	if ctx.Pkg.UsesErrorHandling {
		body.WriteString("data class ErrorEvent(val message: String = \"\", val kind: String = \"\")\n\n")
		body.WriteString("class SnglRaise(val event: ErrorEvent) : RuntimeException(event.message)\n\n")
	}

	// The two stdlib event payloads this platform materializes itself, each
	// gated on a program reaching the site that spells it -- ErrorEvent's
	// shape, because a data class nothing names is dead Kotlin in every file
	// that never takes an event.
	//
	// InputEvent is built from the value Compose hands a callback
	// (emitInputHandlerCall, and the lambda a declared intrinsic takes), so it
	// is named for this platform. ChangeEvent is built by an override --
	// `change({value=opt})` in radio and input -- and the emitter writes a
	// struct's name as declared, so it keeps its own. Neither flows through
	// info.Structs, which is what makes both of these declarations necessary
	// rather than duplicates.
	if usesInputHolder(ctx.Pkg) {
		body.WriteString("data class SnglInputEvent(val value: String)\n\n")
	}
	// A program declaring a struct of that name gets its own data class below
	// and must not get this one as well.
	if usesChangeEvent(ctx.Pkg) &&
		!slices.ContainsFunc(info.Structs, func(sd *ir.StructDef) bool { return exportName(sd.Name) == "ChangeEvent" }) {
		body.WriteString("data class ChangeEvent(val value: String = \"\")\n\n")
	}

	// string(float) is a helper rather than a method call: Kotlin's own
	// Double.toString writes a fraction a whole number does not have.
	body.WriteString(kotlin.FloatStringDecl)
	body.WriteString("\n")
	body.WriteString(kotlin.SplitDecl)
	body.WriteString("\n")

	// Struct-merge helpers for opaque spreads (flatten_struct_spread lowering).
	if mf := kotlin.EmitMergeFuncs(ctx.Pkg.MergeStructs); mf != "" {
		body.WriteString(mf)
	}

	// Enum classes
	for _, ed := range info.Enums {
		fmt.Fprintf(&body, "enum class %s {\n", exportName(ed.Name))
		for i, m := range ed.Members {
			comma := ","
			if i == len(ed.Members)-1 {
				comma = ""
			}
			fmt.Fprintf(&body, "    %s%s\n", kotlin.EnumEntry(m.Name), comma)
		}
		body.WriteString("}\n\n")
	}

	for _, c := range ctx.Pkg.Consts {
		if c.Init == nil {
			continue
		}
		fmt.Fprintf(&body, "val %s: %s = %s\n\n", c.Name, kotlin.IRTypeToKt(c.Type), kc.EvalExpr(c.Init))
	}

	// State hoisting (test mode): emit a MainScreenState class
	// with the binds as `var x by mutableStateOf(...)`. The
	// composable takes one as a parameter so tests can hold a
	// reference for assertions/mutations from outside the
	// composition.
	if testMode {
		body.WriteString("class MainScreenState {\n")
		// Init expressions inside the class body resolve sibling state vars
		// via implicit `this`, NOT the `state` parameter (which doesn't exist
		// here). Use a rewrite-free context so e.g. `__ctx_locale__inst1 =
		// __ctx_locale` rather than `state.__ctx_locale`.
		classKC := kotlin.NewIRContext(exprCtx)
		for _, bind := range info.binds {
			initVal := bind.init
			if bind.initEx != nil {
				initVal = classKC.EvalExpr(bind.initEx)
			}
			if bind.isList {
				fmt.Fprintf(&body, "    val %s = %s\n", bind.name, listStateInitKt(bind, initVal))
			} else {
				fmt.Fprintf(&body, "    var %s by mutableStateOf%s(%s)\n", bind.name, stateTypeArg(bind, initVal), initVal)
			}
		}
		// Computeds become `derivedStateOf` members so tests can read them via
		// the state accessor (c.<name>). Init exprs use classKC (bare sibling
		// refs, resolved via implicit `this`).
		for _, comp := range info.computeds {
			fmt.Fprintf(&body, "    val %s by %s\n", comp.name, computedCalcKt(comp, cfg, classKC, "    "))
		}
		// Component-level user funcs become members of the state
		// class so their bodies resolve reactive vars via implicit
		// `this` rather than the out-of-scope `state` parameter
		// they'd see as top-level functions. Use a fresh context
		// with no IdentRewrites: inside the class, `__ctx_locale`
		// resolves via implicit `this`, not `state.__ctx_locale`.
		if len(stateFuncs) > 0 {
			memberKC := kotlin.NewIRContext(exprCtx)
			for _, fn := range stateFuncs {
				emitIRKtMemberFunc(&body, fn, memberKC)
			}
		}
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
				fmt.Fprintf(&body, "val %s = %s\n\n", bind.name, listStateInitKt(bind, initVal))
			} else {
				fmt.Fprintf(&body, "var %s by mutableStateOf%s(%s)\n\n", bind.name, stateTypeArg(bind, initVal), initVal)
			}
		}
	}

	// Main composable
	body.WriteString("@OptIn(ExperimentalMaterial3Api::class)\n")
	body.WriteString("@Composable\n")
	if testMode {
		body.WriteString("fun MainScreen(state: MainScreenState = remember { MainScreenState() }) {\n")
	} else {
		body.WriteString("fun MainScreen() {\n")
	}

	if info.NeedsToast {
		body.WriteString("    val context = LocalContext.current\n")
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
				fmt.Fprintf(&body, "    val %s = remember { %s }\n", bind.name, listStateInitKt(bind, initVal))
			} else {
				fmt.Fprintf(&body, "    var %s by remember { mutableStateOf%s(%s) }\n", bind.name, stateTypeArg(bind, initVal), initVal)
			}
		}
	}

	// Computed state. In test mode computeds live on MainScreenState (emitted
	// above) so the test accessor can read them; here they're locals only in
	// non-test mode.
	if !testMode {
		for _, comp := range info.computeds {
			fmt.Fprintf(&body, "    val %s by remember { %s }\n", comp.name, computedCalcKt(comp, cfg, kc, "    "))
		}
	}

	if len(info.binds) > 0 || len(info.computeds) > 0 {
		body.WriteString("\n")
	}

	// The component's own funcs, as local funs closing over the state
	// declared just above. In test mode they are members of MainScreenState
	// instead, and the call sites are rewritten to reach them there.
	if !testMode && !cfg.GoLib {
		for _, fn := range ctx.AllFuncs() {
			if !mainOwnFuncs[fn] || fn.IsTest || codegen.IsComputed(fn) {
				continue
			}
			if !needsStateScope(fn, stateNames, stateReaching) {
				continue
			}
			if fn.Return != nil && fn.Return.Kind == ir.TypeDyn {
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

	// Timers
	for _, t := range info.Timers {
		// The gate as an expression, not the bare variable name it used to be
		// reduced to. `enabled=true` names no variable, and neither does a gate
		// folded from an enclosing branch -- both produced an empty ActiveVar,
		// which came out as `LaunchedEffect() { while () {`. That is not Kotlin,
		// and no golden covered an android timer to say so.
		//
		// EvalExpr routes idents through the same rewrite map the body uses, so
		// a var that lives on the hoisted state object in test mode is spelled
		// the way the body spells it.
		gate := "true"
		if t.Enabled != nil {
			gate = kc.EvalExpr(t.Enabled)
		}
		fmt.Fprintf(&body, "    LaunchedEffect(%s) {\n", gate)
		fmt.Fprintf(&body, "        while (%s) {\n", gate)
		fmt.Fprintf(&body, "            delay(%dL)\n", t.IntervalMs)
		for _, stmt := range t.Body {
			for _, line := range kc.EvalStmt(stmt) {
				fmt.Fprintf(&body, "            %s\n", line)
			}
		}
		body.WriteString("        }\n")
		body.WriteString("    }\n\n")
	}

	// Visual tree
	cc := &irComposeContext{
		kc:     kc,
		ctx:    ctx,
		buf:    &body,
		indent: 1,
		combo:  cfg.combo(),
		// This one renders the window's own content. A sub-component's
		// composable never does, so the cutout padding belongs here alone.
		atRoot: true,
	}

	wins := ctx.Windows()
	if len(wins) > 0 && len(wins[0].Body) > 0 {
		bodyStmts := wins[0].Body
		if len(bodyStmts) == 1 {
			cc.renderStmt(bodyStmts[0])
		} else {
			cc.line("Column {")
			cc.indent++
			for _, s := range bodyStmts {
				cc.renderStmt(s)
			}
			cc.indent--
			cc.line("}")
		}
	}

	body.WriteString("}\n")

	// User component composables
	for _, comp := range ctx.NonRootComponents() {
		emitIRComponentComposable(&body, comp, ctx, kc, cfg, cfg.combo())
	}

	// User functions (non-GoLib). In test mode the state class holds them,
	// with one exception: a method on a struct or an enum is an extension
	// function either way -- the class is the component's state and a method
	// on a value of a user type has nothing to do with it. Skipped in both
	// places, `Calc.pending` was declared by nobody while every call site
	// spelled it.
	if !cfg.GoLib {
		allFuncs := ctx.AllFuncs()
		for _, fn := range allFuncs {
			if fn.IsTest || codegen.IsComputed(fn) {
				continue
			}
			if testMode && stateMembers[fn] {
				continue
			}
			if fn.Return != nil && fn.Return.Kind == ir.TypeDyn {
				continue
			}
			// A method on a user type is called as one -- `state.pending()` --
			// so it is an extension function, and the receiver passNoImplicitRecv
			// put in front of the parameters is Kotlin's own `this`. A
			// component's own func is not that: it is called by bare name from
			// the composable, and the component is no Kotlin type to extend.
			if fn.Receiver != "" && kotlin.ReceiverIsUserType(ctx.Pkg, fn.Receiver) {
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
			if componentOwnFuncs[fn] && needsStateScope(fn, stateNames, stateReaching) {
				continue
			}
			emitIRKtFunc(&body, fn, kc)
		}
	}

	// Assemble final output: package clause + imports (collected during
	// pass 1 and dynamically during pass 2) + body.
	var out strings.Builder
	fmt.Fprintf(&out, "package %s\n\n", cfg.Package)
	for _, imp := range kc.Imports() {
		fmt.Fprintf(&out, "import %s\n", imp)
	}
	out.WriteString("\n")
	out.WriteString(body.String())
	return []byte(out.String())
}

func emitIRComponentComposable(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, kc *kotlin.KtIRContext, cfg Config, combo androidtc.Combo) {
	b.WriteString("\n@Composable\n")
	var params []string
	for _, p := range cc.Props {
		ktType := kotlin.IRTypeToKt(p.Type)
		def := ""
		if p.Default != nil {
			if _, ok := p.Default.(*ir.Literal); ok {
				def = " = " + kotlin.IRLiteralToKt(p.Default)
			}
		}
		params = append(params, p.Name+": "+ktType+def)
	}
	hasSlot := cc.Component.ChildrenType != nil
	if hasSlot {
		params = append(params, "slotContent: @Composable () -> Unit = {}")
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
			fmt.Fprintf(b, "    val %s = remember { %s }\n", bind.name, listStateInitKt(bind, initVal))
		} else {
			fmt.Fprintf(b, "    var %s by remember { mutableStateOf%s(%s) }\n", bind.name, stateTypeArg(bind, initVal), initVal)
		}
		decls++
	}
	for _, fn := range cc.Computeds {
		comp := irAndroidComputed{name: fn.Name, ktType: irFuncReturnKt(fn), fn: fn}
		fmt.Fprintf(b, "    val %s by remember { %s }\n", comp.name, computedCalcKt(comp, cfg, compKC, "    "))
		decls++
	}
	for _, fn := range cc.Funcs {
		if fn.IsTest || codegen.IsComputed(fn) {
			continue
		}
		if fn.Return != nil && fn.Return.Kind == ir.TypeDyn {
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

	vc := &irComposeContext{
		kc:      compKC,
		ctx:     ctx,
		buf:     b,
		indent:  1,
		hasSlot: hasSlot,
		combo:   combo,
	}

	for _, s := range cc.Body {
		vc.renderStmt(s)
	}

	b.WriteString("}\n")
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
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fnParams {
		localKC = localKC.WithLocal(p.Name)
	}

	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := localKC.EvalExpr(ret.Value)
			fmt.Fprintf(b, "    fun %s(%s)%s = %s\n", fn.Name, paramStr, retType, body)
			return
		}
	}

	if len(fn.Block) > 0 {
		fmt.Fprintf(b, "    fun %s(%s)%s {\n", fn.Name, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localKC.EvalStmt(stmt) {
				fmt.Fprintf(b, "        %s\n", line)
			}
		}
		b.WriteString("    }\n")
	}
}

// isReceiverParam reports whether p is the receiver passNoImplicitRecv put in
// front of a method's parameters. It is usually named `this`, but the name is
// the declaration's to choose -- `func Op.symbol(o Op)` calls it `o`, and
// matching on the name alone left it in the signature of an extension whose
// receiver it already was.
func isReceiverParam(fn *ir.Func, p *ir.Param) bool {
	if fn.Receiver == "" {
		return false
	}
	if p.Name == "this" {
		return true
	}
	return p.Type != nil && p.Type.Decl != nil && p.Type.Decl.SymName() == fn.Receiver
}

// emitIRKtMethod emits a method on a user type as a Kotlin extension
// function. The explicit receiver parameter is dropped: the body already
// names it `this`, which is what an extension's receiver is called.
//
// Skipping every func with a receiver left `Calc.pending` undeclared while
// `state.pending()` was emitted at two call sites.
func emitIRKtMethod(b *strings.Builder, fn *ir.Func, kc *kotlin.KtIRContext) {
	recv := exportName(fn.Receiver)
	fnCopy := *fn
	fnCopy.Name = recv + "." + fn.Name
	// Dropped here rather than left to emitIRKtFunc, which decides by asking
	// fn.Receiver -- and this copy is about to stop having one.
	localKC := kc
	if len(fnCopy.Params) > 0 && isReceiverParam(fn, fnCopy.Params[0]) {
		// The body names the receiver whatever the declaration called it
		// (`func Op.symbol(o Op)`), and an extension's receiver is `this`.
		if name := fnCopy.Params[0].Name; name != "this" {
			localKC = localKC.WithIdentRewrite(name, "this")
		}
		fnCopy.Params = fnCopy.Params[1:]
	}
	fnCopy.Receiver = ""
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
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fnParams {
		localKC = localKC.WithLocal(p.Name)
	}

	// Expression body
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := localKC.EvalExpr(ret.Value)
			fmt.Fprintf(b, "\nfun %s(%s)%s = %s\n", fn.Name, paramStr, retType, body)
			return
		}
	}

	// Block body
	if len(fn.Block) > 0 {
		fmt.Fprintf(b, "\nfun %s(%s)%s {\n", fn.Name, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localKC.EvalStmt(stmt) {
				fmt.Fprintf(b, "    %s\n", line)
			}
		}
		b.WriteString("}\n")
	}
}

// --- helpers ---

func irVarInitKt(v *ir.Var) string { return irBindInitKt(v.Type, v.Init) }

func irBindInitKt(typ *ir.Type, init ir.Expr) string {
	if init == nil {
		return ktZeroValue(typ)
	}
	return kotlin.IRLiteralToKt(init)
}

func irFuncReturnKt(f *ir.Func) string {
	if f.Return != nil && f.Return.Kind != ir.TypeDyn {
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
func needsStateScope(fn *ir.Func, state map[string]struct{}, reaches map[*ir.Func]bool) bool {
	if fn.Receiver != "" || reaches[fn] {
		return true
	}
	found := false
	ir.WalkExprs(fn.Block, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok {
			if _, isState := state[id.Name]; isState {
				found = true
			}
		}
		return nil
	})
	return found
}

// sharedPageState is the page's state a composable other than MainScreen reads
// or writes, and what those vars' initializers read, by name.
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
	for _, cc := range ctx.NonRootComponents() {
		visit(cc.Body)
		for _, fn := range cc.Funcs {
			if fn != nil {
				visit(fn.Block)
			}
		}
		for _, h := range cc.Handlers {
			if h != nil && h.Func != nil {
				visit(h.Func.Block)
			}
		}
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
