package gtk4

import (
	"fmt"
	"maps"
	"slices"
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

	// nodeBindings maps each NodeID (assigned by internal/lower's
	// passReactivity) → reactive-prop name → C setter info. Populated
	// during renderGtkWidget's reactive-prop loop; consulted by
	// resolveReactiveTokens after the full walk to substitute
	// /*SNGLREACT:i*/ placeholders with `C.<setter>(widget, c_value)`.
	nodeBindings map[string]map[string]gtkBinding
	lateReactive []gtkLateReactiveAssign

	// outerNodeID is the NodeID assigned by passReactivity to the
	// outer SNGL stdlib node currently being inline-expanded (e.g.
	// the `text` in `text(value=…)`). The inner GtkLabel's reactive
	// props are registered under this id too, mapping outer SNGL prop
	// names → inner C setters.
	outerNodeID string

	// eventInvokers collects (id, sngl-event, field, gtk-signal, cType)
	// tuples for every signal connected during the walk. After emit
	// we generate one method per tuple: `func (m *Model) <id><Event>()`
	// that fires the GTK signal — used by the Go test runner to drive
	// `c.<id>.@<event>()` test syntax through the real bridge.
	eventInvokers []gtkEventInvoker

	// conditionalIDs records each user-id'd node materialized inside an
	// `if` branch. Drives emission of `(m *Model) <id>() *<id>Ref` test
	// readers that return nil when the branch isn't mounted. Populated
	// during renderStmt's If case.
	conditionalIDs map[string]conditionalRef

	// inConditional tracks the closest enclosing `if`-branch depth.
	// When nonzero, renderNode flags the node id as conditional.
	inConditional int

	// ifBlocks records every `if` statement encountered in the visual
	// tree so emitIR can generate one `refreshIfN()` method per block.
	// Each block carries the mount/unmount code; reactive refresh
	// invokes the method whenever the cond's deps change.
	ifBlocks []ifBlockInfo

	// forBlocks records every `for` statement. Each block emits a
	// `refreshForN()` method that tears down its prior list refs and
	// rebuilds from the current iter expression.
	forBlocks []forBlockInfo

	// parentBoxField names the enclosing GtkBox model field for the
	// current children loop. Used by renderIf to capture the append
	// target so refreshIfN knows where to mount/unmount. Empty outside
	// a box's children loop.
	parentBoxField string

	// localMode switches widget creation from Model-field storage to
	// Go-local storage. Used by renderFor so multiple iterations don't
	// collide on a single `m.lblN` field. While set, recordNodeBinding
	// is bypassed; widget info is collected into localWidgets instead
	// for the caller (renderFor) to materialize per-id ref structs.
	localMode    bool
	localWidgets []localWidgetInfo
	localCount   int
}

// localWidgetInfo records one widget materialized inside a for-body.
// Each iteration generates one entry per #id'd node, capturing the
// local Go var name and getters drawn from the same dispatch tables
// the field-mode path uses — keeping the type/getter info out of
// renderFor and in step with the rest of view_ir.go.
type localWidgetInfo struct {
	NodeID  string
	VarName string                // Go local var of widget pointer
	CType   string                // widget C type, e.g. "GtkLabel"
	Props   map[string]gtkBinding // prop name → binding (Getter/Setter/etc.)
}

// forBlockInfo carries everything needed to emit one refreshForN
// method post-walk: the iter expression, the per-iteration body
// (Go source), and the (id → listField, refType) bookkeeping.
type forBlockInfo struct {
	Idx         int
	MethodName  string // e.g. "refreshFor0"
	ParentField string
	IterExpr    ir.Expr
	KeyVar      string // Go loop key var, "" when not used
	ValueVar    string // Go loop value var (the `item` in `for item = items`)
	Body        string // pre-rendered Go for the loop body (per iteration)
	IDs         []forIDInfo
}

type forIDInfo struct {
	ID        string                // user-set #id, e.g. "item"
	ListField string                // model field holding the per-iteration refs, e.g. "itemList"
	RefType   string                // generated Go ref struct name, e.g. "itemRef"
	CType     string                // widget C type, e.g. "GtkLabel"
	Props     map[string]gtkBinding // prop name → binding (used to generate ref struct getters)
}

// ifBlockInfo carries everything needed to emit one refreshIfN
// method post-walk: the cond IR, mount/unmount source, and the
// model-field bookkeeping for the conditional refs it owns.
type ifBlockInfo struct {
	Idx          int
	MethodName   string   // e.g. "refreshIf0"
	MountedField string   // e.g. "ifMounted0"
	ParentField  string   // enclosing GtkBox model field
	Cond         ir.Expr  // cond expression to re-evaluate
	MountBody    string   // pre-rendered Go for the cond=true branch (creates widgets, appends)
	TopChildren  []string // local var names of top-level body widgets (for unparent)
	UnmountNulls []string // model field names to set to nil on unmount (conditional refs)
}

// conditionalRef carries enough to emit an `<id>Ref` Go struct with
// per-prop getters. CType is the GTK class C type ("GtkLabel" etc.);
// Field is the Model widget field assigned during the walk.
type conditionalRef struct {
	Field string
	CType string
}

type gtkEventInvoker struct {
	IDLabel    string // SNGL #id (must be user-set; synthetic __n* ids skipped)
	SnglEvent  string // SNGL event name, e.g. "click"
	FieldName  string // Model widget field, e.g. "btn3"
	GTKSignal  string // GTK signal name, e.g. "clicked"
	WidgetType string // C type for the gpointer cast, e.g. "GtkButton"
	// ValueParam, when non-empty, names the Go type of an extra
	// invoker arg that pre-populates the widget before firing the
	// signal (e.g. "string" for entry `@input` / `@change` — the
	// runtime test calls `m.fieldInput("hello")` which sets the
	// entry text and then fires "changed").
	ValueParam string
	// PreFire is a snippet emitted inside the invoker body before
	// the signal fires — typically the C call that pushes the
	// invoker arg into the widget's underlying state (e.g.
	// `C.sngl_set_entry_text((*C.GtkEditable)(unsafe.Pointer(m.entry)), C.CString(v))`).
	PreFire string
}

// userNodeID returns n.ID when it looks like a user-authored #id
// rather than a synthetic passReactivity id ("__nN"). Empty return
// means "no addressable id".
func userNodeID(n *ir.NodeInst) string {
	if n == nil {
		return ""
	}
	if n.ID == "" || strings.HasPrefix(n.ID, "__n") {
		return ""
	}
	return n.ID
}

// gtkBinding describes how to push a new value of one reactive prop
// onto its underlying GTK widget at runtime — and, for test getters,
// how to read it back.
type gtkBinding struct {
	Setter      string // C function, e.g. "gtk_label_set_text"
	Getter      string // C function, e.g. "gtk_label_get_text"; empty when no read-back is available
	GetterCType string // cast type for the getter call (often the same as CType, but e.g. GtkEditable for entries)
	Field       string // Model field name, e.g. "lbl15"
	CType       string // widget C type for the cast, e.g. "GtkLabel"
	ValueIRType *ir.Type
}

type gtkLateReactiveAssign struct {
	NodeID  string
	Prop    string
	ValueIR ir.Expr // captured for re-eval to a C-bridged expression at resolve time
}

// allocWidget reserves storage for one widget. In normal mode it
// allocates a Model field (`m.lbl3`); in for-body local mode it
// emits a Go local (`w7`) so multiple iterations don't collide on
// a single field. Returns the assignable target expression (LHS for
// the constructor call) plus the bare identifier used as the
// binding's "Field" key — gtk_widget_set_<prop>(m.<field>, …) or
// gtk_widget_set_<prop>(<local>, …) accept the same prefix-stripped
// reference at the call site.
func (vc *viewContext) allocWidget(cType, goType string) (target, name string) {
	if vc.localMode {
		name = fmt.Sprintf("w%d", vc.localCount)
		vc.localCount++
		return name, name
	}
	prefix := girFieldPrefix(cType)
	name = fmt.Sprintf("%s%d", prefix, vc.widgetCount)
	vc.widgetCount++
	vc.fields = append(vc.fields, widgetField{name: name, goType: goType})
	return "m." + name, name
}

// recordWidgetBinding routes binding records to the right sink. In
// normal mode it adds to nodeBindings so emitPropertyReaders and
// buildReactiveRefresh see it. In local mode it accumulates into
// localWidgets so renderFor can emit ref-struct methods (one per
// captured prop) without nodeBindings collisions across iterations.
func (vc *viewContext) recordWidgetBinding(nodeID, prop string, b gtkBinding) {
	if vc.localMode {
		if nodeID == "" {
			return
		}
		// Find or create the localWidgetInfo for this var.
		for i := range vc.localWidgets {
			if vc.localWidgets[i].VarName == b.Field {
				if vc.localWidgets[i].Props == nil {
					vc.localWidgets[i].Props = map[string]gtkBinding{}
				}
				vc.localWidgets[i].Props[prop] = b
				return
			}
		}
		vc.localWidgets = append(vc.localWidgets, localWidgetInfo{
			NodeID:  nodeID,
			VarName: b.Field,
			CType:   b.CType,
			Props:   map[string]gtkBinding{prop: b},
		})
		return
	}
	vc.recordNodeBinding(nodeID, prop, b)
}

func (vc *viewContext) recordNodeBinding(nodeID, prop string, b gtkBinding) {
	if nodeID == "" {
		return
	}
	if vc.nodeBindings == nil {
		vc.nodeBindings = make(map[string]map[string]gtkBinding)
	}
	bag, ok := vc.nodeBindings[nodeID]
	if !ok {
		bag = make(map[string]gtkBinding)
		vc.nodeBindings[nodeID] = bag
	}
	bag[prop] = b
}

func (vc *viewContext) nodeBinding(nodeID, prop string) (gtkBinding, bool) {
	if vc.nodeBindings == nil {
		return gtkBinding{}, false
	}
	bag, ok := vc.nodeBindings[nodeID]
	if !ok {
		return gtkBinding{}, false
	}
	b, ok := bag[prop]
	return b, ok
}

// isUserComponent reports whether comp is defined in the user's source package
// (as opposed to stdlib or platform packages).
func (vc *viewContext) isUserComponent(comp *ir.Component) bool {
	if vc.ctx == nil {
		return false
	}
	return slices.Contains(vc.ctx.Pkg.Components, comp)
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// emitStmt renders one IR statement. For lowering-injected reactive
// Assigns (Target = Select{IsElementRef nID, propName}) the renderer
// records the assignment and emits a /*SNGLREACT:i*/ placeholder;
// resolveReactiveTokens substitutes the recorded C setter call once
// the full visual-tree walk has populated nodeBindings.
func (vc *viewContext) emitStmt(stmt ir.Stmt) {
	if a, ok := stmt.(*ir.Assign); ok {
		if vc.recordLateReactive(a) {
			return
		}
	}
	for _, line := range vc.gc.EvalStmt(stmt) {
		vc.line("%s", line)
	}
}

func (vc *viewContext) recordLateReactive(a *ir.Assign) bool {
	sel, ok := a.Target.(*ir.Select)
	if !ok {
		return false
	}
	id, ok := sel.Operand.(*ir.Ident)
	if !ok || !id.IsElementRef {
		return false
	}
	idx := len(vc.lateReactive)
	vc.lateReactive = append(vc.lateReactive, gtkLateReactiveAssign{
		NodeID:  id.Name,
		Prop:    sel.Field,
		ValueIR: a.Value,
	})
	vc.line("/*SNGLREACT:%d*/", idx)
	return true
}

// buildReactiveRefresh returns Go statements that re-apply every
// (nodeID, prop) reactive binding from current state plus invoke
// each `if`-block refresh method. Used inside doRefresh so SetX
// setters fire the same widget updates that lower-injected reactive
// Assigns do inside handler bodies, and so cond-driven branches
// mount/unmount on state change.
func (vc *viewContext) buildReactiveRefresh() string {
	type key struct{ nodeID, prop string }
	seen := map[key]bool{}
	var b strings.Builder
	for i := len(vc.lateReactive) - 1; i >= 0; i-- {
		late := vc.lateReactive[i]
		k := key{late.NodeID, late.Prop}
		if seen[k] {
			continue
		}
		seen[k] = true
		bnd, ok := vc.nodeBinding(late.NodeID, late.Prop)
		if !ok {
			continue
		}
		rhs := vc.irExprToC(late.ValueIR, bnd.ValueIRType)
		fmt.Fprintf(&b, "\tC.%s((*C.%s)(unsafe.Pointer(m.%s)), %s)\n",
			bnd.Setter, bnd.CType, bnd.Field, rhs)
	}
	for _, blk := range vc.ifBlocks {
		fmt.Fprintf(&b, "\tm.%s()\n", blk.MethodName)
	}
	for _, blk := range vc.forBlocks {
		fmt.Fprintf(&b, "\tm.%s()\n", blk.MethodName)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// resolveReactiveTokens replaces every /*SNGLREACT:i*/ placeholder
// emitted by recordLateReactive with a `C.<setter>(widget, c_value)`
// call drawn from nodeBindings. Run after the full visual-tree walk
// so handler bodies referencing widgets registered later in the tree
// still get the correct setter.
func (vc *viewContext) resolveReactiveTokens(src string) string {
	for i, late := range vc.lateReactive {
		token := fmt.Sprintf("/*SNGLREACT:%d*/", i)
		var replacement string
		if b, ok := vc.nodeBinding(late.NodeID, late.Prop); ok {
			rhs := vc.irExprToC(late.ValueIR, b.ValueIRType)
			// Cast through unsafe.Pointer to the widget's concrete C
			// type — most setter signatures want the typed pointer
			// (e.g. gtk_label_set_text wants *C.GtkLabel).
			replacement = fmt.Sprintf("C.%s((*C.%s)(unsafe.Pointer(m.%s)), %s)", b.Setter, b.CType, b.Field, rhs)
		}
		src = strings.Replace(src, token, replacement, 1)
	}
	return src
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
	case *ir.If:
		vc.renderIf(s, resultVar)
	case *ir.For:
		vc.renderFor(s, resultVar)
	}
}

// renderIf captures an `if` block for deferred emission as a
// refreshIfN() method. At BuildUI it emits one call to that method
// which performs the initial mount/unmount; the reactive refresh
// dispatch calls it again whenever the cond's deps change so the
// branch's widgets are parented/unparented in step with state.
//
// Body widgets are rendered into a side buffer rooted in a fresh
// viewContext that shares mutable state (fields, bindings, etc.)
// with the outer walk so test getters and reactive bindings still
// resolve. The captured body source is then attached to the
// refreshIfN method as its mount clause.
//
// Else-branches are not yet supported.
func (vc *viewContext) renderIf(n *ir.If, _ string) {
	if len(n.Else) > 0 {
		vc.line("// TODO: gtk4 renderIf else branch not implemented")
	}
	idx := len(vc.ifBlocks)
	methodName := fmt.Sprintf("refreshIf%d", idx)
	mountedField := fmt.Sprintf("ifMounted%d", idx)
	parent := vc.parentBoxField
	vc.fields = append(vc.fields, widgetField{name: mountedField, goType: "bool"})

	prevBuf := vc.buf
	prevIndent := vc.indent
	prevCondIDs := vc.conditionalIDs
	vc.conditionalIDs = make(map[string]conditionalRef)
	maps.Copy(vc.conditionalIDs, prevCondIDs)

	var body strings.Builder
	vc.buf = &body
	vc.indent = 1
	vc.inConditional++

	var topChildren []string
	for i, child := range n.Body {
		childVar := fmt.Sprintf("ifChild%d_%d", idx, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		if parent != "" {
			vc.line("if %s != nil { C.gtk_box_append(m.%s, %s) }", childVar, parent, childVar)
		}
		topChildren = append(topChildren, childVar)
	}
	vc.line("m.%s = true", mountedField)

	vc.inConditional--
	vc.buf = prevBuf
	vc.indent = prevIndent

	// Diff new conditional ids (only those introduced inside this block).
	var unmountNulls []string
	for id, r := range vc.conditionalIDs {
		if _, was := prevCondIDs[id]; !was {
			unmountNulls = append(unmountNulls, r.Field)
		}
	}

	vc.ifBlocks = append(vc.ifBlocks, ifBlockInfo{
		Idx:          idx,
		MethodName:   methodName,
		MountedField: mountedField,
		ParentField:  parent,
		Cond:         n.Cond,
		MountBody:    body.String(),
		TopChildren:  topChildren,
		UnmountNulls: unmountNulls,
	})

	vc.line("m.%s()", methodName)
}

// renderFor captures a `for` block for deferred emission as a
// refreshForN() method. The body is rendered through the normal
// stdlib dispatch with localMode=true so widget storage falls into
// Go locals (not Model fields), and binding records accumulate on
// vc.localWidgets. Each user-id'd widget surfaces as
// `(m *Model) <id>() []*<id>Ref` test reader; the ref struct gets
// one method per recorded prop (Value, Placeholder, …), with the
// real C type + getter pulled from the binding — same source the
// field path uses, so widget-type coverage stays uniform.
func (vc *viewContext) renderFor(n *ir.For, _ string) {
	idx := len(vc.forBlocks)
	methodName := fmt.Sprintf("refreshFor%d", idx)
	parent := vc.parentBoxField

	// Render body into a side buffer in local mode.
	prevBuf := vc.buf
	prevIndent := vc.indent
	prevLocalMode := vc.localMode
	prevLocalWidgets := vc.localWidgets
	prevLocalCount := vc.localCount
	var body strings.Builder
	vc.buf = &body
	vc.indent = 2
	vc.localMode = true
	vc.localWidgets = nil
	vc.localCount = 0

	for i, child := range n.Body {
		childVar := fmt.Sprintf("ifor%d_%d", idx, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		if parent != "" {
			vc.line("if %s != nil { C.gtk_box_append(m.%s, %s) }", childVar, parent, childVar)
		}
	}

	// Group localWidgets by NodeID → one ref struct per id.
	type idGroup struct {
		ID    string
		CType string
		Props map[string]gtkBinding
		Vars  []string // local var names per iteration body entry
	}
	groups := map[string]*idGroup{}
	var orderedIDs []string
	for _, lw := range vc.localWidgets {
		if lw.NodeID == "" {
			continue
		}
		g, ok := groups[lw.NodeID]
		if !ok {
			g = &idGroup{ID: lw.NodeID, CType: lw.CType, Props: map[string]gtkBinding{}}
			groups[lw.NodeID] = g
			orderedIDs = append(orderedIDs, lw.NodeID)
		}
		g.Vars = append(g.Vars, lw.VarName)
		maps.Copy(g.Props, lw.Props)
	}
	// Append the per-id `m.<id>List = append(...)` lines to the body.
	for _, id := range orderedIDs {
		g := groups[id]
		// Pick the most-recent local var (last entry) — for the
		// single-widget-per-iteration case that's the right one.
		// Multi-widget-per-iteration bodies would need richer tracking.
		varName := g.Vars[len(g.Vars)-1]
		fmt.Fprintf(&body, "\t\tm.%sList = append(m.%sList, &%sRef{w: %s})\n", id, id, id, varName)
	}

	vc.buf = prevBuf
	vc.indent = prevIndent
	vc.localMode = prevLocalMode
	capturedLocals := vc.localWidgets
	vc.localWidgets = prevLocalWidgets
	vc.localCount = prevLocalCount

	// Build forIDInfo per id from the recorded bindings.
	var ids []forIDInfo
	for _, id := range orderedIDs {
		g := groups[id]
		ids = append(ids, forIDInfo{
			ID:        id,
			ListField: id + "List",
			RefType:   id + "Ref",
			CType:     g.CType,
			Props:     g.Props,
		})
		vc.fields = append(vc.fields, widgetField{
			name:   id + "List",
			goType: "[]*" + id + "Ref",
		})
	}
	_ = capturedLocals // reserved for future use (per-iter unparent of all locals, not just refs)

	vc.forBlocks = append(vc.forBlocks, forBlockInfo{
		Idx:         idx,
		MethodName:  methodName,
		ParentField: parent,
		IterExpr:    n.Iter,
		KeyVar:      n.Key,
		ValueVar:    n.Value,
		Body:        body.String(),
		IDs:         ids,
	})

	vc.line("m.%s()", methodName)
}

func (vc *viewContext) recordConditionalID(id, field, cType string) {
	if id == "" {
		return
	}
	if vc.conditionalIDs == nil {
		vc.conditionalIDs = make(map[string]conditionalRef)
	}
	vc.conditionalIDs[id] = conditionalRef{Field: field, CType: cType}
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
	goType := fmt.Sprintf("*C.%s", info.CType)
	target, name := vc.allocWidget(info.CType, goType)
	assign := "="
	if vc.localMode {
		assign = ":="
	}

	// Build constructor call.
	// Use the first constructor from GIR if available; otherwise fall back to
	// gtk_<type>_new.
	if info.Constructor.Name != "" {
		args := vc.buildCtorArgs(n, info)
		vc.line("%s %s (*C.%s)(unsafe.Pointer(C.%s(%s)))",
			target, assign, info.CType, info.Constructor.Name, strings.Join(args, ", "))
	} else {
		cFn := "gtk_" + strings.ToLower(strings.TrimPrefix(info.CType, "Gtk")) + "_new"
		vc.line("%s %s (*C.%s)(unsafe.Pointer(C.%s()))",
			target, assign, info.CType, cFn)
	}

	// Assign resultVar as GtkWidget* for parent to add as child.
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(%s))", resultVar, target)

	// Connect signal handlers. connectSignal callbacks close over
	// `m.<field>` — they need a Model-rooted target, so skip in local
	// mode for now (for-body widgets don't carry handlers today).
	if !vc.localMode {
		for _, h := range n.Handlers {
			signal := h.Name
			vc.connectSignal(name, signal, &h, n)
		}
	}

	// Register bindings for props. In normal mode bindings drive
	// passReactivity's resolver (reactive deps only) and tests'
	// property-reader emission. In local mode bindings feed
	// renderFor's per-id ref struct (every user prop, since
	// per-iteration getters need to be exposed regardless of
	// reactivity).
	for _, arg := range n.Props {
		setter := gtkSetter(info.CType, arg.Name)
		hasDeps := len(vc.exprDeps(arg.Value)) > 0
		if !vc.localMode {
			if setter == "" || !hasDeps {
				continue
			}
		}
		binding := gtkBinding{
			Setter:      setter,
			Field:       name,
			CType:       info.CType,
			ValueIRType: arg.Value.ExprType(),
		}
		if g, ok := gtkGetterFor(info.CType, arg.Name); ok {
			binding.Getter = g.Fn
			binding.GetterCType = g.Cast
		}
		// Inner-node id + C prop name (e.g. GtkLabel.label).
		vc.recordWidgetBinding(n.ID, arg.Name, binding)
		// When this widget is inside a stdlib inline expansion (e.g.
		// `text(value=…)` → `GtkLabel(label=value)`), also alias the
		// binding under the outer SNGL node's id + outer prop name so
		// passReactivity-injected Assigns (which reference the outer
		// id) resolve correctly.
		if vc.outerNodeID != "" && !vc.localMode {
			if id, ok := arg.Value.(*ir.Ident); ok {
				if _, inScope := vc.propScope[id.Name]; inScope {
					vc.recordNodeBinding(vc.outerNodeID, id.Name, binding)
				}
			}
		}
	}

	// Add children. Parent expression matches the storage form so
	// child-add calls work uniformly for field-mode and local-mode.
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sChild%d", resultVar, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		vc.addChildToWidget(target, info.CType, childVar)
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
	// Record an invoker for user-id'd nodes so tests can drive
	// `c.<id>.@<event>()` through the real GTK signal bridge. Skip
	// passReactivity's synthetic __nN ids — those aren't reachable
	// from test syntax.
	if id := userNodeID(n); id != "" && h != nil {
		cType := ""
		for _, wf := range vc.fields {
			if wf.name == fieldName {
				cType = strings.TrimPrefix(strings.TrimPrefix(wf.goType, "*"), "C.")
				break
			}
		}
		inv := gtkEventInvoker{
			IDLabel:    id,
			SnglEvent:  h.Name,
			FieldName:  fieldName,
			GTKSignal:  signal,
			WidgetType: cType,
		}
		// Entry input/change events carry an event payload — the
		// invoker takes the corresponding stdlib event struct (e.g.
		// InputEvent{Value: "h"}) and preloads the entry text from
		// the struct's Value field before firing the connected
		// handler.
		if cType == "GtkEntry" && (h.Name == "input" || h.Name == "change" || h.Name == "changed") {
			eventType := "InputEvent"
			if h.Name == "change" || h.Name == "changed" {
				eventType = "ChangeEvent"
			}
			inv.ValueParam = "e " + eventType
			inv.PreFire = fmt.Sprintf(
				"C.sngl_set_entry_text((*C.GtkEditable)(unsafe.Pointer(m.%s)), C.CString(e.Value))",
				fieldName,
			)
		}
		vc.eventInvokers = append(vc.eventInvokers, inv)
	}
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
			vc.emitStmt(stmt)
		}
	}
	vc.indent--
	vc.line("})")
	vc.line("C.sngl_connect(unsafe.Pointer(m.%s), C.CString(%q), C.int(_idx))", fieldName, signal)
	vc.indent--
	vc.line("}")
}

// addChildToWidget emits the appropriate gtk_xxx_append or set_child call.
// addChildToWidget emits the child-add call for parentExpr (a fully
// qualified Go expression — "m.box0" in field mode, "w3" in local
// mode). The caller picks the right form for the current storage.
func (vc *viewContext) addChildToWidget(parentExpr, cType, childVar string) {
	vc.line("if %s != nil {", childVar)
	vc.indent++
	if cType == "GtkApplicationWindow" || cType == "GtkWindow" {
		vc.line("C.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(%s)), %s)", parentExpr, childVar)
	} else if fn := gtkChildAdd(cType); fn != "" {
		vc.line("C.%s((*C.GtkWidget)(unsafe.Pointer(%s)), %s)", fn, parentExpr, childVar)
	} else {
		// Fallback: try gtk_widget_set_child (may not compile — better than silent drop)
		vc.line("_ = %s // TODO: no child-add for %s", parentExpr, cType)
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
	prevParent := vc.parentBoxField
	vc.parentBoxField = fieldName
	for i, child := range n.Children {
		if ifs, ok := child.(*ir.If); ok {
			vc.renderIf(ifs, "")
			continue
		}
		if forStmt, ok := child.(*ir.For); ok {
			vc.renderFor(forStmt, "")
			continue
		}
		childVar := fmt.Sprintf("%sChild%d", resultVar, i)
		vc.line("var %s *C.GtkWidget", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { C.gtk_box_append(m.%s, %s) }", childVar, fieldName, childVar)
	}
	vc.parentBoxField = prevParent
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
	target, name := vc.allocWidget(info.CType, fmt.Sprintf("*C.%s", info.CType))

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
	ctorFn := "gtk_label_new"
	if info.Constructor.Name != "" {
		ctorFn = info.Constructor.Name
	}
	if vc.localMode {
		vc.line("%s := (*C.%s)(unsafe.Pointer(C.%s(%s)))", target, info.CType, ctorFn, initVal)
	} else {
		vc.line("%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))", target, info.CType, ctorFn, initVal)
	}
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(%s))", resultVar, target)

	// Register a binding for every user-id'd label so tests can
	// read its current text via `c.<id>.value`. Reactive deps
	// also feed resolveReactiveTokens — both consumers want the
	// same binding shape. In local mode the binding routes to
	// localWidgets so renderFor can synthesize ref methods.
	if n.ID != "" {
		binding := gtkBinding{
			Setter:      "gtk_label_set_text",
			Getter:      "gtk_label_get_text",
			GetterCType: "GtkLabel",
			Field:       name,
			CType:       info.CType,
			ValueIRType: &ir.Type{Kind: ir.TypeString},
		}
		vc.recordWidgetBinding(n.ID, "value", binding)
		vc.recordWidgetBinding(n.ID, "label", binding)
		if id := userNodeID(n); id != "" && vc.inConditional > 0 && !vc.localMode {
			vc.recordConditionalID(id, name, info.CType)
		}
	}
}

func (vc *viewContext) renderStdlibEntry(n *ir.NodeInst, resultVar string) {
	info, ok := vc.registry.Classes["Entry"]
	if !ok {
		vc.line("// TODO: GtkEntry not in GIR")
		return
	}
	target, name := vc.allocWidget(info.CType, fmt.Sprintf("*C.%s", info.CType))
	if vc.localMode {
		vc.line("%s := (*C.%s)(unsafe.Pointer(C.gtk_entry_new()))", target, info.CType)
	} else {
		vc.line("%s = (*C.%s)(unsafe.Pointer(C.gtk_entry_new()))", target, info.CType)
	}
	vc.line("%s = (*C.GtkWidget)(unsafe.Pointer(%s))", resultVar, target)

	// Placeholder prop.
	if ph := codegen.NodeProp(n, "placeholder"); ph != nil {
		phStr := vc.irExprToC(ph, &ir.Type{Kind: ir.TypeString})
		vc.line("C.gtk_entry_set_placeholder_text(%s, %s)", target, phStr)
	}

	// Value prop: init the entry text and register a binding so
	// passReactivity-injected updates fire gtk_editable_set_text.
	if valExpr := codegen.NodeProp(n, "value"); valExpr != nil {
		valStr := vc.irExprToC(valExpr, &ir.Type{Kind: ir.TypeString})
		vc.line("C.gtk_editable_set_text((*C.GtkEditable)(unsafe.Pointer(%s)), %s)", target, valStr)
		// Always record getter (tests want c.<id>.value); only register
		// the setter side when the prop has reactive deps so the
		// resolveReactiveTokens path can write back.
		binding := gtkBinding{
			Setter:      "sngl_set_entry_text",
			Getter:      "gtk_editable_get_text",
			GetterCType: "GtkEditable",
			Field:       name,
			CType:       "GtkEditable",
			ValueIRType: &ir.Type{Kind: ir.TypeString},
		}
		if n.ID != "" && (len(vc.exprDeps(valExpr)) > 0 || vc.localMode) {
			vc.recordWidgetBinding(n.ID, "value", binding)
		} else if len(vc.exprDeps(valExpr)) > 0 {
			vc.recordNodeBinding(n.ID, "value", binding)
		}
	}

	// Entry event handlers: every `@input` / `@change` connects to
	// the GTK "changed" signal (GtkEntry has no separate commit
	// signal) and synthesizes the event-param value from the entry's
	// current text. Signal connection needs a Model-field target —
	// skip in local mode (for-body iterations don't emit handlers).
	if !vc.localMode {
		for i := range n.Handlers {
			h := &n.Handlers[i]
			if h.Name != "input" && h.Name != "change" && h.Name != "changed" {
				continue
			}
			var synth []string
			if h.Func != nil && len(h.Func.Params) > 0 {
				param := h.Func.Params[0]
				synth = append(synth,
					fmt.Sprintf("%s := struct{ Value string }{Value: C.GoString(C.gtk_editable_get_text((*C.GtkEditable)(unsafe.Pointer(m.%s))))}", param.Name, name),
				)
			}
			vc.connectSignal(name, "changed", h, n, synth...)
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
	supplied := map[string]bool{}
	if textExpr != nil {
		supplied["label"] = true
	}
	ctor := info.ConstructorFor(supplied)
	switch len(ctor.Params) {
	case 0:
		// Zero-arg constructor (e.g. gtk_button_new) — set the label
		// afterwards via the writable property when supplied.
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s()))", fieldName, info.CType, ctor.Name)
		if textExpr != nil {
			labelArg := vc.irExprToC(textExpr, &ir.Type{Kind: ir.TypeString})
			vc.line("C.gtk_button_set_label(m.%s, %s)", fieldName, labelArg)
		}
	default:
		// Constructor takes the label directly (e.g. gtk_button_new_with_label).
		var labelArg string
		if textExpr != nil {
			labelArg = vc.irExprToC(textExpr, &ir.Type{Kind: ir.TypeString})
		} else {
			labelArg = `C.CString("")`
		}
		vc.line("m.%s = (*C.%s)(unsafe.Pointer(C.%s(%s)))", fieldName, info.CType, ctor.Name, labelArg)
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

	// Preserve the outer NodeID so the inner GtkWidget registers its
	// reactive bindings under it too — passReactivity injected
	// Assigns reference the outer SNGL node's ID and prop names.
	savedOuter := vc.outerNodeID
	if n.ID != "" {
		vc.outerNodeID = n.ID
	}

	// Walk the component body. We need a fake NodeInst for each body stmt
	// to thread the slot children. Simpler: just render each body stmt
	// with the slot children attached via a temporary mechanism.
	// For now: walk body, and when we hit a SlotInst, render n.Children.
	for _, stmt := range savedChildren {
		vc.renderStmtWithSlot(stmt, resultVar, n.Children)
	}

	vc.outerNodeID = savedOuter
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

// gtkGetterTable mirrors gtkSetterTable for property reads. Tests use
// the emitted `(m *Model) <id><Prop>()` getters to inspect widget
// state after firing events. Values are (cFunc, castType) — the cast
// type sometimes differs from the field's CType (e.g. GtkEntry's
// text getter lives on GtkEditable).
var gtkGetterTable = map[string]map[string]gtkGetter{
	"GtkButton": {
		"label": {Fn: "gtk_button_get_label", Cast: "GtkButton"},
	},
	"GtkLabel": {
		"label": {Fn: "gtk_label_get_text", Cast: "GtkLabel"},
	},
	"GtkEntry": {
		"text": {Fn: "gtk_editable_get_text", Cast: "GtkEditable"},
	},
	"GtkEditable": {
		"text": {Fn: "gtk_editable_get_text", Cast: "GtkEditable"},
	},
	"GtkCheckButton": {
		"active": {Fn: "gtk_check_button_get_active", Cast: "GtkCheckButton"},
		"label":  {Fn: "gtk_check_button_get_label", Cast: "GtkCheckButton"},
	},
	"GtkApplicationWindow": {
		"title": {Fn: "gtk_window_get_title", Cast: "GtkWindow"},
	},
}

type gtkGetter struct {
	Fn   string
	Cast string
}

func gtkGetterFor(cType, prop string) (gtkGetter, bool) {
	if m, ok := gtkGetterTable[cType]; ok {
		g, ok := m[prop]
		return g, ok
	}
	return gtkGetter{}, false
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
