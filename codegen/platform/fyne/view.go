package fyne

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// viewContext tracks state during BuildUI() code generation.
type viewContext struct {
	ec         *exprContext
	buf        *strings.Builder
	indent     int
	components []*ast.ComponentDecl
	entryIndex int             // next entry index for persistent entry fields
	info       *analysisResult // for dependency tracking (nil for component renders)

	// Selective update tracking — collected during rendering
	widgetFields   []widgetField
	updaters       []widgetUpdater
	labelCount     int
	btnCount       int
	checkCount     int
	containerCount int
	progressCount  int
	radioCount     int
	selectCount    int
	scrollCount    int
	badgeCount     int
	spinnerCount   int
	slotVar        string // variable holding pre-rendered slot content

	// Component expansion support
	doc            *ast.Document
	slotChildren   []ast.Stmt
	callerEvents   map[string]*ast.EventHandler
	componentDepth int
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *viewContext) addField(name, goType string) {
	vc.widgetFields = append(vc.widgetFields, widgetField{name, goType})
}

func (vc *viewContext) addUpdater(name, body string, deps map[string]bool) {
	if len(deps) == 0 {
		return
	}
	vc.updaters = append(vc.updaters, widgetUpdater{name: name, body: body, deps: deps})
}

func (vc *viewContext) exprDeps(expr ast.Expr) map[string]bool {
	if vc.info == nil {
		return nil
	}
	return vc.info.depTracker().ExprDeps(expr)
}

func (vc *viewContext) emitEventHandlerBlock(block *ast.StmtBlock) {
	mutated := make(map[string]bool)
	for _, bodyStmt := range block.Stmts {
		stmts := vc.ec.TranslateMutation(bodyStmt)
		for _, s := range stmts {
			vc.line("%s", s)
		}
		maps.Copy(mutated, codegen.MutatedFields(bodyStmt))
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

// renderStmt dispatches a top-level statement (VisualNode, IfStmt, ForStmt).
func (vc *viewContext) renderStmt(stmt ast.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ast.VisualNode:
		vc.renderNodeInner(s, resultVar)
	case *ast.IfStmt:
		vc.renderIfStmt(s, resultVar)
	case *ast.ForStmt:
		vc.renderForStmt(s, resultVar)
	case *ast.PlatformStmt:
		for _, bs := range s.Body.Stmts {
			vc.renderStmt(bs, resultVar)
		}
	}
}

func (vc *viewContext) renderIfStmt(s *ast.IfStmt, resultVar string) {
	if vc.info != nil {
		vc.renderConditionalStmt(s, resultVar)
		return
	}
	cond := exprToGoCond(s.Cond, vc.ec)
	vc.line("if %s {", cond)
	vc.indent++
	for _, child := range s.Body.Stmts {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else.Stmts) > 0 {
		vc.line("} else {")
		vc.indent++
		for _, child := range s.Else.Stmts {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("}")
}

func (vc *viewContext) renderConditionalStmt(s *ast.IfStmt, resultVar string) {
	id := vc.containerCount
	vc.containerCount++
	fieldName := fmt.Sprintf("ifBox%d", id)
	vc.addField(fieldName, "*fyne.Container")

	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)

	for _, child := range s.Body.Stmts {
		vc.renderStmt(child, innerVar)
	}

	vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", innerVar, innerVar)
	vc.line("m.%s = container.NewStack(%s)", fieldName, innerVar)

	cond := exprToGoCond(s.Cond, vc.ec)
	vc.line("if !(%s) { m.%s.Hide() }", cond, fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)

	deps := vc.exprDeps(s.Cond)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateIf%d", id)
		body := fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", cond, fieldName, fieldName)
		vc.addUpdater(updaterName, body, deps)
	}
}

func (vc *viewContext) renderForStmt(s *ast.ForStmt, resultVar string) {
	iterVar := s.Key
	iterExpr := exprToGoValue(s.Iter, vc.ec)
	indexVar := "_"
	if s.Value != "" {
		indexVar = s.Key
		iterVar = s.Value
		vc.ec.LocalVars[indexVar] = true
		defer func() { delete(vc.ec.LocalVars, indexVar) }()
	}
	vc.ec.LocalVars[iterVar] = true
	defer func() { delete(vc.ec.LocalVars, iterVar) }()

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
		for _, child := range s.Body.Stmts {
			vc.renderStmt(child, innerVar)
		}
		vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
		vc.indent--
		vc.line("}")
		if len(s.Else.Stmts) > 0 {
			vc.line("if len(%s) == 0 {", iterExpr)
			vc.indent++
			for _, elseStmt := range s.Else.Stmts {
				vc.renderStmt(elseStmt, loopItems+"Else")
				vc.line("%s = append(%s, %sElse)", loopItems, loopItems, loopItems)
			}
			vc.indent--
			vc.line("}")
		}
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
	for _, child := range s.Body.Stmts {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
	vc.indent--
	vc.line("}")
	if len(s.Else.Stmts) > 0 {
		vc.line("if len(%s) == 0 {", iterExpr)
		vc.indent++
		for _, elseStmt := range s.Else.Stmts {
			elseVar := loopItems + "Else"
			vc.line("var %s fyne.CanvasObject", elseVar)
			vc.renderStmt(elseStmt, elseVar)
			vc.line("if %s != nil { %s = append(%s, %s) }", elseVar, loopItems, loopItems, elseVar)
		}
		vc.indent--
		vc.line("}")
	}
	vc.line("%s = container.NewVBox(%s...)", resultVar, loopItems)
}

func (vc *viewContext) renderNodeInner(vn *ast.VisualNode, resultVar string) {
	name := vnName(vn)
	if name == "slot" {
		if len(vc.slotChildren) > 0 {
			for i, child := range vc.slotChildren {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderStmt(child, childVar)
				vc.line("if %s != nil { %s = %s }", childVar, resultVar, childVar)
			}
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
		return
	}

	comp := vc.findComponent(name)
	if comp != nil {
		body := compBodyStmts(comp)
		if len(body) > 0 {
			vc.expandComponent(comp, vn, body, resultVar)
		} else {
			vc.renderUserComponent(vn, resultVar)
		}
		return
	}

	vc.renderRawWidget(vn, resultVar)
}

func (vc *viewContext) findComponent(name string) *ast.ComponentDecl {
	if vc.doc != nil {
		return findComponentInDoc(vc.doc, name)
	}
	for _, c := range vc.components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (vc *viewContext) expandComponent(comp *ast.ComponentDecl, vn *ast.VisualNode, body []ast.Stmt, resultVar string) {
	vc.componentDepth++
	if vc.componentDepth > 10 {
		vc.componentDepth--
		return
	}
	defer func() { vc.componentDepth-- }()

	savedLocals := maps.Clone(vc.ec.LocalVars)
	savedSlot := vc.slotChildren
	savedEvents := vc.callerEvents

	savedOverrides := vc.ec.PropOverrides
	overrides := make(map[string]string)
	if savedOverrides != nil {
		maps.Copy(overrides, savedOverrides)
	}
	props := vnProps(vn)
	for _, p := range compParams(comp) {
		vc.ec.LocalVars[p.Name] = true
		if expr, ok := props[p.Name]; ok {
			overrides[p.Name] = exprToGoValue(expr, vc.ec)
		} else if p.Default != nil && exprIsLiteral(p.Default) {
			overrides[p.Name] = literalToGo(p.Default)
		} else {
			overrides[p.Name] = zeroValueGo(exprTypeHint(p.Type))
		}
	}
	vc.ec.PropOverrides = overrides

	vc.slotChildren = vnChildren(vn)
	vc.callerEvents = vnEvents(vn)

	for _, child := range body {
		vc.renderStmt(child, resultVar)
	}

	vc.ec.LocalVars = savedLocals
	vc.ec.PropOverrides = savedOverrides
	vc.slotChildren = savedSlot
	vc.callerEvents = savedEvents
}

// renderRawWidget renders an implicit Fyne widget by interpreting metadata props.
// Patterns:
//   - constructor + constructorArgs → persistent widget/container
//   - entry=true → existing entry infrastructure (input/textarea)
//   - fallback → widget.NewLabel(content)
func (vc *viewContext) renderRawWidget(vn *ast.VisualNode, resultVar string) {
	props := vnProps(vn)
	// Constructor pattern: constructor="widget.NewLabel" constructorArgs=[value]
	if ctr, ok := props["constructor"]; ok {
		if ctorName, ok := exprLiteralString(ctr); ok {
			vc.renderConstructor(vn, resultVar, ctorName)
			return
		}
	}

	// Entry (input) pattern — uses existing entry infrastructure
	if _, ok := props["entry"]; ok {
		idx := vc.entryIndex
		vc.entryIndex++
		vc.line("%s = m.entry%d", resultVar, idx)
		return
	}

	// Fallback: emit as label
	content := `""`
	if v, ok := props["content"]; ok {
		content = exprToGoValue(v, vc.ec)
	}
	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, content)
}

// renderConstructor handles the constructor/constructorArgs metadata pattern.
// It inspects each element of constructorArgs:
//   - VariadicChildren{} sentinel → render children into slice, spread
//   - nil literal → emit nil
//   - prop reference → fmt.Sprint(value)
//
// If there are children but no VariadicChildren in args, children are rendered
// as a single object and passed as the first (or only) arg.
func (vc *viewContext) renderConstructor(vn *ast.VisualNode, resultVar, ctorName string) {
	props := vnProps(vn)
	children := vnChildNodes(vn)

	// Collect import path if specified
	if ip, ok := props["importPath"]; ok {
		if s, ok := exprLiteralString(ip); ok && vc.info != nil {
			vc.info.goImports[s] = true
		}
	}

	// Parse constructorArgs list
	var goArgs []string
	hasVariadic := false
	if argsExpr, ok := props["constructorArgs"]; ok {
		if list, ok := argsExpr.(*ast.ListExpr); ok {
			for _, elem := range list.Elements {
				// Check for VariadicChildren sentinel
				if se, ok := elem.(*ast.StructExpr); ok && se.Name == "VariadicChildren" {
					hasVariadic = true
					childrenVar := resultVar + "Children"
					vc.line("var %s []fyne.CanvasObject", childrenVar)
					for i, child := range children {
						itemVar := fmt.Sprintf("%sC%d", resultVar, i)
						vc.line("var %s fyne.CanvasObject", itemVar)
						vc.renderNodeInner(child, itemVar)
						vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
					}
					goArgs = append(goArgs, childrenVar+"...")
					continue
				}
				// nil literal (LiteralNull or IdentExpr "nil")
				if lit, ok := elem.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralNull {
					goArgs = append(goArgs, "nil")
					continue
				}
				if ident, ok := elem.(*ast.IdentExpr); ok && ident.Name == "nil" {
					goArgs = append(goArgs, "nil")
					continue
				}
				// Regular expression → wrap in fmt.Sprint for string conversion
				val := exprToGoValue(elem, vc.ec)
				goArgs = append(goArgs, "fmt.Sprint("+val+")")
			}
		}
	}

	// If component has children but no VariadicChildren, render them as a single
	// object and pass as the first arg (e.g., container.NewVScroll(child)).
	if !hasVariadic && len(children) > 0 {
		childVar := resultVar + "Child"
		vc.renderChildrenToObject(children, childVar)
		goArgs = append([]string{childVar}, goArgs...)
	}

	// Derive field type from constructor name: "widget.NewLabel" → "*widget.Label"
	goType := inferFieldType(ctorName)

	// Allocate persistent field
	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("widget%d", id)
	vc.addField(fieldName, goType)

	vc.line("m.%s = %s(%s)", fieldName, ctorName, strings.Join(goArgs, ", "))
	vc.line("%s = m.%s", resultVar, fieldName)

	// Register updater if specified
	updater := ""
	if v, ok := props["updater"]; ok {
		if s, ok := exprLiteralString(v); ok {
			updater = s
		}
	}
	updaterProp := ""
	if v, ok := props["updaterProp"]; ok {
		if s, ok := exprLiteralString(v); ok {
			updaterProp = s
		}
	}
	if updater != "" && updaterProp != "" {
		if expr, ok := props[updaterProp]; ok {
			deps := vc.exprDeps(expr)
			if len(deps) > 0 {
				val := exprToGoValue(expr, vc.ec)
				updaterName := fmt.Sprintf("updateWidget%d", id)
				body := fmt.Sprintf("m.%s%s(fmt.Sprint(%s))", fieldName, updater, val)
				vc.addUpdater(updaterName, body, deps)
			}
		}
	}
}

// inferFieldType derives a Go type from a constructor function name.
// "widget.NewLabel" → "*widget.Label", "container.NewVBox" → "*fyne.Container"
func inferFieldType(ctorName string) string {
	// Special cases where the return type doesn't follow the convention
	switch ctorName {
	case "container.NewVBox", "container.NewHBox", "container.NewStack":
		return "*fyne.Container"
	case "container.NewVScroll":
		return "*container.Scroll"
	case "layout.NewSpacer":
		return "fyne.CanvasObject"
	}
	// Convention: "pkg.NewFoo" → "*pkg.Foo"
	dotIdx := strings.LastIndex(ctorName, ".")
	if dotIdx < 0 {
		return "fyne.CanvasObject"
	}
	pkg := ctorName[:dotIdx]
	name := ctorName[dotIdx+1:]
	name = strings.TrimPrefix(name, "New")
	return "*" + pkg + "." + name
}

func (vc *viewContext) renderUserComponent(vn *ast.VisualNode, resultVar string) {
	name := vnName(vn)
	var comp *ast.ComponentDecl
	for _, c := range vc.components {
		if c.Name == name {
			comp = c
			break
		}
	}
	if comp == nil {
		vc.line("%s = widget.NewLabel(\"[unknown: %s]\")", resultVar, name)
		return
	}

	props := vnProps(vn)
	var args []string
	for _, p := range compParams(comp) {
		if v, ok := props[p.Name]; ok {
			args = append(args, exprToGoValue(v, vc.ec))
		} else {
			args = append(args, "nil")
		}
	}

	children := vnChildNodes(vn)
	if compHasChildren(comp) && len(children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenToObject(children, slotVar)
		args = append(args, slotVar)
	}

	vc.line("%s = m.render%s(%s)", resultVar, exportName(name), strings.Join(args, ", "))
}

func (vc *viewContext) renderChildrenToObject(children []*ast.VisualNode, resultVar string) {
	if len(children) == 1 {
		vc.line("var %s fyne.CanvasObject", resultVar)
		vc.renderNodeInner(children[0], resultVar)
	} else {
		partsVar := resultVar + "Parts"
		vc.line("var %s []fyne.CanvasObject", partsVar)
		for i, child := range children {
			childVar := fmt.Sprintf("%sPart%d", resultVar, i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderNodeInner(child, childVar)
			vc.line("%s = append(%s, %s)", partsVar, partsVar, childVar)
		}
		vc.line("%s := container.NewVBox(%s...)", resultVar, partsVar)
	}
}
