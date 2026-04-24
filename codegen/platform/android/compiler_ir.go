package android

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Config controls code generation.
type Config struct {
	Package      string // Kotlin package name (default: "test.sngl.app")
	AppName      string // display name for the app (default: derived from package)
	GenerateMain bool   // emit MainActivity.kt + project scaffold
	Gradle       bool   // use Gradle build system (default: true)
	GoLib        bool   // true when user funcs live in a Go module (gomobile bind)
	Icon         string // path to icon file (SVG or PNG), relative to project root
	Color        string // theme/icon background color as hex (#RRGGBB)
	ProjectDir   string // project root directory (for resolving relative icon paths)
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "test.sngl.app"
	}
	if c.Color == "" {
		c.Color = "#6750A4"
	}
	return c
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
	src := emitIR(info, ctx, cfg)
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
	init   string
	isList bool
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
		info.binds = append(info.binds, irAndroidBind{
			name:   v.Name,
			ktType: ktType,
			init:   initVal,
			isList: isList,
		})
	}

	allFuncs := pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
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

func emitIR(info *irAndroidAnalysis, ctx *codegen.CodegenCtx, cfg Config) []byte {
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	kc := kotlin.NewIRContext(exprCtx)

	var b strings.Builder

	// Package
	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)

	// Imports
	b.WriteString("import androidx.compose.foundation.background\n")
	b.WriteString("import androidx.compose.foundation.clickable\n")
	b.WriteString("import androidx.compose.foundation.layout.*\n")
	b.WriteString("import androidx.compose.foundation.rememberScrollState\n")
	b.WriteString("import androidx.compose.foundation.verticalScroll\n")
	b.WriteString("import androidx.compose.material3.*\n")
	b.WriteString("import androidx.compose.material3.pulltorefresh.PullToRefreshBox\n")
	b.WriteString("import androidx.compose.runtime.*\n")
	b.WriteString("import androidx.compose.ui.Alignment\n")
	b.WriteString("import androidx.compose.ui.Modifier\n")
	b.WriteString("import androidx.compose.ui.draw.alpha\n")
	b.WriteString("import androidx.compose.ui.draw.clip\n")
	b.WriteString("import androidx.compose.ui.graphics.Color\n")
	b.WriteString("import androidx.compose.ui.text.TextStyle\n")
	b.WriteString("import androidx.compose.ui.text.font.FontWeight\n")
	b.WriteString("import androidx.compose.ui.text.style.TextAlign\n")
	b.WriteString("import androidx.compose.ui.unit.dp\n")
	b.WriteString("import androidx.compose.ui.unit.sp\n")
	b.WriteString("import androidx.compose.ui.window.Dialog\n")
	if len(info.Timers) > 0 {
		b.WriteString("import kotlinx.coroutines.delay\n")
	}
	if info.NeedsToast {
		b.WriteString("import android.widget.Toast\n")
		b.WriteString("import androidx.compose.ui.platform.LocalContext\n")
	}
	if cfg.GoLib {
		b.WriteString("import golib.Golib\n")
	}
	b.WriteString("\n")

	// Data classes
	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "data class %s(\n", exportName(sd.Name))
		for i, f := range sd.Fields {
			ktType := kotlin.IRTypeToKt(f.Type)
			def := ""
			if f.Default != nil {
				if _, ok := f.Default.(*ir.Literal); ok {
					def = " = " + kotlin.IRLiteralToKt(f.Default)
				}
			}
			comma := ","
			if i == len(sd.Fields)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    val %s: %s%s%s\n", f.Name, ktType, def, comma)
		}
		b.WriteString(")\n\n")
	}

	// ErrorEvent is emitted when any error-handling construct is present
	// (see bubbletea comment). The stdlib struct is not flowed through
	// user output, so materialise it here.
	if codegen.PackageUsesErrorHandling(ctx.Pkg) {
		b.WriteString("data class ErrorEvent(val message: String = \"\", val kind: String = \"\")\n\n")
	}

	// Enum classes
	for _, ed := range info.Enums {
		fmt.Fprintf(&b, "enum class %s {\n", exportName(ed.Name))
		for i, m := range ed.Members {
			comma := ","
			if i == len(ed.Members)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %s%s\n", strings.ToUpper(m.Name), comma)
		}
		b.WriteString("}\n\n")
	}

	// Main composable
	b.WriteString("@OptIn(ExperimentalMaterial3Api::class)\n")
	b.WriteString("@Composable\n")
	b.WriteString("fun MainScreen() {\n")

	if info.NeedsToast {
		b.WriteString("    val context = LocalContext.current\n")
	}

	// State declarations
	for _, bind := range info.binds {
		if bind.isList {
			elemType := listElementTypeKt(bind.ktType)
			if bind.init != "" {
				fmt.Fprintf(&b, "    val %s = remember { mutableStateListOf(%s) }\n", bind.name, bind.init)
			} else {
				fmt.Fprintf(&b, "    val %s = remember { mutableStateListOf<%s>() }\n", bind.name, elemType)
			}
		} else {
			fmt.Fprintf(&b, "    var %s by remember { mutableStateOf(%s) }\n", bind.name, bind.init)
		}
	}

	// Computed state
	for _, comp := range info.computeds {
		body := ""
		if cfg.GoLib {
			body = "golib.Golib." + exportName(comp.name) + "()"
		} else if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = kc.EvalExpr(ret.Value)
			}
		}
		if body == "" {
			body = `""`
		}
		fmt.Fprintf(&b, "    val %s by remember { derivedStateOf { %s } }\n", comp.name, body)
	}

	if len(info.binds) > 0 || len(info.computeds) > 0 {
		b.WriteString("\n")
	}

	// Timers
	for _, t := range info.Timers {
		fmt.Fprintf(&b, "    LaunchedEffect(%s) {\n", t.ActiveVar)
		fmt.Fprintf(&b, "        while (%s) {\n", t.ActiveVar)
		fmt.Fprintf(&b, "            delay(%dL)\n", t.IntervalMs)
		for _, stmt := range t.Body {
			for _, line := range kc.EvalStmt(stmt) {
				fmt.Fprintf(&b, "            %s\n", line)
			}
		}
		b.WriteString("        }\n")
		b.WriteString("    }\n\n")
	}

	// Visual tree
	cc := &irComposeContext{
		kc:     kc,
		ctx:    ctx,
		buf:    &b,
		indent: 1,
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

	b.WriteString("}\n")

	// User component composables
	for _, comp := range ctx.NonMainComponents() {
		emitIRComponentComposable(&b, comp, ctx, kc)
	}

	// User functions (non-GoLib)
	if !cfg.GoLib {
		allFuncs := ctx.Pkg.Funcs
		if main := ctx.MainComponent(); main != nil {
			allFuncs = append(allFuncs, main.Funcs...)
		}
		for _, fn := range allFuncs {
			if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
				continue
			}
			if fn.Return == nil || fn.Return.Kind == ir.TypeDyn {
				continue
			}
			emitIRKtFunc(&b, fn, kc)
		}
	}

	return []byte(b.String())
}

func emitIRComponentComposable(b *strings.Builder, cc *codegen.ComponentCtx, ctx *codegen.CodegenCtx, kc *kotlin.KtIRContext) {
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
	}

	for _, s := range cc.Body {
		vc.renderStmt(s)
	}

	b.WriteString("}\n")
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
