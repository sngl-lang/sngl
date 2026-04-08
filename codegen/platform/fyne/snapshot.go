package fyne

import (
	"bytes"
	"fmt"
	"image/png"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
)

func (g *Generator) interpreterSnapshot() {} // implements codegen.InterpreterSnapshotter

func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	env, err := testrunner.BuildEnv(doc, "main")
	if err != nil {
		return nil, fmt.Errorf("build env: %w", err)
	}

	ctx := &snapContext{
		env: env,
		doc: doc,
	}

	var content fyne.CanvasObject
	if doc.App != nil && len(doc.App.Children) > 0 {
		content = ctx.renderChildren(doc.App.Children, true)
	} else {
		content = widget.NewLabel("")
	}

	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Snapshot")
	w.SetContent(content)
	// Resize twice to force a layout pass (workaround for Fyne's
	// deferred initial layout — content isn't laid out until first resize).
	size := fyne.NewSize(float32(width), float32(height))
	w.Resize(size)
	w.Canvas().Content().Resize(size)
	w.Resize(size)

	img := w.Canvas().Capture()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type snapContext struct {
	env *testrunner.Env
	doc *ast.Document
}

func (sc *snapContext) renderChildren(nodes []*ast.VisualNode, vertical bool) fyne.CanvasObject {
	var objects []fyne.CanvasObject
	for _, child := range nodes {
		obj := sc.renderNode(child)
		if obj != nil {
			objects = append(objects, obj)
		}
	}
	if len(objects) == 0 {
		return widget.NewLabel("")
	}
	if len(objects) == 1 {
		return objects[0]
	}
	if vertical {
		return container.NewVBox(objects...)
	}
	return container.NewHBox(objects...)
}

func (sc *snapContext) renderNode(vn *ast.VisualNode) fyne.CanvasObject {
	if vn.If != nil && vn.If.SNGL != nil {
		v, err := sc.env.Eval(vn.If.SNGL)
		b, _ := v.(bool)
		if err != nil || !b {
			return nil
		}
	}

	if vn.For != nil {
		return sc.renderFor(vn)
	}

	if comp := sc.doc.FindComponent(vn.Component); comp != nil {
		return sc.renderComponent(comp, vn)
	}

	return sc.renderPrimitive(vn)
}

func (sc *snapContext) renderPrimitive(vn *ast.VisualNode) fyne.CanvasObject {
	switch vn.Component {
	case "vbox":
		return sc.renderVBox(vn)
	case "hbox":
		return sc.renderHBox(vn)
	case "text":
		return sc.renderText(vn)
	case "button":
		text := fmt.Sprint(sc.evalProp(vn, "text", ""))
		return widget.NewButton(text, nil)
	case "input":
		entry := widget.NewEntry()
		entry.PlaceHolder = fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
		if v := fmt.Sprint(sc.evalProp(vn, "value", "")); v != "" {
			entry.SetText(v)
		}
		return entry
	case "textarea":
		entry := widget.NewMultiLineEntry()
		entry.PlaceHolder = fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
		if v := fmt.Sprint(sc.evalProp(vn, "value", "")); v != "" {
			entry.SetText(v)
		}
		return entry
	case "checkbox":
		checked, _ := sc.evalProp(vn, "checked", false).(bool)
		label := fmt.Sprint(sc.evalProp(vn, "label", ""))
		chk := widget.NewCheck(label, nil)
		chk.Checked = checked
		return chk
	case "toggle":
		checked, _ := sc.evalProp(vn, "checked", false).(bool)
		label := fmt.Sprint(sc.evalProp(vn, "label", ""))
		chk := widget.NewCheck(label, nil)
		chk.Checked = checked
		return chk
	case "radio":
		options, _ := sc.evalProp(vn, "options", []any{}).([]any)
		var strs []string
		for _, o := range options {
			strs = append(strs, fmt.Sprint(o))
		}
		r := widget.NewRadioGroup(strs, nil)
		if v := fmt.Sprint(sc.evalProp(vn, "value", "")); v != "" {
			r.SetSelected(v)
		}
		return r
	case "select":
		options, _ := sc.evalProp(vn, "options", []any{}).([]any)
		var strs []string
		for _, o := range options {
			strs = append(strs, fmt.Sprint(o))
		}
		sel := widget.NewSelect(strs, nil)
		sel.PlaceHolder = fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
		if v := fmt.Sprint(sc.evalProp(vn, "value", "")); v != "" {
			sel.SetSelected(v)
		}
		return sel
	case "progress":
		p := widget.NewProgressBar()
		val := 0.0
		if v := sc.evalProp(vn, "value", 0.0); v != nil {
			switch n := v.(type) {
			case float64:
				val = n
			case int:
				val = float64(n)
			}
		}
		maxVal := 1.0
		if v := sc.evalProp(vn, "max", 1.0); v != nil {
			switch n := v.(type) {
			case float64:
				maxVal = n
			case int:
				maxVal = float64(n)
			}
		}
		if maxVal > 0 {
			p.SetValue(val / maxVal)
		}
		return p
	case "spinner":
		return widget.NewProgressBarInfinite()
	case "badge":
		value := fmt.Sprint(sc.evalProp(vn, "value", ""))
		return widget.NewLabel("[" + value + "]")
	case "tabs":
		return sc.renderTabs(vn)
	case "divider":
		return widget.NewSeparator()
	case "spacer":
		return layout.NewSpacer()
	case "card":
		return sc.renderCard(vn)
	case "link":
		text := fmt.Sprint(sc.evalProp(vn, "text", ""))
		href := fmt.Sprint(sc.evalProp(vn, "href", ""))
		link, _ := fyne.LoadResourceFromURLString(href)
		_ = link
		return widget.NewHyperlink(text, nil)
	case "accordion":
		return sc.renderAccordion(vn)
	case "modal", "drawer":
		return sc.renderConditional(vn)
	case "image":
		alt := fmt.Sprint(sc.evalProp(vn, "alt", "image"))
		return widget.NewLabel("[image: " + alt + "]")
	case "tooltip":
		if len(vn.Children) > 0 {
			return sc.renderNode(vn.Children[0])
		}
		return nil
	default:
		// Fallback: render children vertically
		if len(vn.Children) > 0 {
			return sc.renderChildren(vn.Children, true)
		}
		return widget.NewLabel("")
	}
}

func (sc *snapContext) renderVBox(vn *ast.VisualNode) fyne.CanvasObject {
	content := sc.renderChildren(vn.Children, true)
	if sc.hasProp(vn, "scroll", "true") {
		return container.NewVScroll(content)
	}
	if sc.hasPadding(vn) {
		return container.NewPadded(content)
	}
	return content
}

func (sc *snapContext) renderHBox(vn *ast.VisualNode) fyne.CanvasObject {
	return sc.renderChildren(vn.Children, false)
}

func (sc *snapContext) renderText(vn *ast.VisualNode) fyne.CanvasObject {
	val := fmt.Sprint(sc.evalProp(vn, "value", ""))
	if sc.isBold(vn) {
		return widget.NewLabelWithStyle(val, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	return widget.NewLabel(val)
}

func (sc *snapContext) renderTabs(vn *ast.VisualNode) fyne.CanvasObject {
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	var tabs []*container.TabItem
	for _, item := range items {
		tabs = append(tabs, container.NewTabItem(fmt.Sprint(item), widget.NewLabel("")))
	}
	if len(tabs) == 0 {
		return widget.NewLabel("")
	}
	t := container.NewAppTabs(tabs...)
	selected := 0
	if v := sc.evalProp(vn, "selected", 0); v != nil {
		if n, ok := v.(int); ok {
			selected = n
		}
	}
	if selected >= 0 && selected < len(tabs) {
		t.SelectIndex(selected)
	}
	return t
}

func (sc *snapContext) renderCard(vn *ast.VisualNode) fyne.CanvasObject {
	title := fmt.Sprint(sc.evalProp(vn, "title", ""))
	subtitle := fmt.Sprint(sc.evalProp(vn, "subtitle", ""))
	content := sc.renderChildren(vn.Children, true)
	return widget.NewCard(title, subtitle, content)
}

func (sc *snapContext) renderAccordion(vn *ast.VisualNode) fyne.CanvasObject {
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	var accItems []*widget.AccordionItem
	for _, item := range items {
		accItems = append(accItems, widget.NewAccordionItem(fmt.Sprint(item), widget.NewLabel("")))
	}
	if len(accItems) == 0 {
		return widget.NewLabel("")
	}
	return widget.NewAccordion(accItems...)
}

func (sc *snapContext) renderConditional(vn *ast.VisualNode) fyne.CanvasObject {
	open, _ := sc.evalProp(vn, "open", false).(bool)
	if !open {
		return nil
	}
	title := fmt.Sprint(sc.evalProp(vn, "title", ""))
	content := sc.renderChildren(vn.Children, true)
	return widget.NewCard(title, "", content)
}

func (sc *snapContext) renderFor(vn *ast.VisualNode) fyne.CanvasObject {
	iterVal, err := sc.env.Eval(vn.For.Iterable.SNGL)
	if err != nil {
		return nil
	}
	list, ok := iterVal.([]any)
	if !ok || len(list) == 0 {
		if len(vn.For.Else) > 0 {
			var elseObjects []fyne.CanvasObject
			for _, elseNode := range vn.For.Else {
				obj := sc.renderNode(elseNode)
				if obj != nil {
					elseObjects = append(elseObjects, obj)
				}
			}
			if len(elseObjects) == 1 {
				return elseObjects[0]
			}
			if len(elseObjects) > 1 {
				return container.NewVBox(elseObjects...)
			}
		}
		return nil
	}

	var objects []fyne.CanvasObject
	for i, item := range list {
		childEnv := sc.env.Snapshot()
		childEnv.SetVar(vn.For.Variable, item)
		if vn.For.IndexVar != "" {
			childEnv.SetVar(vn.For.IndexVar, i)
		}

		childCtx := &snapContext{
			env: childEnv,
			doc: sc.doc,
		}

		noFor := *vn
		noFor.For = nil
		obj := childCtx.renderNode(&noFor)
		if obj != nil {
			objects = append(objects, obj)
		}
	}

	if len(objects) == 0 {
		return nil
	}
	return container.NewVBox(objects...)
}

func (sc *snapContext) renderComponent(comp *ast.Component, vn *ast.VisualNode) fyne.CanvasObject {
	childCtx := &snapContext{
		env: testrunner.NewEnv(),
		doc: sc.doc,
	}

	for _, p := range comp.Params {
		if expr, ok := vn.Props[p.Name]; ok {
			childCtx.env.SetVar(p.Name, sc.evalExpr(expr))
		} else {
			childCtx.env.SetVar(p.Name, evalLiteral(p.Default))
		}
	}

	return childCtx.renderChildren(comp.Body, true)
}

// --- helpers ---

func (sc *snapContext) evalProp(vn *ast.VisualNode, name string, fallback any) any {
	expr, ok := vn.Props[name]
	if !ok {
		return fallback
	}
	return sc.evalExpr(expr)
}

func (sc *snapContext) evalExpr(expr ast.Expr) any {
	if expr.SNGL != nil {
		v, err := sc.env.Eval(expr.SNGL)
		if err == nil {
			return v
		}
	}
	if expr.Literal != nil {
		return expr.Literal
	}
	return nil
}

func evalLiteral(expr ast.Expr) any {
	if expr.Literal != nil {
		return expr.Literal
	}
	return nil
}

func (sc *snapContext) isBold(vn *ast.VisualNode) bool {
	if sf := vn.StyleFields(); sf != nil {
		if expr, ok := sf["fontWeight"]; ok {
			return fmt.Sprint(sc.evalExpr(expr)) == "bold"
		}
	}
	return false
}

func (sc *snapContext) hasPadding(vn *ast.VisualNode) bool {
	if sf := vn.StyleFields(); sf != nil {
		_, ok := sf["padding"]
		return ok
	}
	return false
}

func (sc *snapContext) hasProp(vn *ast.VisualNode, name, value string) bool {
	v := fmt.Sprint(sc.evalProp(vn, name, ""))
	return v == value
}
