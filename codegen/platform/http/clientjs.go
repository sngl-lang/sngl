package http

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// --- AST-based dependency helpers ---
// These mirror the IR-based functions in codegen/deps.go but operate on
// AST types. The HTTP platform still uses AST event handlers and expressions
// internally; these bridge until the platform is fully IR-native.

// findRootIdentAST extracts the root identifier name from an AST expression.
func findRootIdentAST(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findRootIdentAST(n.Operand)
	case *ast.IndexExpr:
		return findRootIdentAST(n.Operand)
	}
	return ""
}

// mutatedFieldsAST returns the set of field names mutated by an AST statement.
func mutatedFieldsAST(s ast.Stmt) map[string]bool {
	fields := make(map[string]bool)
	if s == nil {
		return fields
	}
	switch n := s.(type) {
	case *ast.AssignStmt:
		if root := findRootIdentAST(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		if root := findRootIdentAST(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.CallStmt:
		if n.Call != nil {
			if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
				if root := findRootIdentAST(sel.Operand); root != "" {
					fields[root] = true
				}
			}
		}
	}
	return fields
}

// walkExprDepsAST recursively finds model field references in an AST expression.
func walkExprDepsAST(e ast.Expr, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ast.SelectExpr:
		if root := findRootIdentAST(n.Operand); root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ast.BinaryExpr:
		walkExprDepsAST(n.Left, modelFields, deps)
		walkExprDepsAST(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkExprDepsAST(n.Cond, modelFields, deps)
		walkExprDepsAST(n.Then, modelFields, deps)
		walkExprDepsAST(n.Else, modelFields, deps)
	case *ast.CallExpr:
		walkExprDepsAST(n.Func, modelFields, deps)
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				walkExprDepsAST(arg.Value, modelFields, deps)
			}
		}
	case *ast.IndexExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
		walkExprDepsAST(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkExprDepsAST(el, modelFields, deps)
		}
	case *ast.SpreadExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
	}
}

// extractDepsAST returns all model field references from an AST expression.
func extractDepsAST(e ast.Expr, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkExprDepsAST(e, modelFields, deps)
	return deps
}

// clientUpdate tracks a DOM update needed when client state changes.
type clientUpdate struct {
	elemID string
	body   string          // JS statement
	deps   map[string]bool // client state fields this depends on
}

// clientHandler tracks an event handler for client state mutations.
type clientHandler struct {
	elemID  string
	event   string
	body    string          // JS statements
	mutated map[string]bool // client state fields this mutates
}

// clientJSState collects client-side JS elements during rendering.
type clientJSState struct {
	info      *analysisResult
	jsLang    codegen.LangTranslator
	scope     *codegen.ExprScope
	updates   []clientUpdate
	handlers  []clientHandler
	stateInit map[string]string // field name → JS init value
}

func newClientJSState(info *analysisResult, doc *ast.Document) *clientJSState {
	jsLang := codegen.LookupLang("js")
	if jsLang == nil {
		return nil
	}

	// Build a JS-oriented scope: client state fields are model fields (state.X).
	scope := &codegen.ExprScope{
		ModelFields:    make(map[string]bool),
		ComputedFields: make(map[string]bool),
		FuncNames:      make(map[string]bool),
		ExternFuncs:    make(map[string]bool),
		ExternVars:     make(map[string]bool),
		LocalVars:      make(map[string]bool),
		NeededHelpers:  make(map[string]bool),
	}
	for name := range info.clientFields {
		scope.ModelFields[name] = true
	}

	// Collect initial values for client state.
	stateInit := make(map[string]string)
	for _, vd := range codegen.DocVarDecls(doc) {
		for _, spec := range vd.Specs {
			for _, name := range spec.Names {
				if !info.clientFields[name] {
					continue
				}
				stateInit[name] = jsLang.TranslateLiteral(spec.Default)
			}
		}
	}

	return &clientJSState{
		info:      info,
		jsLang:    jsLang,
		scope:     scope,
		stateInit: stateInit,
	}
}

// isClientOnly reports whether an expression only references client state fields.
func (cs *clientJSState) isClientOnly(expr ast.Expr) bool {
	if expr == nil || codegen.ExprIsLiteral(expr) {
		return false
	}
	deps := cs.exprDeps(expr)
	if len(deps) == 0 {
		return false
	}
	for dep := range deps {
		if !cs.info.clientFields[dep] {
			return false
		}
	}
	return true
}

// isClientMutation reports whether a mutation only touches client state.
func (cs *clientJSState) isClientMutation(stmt ast.Stmt) bool {
	if stmt == nil {
		return false
	}
	mutated := mutatedFieldsAST(stmt)
	if len(mutated) == 0 {
		return false
	}
	for field := range mutated {
		if !cs.info.clientFields[field] {
			return false
		}
	}
	return true
}

// addIfUpdater registers a visibility updater for a client-state conditional.
func (cs *clientJSState) addIfUpdater(elemID string, expr ast.Expr) {
	jsExpr := cs.jsLang.TranslateExpr(expr, cs.scope)
	cs.updates = append(cs.updates, clientUpdate{
		elemID: elemID,
		body:   fmt.Sprintf(`document.getElementById("%s").style.display = %s ? "" : "none";`, elemID, jsExpr),
		deps:   cs.exprDeps(expr),
	})
}

// addClickHandler registers a click handler for a client-state mutation.
func (cs *clientJSState) addClickHandler(elemID string, handler ast.Stmt) {
	mutated := mutatedFieldsAST(handler)
	stmts := cs.jsLang.TranslateMutation(handler, cs.scope)
	var body strings.Builder
	for _, s := range stmts {
		body.WriteString("    " + s + ";\n")
	}

	cs.handlers = append(cs.handlers, clientHandler{
		elemID:  elemID,
		event:   "click",
		body:    body.String(),
		mutated: mutated,
	})
}

// emitScript generates the inline <script> block for client-side state.
// Returns empty string if no client-side interactivity is needed.
func (cs *clientJSState) emitScript() string {
	if len(cs.updates) == 0 && len(cs.handlers) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<script>\n")

	// State initialization.
	b.WriteString("let state = {")
	var fields []string
	for name, init := range cs.stateInit {
		fields = append(fields, name+": "+init)
	}
	b.WriteString(strings.Join(fields, ", "))
	b.WriteString("};\n")

	// Update functions.
	for i, u := range cs.updates {
		fmt.Fprintf(&b, "function $u%d() { %s }\n", i, u.body)
	}

	// Event handlers.
	for _, h := range cs.handlers {
		fmt.Fprintf(&b, "document.getElementById(\"%s\").addEventListener(\"%s\", function() {\n%s", h.elemID, h.event, h.body)
		// Call affected updaters.
		for i, u := range cs.updates {
			for dep := range u.deps {
				if h.mutated[dep] {
					fmt.Fprintf(&b, "    $u%d();\n", i)
					break
				}
			}
		}
		b.WriteString("});\n")
	}

	// Initial sync: call all updaters once.
	for i := range cs.updates {
		fmt.Fprintf(&b, "$u%d();\n", i)
	}

	b.WriteString("</script>")
	return b.String()
}

func (cs *clientJSState) exprDeps(expr ast.Expr) map[string]bool {
	if expr == nil || codegen.ExprIsLiteral(expr) {
		return nil
	}
	return extractDepsAST(expr, cs.info.clientFields)
}
