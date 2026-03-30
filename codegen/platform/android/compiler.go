package android

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Config controls code generation.
type Config struct {
	Package      string // Kotlin package name (default: "app")
	GenerateMain bool   // emit MainActivity.kt + project scaffold
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "test.sngl.app"
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

type timerInfo struct {
	intervalMs int
	activeVar  string
	body       ast.Node
}

type analysisResult struct {
	binds          []bindInfo
	computeds      []computedInfo
	timers         []timerInfo
	components     []*ast.Component
	structs        []*ast.StructDef
	enums          []*ast.EnumDef
	modelFields    map[string]bool
	computedFields map[string]bool
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
	}

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
		info.modelFields[d.Name] = true
	}

	for _, c := range doc.Computeds {
		ktType := inferKtType(c.Expr)
		if ktType == "Any" && c.Expr.SNGL != nil {
			ktType = snglNodeKtType(c.Expr.SNGL)
		}
		info.computeds = append(info.computeds, computedInfo{
			name:   c.Name,
			ktType: ktType,
		})
		info.modelFields[c.Name] = true
		info.computedFields[c.Name] = true
	}

	for _, t := range doc.Timers {
		ms := intervalToMs(t.Interval)
		info.timers = append(info.timers, timerInfo{
			intervalMs: ms,
			activeVar:  t.Active,
			body:       t.Body,
		})
	}

	info.components = doc.AllComponents()
	info.structs = doc.Structs
	info.enums = doc.Enums

	return info
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
	var b strings.Builder

	structFields := make(map[string][]string)
	for _, sd := range info.structs {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		structFields[sd.Name] = fields
	}

	ec := &exprContext{
		modelFields:    info.modelFields,
		computedFields: info.computedFields,
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
	if len(info.timers) > 0 {
		b.WriteString("import kotlinx.coroutines.delay\n")
	}
	b.WriteString("\n")

	// Data classes for user structs
	for _, sd := range info.structs {
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
	for _, ed := range info.enums {
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
		for _, c := range doc.Computeds {
			if c.Name == comp.name {
				if c.Expr.SNGL != nil {
					body = ec.translateExpr(c.Expr.SNGL)
				} else if c.Expr.Literal != nil {
					body = literalToKt(c.Expr)
				}
				break
			}
		}
		fmt.Fprintf(&b, "    val %s by remember { derivedStateOf { %s } }\n", comp.name, body)
	}

	if len(info.binds) > 0 || len(info.computeds) > 0 {
		b.WriteString("\n")
	}

	// Timers
	for _, t := range info.timers {
		fmt.Fprintf(&b, "    LaunchedEffect(%s) {\n", t.activeVar)
		fmt.Fprintf(&b, "        while (%s) {\n", t.activeVar)
		fmt.Fprintf(&b, "            delay(%dL)\n", t.intervalMs)
		stmts := ec.translateMutation(t.body)
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
		components: info.components,
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

	// User-defined component composables
	for _, comp := range info.components {
		emitComponentComposable(&b, comp, info.components, ec)
	}

	// User-defined functions (only pure functions that return values;
	// void/mutation functions can't be top-level since they need Compose state)
	for _, fn := range doc.Functions {
		if fn.IsStdlib || fn.ReturnType == "" {
			continue
		}
		emitKtFunc(&b, fn, ec)
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
	fmt.Fprintf(b, "fun %s(%s) {\n", exportName(comp.Name), strings.Join(params, ", "))

	// Add params as local vars
	savedLocals := make(map[string]bool)
	for k, v := range ec.localVars {
		savedLocals[k] = v
	}
	for _, p := range comp.Params {
		ec.localVars[p.Name] = true
	}

	vc := &composeContext{
		ec:         ec,
		buf:        b,
		indent:     1,
		components: allComponents,
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
	}
	return "Any"
}

func intervalToMs(expr ast.Expr) int {
	if expr.SNGL == nil {
		return 0
	}
	lit, ok := expr.SNGL.(*ast.LiteralExpr)
	if !ok || lit.Kind != ast.LiteralUnit {
		return 0
	}
	ul, ok := lit.Value.(ast.UnitLiteral)
	if !ok {
		return 0
	}
	num := 0.0
	fmt.Sscanf(ul.Number, "%f", &num)
	switch ul.Suffix {
	case "ms":
		return int(num)
	case "s":
		return int(num * 1000)
	case "m":
		return int(num * 60000)
	case "h":
		return int(num * 3600000)
	}
	return int(num)
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
