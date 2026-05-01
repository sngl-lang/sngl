package fyne

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irWidgetField tracks a persistent widget stored on Model.
type irWidgetField struct {
	name   string
	goType string
}

// irWidgetUpdater tracks a function that updates one widget property.
type irWidgetUpdater struct {
	name string
	body string
	deps map[string]bool
}

func (u irWidgetUpdater) DepFields() map[string]bool { return u.deps }

// irViewContext tracks state during IR-based BuildUI() code generation.
type irViewContext struct {
	gc             *golang.GoIRContext
	ctx            *codegen.CodegenCtx
	buf            *strings.Builder
	indent         int
	info           *irAnalysis // for dep tracking (nil in component renders)
	widgetFields   []irWidgetField
	updaters       []irWidgetUpdater
	labelCount     int
	containerCount int
	slotVar        string
	slotChildren   []ir.Stmt
	propVals       map[string]ir.Expr
	// entrySync records (varName → fieldName,target) pairs discovered while
	// walking blueprint-driven components whose Event bindParam handlers do a
	// `var = bindParam` assignment. Drives bindData.SetterExtra so SetVar()
	// keeps the underlying widget in sync.
	entrySync []entrySyncRec
	// imports collects Go import paths declared by blueprint-driven
	// components that were actually rendered into the BuildUI tree. The
	// emitter unions these with always-on imports for the import block.
	imports map[string]bool
}

func (vc *irViewContext) addImports(paths []string) {
	if len(paths) == 0 {
		return
	}
	if vc.imports == nil {
		vc.imports = make(map[string]bool)
	}
	for _, p := range paths {
		if p != "" {
			vc.imports[p] = true
		}
	}
}

type entrySyncRec struct {
	varName   string // sngl var to sync from
	fieldName string // Model field of the widget
	target    string // method to call, e.g. ".SetText"
}

func (vc *irViewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *irViewContext) addField(name, goType string) {
	vc.widgetFields = append(vc.widgetFields, irWidgetField{name, goType})
}

func (vc *irViewContext) addUpdater(name, body string, deps map[string]bool) {
	if len(deps) == 0 {
		return
	}
	vc.updaters = append(vc.updaters, irWidgetUpdater{name: name, body: body, deps: deps})
}

func (vc *irViewContext) exprDeps(expr ir.Expr) map[string]bool {
	if vc.info == nil {
		return nil
	}
	return vc.info.depTracker().ExprDeps(expr)
}

// --- Statement rendering ---

func (vc *irViewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		vc.renderNode(s, resultVar)
	case *ir.If:
		vc.renderIf(s, resultVar)
	case *ir.For:
		vc.renderFor(s, resultVar)
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			vc.renderStmt(bs, resultVar)
		}
	case *ir.SlotInst:
		if len(vc.slotChildren) == 1 {
			vc.renderStmt(vc.slotChildren[0], resultVar)
		} else if len(vc.slotChildren) > 1 {
			partsVar := resultVar + "SlotParts"
			vc.line("var %s []fyne.CanvasObject", partsVar)
			for i, child := range vc.slotChildren {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderStmt(child, childVar)
				vc.line("if %s != nil { %s = append(%s, %s) }", childVar, partsVar, partsVar, childVar)
			}
			vc.line("%s = container.NewVBox(%s...)", resultVar, partsVar)
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmt(child, resultVar)
		}
	}
}

func (vc *irViewContext) renderIf(s *ir.If, resultVar string) {
	if vc.info != nil {
		vc.renderConditional(s, resultVar)
		return
	}
	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if %s {", cond)
	vc.indent++
	for _, child := range s.Body {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else) > 0 {
		vc.line("} else {")
		vc.indent++
		for _, child := range s.Else {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("}")
}

func (vc *irViewContext) renderConditional(s *ir.If, resultVar string) {
	id := vc.containerCount
	vc.containerCount++
	fieldName := fmt.Sprintf("ifBox%d", id)
	vc.addField(fieldName, "*fyne.Container")

	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", innerVar, innerVar)
	vc.line("m.%s = container.NewStack(%s)", fieldName, innerVar)

	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if !(%s) { m.%s.Hide() }", cond, fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)

	deps := vc.exprDeps(s.Cond)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateIf%d", id)
		body := fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", cond, fieldName, fieldName)
		vc.addUpdater(updaterName, body, deps)
	}
}

func (vc *irViewContext) renderFor(s *ir.For, resultVar string) {
	iterVar := s.Key
	iterExpr := vc.gc.EvalExpr(s.Iter)
	indexVar := "_"
	if s.Value != "" {
		indexVar = s.Key
		iterVar = s.Value
	}

	loopGC := vc.gc.WithLocal(iterVar)
	if indexVar != "_" {
		loopGC = loopGC.WithLocal(indexVar)
	}
	savedGC := vc.gc
	vc.gc = loopGC

	if vc.info != nil {
		id := vc.containerCount
		vc.containerCount++
		fieldName := fmt.Sprintf("forBox%d", id)
		vc.addField(fieldName, "*fyne.Container")

		loopItems := resultVar + "Items"
		vc.line("var %s []fyne.CanvasObject", loopItems)
		vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
		vc.indent++
		if indexVar != "_" {
			vc.line("_ = %s", indexVar)
		}
		innerVar := resultVar + "Item"
		vc.line("var %s fyne.CanvasObject", innerVar)
		for _, child := range s.Body {
			vc.renderStmt(child, innerVar)
		}
		vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
		vc.indent--
		vc.line("}")
		vc.line("m.%s = container.NewVBox(%s...)", fieldName, loopItems)
		vc.line("%s = m.%s", resultVar, fieldName)

		deps := vc.exprDeps(s.Iter)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateFor%d", id)
			var bodyBuf strings.Builder
			fmt.Fprintf(&bodyBuf, "var items []fyne.CanvasObject\n")
			fmt.Fprintf(&bodyBuf, "\tfor %s, %s := range %s {\n", indexVar, iterVar, iterExpr)
			if indexVar != "_" {
				fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", indexVar)
			}
			fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t\titems = append(items, widget.NewLabel(fmt.Sprint(%s)))\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t}\n")
			fmt.Fprintf(&bodyBuf, "\tm.%s.Objects = items\n", fieldName)
			fmt.Fprintf(&bodyBuf, "\tm.%s.Refresh()", fieldName)
			vc.addUpdater(updaterName, bodyBuf.String(), deps)
		}
		vc.gc = savedGC
		return
	}

	loopItems := resultVar + "Items"
	vc.line("var %s []fyne.CanvasObject", loopItems)
	vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
	vc.indent++
	if indexVar != "_" {
		vc.line("_ = %s", indexVar)
	}
	innerVar := resultVar + "Item"
	vc.line("var %s fyne.CanvasObject", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
	vc.indent--
	vc.line("}")
	vc.line("%s = container.NewVBox(%s...)", resultVar, loopItems)
	vc.gc = savedGC
}

// --- Node rendering ---

func (vc *irViewContext) renderNode(n *ir.NodeInst, resultVar string) {
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}
	if n.Component != nil {
		vc.renderStdlibComponent(n, resultVar)
		return
	}
	vc.renderRawWidget(n, resultVar)
}

func (vc *irViewContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(vc.ctx.Pkg.Components, comp)
}

func (vc *irViewContext) renderStdlibComponent(n *ir.NodeInst, resultVar string) {
	if bp := loadBlueprints()[n.Name]; bp != nil {
		vc.renderFromBlueprint(n, resultVar, bp)
		return
	}
	// Unknown component: best-effort render as a vbox of children or empty label.
	if len(n.Children) > 0 {
		vc.renderUnknownContainer(n, resultVar)
	} else {
		vc.line("%s = widget.NewLabel(\"\")", resultVar)
	}
}

func (vc *irViewContext) renderUnknownContainer(n *ir.NodeInst, resultVar string) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", childVar, childrenVar, childrenVar, childVar)
	}
	vc.line("%s = container.NewVBox(%s...)", resultVar, childrenVar)
}

// renderFromBlueprint emits Go code for a stdlib component using blueprint loaded
// from fyne.sngl. Replaces the per-component renderXxx methods.
func (vc *irViewContext) renderFromBlueprint(n *ir.NodeInst, resultVar string, bp *fyneBlueprint) {
	if bp.CondProp != "" {
		condExpr := codegen.NodeProp(n, bp.CondProp)
		if condExpr == nil {
			return
		}
		cond := vc.gc.EvalExpr(condExpr)
		vc.line("if %s {", cond)
		vc.indent++
		vc.renderFromBlueprintBody(n, resultVar, bp)
		vc.indent--
		vc.line("}")
		return
	}
	vc.renderFromBlueprintBody(n, resultVar, bp)
}

func (vc *irViewContext) renderFromBlueprintBody(n *ir.NodeInst, resultVar string, bp *fyneBlueprint) {
	if bp.Constructor == nil {
		vc.line("%s = widget.NewLabel(\"\")", resultVar)
		return
	}
	vc.addImports(bp.Constructor.Imports)
	prefix := bp.FieldPrefix
	if prefix == "" {
		prefix = "w"
	}
	id := vc.labelCount
	fieldName := fmt.Sprintf("%s%d", prefix, id)
	target := "m." + fieldName // expression for the widget reference
	if bp.Transient {
		// Transient widgets aren't stored on Model and don't consume an id;
		// the next stored widget gets id 0, matching legacy field naming.
		target = resultVar
	} else {
		vc.labelCount++
		vc.addField(fieldName, bp.Constructor.GoType)
	}

	// If any arg or prelude line references ${children}, emit the children
	// slice ahead of the ctor so substituteTemplate can splice it in.
	var childrenVar string
	if blueprintUsesChildren(bp.Constructor) {
		childrenVar = resultVar + "Children"
		vc.line("var %s []fyne.CanvasObject", childrenVar)
		for i, child := range n.Children {
			childVar := fmt.Sprintf("%sC%d", resultVar, i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderStmt(child, childVar)
			vc.line("if %s != nil { %s = append(%s, %s) }", childVar, childrenVar, childrenVar, childVar)
		}
	}

	// Prelude wraps ctor in a scope so locals (e.g. linkURL%d) don't leak.
	scoped := len(bp.Constructor.Prelude) > 0
	if scoped {
		vc.line("{")
		vc.indent++
		for _, line := range bp.Constructor.Prelude {
			vc.line("%s", substituteTemplate(line, n, vc.gc, id, childrenVar))
		}
	}

	// Decide single-line vs multi-line ctor based on whether any arg is an event.
	hasEvent := false
	for _, a := range bp.Constructor.Args {
		if a.Event != "" {
			hasEvent = true
			break
		}
	}

	goFn := resolveGoFn(bp.Constructor, n)
	if !hasEvent {
		args := make([]string, 0, len(bp.Constructor.Args))
		for _, a := range bp.Constructor.Args {
			args = append(args, vc.resolveCtorArg(a, n, childrenVar, id))
		}
		vc.line("%s = %s(%s)", target, goFn, strings.Join(args, ", "))
	} else {
		vc.line("%s = %s(", target, goFn)
		vc.indent++
		for _, a := range bp.Constructor.Args {
			if a.Event != "" {
				h := codegen.NodeHandler(n, a.Event)
				if h == nil || h.Func == nil {
					vc.line("nil,")
					continue
				}
				sig := a.EventSig
				if sig == "" {
					sig = "func()"
				}
				vc.line("%s {", sig)
				vc.indent++
				vc.emitEventHandlerBlock(h.Func.Block)
				vc.indent--
				vc.line("},")
				continue
			}
			vc.line("%s,", vc.resolveCtorArg(a, n, childrenVar, id))
		}
		vc.indent--
		vc.line(")")
	}

	// Init bindings — one-shot setters at construction.
	for _, b := range bp.Bindings {
		if b.Kind != bindInit {
			continue
		}
		propExpr := codegen.NodeProp(n, b.Prop)
		if propExpr == nil {
			continue
		}
		rhs := vc.gc.EvalExpr(propExpr)
		if b.Quoted {
			if s, ok := codegen.IRLiteralString(propExpr); ok {
				rhs = fmt.Sprintf("%q", s)
			}
		}
		if b.Transform != "" {
			rhs = b.Transform + "(" + rhs + ")"
		}
		vc.line("%s%s(%s)", target, b.Target, rhs)
	}

	// Reactive bindings — emit init setter (unless an Init binding already
	// did) and register a dep-tracked updater.
	updaterPrefix := bp.UpdaterPrefix
	if updaterPrefix == "" {
		updaterPrefix = "update_" + fieldName
	}
	reactiveCount := 0
	for _, b := range bp.Bindings {
		if b.Kind != bindReactive {
			continue
		}
		propExpr := codegen.NodeProp(n, b.Prop)
		if propExpr == nil {
			continue
		}
		rhs := vc.gc.EvalExpr(propExpr)
		if b.Transform != "" {
			rhs = b.Transform + "(" + rhs + ")"
		}
		body := fmt.Sprintf("%s%s(%s)", target, b.Target, rhs)
		// Skip inline init when the prop is already wired through the
		// constructor (the ctor call already sets the value), or when an
		// explicit Init binding handles it.
		if !hasInit(bp.Bindings, b.Prop) && !ctorHasProp(bp.Constructor, b.Prop) {
			vc.line("%s", body)
		}
		deps := vc.exprDeps(propExpr)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("%s%d", updaterPrefix, id)
			if reactiveCount > 0 {
				updaterName = fmt.Sprintf("%s%d_%d", updaterPrefix, id, reactiveCount)
			}
			vc.addUpdater(updaterName, body, deps)
		}
		reactiveCount++
	}

	// Event bindings — assign closure to a field after construction.
	for _, b := range bp.Bindings {
		if b.Kind != bindEvent {
			continue
		}
		h := codegen.NodeHandler(n, b.Prop)
		if h == nil || h.Func == nil {
			continue
		}
		sig := b.Signature
		if sig == "" {
			sig = "func()"
		}
		// Two-way bind: handler body of `var = bindParam` records entrySync.
		var bindVar string
		if b.BindParam != "" {
			bindVar = extractIRAssignTarget(h.Func.Block)
			if bindVar != "" && b.SyncTarget != "" {
				vc.entrySync = append(vc.entrySync, entrySyncRec{
					varName:   bindVar,
					fieldName: fieldName,
					target:    b.SyncTarget,
				})
			}
		}
		vc.line("%s%s = %s {", target, b.Target, sig)
		vc.indent++
		if bindVar != "" {
			// Match the legacy template's behavior: discard the synthesized
			// handler body (which references `e.value`) and assign the closure
			// param directly. Then fire affected updaters.
			vc.line("m.%s = %s", bindVar, b.BindParam)
			if vc.info != nil {
				mutated := map[string]bool{bindVar: true}
				affected := codegen.FindAffected(vc.info.depTracker(), vc.updaters, mutated)
				if len(affected) > 0 {
					for _, u := range affected {
						vc.line("m.%s()", u.name)
					}
				} else {
					vc.line("m.doRefresh()")
				}
			} else {
				vc.line("m.doRefresh()")
			}
		} else {
			vc.emitEventHandlerBlock(h.Func.Block)
		}
		vc.indent--
		vc.line("}")
	}

	if !bp.Transient {
		vc.line("%s = m.%s", resultVar, fieldName)
	}
	if scoped {
		vc.indent--
		vc.line("}")
	}
}

// resolveCtorArg renders one constructor argument. Event args are handled by
// the caller because they need surrounding lines.
func (vc *irViewContext) resolveCtorArg(a ctorArg, n *ir.NodeInst, childrenVar string, id int) string {
	switch {
	case a.Prop != "":
		propExpr := codegen.NodeProp(n, a.Prop)
		if propExpr == nil {
			if a.Transform != "" {
				return a.Transform + `("")`
			}
			return `""`
		}
		s := vc.gc.EvalExpr(propExpr)
		if a.Transform != "" {
			s = a.Transform + "(" + s + ")"
		}
		return s
	case a.Raw != "":
		return substituteTemplate(a.Raw, n, vc.gc, id, childrenVar)
	}
	return "nil"
}

// substituteTemplate replaces `${propname}` with the user's prop expression,
// `${id}` with the numeric widget id, and `${children}` with the children-slice
// variable name. Used for prelude lines and Raw args.
func substituteTemplate(tmpl string, n *ir.NodeInst, gc *golang.GoIRContext, id int, childrenVar string) string {
	out := tmpl
	for {
		i := strings.Index(out, "${")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "}")
		if j < 0 {
			break
		}
		key := out[i+2 : i+j]
		var repl string
		switch key {
		case "id":
			repl = fmt.Sprintf("%d", id)
		case "children":
			repl = childrenVar
		default:
			if propExpr := codegen.NodeProp(n, key); propExpr != nil {
				repl = gc.EvalExpr(propExpr)
			}
		}
		out = out[:i] + repl + out[i+j+1:]
	}
	return out
}

func blueprintUsesChildren(c *ctorMeta) bool {
	if c == nil {
		return false
	}
	if containsChildrenToken(c.Args) {
		return true
	}
	for _, line := range c.Prelude {
		if strings.Contains(line, "${children}") {
			return true
		}
	}
	return false
}

func containsChildrenToken(args []ctorArg) bool {
	for _, a := range args {
		if strings.Contains(a.Raw, "${children}") {
			return true
		}
	}
	return false
}

func hasInit(bindings []bindMeta, prop string) bool {
	for _, b := range bindings {
		if b.Kind == bindInit && b.Prop == prop {
			return true
		}
	}
	return false
}

// resolveGoFn applies ctorMeta.Switches: for each switch, if the user passed
// a literal string prop matching the switch value, use the switch's goFn.
func resolveGoFn(c *ctorMeta, n *ir.NodeInst) string {
	for _, s := range c.Switches {
		propExpr := codegen.NodeProp(n, s.Prop)
		if propExpr == nil {
			continue
		}
		if v, ok := codegen.IRLiteralString(propExpr); ok && v == s.Value {
			return s.GoFn
		}
	}
	return c.GoFn
}

func ctorHasProp(c *ctorMeta, prop string) bool {
	if c == nil {
		return false
	}
	for _, a := range c.Args {
		if a.Prop == prop {
			return true
		}
	}
	return false
}

func (vc *irViewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			propVal := codegen.NodeProp(n, p.Name)
			if propVal != nil {
				args = append(args, vc.gc.EvalExpr(propVal))
			} else if p.Default != nil {
				args = append(args, golang.IRLiteralToGo(p.Default))
			} else {
				args = append(args, "nil")
			}
		}
	}
	if n.Component != nil && n.Component.ChildrenType != nil && len(n.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenToObject(n.Children, slotVar)
		args = append(args, slotVar)
	}
	vc.line("%s = m.render%s(%s)", resultVar, golang.ExportName(n.Name), strings.Join(args, ", "))
}

func (vc *irViewContext) renderChildrenToObject(children []ir.Stmt, resultVar string) {
	if len(children) == 1 {
		if n, ok := children[0].(*ir.NodeInst); ok {
			vc.line("var %s fyne.CanvasObject", resultVar)
			vc.renderNode(n, resultVar)
			return
		}
	}
	partsVar := resultVar + "Parts"
	vc.line("var %s []fyne.CanvasObject", partsVar)
	for i, child := range children {
		childVar := fmt.Sprintf("%sPart%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", childVar, partsVar, partsVar, childVar)
	}
	vc.line("%s := container.NewVBox(%s...)", resultVar, partsVar)
}

func (vc *irViewContext) renderRawWidget(n *ir.NodeInst, resultVar string) {
	content := `""`
	if v := codegen.NodeProp(n, "content"); v != nil {
		content = vc.gc.EvalExpr(v)
	}
	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, content)
}

// --- Event handler emission ---

func (vc *irViewContext) emitEventHandlerBlock(stmts []ir.Stmt) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for _, line := range vc.gc.EvalStmt(stmt) {
			vc.line("%s", line)
		}
		maps.Copy(mutated, codegen.MutatedFields(stmt))
	}
	if vc.info != nil {
		affected := codegen.FindAffected(vc.info.depTracker(), vc.updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				vc.line("m.%s()", u.name)
			}
		} else {
			vc.line("m.doRefresh()")
		}
	} else {
		vc.line("m.doRefresh()")
	}
}

