package android

import (
	"fmt"
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
	return src, nil
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
	return src, nil
}

// irAnalysis is the IR-based replacement for analysisResult.
type irAndroidAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irAndroidBind
	computeds []irAndroidComputed
}

type irAndroidBind struct {
	name   string
	ktType string
	init   string  // pre-computed literal init (used for simple values)
	initEx ir.Expr // raw IR expression when init needs EvalExpr (e.g. i18n calls)
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

// computedValueKt renders the Kotlin expression for a computed's derivedStateOf
// body using the supplied context (which controls state-var prefixing).
func computedValueKt(comp irAndroidComputed, cfg Config, kc *kotlin.KtIRContext) string {
	if cfg.GoLib {
		return "golib.Golib." + exportName(comp.name) + "()"
	}
	if comp.fn != nil && len(comp.fn.Block) == 1 {
		if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			if v := kc.EvalExpr(ret.Value); v != "" {
				return v
			}
		}
	}
	return `""`
}

type irAndroidComputed struct {
	name   string
	ktType string
	fn     *ir.Func
}

func analyzeIR(ctx *codegen.CodegenCtx) *irAndroidAnalysis {
	info := &irAndroidAnalysis{
		CommonAnalysis: ctx.Analysis,
	}

	pkg := ctx.Pkg

	allVars := pkg.Vars
	if main := ctx.MainComponent(); main != nil {
		allVars = append(allVars, main.Vars...)
	}
	for _, v := range allVars {
		if v.IsConst {
			continue
		}
		ktType := kotlin.IRTypeToKt(v.Type)
		initVal := irVarInitKt(v)
		isList := v.Type != nil && v.Type.Kind == ir.TypeList
		if isList && (initVal == `""` || initVal == "emptyList()") {
			initVal = ""
		}
		// When the init is a non-literal expression (e.g. an i18n.tr call),
		// IRLiteralToKt returns "" — store the raw expr so emitIR can
		// re-evaluate it via kc.EvalExpr.
		var initEx ir.Expr
		if initVal == `""` && v.Init != nil {
			if _, isLit := v.Init.(*ir.Literal); !isLit {
				initEx = v.Init
			}
		}
		info.binds = append(info.binds, irAndroidBind{
			name:   v.Name,
			ktType: ktType,
			init:   initVal,
			initEx: initEx,
			isList: isList,
		})
	}

	allFuncs := ctx.AllFuncs()
	for _, f := range allFuncs {
		if codegen.IsComputed(f) {
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
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	kc := kotlin.NewIRContext(exprCtx)

	// Collect the non-computed, non-GoLib user funcs we'll emit as
	// MainScreenState members in test mode. Need this list in two
	// places: (1) to register `name → state.name` rewrites so call
	// sites in the composable route through `state`, and (2) to
	// actually emit them inside the class body below.
	var stateFuncs []*ir.Func
	if testMode && !cfg.GoLib {
		allFuncs := ctx.AllFuncs()
		for _, fn := range allFuncs {
			if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
				continue
			}
			// Synthesized canvas draw funcs hold canvas-intrinsic CallStmts
			// that only the canvas translation understands; they're inlined
			// into the Canvas {} DrawScope lambda, not emitted as funcs.
			if isCanvasDrawFunc(fn) {
				continue
			}
			if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
				continue
			}
			stateFuncs = append(stateFuncs, fn)
		}
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
	if cfg.GoLib {
		kc.RequireImport("golib.Golib")
	}
	// i18n import is registered dynamically by kc.RequireImport during
	// body rendering whenever an I18n.* call is emitted.

	// Pass 2: render body. kc.RequireImport fires for any i18n calls.
	var body strings.Builder

	// Data classes
	for _, sd := range info.Structs {
		fmt.Fprintf(&body, "data class %s(\n", exportName(sd.Name))
		for i, f := range sd.Fields {
			ktType := kotlin.IRTypeToKt(f.Type)
			def := ""
			if f.Default != nil {
				// A literal default is emitted as written. Anything else is
				// an expression a data class header cannot hold — `false` is
				// a constant declaration whose own initializer is `0 != 0` —
				// so the field takes the type's Kotlin zero.
				lit := kotlin.KtZeroFor(f.Type)
				if _, isLit := f.Default.(*ir.Literal); isLit {
					if s := kotlin.IRLiteralToKt(f.Default); s != "" {
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

	// Canvas2D stdlib structs (Color/CanvasStyle/PathCmd) + color helper.
	// Stdlib-only structs aren't carried on pkg.Structs, so materialize them
	// here (analogous to canvasutil for the Go platforms) whenever the
	// program contains a canvas. ComposeColor is the aliased framework color
	// import; Color is our SNGL `color` data class.
	if packageHasCanvas(ctx.Pkg) {
		declared := map[string]struct{}{}
		for _, sd := range info.Structs {
			switch sd.Name {
			case "Color", "CanvasStyle", "PathCmd":
				declared[exportName(sd.Name)] = struct{}{}
			}
		}
		body.WriteString(canvasKotlinDecls(declared))
	}

	// ErrorEvent is emitted when any error-handling construct is present
	// (see bubbletea comment). The stdlib struct is not flowed through
	// user output, so materialise it here.
	if ctx.Pkg.UsesErrorHandling {
		body.WriteString("data class ErrorEvent(val message: String = \"\", val kind: String = \"\")\n\n")
	}

	// Stdlib InputEvent / ChangeEvent payload — emitInputHandlerCall
	// in compose_ir.go materializes the handler's event param as
	// `val e = SnglInputEvent(newValue)` so user code reading
	// `e.value` resolves without flowing the stdlib struct through
	// user output.
	body.WriteString("data class SnglInputEvent(val value: String)\n\n")

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
			fmt.Fprintf(&body, "    %s%s\n", strings.ToUpper(m.Name), comma)
		}
		body.WriteString("}\n\n")
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
				elemType := listElementTypeKt(bind.ktType)
				elems := initVal
				if strings.HasPrefix(elems, "listOf(") && strings.HasSuffix(elems, ")") {
					elems = elems[len("listOf(") : len(elems)-1]
				}
				if elems != "" {
					fmt.Fprintf(&body, "    val %s = mutableStateListOf(%s)\n", bind.name, elems)
				} else {
					fmt.Fprintf(&body, "    val %s = mutableStateListOf<%s>()\n", bind.name, elemType)
				}
			} else {
				fmt.Fprintf(&body, "    var %s by mutableStateOf%s(%s)\n", bind.name, stateTypeArg(bind, initVal), initVal)
			}
		}
		// Computeds become `derivedStateOf` members so tests can read them via
		// the state accessor (c.<name>). Init exprs use classKC (bare sibling
		// refs, resolved via implicit `this`).
		for _, comp := range info.computeds {
			compVal := computedValueKt(comp, cfg, classKC)
			fmt.Fprintf(&body, "    val %s by derivedStateOf { %s }\n", comp.name, compVal)
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
			initVal := bind.init
			if bind.initEx != nil {
				// Non-literal init (e.g. i18n.tr call): evaluate via the full IR context.
				initVal = kc.EvalExpr(bind.initEx)
			}
			if bind.isList {
				elemType := listElementTypeKt(bind.ktType)
				elems := initVal
				if strings.HasPrefix(elems, "listOf(") && strings.HasSuffix(elems, ")") {
					elems = elems[len("listOf(") : len(elems)-1]
				}
				if elems != "" {
					fmt.Fprintf(&body, "    val %s = remember { mutableStateListOf(%s) }\n", bind.name, elems)
				} else {
					fmt.Fprintf(&body, "    val %s = remember { mutableStateListOf<%s>() }\n", bind.name, elemType)
				}
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
			compVal := computedValueKt(comp, cfg, kc)
			fmt.Fprintf(&body, "    val %s by remember { derivedStateOf { %s } }\n", comp.name, compVal)
		}
	}

	if len(info.binds) > 0 || len(info.computeds) > 0 {
		body.WriteString("\n")
	}

	// Timers
	for _, t := range info.Timers {
		// ActiveVar is a bare reactive-var name; in test mode it lives on the
		// hoisted state object, so route it through the same rewrite map the
		// body uses (e.g. `animating` → `state.animating`).
		activeVar := t.ActiveVar
		if kc.IdentRewrites != nil {
			if rw, ok := kc.IdentRewrites[activeVar]; ok {
				activeVar = rw
			}
		}
		fmt.Fprintf(&body, "    LaunchedEffect(%s) {\n", activeVar)
		fmt.Fprintf(&body, "        while (%s) {\n", activeVar)
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
	for _, comp := range ctx.NonMainComponents() {
		emitIRComponentComposable(&body, comp, ctx, kc, cfg.combo())
	}

	// User functions (non-GoLib). In test mode these were emitted
	// as members of MainScreenState already.
	if !cfg.GoLib && !testMode {
		allFuncs := ctx.AllFuncs()
		for _, fn := range allFuncs {
			if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
				continue
			}
			// Canvas draw funcs are inlined into the Canvas {} DrawScope
			// lambda; never emit them as standalone Kotlin funcs.
			if isCanvasDrawFunc(fn) {
				continue
			}
			if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
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

func emitIRComponentComposable(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, kc *kotlin.KtIRContext, combo androidtc.Combo) {
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
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		ktType := kotlin.IRTypeToKt(p.Type)
		params[i] = p.Name + ": " + ktType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fn.Params {
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

func emitIRKtFunc(b *strings.Builder, fn *ir.Func, kc *kotlin.KtIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		ktType := kotlin.IRTypeToKt(p.Type)
		params[i] = p.Name + ": " + ktType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = ": " + kotlin.IRTypeToKt(fn.Return)
	}

	localKC := kc
	for _, p := range fn.Params {
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

func irVarInitKt(v *ir.Var) string {
	if v.Init == nil {
		return ktZeroValue(v.Type)
	}
	return kotlin.IRLiteralToKt(v.Init)
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
