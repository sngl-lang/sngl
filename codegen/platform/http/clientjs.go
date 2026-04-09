package http

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

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
	for _, d := range doc.Data {
		if !info.clientFields[d.Name] {
			continue
		}
		stateInit[d.Name] = jsLang.TranslateLiteral(d.Init)
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
	if expr.SNGL == nil {
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
func (cs *clientJSState) isClientMutation(expr ast.Expr) bool {
	if expr.SNGL == nil {
		return false
	}
	mutated := codegen.MutatedFields(expr.SNGL)
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
	jsExpr := cs.jsLang.TranslateExpr(expr.SNGL, cs.scope)
	cs.updates = append(cs.updates, clientUpdate{
		elemID: elemID,
		body:   fmt.Sprintf(`document.getElementById("%s").style.display = %s ? "" : "none";`, elemID, jsExpr),
		deps:   cs.exprDeps(expr),
	})
}

// addClickHandler registers a click handler for a client-state mutation.
func (cs *clientJSState) addClickHandler(elemID string, expr ast.Expr) {
	mutated := codegen.MutatedFields(expr.SNGL)
	stmts := cs.jsLang.TranslateMutation(expr.SNGL, cs.scope)
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
	if expr.SNGL == nil {
		return nil
	}
	return codegen.ExtractDeps(expr.SNGL, cs.info.clientFields)
}
