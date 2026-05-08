package gtk4

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// widgetField is a persistent widget reference stored on the Model struct.
type widgetField struct {
	name   string // e.g. "btn0"
	goType string // e.g. "*C.GtkButton"
}

// widgetUpdater is a reactive method that patches one widget property.
type widgetUpdater struct {
	name string // method name, e.g. "updateBtn0Label"
	body string // method body line(s)
	deps map[string]bool
}

func (u widgetUpdater) DepFields() map[string]bool { return u.deps }

// viewContext tracks state while generating BuildUI code strings.
type viewContext struct {
	gc          *golang.GoIRContext
	registry    *gir.TypeRegistry // may be nil when GIR unavailable
	ctx         *codegen.CodegenCtx
	buf         *strings.Builder
	indent      int
	widgetCount int
	fields      []widgetField
	updaters    []widgetUpdater
	propScope   map[string]ir.Expr // for inline stdlib expansion
	depTracker  *codegen.DepTracker
}

// isUserComponent reports whether comp is defined in the user's source package
// (as opposed to stdlib or platform packages).
func (vc *viewContext) isUserComponent(comp *ir.Component) bool {
	if vc.ctx == nil {
		return false
	}
	for _, c := range vc.ctx.Pkg.Components {
		if c == comp {
			return true
		}
	}
	return false
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// --- Statement rendering ---

func (vc *viewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		vc.renderNode(s, resultVar)
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			vc.renderStmt(bs, resultVar)
		}
	case *ir.SlotInst:
		// slot: nothing to render in BuildUI; caller handles children
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmt(child, resultVar)
		}
	}
}

// --- Node dispatch ---

func (vc *viewContext) renderNode(n *ir.NodeInst, resultVar string) {
	// GIR widgets: name starts with "Gtk"
	if after, ok := strings.CutPrefix(n.Name, "Gtk"); ok {
		if vc.registry != nil {
			name := after
			if info, ok := vc.registry.Classes[name]; ok {
				vc.renderGtkWidget(n, info, resultVar)
				return
			}
		}
		// Unknown GIR widget — emit fallback comment
		vc.line("// TODO: unknown GIR widget %s", n.Name)
		return
	}

	// User-defined component: call generated render method
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}

	// Platform-overridden stdlib component (resolved inside a platform block)
	// has a non-empty body from the platform's .sngl package.
	if n.Component != nil && len(n.Component.Body) > 0 {
		vc.renderStdlibInline(n, resultVar)
		return
	}

	// Abstract stdlib component: use built-in GTK4 mapping.
	if n.Component != nil {
		vc.renderStdlibComponent(n, resultVar)
		return
	}

	vc.line("// TODO: unresolved node %s", n.Name)
}

// --- GIR widget constructor ---

func (vc *viewContext) renderGtkWidget(n *ir.NodeInst, info *gir.ClassInfo, resultVar string) {
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++

	goType := fmt.Sprintf("*C.%s", info.CType)
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: goType})

	// Build constructor call.
	// Use the first constructor from GIR if available; otherwise fall back to
	// gtk_<type>_new.
	if info.Constructor.Name != "" {
		args := vc.buildCtorArgs(n, info)
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))",
			fieldName, info.CType, info.Constructor.Name, strings.Join(args, ", "))
	} else {
		cFn := "gtk_" + strings.ToLower(strings.TrimPrefix(info.CType, "Gtk")) + "_new"
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s()))",
			fieldName, info.CType, cFn)
	}

	// Assign resultVar as GtkWidget* for parent to add as child.
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)

	// Connect signal handlers.
	for _, h := range n.Handlers {
		signal := h.Name
		vc.connectSignal(fieldName, signal, &h, n)
	}

	// Register reactive updaters for props that reference model state.
	for _, arg := range n.Props {
		setter := gtkSetter(info.CType, arg.Name)
		if setter == "" {
			continue
		}
		deps := vc.exprDeps(arg.Value)
		if len(deps) == 0 {
			continue
		}
		updName := fmt.Sprintf("update%s%s", golang.ExportName(fieldName), golang.ExportName(arg.Name))
		rhs := vc.irExprToC(arg.Value, nil)
		body := fmt.Sprintf("C.%s((*C.GtkWidget)(unsafe.Pointer(m.%s)), %s)", setter, fieldName, rhs)
		vc.updaters = append(vc.updaters, widgetUpdater{name: updName, body: body, deps: deps})
	}

	// Add children.
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sChild%d", resultVar, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		vc.addChildToWidget(fieldName, info.CType, childVar)
	}
}

// buildCtorArgs produces the C argument list for the GIR constructor.
func (vc *viewContext) buildCtorArgs(n *ir.NodeInst, info *gir.ClassInfo) []string {
	args := make([]string, len(info.Constructor.Params))
	for i, p := range info.Constructor.Params {
		expr := vc.lookupPropExpr(n, p.Name)
		if expr == nil {
			// Use zero value appropriate for the type.
			args[i] = cZeroForIRType(p.IRType)
		} else {
			args[i] = vc.irExprToC(expr, p.IRType)
		}
	}
	return args
}

// connectSignal emits a sngl_connect call for one signal.
// synthLines are emitted inside the callback closure before the handler body;
// use them to synthesize event variables (e.g. the input text from a GtkEntry).
func (vc *viewContext) connectSignal(fieldName, signal string, h *ir.EventHandler, n *ir.NodeInst, synthLines ...string) {
	vc.line("{")
	vc.indent++
	vc.line("_idx := len(snglCallbacks)")
	vc.line("snglCallbacks = append(snglCallbacks, func() {")
	vc.indent++
	for _, sl := range synthLines {
		vc.line("%s", sl)
	}
	if h.Func != nil {
		for _, stmt := range h.Func.Block {
			for _, line := range vc.gc.EvalStmt(stmt) {
				vc.line("%s", line)
			}
		}
	}
	vc.indent--
	vc.line("})")
	vc.line("C.sngl_connect(unsafe.Pointer(m.%s), C.CString(%q), C.int(_idx))", fieldName, signal)
	vc.indent--
	vc.line("}")
}

// addChildToWidget emits the appropriate gtk_xxx_append or set_child call.
func (vc *viewContext) addChildToWidget(parentField, cType, childVar string) {
	vc.line("if %s != nil {", childVar)
	vc.indent++
	if cType == "GtkApplicationWindow" || cType == "GtkWindow" {
		vc.line("C.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(m.%s)), %s)", parentField, childVar)
	} else if fn := gtkChildAdd(cType); fn != "" {
		vc.line("C.%s((*C.GtkWidget)(unsafe.Pointer(m.%s)), %s)", fn, parentField, childVar)
	} else {
		// Fallback: try gtk_widget_set_child (may not compile — better than silent drop)
		vc.line("_ = m.%s // TODO: no child-add for %s", parentField, cType)
		vc.line("_ = %s", childVar)
	}
	vc.indent--
	vc.line("}")
}

// --- Abstract stdlib component fallback ---

// renderStdlibComponent handles abstract stdlib components (vbox, hbox, text,
// label, input, etc.) that are used outside a platform block. It maps them to
// their GTK4 equivalents directly without relying on the platform IR body.
func (vc *viewContext) renderStdlibComponent(n *ir.NodeInst, resultVar string) {
	if vc.registry == nil {
		vc.line("// TODO: GIR unavailable — cannot render stdlib component %s", n.Name)
		return
	}
	switch n.Name {
	case "vbox":
		vc.renderStdlibBox(n, resultVar, "vertical")
	case "hbox":
		vc.renderStdlibBox(n, resultVar, "horizontal")
	case "scroll":
		vc.renderStdlibScroll(n, resultVar)
	case "text", "label":
		vc.renderStdlibLabel(n, resultVar)
	case "input", "entry":
		vc.renderStdlibEntry(n, resultVar)
	case "button":
		vc.renderStdlibButton(n, resultVar)
	case "window":
		vc.renderStdlibWindow(n, resultVar)
	default:
		vc.line("// TODO: unhandled stdlib component %s", n.Name)
	}
}

func (vc *viewContext) renderStdlibBox(n *ir.NodeInst, resultVar, orientation string) {
	info, ok := vc.registry.Classes["Box"]
	if !ok {
		vc.line("// TODO: GtkBox not in GIR")
		return
	}
	spacing := 6
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: "*C.GtkBox"})
	vc.line("m.%s = (*C.GtkBox)(unsafe.Pointer(C.gtk_box_new(C.GTK_ORIENTATION_%s, %d)))", fieldName, strings.ToUpper(orientation), spacing)
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sChild%d", resultVar, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { C.gtk_box_append(m.%s, %s) }", childVar, fieldName, childVar)
	}
}

func (vc *viewContext) renderStdlibScroll(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["ScrolledWindow"]
	if !ok {
		vc.line("// TODO: GtkScrolledWindow not in GIR")
		return
	}
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: "*C.GtkScrolledWindow"})
	vc.line("m.%s = C.gtk_scrolled_window_new()", fieldName)
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sChild%d", resultVar, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { C.gtk_scrolled_window_set_child(m.%s, %s) }", childVar, fieldName, childVar)
	}
}

func (vc *viewContext) renderStdlibLabel(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["Label"]
	if !ok {
		vc.line("// TODO: GtkLabel not in GIR")
		return
	}
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: fmt.Sprintf("*C.%s", info.CType)})

	// Find label/value prop.
	valExpr := codegen.NodeProp(n, "value")
	if valExpr == nil {
		valExpr = codegen.NodeProp(n, "label")
	}
	var initVal string
	if valExpr != nil {
		initVal = vc.irExprToC(valExpr, &ir.Type{Kind: ir.TypeString})
	} else {
		initVal = `C.CString("")`
	}
	if info.Constructor.Name != "" {
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))", fieldName, info.CType, info.Constructor.Name, initVal)
	} else {
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.gtk_label_new(%s)))", fieldName, info.CType, initVal)
	}
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)

	// Reactive updater if value has deps.
	if valExpr != nil {
		deps := vc.exprDeps(valExpr)
		if len(deps) > 0 {
			updName := fmt.Sprintf("update%sValue", golang.ExportName(fieldName))
			rhs := vc.irExprToC(valExpr, &ir.Type{Kind: ir.TypeString})
			body := fmt.Sprintf("C.gtk_label_set_text((*C.GtkLabel)(unsafe.Pointer(m.%s)), %s)", fieldName, rhs)
			vc.updaters = append(vc.updaters, widgetUpdater{name: updName, body: body, deps: deps})
		}
	}
}

func (vc *viewContext) renderStdlibEntry(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["Entry"]
	if !ok {
		vc.line("// TODO: GtkEntry not in GIR")
		return
	}
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: fmt.Sprintf("*C.%s", info.CType)})
	vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.gtk_entry_new()))", fieldName, info.CType)
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)

	// Placeholder prop.
	if ph := codegen.NodeProp(n, "placeholder"); ph != nil {
		phStr := vc.irExprToC(ph, &ir.Type{Kind: ir.TypeString})
		vc.line("C.gtk_entry_set_placeholder_text(m.%s, %s)", fieldName, phStr)
	}

	// @input event handler: synthesize event variable from entry text.
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Name == "input" || h.Name == "change" || h.Name == "changed" {
			var synth []string
			if h.Func != nil && len(h.Func.Params) > 0 {
				param := h.Func.Params[0]
				synth = append(synth,
					fmt.Sprintf("%s := struct{ Value string }{Value: C.GoString(C.gtk_editable_get_text((*C.GtkEditable)(unsafe.Pointer(m.%s))))}", param.Name, fieldName),
				)
			}
			vc.connectSignal(fieldName, "changed", h, n, synth...)
			break
		}
	}
}

func (vc *viewContext) renderStdlibButton(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["Button"]
	if !ok {
		vc.line("// TODO: GtkButton not in GIR")
		return
	}
	prefix := girFieldPrefix(info.CType)
	fieldName := fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: fieldName, goType: fmt.Sprintf("*C.%s", info.CType)})

	textExpr := codegen.NodeProp(n, "text")
	if textExpr == nil {
		textExpr = codegen.NodeProp(n, "label")
	}
	var labelArg string
	if textExpr != nil {
		labelArg = vc.irExprToC(textExpr, &ir.Type{Kind: ir.TypeString})
	} else {
		labelArg = `C.CString("")`
	}
	if info.Constructor.Name != "" {
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))", fieldName, info.CType, info.Constructor.Name, labelArg)
	} else {
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.gtk_button_new_with_label(%s)))", fieldName, info.CType, labelArg)
	}
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(m.%s))", resultVar, fieldName)

	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Name == "click" || h.Name == "clicked" {
			vc.connectSignal(fieldName, "clicked", h, n)
			break
		}
	}
}

func (vc *viewContext) renderStdlibWindow(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["ApplicationWindow"]
	if !ok {
		vc.line("// TODO: GtkApplicationWindow not in GIR")
		return
	}
	vc.renderGtkWidget(n, info, resultVar)
}

// --- Stdlib inline expansion ---

// renderStdlibInline inline-expands a gtk4.sngl stdlib component (e.g. button)
// by re-entering renderNode with prop substitution via propScope.
func (vc *viewContext) renderStdlibInline(n *ir.NodeInst, resultVar string) {
	// Save and merge propScope.
	savedScope := vc.propScope
	newScope := make(map[string]ir.Expr)
	maps.Copy(newScope, savedScope)
	for _, arg := range n.Props {
		newScope[arg.Name] = arg.Value
	}
	// Pass children through for slot expansion.
	savedChildren := n.Component.Body
	vc.propScope = newScope

	// Walk the component body. We need a fake NodeInst for each body stmt
	// to thread the slot children. Simpler: just render each body stmt
	// with the slot children attached via a temporary mechanism.
	// For now: walk body, and when we hit a SlotInst, render n.Children.
	for _, stmt := range savedChildren {
		vc.renderStmtWithSlot(stmt, resultVar, n.Children)
	}

	vc.propScope = savedScope
}

// renderStmtWithSlot is like renderStmt but also handles SlotInst by
// substituting slotChildren.
func (vc *viewContext) renderStmtWithSlot(stmt ir.Stmt, resultVar string, slotChildren []ir.Stmt) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		// Clone the NodeInst with slot children substituted.
		clone := *s
		// Replace any SlotInst children with the actual slot children.
		clone.Children = substituteSlot(s.Children, slotChildren)
		vc.renderNode(&clone, resultVar)
	case *ir.SlotInst:
		for _, child := range slotChildren {
			vc.renderStmt(child, resultVar)
		}
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			vc.renderStmtWithSlot(bs, resultVar, slotChildren)
		}
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmtWithSlot(child, resultVar, slotChildren)
		}
	default:
		vc.renderStmt(stmt, resultVar)
	}
}

// substituteSlot replaces SlotInst entries in children with slotChildren.
func substituteSlot(children []ir.Stmt, slotChildren []ir.Stmt) []ir.Stmt {
	result := make([]ir.Stmt, 0, len(children))
	for _, c := range children {
		if _, ok := c.(*ir.SlotInst); ok {
			result = append(result, slotChildren...)
		} else {
			result = append(result, c)
		}
	}
	return result
}

// --- User component ---

func (vc *viewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			expr := codegen.NodeProp(n, p.Name)
			if expr != nil {
				args = append(args, vc.gc.EvalExpr(expr))
			} else if p.Default != nil {
				args = append(args, golang.IRLiteralToGo(p.Default))
			} else {
				args = append(args, "nil")
			}
		}
	}
	vc.line("%s = m.render%s(%s)", resultVar, golang.ExportName(n.Name), strings.Join(args, ", "))
}

// --- Prop lookup (respects propScope for inline expansion) ---

// lookupPropExpr finds the expression for a named prop on a NodeInst.
// It checks the node's own Props first, then falls back to propScope
// (used during inline stdlib expansion).
func (vc *viewContext) lookupPropExpr(n *ir.NodeInst, name string) ir.Expr {
	for _, arg := range n.Props {
		if arg.Name == name {
			// If this is a bare ident that names a propScope key, resolve it.
			if id, ok := arg.Value.(*ir.Ident); ok {
				if expr, found := vc.propScope[id.Name]; found {
					return expr
				}
			}
			return arg.Value
		}
	}
	if vc.propScope != nil {
		if expr, ok := vc.propScope[name]; ok {
			return expr
		}
	}
	return nil
}

// --- C expression conversion ---

// irExprToC converts an IR expression to a C argument string, inserting
// casts or C.CString() wrapping as needed based on targetType.
func (vc *viewContext) irExprToC(expr ir.Expr, targetType *ir.Type) string {
	if expr == nil {
		return cZeroForIRType(targetType)
	}
	// String literals / string-typed expressions need C.CString.
	if isStringType(targetType) || isStringExpr(expr) {
		goStr := vc.gc.EvalExpr(expr)
		return "C.CString(" + goStr + ")"
	}
	// Bool → C.int (gtk uses gboolean which is int).
	if isBoolType(targetType) {
		goExpr := vc.gc.EvalExpr(expr)
		return "C.int(ternary(" + goExpr + ", 1, 0))"
	}
	return vc.gc.EvalExpr(expr)
}

// exprDeps returns the reactive deps of an expression, or nil.
func (vc *viewContext) exprDeps(expr ir.Expr) map[string]bool {
	if vc.depTracker == nil {
		return nil
	}
	return vc.depTracker.ExprDeps(expr)
}

// --- Lookup tables ---

var gtkSetterTable = map[string]map[string]string{
	"GtkButton": {
		"label": "gtk_button_set_label",
	},
	"GtkLabel": {
		"label": "gtk_label_set_text",
	},
	"GtkEntry": {
		"text": "gtk_editable_set_text",
	},
	"GtkCheckButton": {
		"active": "gtk_check_button_set_active",
		"label":  "gtk_check_button_set_label",
	},
	"GtkApplicationWindow": {
		"title": "gtk_window_set_title",
	},
	"GtkImage": {
		"file": "gtk_image_set_from_file",
	},
}

func gtkSetter(cType, prop string) string {
	if m, ok := gtkSetterTable[cType]; ok {
		return m[prop]
	}
	return ""
}

var gtkChildAddTable = map[string]string{
	"GtkBox":            "gtk_box_append",
	"GtkScrolledWindow": "gtk_scrolled_window_set_child",
}

func gtkChildAdd(cType string) string {
	return gtkChildAddTable[cType]
}

func girFieldPrefix(cType string) string {
	switch strings.TrimPrefix(cType, "Gtk") {
	case "Button":
		return "btn"
	case "Label":
		return "lbl"
	case "Entry":
		return "entry"
	case "CheckButton":
		return "chk"
	case "Box":
		return "box"
	case "ScrolledWindow":
		return "scroll"
	case "ApplicationWindow", "Window":
		return "win"
	case "Image":
		return "img"
	default:
		name := strings.ToLower(strings.TrimPrefix(cType, "Gtk"))
		if len(name) > 3 {
			name = name[:3]
		}
		return name
	}
}

// --- Type helpers ---

func isStringType(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeString
}

func isBoolType(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeBool
}

func isStringExpr(expr ir.Expr) bool {
	t := expr.ExprType()
	if t == nil {
		return false
	}
	return t.Kind == ir.TypeString
}

func cZeroForIRType(t *ir.Type) string {
	if t == nil {
		return "nil"
	}
	switch t.Kind {
	case ir.TypeString:
		return `C.CString("")`
	case ir.TypeInt, ir.TypeFloat:
		return "0"
	case ir.TypeBool:
		return "0"
	default:
		return "nil"
	}
}
