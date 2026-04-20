package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// KtIRContext translates IR expressions and statements into Kotlin code.
type KtIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string // what "event" maps to in current handler scope
}

// NewIRContext creates a KtIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *KtIRContext {
	return &KtIRContext{Ctx: ctx}
}

// EvalExpr translates an IR expression into a Kotlin expression string.
func (kc *KtIRContext) EvalExpr(e ir.Expr) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return kc.evalLiteral(n)
	case *ir.Ident:
		return kc.evalIdent(n)
	case *ir.Binary:
		left := kc.EvalExpr(n.Left)
		right := kc.EvalExpr(n.Right)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := kc.EvalExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		cond := kc.EvalExpr(n.Cond)
		a := kc.EvalExpr(n.Then)
		b := kc.EvalExpr(n.Else)
		return "(if (" + cond + ") " + a + " else " + b + ")"
	case *ir.Select:
		operand := kc.EvalExpr(n.Operand)
		return operand + "." + n.Field
	case *ir.Index:
		operand := kc.EvalExpr(n.Operand)
		idx := kc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	case *ir.Call:
		return kc.evalCall(n)
	case *ir.Conversion:
		return kc.evalConversion(n)
	case *ir.StructLit:
		return kc.evalStructLit(n)
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = kc.EvalExpr(el)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ir.Spread:
		return "*" + kc.EvalExpr(n.Operand)
	case *ir.Lambda:
		return kc.evalLambda(n)
	default:
		return fmt.Sprintf("/* unsupported IR node %T */null", e)
	}
}

// EvalStmt translates an IR statement into Kotlin statement strings.
func (kc *KtIRContext) EvalStmt(s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		target := kc.evalMutTarget(n.Target)
		value := kc.EvalExpr(n.Value)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := kc.evalMutTarget(n.Target)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		return []string{kc.EvalExpr(n.Call)}
	case *ir.Emit:
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = kc.EvalExpr(a.Value)
		}
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"var " + n.Name + " = " + kc.EvalExpr(n.Init)}
		}
		goType := "Any"
		if n.Type != nil {
			goType = IRTypeToKt(n.Type)
		}
		return []string{"var " + n.Name + ": " + goType}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + kc.EvalExpr(n.Value)}
		}
		return []string{"return"}
	default:
		return []string{"// unsupported IR stmt: " + fmt.Sprintf("%T", s)}
	}
}

func (kc *KtIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Raw
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Raw)
	case ir.TypeInt:
		return n.Raw
	case ir.TypeFloat:
		s := n.Raw
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case ir.TypeBool:
		return n.Raw
	case ir.TypeNull:
		return "null"
	case ir.TypeColor:
		return fmt.Sprintf("%q", n.Raw)
	default:
		return n.Raw
	}
}

func (kc *KtIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}
	name := n.Name
	if n.Type != nil && n.Type.Kind == ir.TypeEnum {
		return fmt.Sprintf("%q", name)
	}
	_, kind := kc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return kc.Ctx.RenamedName(name)
	default:
		return name
	}
}

func (kc *KtIRContext) evalCall(n *ir.Call) string {
	if n.Receiver != nil {
		return kc.evalMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := kc.evalCallArgs(n.Args)
		switch fname {
		case "string":
			if len(args) == 1 {
				return args[0] + ".toString()"
			}
		case "int":
			if len(args) == 1 {
				return args[0] + ".toInt()"
			}
		case "float":
			if len(args) == 1 {
				return args[0] + ".toDouble()"
			}
		}
		return fname + "(" + strings.Join(args, ", ") + ")"
	}
	args := kc.evalCallArgs(n.Args)
	return "(" + strings.Join(args, ", ") + ")"
}

func (kc *KtIRContext) evalMethodCall(n *ir.Call) string {
	receiver := kc.EvalExpr(n.Receiver)
	args := kc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		receiverName := n.Func.Receiver

		// Builtin method check
		qualName := receiverName + "." + fname
		allArgs := append([]string{receiver}, args...)
		if result := kotlinBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		wildArgs := append([]string{receiver}, args...)
		if result := kotlinBuiltinMethodFromArgs("*."+fname, wildArgs); result != "" {
			return result
		}

		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}
	return receiver + "(" + strings.Join(args, ", ") + ")"
}

func (kc *KtIRContext) evalConversion(n *ir.Conversion) string {
	operand := kc.EvalExpr(n.Operand)
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt:
			return operand + ".toInt()"
		case ir.TypeFloat:
			return operand + ".toDouble()"
		case ir.TypeString:
			return operand + ".toString()"
		case ir.TypeBool:
			return operand + " as Boolean"
		}
	}
	return operand
}

func (kc *KtIRContext) evalStructLit(n *ir.StructLit) string {
	name := "Any"
	if n.Def != nil {
		name = exportName(n.Def.Name)
	}
	var parts []string
	for _, f := range n.Fields {
		if f.Spread {
			parts = append(parts, "/* ..."+kc.EvalExpr(f.Value)+" */")
		} else {
			parts = append(parts, f.Name+" = "+kc.EvalExpr(f.Value))
		}
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func (kc *KtIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "{ }"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := kc.EvalExpr(ret.Value)
			return "{ " + strings.Join(params, ", ") + " -> " + body + " }"
		}
	}
	var b strings.Builder
	b.WriteString("{ " + strings.Join(params, ", ") + " ->\n")
	for _, stmt := range n.Func.Block {
		for _, line := range kc.EvalStmt(stmt) {
			b.WriteString("    " + line + "\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func (kc *KtIRContext) evalCallArgs(args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = kc.EvalExpr(a.Value)
	}
	return out
}

func (kc *KtIRContext) evalMutTarget(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		return n.Name
	case *ir.Select:
		operand := kc.evalMutTarget(n.Operand)
		return operand + "." + n.Field
	case *ir.Index:
		operand := kc.evalMutTarget(n.Operand)
		idx := kc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	default:
		return kc.EvalExpr(e)
	}
}

// WithLocal returns a new context with an additional local variable.
func (kc *KtIRContext) WithLocal(name string) *KtIRContext {
	return &KtIRContext{
		Ctx:      kc.Ctx.WithLocal(name),
		EventVar: kc.EventVar,
	}
}

// ForComponent returns a new context scoped to a component.
func (kc *KtIRContext) ForComponent(comp *ir.Component) *KtIRContext {
	return &KtIRContext{
		Ctx:      kc.Ctx.ForComponent(comp),
		EventVar: kc.EventVar,
	}
}

// --- IR type → Kotlin type ---

// IRTypeToKt converts an IR type to a Kotlin type string.
func IRTypeToKt(t *ir.Type) string {
	if t == nil {
		return "Any"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "Boolean"
	case ir.TypeInt:
		return "Int"
	case ir.TypeFloat:
		return "Double"
	case ir.TypeString, ir.TypeColor,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname,
		ir.TypeDecimal:
		return "String"
	case ir.TypeList:
		if len(t.Elems) > 0 {
			return "List<" + IRTypeToKt(t.Elems[0]) + ">"
		}
		return "List<Any>"
	case ir.TypeOption:
		if len(t.Elems) > 0 {
			return IRTypeToKt(t.Elems[0]) + "?"
		}
		return "Any?"
	case ir.TypeStruct:
		if t.Decl != nil {
			return exportName(t.Decl.SymName())
		}
		return "Any"
	case ir.TypeEnum:
		if t.Decl != nil {
			return exportName(t.Decl.SymName())
		}
		return "String"
	case ir.TypeUnit:
		if t.Decl != nil && t.Decl.SymName() == "duration" {
			return "Long" // milliseconds
		}
		return "String"
	case ir.TypeFunc:
		return "Any" // TODO: proper function types
	case ir.TypeNull:
		return "Any?"
	default:
		return "Any"
	}
}

// IRLiteralToKt converts an IR literal expression to a Kotlin literal.
func IRLiteralToKt(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	switch n := e.(type) {
	case *ir.Literal:
		if n.Type == nil {
			return n.Raw
		}
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt:
			return n.Raw
		case ir.TypeFloat:
			s := n.Raw
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
		case ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "null"
		case ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		default:
			return n.Raw
		}
	case *ir.ListLit:
		if len(n.Elems) == 0 {
			return "emptyList()"
		}
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = IRLiteralToKt(el)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ir.StructLit:
		name := "Any"
		if n.Def != nil {
			name = exportName(n.Def.Name)
		}
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, f.Name+" = "+IRLiteralToKt(f.Value))
		}
		return name + "(" + strings.Join(parts, ", ") + ")"
	}
	return `""`
}

// kotlinBuiltinMethodFromArgs checks for builtin Kotlin methods.
func kotlinBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "null"
	}

	switch qualName {
	case "int.min", "*.min":
		return "minOf(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "maxOf(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "kotlin.math.abs(" + a(0) + ")"
	case "string.length", "*.length":
		return a(0) + ".length"
	case "string.upper", "*.upper":
		return a(0) + ".uppercase()"
	case "string.lower", "*.lower":
		return a(0) + ".lowercase()"
	case "string.trim", "*.trim":
		return a(0) + ".trim()"
	case "string.replace", "*.replace":
		return a(0) + ".replace(" + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "string.contains":
		return a(0) + ".contains(" + a(1) + ")"
	case "list.length":
		return a(0) + ".size"
	case "list.join", "*.join":
		return a(0) + ".joinToString(" + a(1) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	}
	return ""
}
