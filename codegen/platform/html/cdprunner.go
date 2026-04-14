//go:build !js

package html

import (
	"fmt"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CDPRunner executes SNGL test assertions against a live browser page.
// It walks the same test AST the interpreter walks, but translates each
// node to JavaScript and evaluates it via Chrome DevTools Protocol.
type CDPRunner struct {
	page  *rod.Page
	scope *codegen.ExprScope
	lang  codegen.LangTranslator

	// dataFields tracks which names are state data (not computed).
	dataFields map[string]bool
}

// NewCDPRunner creates a runner for the given page and promoted document.
func NewCDPRunner(page *rod.Page, doc *ast.Document, lang codegen.LangTranslator) *CDPRunner {
	modelFields := make(map[string]bool)
	computedFields := make(map[string]bool)
	dataFields := make(map[string]bool)

	for _, d := range docVars(doc) {
		modelFields[d.Name] = true
		dataFields[d.Name] = true
	}
	for _, fn := range docFuncs(doc) {
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			modelFields[fn.Name] = true
			computedFields[fn.Name] = true
		}
	}

	localVars := make(map[string]bool)
	for _, c := range docConsts(doc) {
		localVars[c.Name] = true
	}

	return &CDPRunner{
		page: page,
		scope: &codegen.ExprScope{
			ModelFields:    modelFields,
			ComputedFields: computedFields,
			LocalVars:      localVars,
		},
		lang:       lang,
		dataFields: dataFields,
	}
}

// ExecTest runs all statements in a test body, returning the first error.
func (r *CDPRunner) ExecTest(body []ast.Stmt) error {
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

func (r *CDPRunner) execStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return r.execAssign(s)
	case *ast.ToggleStmt:
		return r.execToggle(s)
	case *ast.CallStmt:
		name := callFuncName(s.Call)
		args := callArgs(s.Call)
		if name == "assert" && len(args) == 1 {
			return r.execAssert(args[0])
		}
		js := r.exprToJS(s.Call)
		return r.evalVoid(js + ";")
	case *ast.EmitStmt:
		return nil
	default:
		// Try as expression
		if expr, ok := stmt.(ast.Expr); ok {
			// Method call: SelectExpr-based CallExpr
			if call, ok := expr.(*ast.CallExpr); ok {
				name := callFuncName(call)
				args := callArgs(call)
				if name == "assert" && len(args) == 1 {
					return r.execAssert(args[0])
				}
				if sel, ok := call.Func.(*ast.SelectExpr); ok {
					if strings.HasPrefix(sel.Field, "@") {
						return r.triggerEvent(sel.Operand, sel.Field, args)
					}
					stmts := r.lang.TranslateMutation(stmt, r.scope)
					for _, js := range stmts {
						if err := r.evalVoid(js + ";"); err != nil {
							return fmt.Errorf("method: %w", err)
						}
					}
					return r.syncAll()
				}
			}
			js := r.exprToJS(expr)
			return r.evalVoid(js + ";")
		}
		return nil
	}
}

func (r *CDPRunner) execAssign(s *ast.AssignStmt) error {
	stmts := r.lang.TranslateMutation(s, r.scope)
	for _, js := range stmts {
		if err := r.evalVoid(js + ";"); err != nil {
			return fmt.Errorf("assign: %w", err)
		}
	}
	return r.syncAll()
}

func (r *CDPRunner) execToggle(s *ast.ToggleStmt) error {
	stmts := r.lang.TranslateMutation(s, r.scope)
	for _, js := range stmts {
		if err := r.evalVoid(js + ";"); err != nil {
			return fmt.Errorf("toggle: %w", err)
		}
	}
	return r.syncAll()
}

func (r *CDPRunner) execAssert(expr ast.Expr) error {
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

func (r *CDPRunner) triggerEvent(receiver ast.Expr, eventField string, args []ast.Expr) error {
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

func (r *CDPRunner) domSelector(n ast.Expr) string {
	switch e := n.(type) {
	case *ast.ElementRefExpr:
		return fmt.Sprintf("[data-sngl-id=%q]", e.Name)
	case *ast.IndexExpr:
		// #id[0] — element ref with index
		if ref, ok := e.Operand.(*ast.ElementRefExpr); ok {
			return fmt.Sprintf("[data-sngl-id=%q]", ref.Name)
		}
	}
	return ""
}

func (r *CDPRunner) exprToJS(n ast.Expr) string {
	if n == nil {
		return "null"
	}
	switch e := n.(type) {
	case *ast.ElementRefExpr:
		// Return null for elements hidden by if= (display:none)
		return fmt.Sprintf(`(function(){var el=document.querySelector('[data-sngl-id=%q]'); return el && el.style.display!=="none" ? el : null})()`, e.Name)

	case *ast.IdentExpr:
		return r.lang.TranslateExpr(e, r.scope)

	case *ast.CallExpr:
		// Method calls: SelectExpr-based
		if sel, ok := e.Func.(*ast.SelectExpr); ok {
			method := sel.Field
			args := callArgs(e)
			switch method {
			case "length":
				recv := r.exprToJS(sel.Operand)
				if isElementExpr(sel.Operand) {
					return fmt.Sprintf("Array.from(%s).length", recv)
				}
				return recv + ".length"
			case "contains":
				recv := r.exprToJS(sel.Operand)
				if len(args) == 1 {
					arg := r.exprToJS(args[0])
					return recv + ".includes(" + arg + ")"
				}
			}
			if strings.HasPrefix(method, "@") {
				return "null"
			}
			return r.lang.TranslateExpr(e, r.scope)
		}
		return r.callToJS(e)

	case *ast.SelectExpr:
		if isElementExpr(e.Operand) {
			recv := r.exprToJS(e.Operand)
			switch e.Field {
			case "value", "text":
				return recv + ".textContent"
			case "class":
				return recv + ".className"
			}
			return recv + "." + e.Field
		}
		return r.lang.TranslateExpr(e, r.scope)

	case *ast.IndexExpr:
		operand := r.exprToJS(e.Operand)
		index := r.exprToJS(e.Index)
		return operand + "[" + index + "]"

	case *ast.BinaryExpr:
		left := r.exprToJS(e.Left)
		right := r.exprToJS(e.Right)
		op := jsBinaryOp(e.Op)
		return "(" + left + " " + op + " " + right + ")"

	case *ast.UnaryExpr:
		operand := r.exprToJS(e.Operand)
		if e.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand

	case *ast.TernaryExpr:
		cond := r.exprToJS(e.Cond)
		then := r.exprToJS(e.Then)
		els := r.exprToJS(e.Else)
		return "(" + cond + " ? " + then + " : " + els + ")"

	case *ast.ParenExpr:
		return "(" + r.exprToJS(e.Inner) + ")"

	default:
		return r.lang.TranslateExpr(n, r.scope)
	}
}

func (r *CDPRunner) callToJS(e *ast.CallExpr) string {
	name := callFuncName(e)
	args := callArgs(e)
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
	// rod's Eval expects a function expression, not raw JS.
	wrapped := "() => { return " + js + "; }"
	result, err := r.page.Timeout(5 * time.Second).Eval(wrapped)
	if err != nil {
		return nil, err
	}
	return result.Value.Val(), nil
}

// evalVoid executes JS statements that don't return a value.
func (r *CDPRunner) evalVoid(js string) error {
	wrapped := "() => { " + js + " }"
	_, err := r.page.Timeout(5 * time.Second).Eval(wrapped)
	return err
}

func isElementExpr(n ast.Expr) bool {
	switch e := n.(type) {
	case *ast.ElementRefExpr:
		return true
	case *ast.SelectExpr:
		return isElementExpr(e.Operand)
	case *ast.IndexExpr:
		return isElementExpr(e.Operand)
	}
	return false
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
