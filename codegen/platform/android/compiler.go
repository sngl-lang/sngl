package android

import (
	"fmt"
	"maps"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
		c.Color = "#6750A4" // Material 3 default primary
	}
	return c
}

// Compile generates a Kotlin source file from a checked SNGL document.
func Compile(doc *ast.Document, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	info := analyze(doc)
	src := emit(info, doc, cfg)
	return src, nil
}

// analysis results

type bindInfo struct {
	name   string
	ktType string
	init   string // Kotlin expression for default value
	isList bool
}

type computedInfo struct {
	name   string
	ktType string
}

type analysisResult struct {
	*codegen.CommonAnalysis
	binds     []bindInfo
	computeds []computedInfo
}

func analyze(doc *ast.Document) *analysisResult {
	common := codegen.AnalyzeCommon(doc)

	info := &analysisResult{
		CommonAnalysis: common,
	}

	// Platform-specific data field analysis (Kotlin types)
	for _, d := range doc.Data {
		if d.Extern {
			continue
		}
		ktType := inferKtType(d.Init)
		initVal := literalToKt(d.Init)
		isList := strings.HasPrefix(d.Init.TypeHint, "[]") || strings.HasPrefix(d.Init.TypeHint, "list:")
		if isList && initVal == `""` {
			initVal = ""
		}
		info.binds = append(info.binds, bindInfo{
			name:   d.Name,
			ktType: ktType,
			init:   initVal,
			isList: isList,
		})
	}

	// Platform-specific computed function analysis (Kotlin types)
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			ktType := inferKtType(fn.Body)
			if ktType == "Any" && fn.Body.SNGL != nil {
				ktType = snglNodeKtType(fn.Body.SNGL)
			}
			info.computeds = append(info.computeds, computedInfo{
				name:   fn.Name,
				ktType: ktType,
			})
		}
	}

	return info
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
	var b strings.Builder

	structFields := make(map[string][]string)
	for _, sd := range info.Structs {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		structFields[sd.Name] = fields
	}

	ec := &exprContext{
		modelFields:    info.ModelFields,
		computedFields: info.ComputedFields,
		localVars:      make(map[string]bool),
		structNames:    structFields,
	}

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

	// Data classes for user structs
	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "data class %s(\n", exportName(sd.Name))
		for i, f := range sd.Fields {
			ktType := typeHintToKt(f.Type)
			def := ""
			if f.Default.Literal != nil {
				def = " = " + literalToKt(f.Default)
			}
			comma := ","
			if i == len(sd.Fields)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    val %s: %s%s%s\n", f.Name, ktType, def, comma)
		}
		b.WriteString(")\n\n")
	}

	// Enum classes
	for _, ed := range info.Enums {
		fmt.Fprintf(&b, "enum class %s {\n", exportName(ed.Name))
		for i, v := range ed.Values {
			comma := ","
			if i == len(ed.Values)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "    %s%s\n", strings.ToUpper(v), comma)
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
			elemType := listElementType(bind.ktType)
			fmt.Fprintf(&b, "    val %s = remember { mutableStateListOf<%s>() }\n", bind.name, elemType)
		} else {
			fmt.Fprintf(&b, "    var %s by remember { mutableStateOf(%s) }\n", bind.name, bind.init)
		}
	}

	// Computed state
	for _, comp := range info.computeds {
		body := ""
		if cfg.GoLib {
			// Call into Go module for computed values
			body = "golib.Golib." + exportName(comp.name) + "()"
		} else {
			for _, fn := range doc.Functions {
				if fn.Name == comp.name && fn.Body.SNGL != nil && len(fn.Params) == 0 {
					if fn.Body.SNGL != nil {
						body = ec.translateExpr(fn.Body.SNGL)
					} else if fn.Body.Literal != nil {
						body = literalToKt(fn.Body)
					}
					break
				}
			}
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
		stmts := ec.translateMutation(t.Body)
		for _, s := range stmts {
			fmt.Fprintf(&b, "            %s\n", s)
		}
		b.WriteString("        }\n")
		b.WriteString("    }\n\n")
	}

	// Visual tree
	vc := &composeContext{
		ec:         ec,
		buf:        &b,
		indent:     1,
		components: info.Components,
	}

	if doc.App != nil {
		if len(doc.App.Children) == 1 {
			vc.renderNode(doc.App.Children[0])
		} else {
			vc.line("Column {")
			vc.indent++
			for _, child := range doc.App.Children {
				vc.renderNode(child)
			}
			vc.indent--
			vc.line("}")
		}
	}

	b.WriteString("}\n")

	// User-defined component composables (not stdlib overrides or abstract components)
	for _, comp := range doc.Components {
		emitComponentComposable(&b, comp, info.Components, ec)
	}

	// User-defined functions — when GoLib is true, these live in the Go
	// module and are called via Golib.FuncName(); otherwise emit inline Kotlin.
	if !cfg.GoLib {
		for _, fn := range doc.Functions {
			if fn.IsStdlib || fn.ReturnType == "" {
				continue
			}
			emitKtFunc(&b, fn, ec)
		}
	}

	return []byte(b.String())
}

func emitComponentComposable(b *strings.Builder, comp *ast.Component, allComponents []*ast.Component, ec *exprContext) {
	b.WriteString("\n@Composable\n")
	var params []string
	for _, p := range comp.Params {
		ktType := inferKtType(p.Default)
		def := ""
		if p.Default.Literal != nil {
			def = " = " + literalToKt(p.Default)
		}
		params = append(params, p.Name+": "+ktType+def)
	}
	hasSlot := comp.ChildrenType != ""
	if hasSlot {
		params = append(params, "slotContent: @Composable () -> Unit = {}")
	}
	fmt.Fprintf(b, "fun %s(%s) {\n", exportName(comp.Name), strings.Join(params, ", "))

	// Add params as local vars
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, ec.localVars)
	for _, p := range comp.Params {
		ec.localVars[p.Name] = true
	}

	vc := &composeContext{
		ec:         ec,
		buf:        b,
		indent:     1,
		components: allComponents,
		hasSlot:    hasSlot,
	}

	for _, child := range comp.Body {
		vc.renderNode(child)
	}

	b.WriteString("}\n")

	ec.localVars = savedLocals
}

func emitKtFunc(b *strings.Builder, fn *ast.FuncDef, ec *exprContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		ktType := typeHintToKt(p.Type)
		params[i] = p.Name + ": " + ktType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.ReturnType != "" {
		retType = ": " + typeHintToKt(fn.ReturnType)
	}

	isTypeMethod := strings.Contains(fn.Name, ".")
	ktName := fn.Name
	if typeName, methodName, ok := ast.SplitMethodName(fn.Name); ok {
		ktName = typeName + exportName(methodName)
	}
	if !isTypeMethod {
		ktName = fn.Name
	}

	if fn.Body.SNGL != nil {
		for _, p := range fn.Params {
			ec.localVars[p.Name] = true
		}
		body := ec.translateExpr(fn.Body.SNGL)
		for _, p := range fn.Params {
			delete(ec.localVars, p.Name)
		}
		fmt.Fprintf(b, "\nfun %s(%s)%s = %s\n", ktName, paramStr, retType, body)
	} else if fn.Block != nil {
		fmt.Fprintf(b, "\nfun %s(%s)%s {\n", ktName, paramStr, retType)
		for _, p := range fn.Params {
			ec.localVars[p.Name] = true
		}
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				ec.localVars[s.Name] = true
				val := ec.translateExpr(s.Init)
				fmt.Fprintf(b, "    var %s = %s\n", s.Name, val)
			default:
				stmts := ec.translateMutation(stmt)
				for _, line := range stmts {
					fmt.Fprintf(b, "    %s\n", line)
				}
			}
		}
		if fn.Block.Return != nil {
			ret := ec.translateExpr(fn.Block.Return)
			fmt.Fprintf(b, "    return %s\n", ret)
		}
		for _, p := range fn.Params {
			delete(ec.localVars, p.Name)
		}
		for _, stmt := range fn.Block.Stmts {
			if s, ok := stmt.(*ast.VarStmt); ok {
				delete(ec.localVars, s.Name)
			}
		}
		b.WriteString("}\n")
	}
}

// Helper functions

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

func inferKtType(expr ast.Expr) string {
	if expr.TypeHint != "" {
		return typeHintToKt(expr.TypeHint)
	}
	if expr.Literal != nil {
		switch expr.Literal.(type) {
		case int:
			return "Int"
		case float64:
			return "Double"
		case bool:
			return "Boolean"
		case string:
			return "String"
		}
	}
	return "Any"
}

func typeHintToKt(hint string) string {
	if strings.HasPrefix(hint, "[]") {
		return "List<" + exportName(hint[2:]) + ">"
	}
	if strings.HasPrefix(hint, "list:") {
		return "List<" + exportName(hint[5:]) + ">"
	}
	if strings.HasPrefix(hint, "option:") {
		return typeHintToKt(hint[7:]) + "?"
	}
	if strings.HasPrefix(hint, "enum:") {
		return "String"
	}
	switch hint {
	case "int":
		return "Int"
	case "float":
		return "Double"
	case "bool":
		return "Boolean"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "String"
	case "date", "time", "dateTime":
		return "String" // Android dates as strings for now
	case "duration":
		return "Long" // milliseconds
	default:
		return "Any"
	}
}

func listElementType(listType string) string {
	if strings.HasPrefix(listType, "List<") && strings.HasSuffix(listType, ">") {
		return listType[5 : len(listType)-1]
	}
	return "Any"
}

func snglNodeKtType(e ast.Node) string {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		switch n.Kind {
		case ast.LiteralInt:
			return "Int"
		case ast.LiteralFloat:
			return "Double"
		case ast.LiteralBool:
			return "Boolean"
		case ast.LiteralString:
			return "String"
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte, ast.BinAnd, ast.BinOr:
			return "Boolean"
		default:
			lt := snglNodeKtType(n.Left)
			rt := snglNodeKtType(n.Right)
			if lt == "Double" || rt == "Double" {
				return "Double"
			}
			return lt
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNot {
			return "Boolean"
		}
		return snglNodeKtType(n.Operand)
	case *ast.TernaryExpr:
		return snglNodeKtType(n.Then)
	case *ast.CallExpr:
		switch n.Func {
		case "string":
			return "String"
		case "int":
			return "Int"
		case "float":
			return "Double"
		case "size":
			return "Int"
		}
	case *ast.ParenExpr:
		return snglNodeKtType(n.Inner)
	}
	return "Any"
}

func literalToKt(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			if expr.TypeHint == "float" {
				return fmt.Sprintf("%d.0", v)
			}
			return fmt.Sprintf("%d", v)
		case float64:
			s := fmt.Sprintf("%v", v)
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return `""`
}

// exprToKtValue converts an ast.Expr to a Kotlin value string.
func exprToKtValue(expr ast.Expr, ec *exprContext) string {
	if expr.Literal != nil {
		return literalToKt(expr)
	}
	if expr.SNGL != nil && ec != nil {
		return ec.translateExpr(expr.SNGL)
	}
	return `""`
}

// exprToKtCond converts an ast.Expr to a Kotlin boolean expression string.
func exprToKtCond(expr ast.Expr, ec *exprContext) string {
	if expr.Literal != nil {
		if v, ok := expr.Literal.(bool); ok {
			if v {
				return "true"
			}
			return "false"
		}
	}
	if expr.SNGL != nil && ec != nil {
		return ec.translateExpr(expr.SNGL)
	}
	return "true"
}
