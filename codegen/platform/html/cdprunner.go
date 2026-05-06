//go:build !js

package html

import (
	"fmt"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CDPRunner executes SNGL test assertions against a live browser page.
// It walks the checked IR test body, translates each node to JavaScript,
// and evaluates it via Chrome DevTools Protocol.
type CDPRunner struct {
	page  *rod.Page
	scope *codegen.ExprScope
	lang  codegen.LangTranslator
}

// NewCDPRunner creates a runner for the given page and type-checked package.
func NewCDPRunner(page *rod.Page, pkg *ir.Package, lang codegen.LangTranslator) *CDPRunner {
	modelFields := make(map[string]bool)
	computedFields := make(map[string]bool)
	localVars := make(map[string]bool)

	if pkg != nil {
		for _, v := range pkg.Vars {
			if !v.IsConst {
				modelFields[v.Name] = true
			} else {
				localVars[v.Name] = true
			}
		}
		for _, v := range pkg.Consts {
			localVars[v.Name] = true
		}
		for _, fn := range pkg.Funcs {
			if len(fn.Params) == 0 {
				modelFields[fn.Name] = true
				computedFields[fn.Name] = true
			}
		}
	}

	return &CDPRunner{
		page: page,
		scope: &codegen.ExprScope{
			ModelFields:    modelFields,
			ComputedFields: computedFields,
			LocalVars:      localVars,
			Pkg:            pkg,
		},
		lang: lang,
	}
}

// ExecTest runs all statements in a test body, returning the first error.
func (r *CDPRunner) ExecTest(body []ir.Stmt) error {
	for _, stmt := range body {
		if err := r.execStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// InjectHelpers adds test helper functions to the page after load.
func (r *CDPRunner) InjectHelpers() error {
	return r.evalVoid(`
		window.__sngl_children = function(el) {
			var result = [];
			for (var i = 0; i < el.children.length; i++) {
				var ch = el.children[i];
				if (ch.id && ch.id.match(/^\$\d+$/) && !ch.hasAttribute('data-key')) {
					for (var j = 0; j < ch.children.length; j++) {
						result.push(ch.children[j]);
					}
				} else {
					result.push(ch);
				}
			}
			return result;
		};
		window.__sngl_updaters = [];
		Object.getOwnPropertyNames(window).forEach(function(name) {
			if (name.startsWith("$u_") && typeof window[name] === "function") {
				window.__sngl_updaters.push(window[name]);
			}
		});
	`)
}

// syncAll calls all DOM updaters after a state mutation.
func (r *CDPRunner) syncAll() error {
	return r.evalVoid(`window.__sngl_updaters.forEach(function(f) { f(); })`)
}

func (r *CDPRunner) execStmt(stmt ir.Stmt) error {
	switch s := stmt.(type) {
	case *ir.Assign:
		return r.execAssign(s)
	case *ir.Toggle:
		return r.execToggle(s)
	case *ir.CallStmt:
		return r.execCallStmt(s)
	case *ir.Emit:
		return nil
	}
	return nil
}

func (r *CDPRunner) execCallStmt(s *ir.CallStmt) error {
	if s.Call == nil {
		return nil
	}
	// assert(x): evaluate x and check truthiness.
	if s.Call.Func != nil && s.Call.Func.Name == "assert" && len(s.Call.Args) == 1 {
		return r.execAssert(s.Call.Args[0].Value)
	}
	// Event-trigger method calls: recv.@click(args).
	if s.Call.Receiver != nil {
		if field, ok := irCallSelectField(s.Call); ok && strings.HasPrefix(field, "@") {
			args := make([]ir.Expr, len(s.Call.Args))
			for i, a := range s.Call.Args {
				args[i] = a.Value
			}
			return r.triggerEvent(s.Call.Receiver, field, args)
		}
		// Non-event method — emit as a mutation via the IR translator.
		stmts := r.lang.TranslateIRMutation(s, r.scope)
		for _, js := range stmts {
			if err := r.evalVoid(js + ";"); err != nil {
				return fmt.Errorf("method: %w", err)
			}
		}
		return r.syncAll()
	}
	// Plain function call.
	js := r.exprToJS(s.Call)
	return r.evalVoid(js + ";")
}

func (r *CDPRunner) execAssign(s *ir.Assign) error {
	stmts := r.lang.TranslateIRMutation(s, r.scope)
	for _, js := range stmts {
		if err := r.evalVoid(js + ";"); err != nil {
			return fmt.Errorf("assign: %w", err)
		}
	}
	return r.syncAll()
}

func (r *CDPRunner) execToggle(s *ir.Toggle) error {
	stmts := r.lang.TranslateIRMutation(s, r.scope)
	for _, js := range stmts {
		if err := r.evalVoid(js + ";"); err != nil {
			return fmt.Errorf("toggle: %w", err)
		}
	}
	return r.syncAll()
}

func (r *CDPRunner) execAssert(expr ir.Expr) error {
	js := r.exprToJS(expr)
	result, err := r.eval(js)
	if err != nil {
		return fmt.Errorf("assert eval error: %w", err)
	}
	if result != true {
		return fmt.Errorf("assert(%s) failed — got %v", js, result)
	}
	return nil
}

func (r *CDPRunner) triggerEvent(receiver ir.Expr, eventField string, args []ir.Expr) error {
	eventName := strings.TrimPrefix(eventField, "@")
	sel := r.domSelector(receiver)
	if sel == "" {
		return fmt.Errorf("cannot determine element selector for %s trigger", eventField)
	}
	switch eventName {
	case "click":
		el, err := r.page.Timeout(5 * time.Second).Element(sel)
		if err != nil {
			return fmt.Errorf("click: element %s: %w", sel, err)
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("click %s: %w", sel, err)
		}
		_ = r.page.WaitStable(200 * time.Millisecond)
		return nil
	case "input":
		if len(args) == 1 {
			val := r.exprToJS(args[0])
			js := fmt.Sprintf(`document.querySelector(%q).value = %s; document.querySelector(%q).dispatchEvent(new Event("input"))`, sel, val, sel)
			_, err := r.eval(js)
			return err
		}
		return fmt.Errorf("@input requires 1 argument")
	default:
		return fmt.Errorf("unsupported event trigger: @%s", eventName)
	}
}

func (r *CDPRunner) domSelector(n ir.Expr) string {
	switch e := n.(type) {
	case *ir.Ident:
		if e.IsElementRef {
			return fmt.Sprintf("[data-sngl-id=%q]", e.Name)
		}
	case *ir.Index:
		if id, ok := e.Operand.(*ir.Ident); ok && id.IsElementRef {
			return fmt.Sprintf("[data-sngl-id=%q]", id.Name)
		}
	}
	return ""
}

// exprToJS translates an IR expression into JS for eval in the browser,
// adding DOM-specific sugar (element refs resolve to DOM nodes, .value/.text
// map to textContent, etc.).
func (r *CDPRunner) exprToJS(n ir.Expr) string {
	if n == nil {
		return "null"
	}
	switch e := n.(type) {
	case *ir.Ident:
		if e.IsElementRef {
			return fmt.Sprintf(`(function(){var el=document.querySelector('[data-sngl-id=%q]'); return el && el.style.display!=="none" ? el : null})()`, e.Name)
		}
		return r.lang.TranslateIRExpr(e, r.scope)

	case *ir.Call:
		// Method calls: Receiver-bearing with a resolved Func.
		if e.Receiver != nil && e.Func != nil {
			method := e.Func.Name
			switch method {
			case "length":
				recv := r.exprToJS(e.Receiver)
				if isIRElementExpr(e.Receiver) {
					return fmt.Sprintf("Array.from(%s).length", recv)
				}
				return recv + ".length"
			case "contains":
				if len(e.Args) == 1 {
					recv := r.exprToJS(e.Receiver)
					arg := r.exprToJS(e.Args[0].Value)
					return recv + ".includes(" + arg + ")"
				}
			}
			if strings.HasPrefix(method, "@") {
				return "null"
			}
			return r.lang.TranslateIRExpr(e, r.scope)
		}
		return r.callToJS(e)

	case *ir.Select:
		if isIRElementExpr(e.Operand) {
			recv := r.exprToJS(e.Operand)
			switch e.Field {
			case "value", "text":
				return recv + ".textContent"
			case "class":
				return recv + ".className"
			}
			return recv + "." + e.Field
		}
		return r.lang.TranslateIRExpr(e, r.scope)

	case *ir.Index:
		return r.exprToJS(e.Operand) + "[" + r.exprToJS(e.Idx) + "]"

	case *ir.Binary:
		return "(" + r.exprToJS(e.Left) + " " + jsBinaryOp(e.Op) + " " + r.exprToJS(e.Right) + ")"

	case *ir.Unary:
		operand := r.exprToJS(e.Operand)
		if e.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand

	case *ir.Ternary:
		return "(" + r.exprToJS(e.Cond) + " ? " + r.exprToJS(e.Then) + " : " + r.exprToJS(e.Else) + ")"

	default:
		return r.lang.TranslateIRExpr(n, r.scope)
	}
}

func (r *CDPRunner) callToJS(e *ir.Call) string {
	name := ""
	if e.Func != nil {
		name = e.Func.Name
	}
	args := make([]ir.Expr, len(e.Args))
	for i, a := range e.Args {
		args[i] = a.Value
	}
	switch name {
	case "string":
		if len(args) == 1 {
			return "String(" + r.exprToJS(args[0]) + ")"
		}
	case "int":
		if len(args) == 1 {
			return "Math.trunc(" + r.exprToJS(args[0]) + ")"
		}
	case "float":
		if len(args) == 1 {
			return "Number(" + r.exprToJS(args[0]) + ")"
		}
	case "assert":
		if len(args) == 1 {
			return r.exprToJS(args[0])
		}
	}
	jsArgs := make([]string, len(args))
	for i, a := range args {
		jsArgs[i] = r.exprToJS(a)
	}
	return name + "(" + strings.Join(jsArgs, ", ") + ")"
}

func (r *CDPRunner) eval(js string) (any, error) {
	wrapped := "() => { return " + js + "; }"
	result, err := r.page.Timeout(5 * time.Second).Eval(wrapped)
	if err != nil {
		return nil, err
	}
	return result.Value.Val(), nil
}

func (r *CDPRunner) evalVoid(js string) error {
	wrapped := "() => { " + js + " }"
	_, err := r.page.Timeout(5 * time.Second).Eval(wrapped)
	return err
}

func isIRElementExpr(n ir.Expr) bool {
	switch e := n.(type) {
	case *ir.Ident:
		return e.IsElementRef
	case *ir.Select:
		return isIRElementExpr(e.Operand)
	case *ir.Index:
		return isIRElementExpr(e.Operand)
	}
	return false
}

// irCallSelectField returns the field name of a SelectExpr-backed call,
// used to recognize `receiver.@event(...)` triggers whose IR form has Func
// nil but an AST SelectExpr carrying the "@eventName".
func irCallSelectField(c *ir.Call) (string, bool) {
	if c == nil || c.AST == nil {
		return "", false
	}
	sel, ok := c.AST.Func.(*ast.SelectExpr)
	if !ok {
		return "", false
	}
	return sel.Field, true
}

func jsBinaryOp(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
	case ast.BinEq:
		return "==="
	case ast.BinNeq:
		return "!=="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	}
	return "?"
}
