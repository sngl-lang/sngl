package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// JsIRContext translates IR expressions and statements into JavaScript code.
type JsIRContext struct {
	Ctx      *codegen.ExprCtx
	EventVar string
}

// NewIRContext creates a JsIRContext from a codegen ExprCtx.
func NewIRContext(ctx *codegen.ExprCtx) *JsIRContext {
	return &JsIRContext{Ctx: ctx}
}

// EvalExpr translates an IR expression into a JavaScript expression string.
func (jc *JsIRContext) EvalExpr(e ir.Expr) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ir.Literal:
		return jc.evalLiteral(n)
	case *ir.Ident:
		return jc.evalIdent(n)
	case *ir.Binary:
		left := jc.EvalExpr(n.Left)
		right := jc.EvalExpr(n.Right)
		return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
	case *ir.Unary:
		operand := jc.EvalExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ir.Ternary:
		return "(" + jc.EvalExpr(n.Cond) + " ? " + jc.EvalExpr(n.Then) + " : " + jc.EvalExpr(n.Else) + ")"
	case *ir.Select:
		return jc.EvalExpr(n.Operand) + "." + n.Field
	case *ir.Index:
		return jc.EvalExpr(n.Operand) + "[" + jc.EvalExpr(n.Idx) + "]"
	case *ir.Call:
		return jc.evalCall(n)
	case *ir.Conversion:
		return jc.evalConversion(n)
	case *ir.StructLit:
		var parts []string
		for _, f := range n.Fields {
			if f.Spread {
				parts = append(parts, "..."+jc.EvalExpr(f.Value))
			} else {
				parts = append(parts, f.Name+": "+jc.EvalExpr(f.Value))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = jc.EvalExpr(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ir.Spread:
		return "..." + jc.EvalExpr(n.Operand)
	case *ir.Lambda:
		return jc.evalLambda(n)
	default:
		return fmt.Sprintf("/* unsupported IR %T */null", e)
	}
}

// EvalStmt translates an IR statement into JS statement strings.
func (jc *JsIRContext) EvalStmt(s ir.Stmt) []string {
	switch n := s.(type) {
	case *ir.Assign:
		target := jc.evalMutTarget(n.Target)
		value := jc.EvalExpr(n.Value)
		op := assignOpStr(n.Op)
		return []string{target + " " + op + " " + value}
	case *ir.Toggle:
		target := jc.evalMutTarget(n.Target)
		return []string{target + " = !" + target}
	case *ir.CallStmt:
		return []string{jc.EvalExpr(n.Call)}
	case *ir.Emit:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = jc.EvalExpr(a.Value)
		}
		return []string{"emit(" + fmt.Sprintf("%q", n.Name) + ", " + strings.Join(argStrs, ", ") + ")"}
	case *ir.LocalVar:
		if n.Init != nil {
			return []string{"let " + n.Name + " = " + jc.EvalExpr(n.Init)}
		}
		return []string{"let " + n.Name}
	case *ir.Return:
		if n.Value != nil {
			return []string{"return " + jc.EvalExpr(n.Value)}
		}
		return []string{"return"}
	default:
		return []string{"// unsupported IR stmt: " + fmt.Sprintf("%T", s)}
	}
}

func (jc *JsIRContext) evalLiteral(n *ir.Literal) string {
	if n.Type == nil {
		return n.Raw
	}
	switch n.Type.Kind {
	case ir.TypeString:
		return fmt.Sprintf("%q", n.Raw)
	case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return n.Raw
	case ir.TypeNull:
		return "null"
	case ir.TypeColor:
		return fmt.Sprintf("%q", n.Raw)
	default:
		if n.Suffix != "" {
			return fmt.Sprintf("%q", n.Raw+n.Suffix)
		}
		return n.Raw
	}
}

func (jc *JsIRContext) evalIdent(n *ir.Ident) string {
	if n.Member != "" {
		return fmt.Sprintf("%q", n.Member)
	}
	name := n.Name
	if name == "event" && jc.EventVar != "" {
		return jc.EventVar
	}
	_, kind := jc.Ctx.Resolve(name)
	switch kind {
	case codegen.NameLocal:
		return jc.Ctx.RenamedName(name)
	case codegen.NameComputed:
		return "$" + name + "()"
	case codegen.NameStateVar:
		return "state." + name
	case codegen.NameConst:
		return name
	default:
		if n.Type != nil && n.Type.Kind == ir.TypeEnum {
			return fmt.Sprintf("%q", name)
		}
		return name
	}
}

func (jc *JsIRContext) evalCall(n *ir.Call) string {
	if n.Receiver != nil {
		return jc.evalMethodCall(n)
	}
	if n.Func != nil {
		fname := n.Func.Name
		args := jc.evalCallArgs(n.Args)
		switch fname {
		case "string":
			if len(args) == 1 {
				if jc.Ctx.Helpers != nil {
					jc.Ctx.Helpers["String"] = true
				}
				return "String(" + args[0] + ")"
			}
		case "int":
			if len(args) == 1 {
				return "Math.trunc(" + args[0] + ")"
			}
		case "float":
			if len(args) == 1 {
				return "parseFloat(" + args[0] + ")"
			}
		}
		return fname + "(" + strings.Join(args, ", ") + ")"
	}
	args := jc.evalCallArgs(n.Args)
	return "(" + strings.Join(args, ", ") + ")"
}

func (jc *JsIRContext) evalMethodCall(n *ir.Call) string {
	receiver := jc.EvalExpr(n.Receiver)
	args := jc.evalCallArgs(n.Args)

	if n.Func != nil {
		fname := n.Func.Name
		receiverName := n.Func.Receiver

		qualName := receiverName + "." + fname
		allArgs := append([]string{receiver}, args...)
		if result := jsBuiltinMethodFromArgs(qualName, allArgs); result != "" {
			return result
		}
		wildArgs := append([]string{receiver}, args...)
		if result := jsBuiltinMethodFromArgs("*."+fname, wildArgs); result != "" {
			return result
		}

		// Mutation methods
		switch fname {
		case "push":
			if len(args) == 1 {
				return receiver + ".push(" + args[0] + ")"
			}
		case "remove":
			if len(args) == 1 {
				return receiver + ".splice(" + args[0] + ", 1)"
			}
		}

		return receiver + "." + fname + "(" + strings.Join(args, ", ") + ")"
	}
	return receiver + "(" + strings.Join(args, ", ") + ")"
}

func (jc *JsIRContext) evalConversion(n *ir.Conversion) string {
	operand := jc.EvalExpr(n.Operand)
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeInt:
			return "Math.trunc(" + operand + ")"
		case ir.TypeFloat:
			return "parseFloat(" + operand + ")"
		case ir.TypeString:
			return "String(" + operand + ")"
		}
	}
	return operand
}

func (jc *JsIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "() => null"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := jc.EvalExpr(ret.Value)
			if len(params) == 1 {
				return params[0] + " => " + body
			}
			return "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString("(" + strings.Join(params, ", ") + ") => {\n")
	for _, stmt := range n.Func.Block {
		for _, line := range jc.EvalStmt(stmt) {
			b.WriteString("  " + line + ";\n")
		}
	}
	b.WriteString("}")
	return b.String()
}

func (jc *JsIRContext) evalCallArgs(args []ir.CallArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = jc.EvalExpr(a.Value)
	}
	return out
}

func (jc *JsIRContext) evalMutTarget(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Ident:
		_, kind := jc.Ctx.Resolve(n.Name)
		if kind == codegen.NameStateVar {
			return "state." + n.Name
		}
		if kind == codegen.NameLocal {
			return jc.Ctx.RenamedName(n.Name)
		}
		return n.Name
	case *ir.Select:
		operand := jc.evalMutTarget(n.Operand)
		return operand + "." + n.Field
	case *ir.Index:
		operand := jc.evalMutTarget(n.Operand)
		idx := jc.EvalExpr(n.Idx)
		return operand + "[" + idx + "]"
	default:
		return jc.EvalExpr(e)
	}
}

// WithLocal returns a new context with an additional local variable.
func (jc *JsIRContext) WithLocal(name string) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.WithLocal(name),
		EventVar: jc.EventVar,
	}
}

// WithEvent returns a clone with EventVar set.
func (jc *JsIRContext) WithEvent(eventVar string) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.Clone(),
		EventVar: eventVar,
	}
}

// ForComponent returns a new context scoped to a component.
func (jc *JsIRContext) ForComponent(comp *ir.Component) *JsIRContext {
	return &JsIRContext{
		Ctx:      jc.Ctx.ForComponent(comp),
		EventVar: jc.EventVar,
	}
}

// --- JS builtin methods ---

func jsBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "undefined"
	}

	switch qualName {
	case "int.min", "*.min":
		return "Math.min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "Math.max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "Math.abs(" + a(0) + ")"
	case "string.length", "*.length":
		return a(0) + ".length"
	case "string.upper", "*.upper":
		return a(0) + ".toUpperCase()"
	case "string.lower", "*.lower":
		return a(0) + ".toLowerCase()"
	case "string.trim", "*.trim":
		return a(0) + ".trim()"
	case "string.replace", "*.replace":
		return a(0) + ".replaceAll(" + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "string.substring", "*.substring":
		return a(0) + ".substring(" + a(1) + ", " + a(2) + ")"
	case "list.length":
		return a(0) + ".length"
	case "list.join", "*.join":
		return a(0) + ".join(" + a(1) + ")"
	case "list.filter", "*.filter":
		return a(0) + ".filter(" + a(1) + ")"
	case "list.map", "*.map":
		return a(0) + ".map(" + a(1) + ")"
	case "list.indexOf":
		return a(0) + ".indexOf(" + a(1) + ")"
	case "list.reverse", "*.reverse":
		return "[..." + a(0) + "].reverse()"
	case "float.floor", "*.floor":
		return "Math.floor(" + a(0) + ")"
	case "float.ceil", "*.ceil":
		return "Math.ceil(" + a(0) + ")"
	case "float.round", "*.round":
		return "Math.round(" + a(0) + ")"
	case "float.sqrt", "*.sqrt":
		return "Math.sqrt(" + a(0) + ")"
	case "Alert.toast":
		return `(function(){var d=document.createElement("div");d.textContent=` + a(0) + `;d.style.cssText="position:fixed;bottom:16px;left:50%;transform:translateX(-50%);padding:12px 24px;border-radius:8px;color:#fff;z-index:9999;background:#333";document.body.appendChild(d);setTimeout(function(){d.remove()},3000)})()`
	case "Alert.info":
		return `alert(` + a(0) + `)`
	case "Alert.warn":
		return `alert("Warning: " + ` + a(0) + `)`
	case "Alert.error":
		return `alert("Error: " + ` + a(0) + `)`
	case "Alert.confirm":
		return `confirm(` + a(0) + `)`
	}
	return ""
}
