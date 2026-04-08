//go:build !js

package bubbletea

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
)

type htmlSnapshotter interface {
	SnapshotHTML(html []byte, width, height int) ([]byte, error)
}

func (g *Generator) interpreterSnapshot() {} // implements codegen.InterpreterSnapshotter

func (g *Generator) Snapshot(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.renderView(doc, width, height)
	if err != nil {
		return nil, err
	}

	cols := width / 10
	rows := height / 20
	html := viewToHTML(view, cols, rows)

	htmlPlat := codegen.LookupPlatform("html")
	hs, ok := htmlPlat.(htmlSnapshotter)
	if !ok {
		return nil, fmt.Errorf("html platform does not implement SnapshotHTML")
	}
	return hs.SnapshotHTML(html, width, height)
}

// SnapshotText renders the TUI output as ANSI text.
func (g *Generator) SnapshotText(doc *ast.Document, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	view, err := g.renderView(doc, width, height)
	if err != nil {
		return nil, err
	}
	return []byte(view), nil
}

func (g *Generator) renderView(doc *ast.Document, width, height int) (string, error) {
	env, err := testrunner.BuildEnv(doc, "main")
	if err != nil {
		return "", fmt.Errorf("build env: %w", err)
	}

	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(termenv.TrueColor))

	ctx := &snapContext{
		env:         env,
		renderer:    r,
		scaleFactor: 8,
		doc:         doc,
	}


	var view string
	if doc.App != nil && len(doc.App.Children) > 0 {
		if len(doc.App.Children) == 1 {
			view = ctx.renderNode(doc.App.Children[0])
		} else {
			var parts []string
			for _, child := range doc.App.Children {
				parts = append(parts, ctx.renderNode(child))
			}
			view = lipgloss.JoinVertical(lipgloss.Left, parts...)
		}
	}
	return view, nil
}

type snapContext struct {
	env         *testrunner.Env
	renderer    *lipgloss.Renderer
	scaleFactor int
	doc         *ast.Document
	vertical    bool
	focusIndex  int
}

func (sc *snapContext) renderNode(vn *ast.VisualNode) string {
	if vn.If != nil && vn.If.SNGL != nil {
		v, err := sc.env.Eval(vn.If.SNGL)
		b, _ := v.(bool)
		if err != nil || !b {
			return ""
		}
	}

	if vn.For != nil {
		return sc.renderFor(vn)
	}

	// User-defined component — only look up names that aren't handled
	// as built-in primitives below.  Abstract stdlib components (vbox,
	// text, etc.) may appear in doc.AbstractComponents as a side-effect
	// of LoadStdlib, but their bodies are empty and must not be used.
	if !isPrimitiveName(vn.Component) {
		if comp := sc.findComponent(vn.Component); comp != nil {
			return sc.renderComponent(comp, vn)
		}
	}

	return sc.renderPrimitive(vn)
}

// isPrimitiveName returns true for stdlib component names that have built-in
// rendering in renderPrimitive.  These must not be looked up via findComponent
// because LoadStdlib may inject abstract (empty-body) definitions into the doc.
func isPrimitiveName(name string) bool {
	switch name {
	case "vbox", "hbox", "text", "button", "checkbox", "input", "spacer",
		"image", "radio", "toggle", "select", "textarea", "progress",
		"spinner", "badge", "tabs", "link", "divider", "modal",
		"drawer", "popover", "tooltip", "accordion", "splitview",
		"table", "tree", "menu", "menubar", "toolbar", "datepicker",
		"chip", "avatar", "card", "scroll", "stack":
		return true
	}
	return false
}

func (sc *snapContext) renderPrimitive(vn *ast.VisualNode) string {
	switch vn.Component {
	case "vbox":
		return sc.renderBox(vn, true)
	case "hbox":
		return sc.renderBox(vn, false)
	case "text":
		return sc.renderText(vn)
	case "button":
		return sc.renderButton(vn)
	case "checkbox":
		return sc.renderCheckbox(vn)
	case "input":
		return sc.renderInput(vn)
	case "spacer":
		return sc.buildStyle(vn).Render("")
	case "image":
		alt := fmt.Sprint(sc.evalProp(vn, "alt", "image"))
		return sc.buildStyle(vn).Render("[image: " + alt + "]")
	case "radio":
		return sc.renderRadioSnap(vn)
	case "toggle":
		return sc.renderToggleSnap(vn)
	case "select":
		return sc.renderSelectSnap(vn)
	case "textarea":
		return sc.renderTextareaSnap(vn)
	case "progress":
		return sc.renderProgressSnap(vn)
	case "spinner":
		label := fmt.Sprint(sc.evalProp(vn, "label", ""))
		return sc.buildStyle(vn).Render("⠋ " + label)
	case "badge":
		value := fmt.Sprint(sc.evalProp(vn, "value", ""))
		return sc.buildStyle(vn).Render("[" + value + "]")
	case "tabs":
		return sc.renderTabsSnap(vn)
	case "link":
		text := fmt.Sprint(sc.evalProp(vn, "text", ""))
		return sc.renderer.NewStyle().Underline(true).Render(text)
	case "divider":
		label := fmt.Sprint(sc.evalProp(vn, "label", ""))
		if label != "" {
			return sc.buildStyle(vn).Render("── " + label + " ──")
		}
		return sc.buildStyle(vn).Render("────────────────")
	case "modal":
		return sc.renderModalSnap(vn)
	case "drawer", "popover":
		return sc.renderConditionalSnap(vn)
	case "tooltip":
		if len(vn.Children) > 0 {
			return sc.renderNode(vn.Children[0])
		}
		return ""
	case "accordion":
		return sc.renderAccordionSnap(vn)
	case "splitview":
		return sc.renderSplitviewSnap(vn)
	case "table":
		return sc.renderTableSnap(vn)
	case "tree":
		return sc.renderTreeSnap(vn)
	case "menu":
		return sc.renderMenuSnap(vn)
	case "menubar":
		return sc.renderMenubarSnap(vn)
	case "toolbar":
		return sc.renderBox(vn, false)
	case "datepicker":
		value := fmt.Sprint(sc.evalProp(vn, "value", ""))
		placeholder := fmt.Sprint(sc.evalProp(vn, "placeholder", "YYYY-MM-DD"))
		sc.focusIndex++
		if value != "" {
			return sc.buildStyle(vn).Render("  " + value)
		}
		return sc.buildStyle(vn).Render("  " + placeholder)
	case "chip":
		label := fmt.Sprint(sc.evalProp(vn, "label", ""))
		removable, _ := sc.evalProp(vn, "removable", false).(bool)
		if removable {
			return sc.buildStyle(vn).Render("[" + label + " ×]")
		}
		return sc.buildStyle(vn).Render("[" + label + "]")
	case "avatar":
		initials := fmt.Sprint(sc.evalProp(vn, "initials", ""))
		if initials == "" {
			initials = fmt.Sprint(sc.evalProp(vn, "alt", ""))
		}
		return sc.buildStyle(vn).Render("[" + initials + "]")
	case "card":
		return sc.renderCardSnap(vn)
	default:
		// Fallback: render children vertically
		return sc.renderBox(vn, true)
	}
}

func (sc *snapContext) renderBox(vn *ast.VisualNode, vertical bool) string {
	style := sc.buildStyle(vn)
	prevVertical := sc.vertical
	sc.vertical = vertical

	var children []string
	for _, child := range vn.Children {
		s := sc.renderNode(child)
		if s != "" {
			children = append(children, s)
		}
	}
	sc.vertical = prevVertical

	gap := sc.getGap(vn)
	var content string
	if vertical {
		if gap > 0 {
			spacer := strings.Repeat("\n", gap)
			content = strings.Join(children, spacer)
		} else {
			content = lipgloss.JoinVertical(lipgloss.Left, children...)
		}
	} else {
		if gap > 0 {
			spacer := strings.Repeat(" ", gap)
			content = strings.Join(children, spacer)
		} else {
			content = lipgloss.JoinHorizontal(lipgloss.Top, children...)
		}
	}

	return style.Render(content)
}

func (sc *snapContext) renderText(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	val := sc.evalProp(vn, "value", "")
	return style.Render(fmt.Sprint(val))
}

func (sc *snapContext) renderButton(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	text := fmt.Sprint(sc.evalProp(vn, "text", ""))
	idx := sc.focusIndex
	sc.focusIndex++
	prefix := " "
	if idx == 0 {
		prefix = ">"
	}
	return style.Render(prefix + " " + text)
}

func (sc *snapContext) renderCheckbox(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	checked, _ := sc.evalProp(vn, "checked", false).(bool)
	label := fmt.Sprint(sc.evalProp(vn, "label", ""))
	sc.focusIndex++

	mark := "[ ] "
	if checked {
		mark = "[x] "
	}
	return style.Render("  " + mark + label)
}

func (sc *snapContext) renderInput(vn *ast.VisualNode) string {
	placeholder := fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
	sc.focusIndex++
	// Render like a simple text input with placeholder
	style := sc.renderer.NewStyle().
		Foreground(lipgloss.Color("#585b70"))
	return style.Render(placeholder)
}

func (sc *snapContext) renderFor(vn *ast.VisualNode) string {
	iterVal, err := sc.env.Eval(vn.For.Iterable.SNGL)
	if err != nil {
		return ""
	}
	list, ok := iterVal.([]any)
	if !ok || len(list) == 0 {
		return ""
	}

	var parts []string
	for i, item := range list {
		childEnv := sc.env.Snapshot()
		childEnv.SetVar(vn.For.Variable, item)
		if vn.For.IndexVar != "" {
			childEnv.SetVar(vn.For.IndexVar, i)
		}

		childCtx := &snapContext{
			env:         childEnv,
			renderer:    sc.renderer,
			scaleFactor: sc.scaleFactor,
			doc:         sc.doc,
			vertical:    sc.vertical,
			focusIndex:  sc.focusIndex,
		}

		noFor := *vn
		noFor.For = nil
		s := childCtx.renderNode(&noFor)
		if s != "" {
			parts = append(parts, s)
		}
	}

	if sc.vertical {
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (sc *snapContext) renderComponent(comp *ast.Component, vn *ast.VisualNode) string {
	childCtx := &snapContext{
		env:         testrunner.NewEnv(),
		renderer:    sc.renderer,
		scaleFactor: sc.scaleFactor,
		doc:         sc.doc,
		vertical:    sc.vertical,
		focusIndex:  sc.focusIndex,
	}

	// Set params
	for _, p := range comp.Params {
		if expr, ok := vn.Props[p.Name]; ok {
			childCtx.env.SetVar(p.Name, sc.evalExpr(expr))
		} else {
			childCtx.env.SetVar(p.Name, evalLiteral(p.Default))
		}
	}

	var parts []string
	for _, child := range comp.Body {
		s := childCtx.renderNode(child)
		if s != "" {
			parts = append(parts, s)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (sc *snapContext) buildStyle(vn *ast.VisualNode) lipgloss.Style {
	style := sc.renderer.NewStyle()

	merged := vn.StyleFields()
	if merged == nil {
		merged = make(map[string]ast.Expr)
	}

	for prop, expr := range merged {
		val := sc.evalExpr(expr)
		style = applyStyleProp(style, prop, val, sc.scaleFactor)
	}

	return style
}

func applyStyleProp(style lipgloss.Style, prop string, val any, scaleFactor int) lipgloss.Style {
	switch prop {
	case "padding":
		style = style.Padding(scaleInt(val, scaleFactor))
	case "paddingTop":
		style = style.PaddingTop(scaleInt(val, scaleFactor))
	case "paddingRight":
		style = style.PaddingRight(scaleInt(val, scaleFactor))
	case "paddingBottom":
		style = style.PaddingBottom(scaleInt(val, scaleFactor))
	case "paddingLeft":
		style = style.PaddingLeft(scaleInt(val, scaleFactor))
	case "margin":
		style = style.Margin(scaleInt(val, scaleFactor))
	case "marginTop":
		style = style.MarginTop(scaleInt(val, scaleFactor))
	case "marginRight":
		style = style.MarginRight(scaleInt(val, scaleFactor))
	case "marginBottom":
		style = style.MarginBottom(scaleInt(val, scaleFactor))
	case "marginLeft":
		style = style.MarginLeft(scaleInt(val, scaleFactor))
	case "width":
		style = style.Width(scaleInt(val, scaleFactor))
	case "height":
		style = style.Height(scaleInt(val, scaleFactor))
	case "maxWidth":
		style = style.MaxWidth(scaleInt(val, scaleFactor))
	case "maxHeight":
		style = style.MaxHeight(scaleInt(val, scaleFactor))
	case "color":
		if s, ok := val.(string); ok {
			style = style.Foreground(lipgloss.Color(s))
		}
	case "background":
		if s, ok := val.(string); ok {
			style = style.Background(lipgloss.Color(s))
		}
	case "fontWeight":
		if fmt.Sprint(val) == "bold" {
			style = style.Bold(true)
		}
	case "fontStyle":
		if fmt.Sprint(val) == "italic" {
			style = style.Italic(true)
		}
	case "textAlign":
		switch fmt.Sprint(val) {
		case "center":
			style = style.AlignHorizontal(lipgloss.Center)
		case "right":
			style = style.AlignHorizontal(lipgloss.Right)
		case "left":
			style = style.AlignHorizontal(lipgloss.Left)
		}
	case "borderWidth":
		style = style.Border(lipgloss.NormalBorder())
	case "borderColor":
		if s, ok := val.(string); ok {
			style = style.BorderForeground(lipgloss.Color(s))
		}
	case "opacity":
		style = style.Faint(true)
	}
	return style
}

func scaleInt(val any, scaleFactor int) int {
	var n int
	switch v := val.(type) {
	case int:
		n = v
	case float64:
		n = int(v)
	default:
		return 0
	}
	if n == 0 {
		return 0
	}
	scaled := max(n/scaleFactor, 1)
	return scaled
}

func (sc *snapContext) getGap(vn *ast.VisualNode) int {
	if m := vn.StyleFields(); m != nil {
		if gapExpr, ok := m["gap"]; ok {
			val := sc.evalExpr(gapExpr)
			switch v := val.(type) {
			case int:
				return max(1, v/8)
			case float64:
				return max(1, int(v)/8)
			}
		}
	}
	return 0
}

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

func (sc *snapContext) findComponent(name string) *ast.Component {
	if sc.doc == nil {
		return nil
	}
	return sc.doc.FindComponent(name)
}

// viewToHTML converts ANSI-styled terminal text to an HTML page for screenshotting.
func viewToHTML(view string, cols, rows int) []byte {
	buf := cellbuf.NewBuffer(cols, rows)
	cellbuf.SetContent(buf, view)

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html><head><style>
body { margin: 0; padding: 0; background: #1e1e2e; color: #cdd6f4; }
pre { margin: 0; padding: 0; font-family: monospace; font-size: 14px; line-height: 1.4; }
</style></head><body><pre>`)

	for y := 0; y < buf.Height(); y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		var lastStyle string
		spanOpen := false
		for x := 0; x < buf.Width(); x++ {
			cell := buf.Cell(x, y)
			css := cellCSS(cell)
			if css != lastStyle {
				if spanOpen {
					b.WriteString("</span>")
				}
				if css != "" {
					fmt.Fprintf(&b, `<span style="%s">`, css)
					spanOpen = true
				} else {
					spanOpen = false
				}
				lastStyle = css
			}
			if cell == nil || cell.Rune == 0 {
				b.WriteByte(' ')
			} else {
				switch cell.Rune {
				case '<':
					b.WriteString("&lt;")
				case '>':
					b.WriteString("&gt;")
				case '&':
					b.WriteString("&amp;")
				default:
					b.WriteRune(cell.Rune)
				}
			}
		}
		if spanOpen {
			b.WriteString("</span>")
		}
	}

	b.WriteString("</pre></body></html>")
	return []byte(b.String())
}

func cellCSS(cell *cellbuf.Cell) string {
	if cell == nil {
		return ""
	}
	var parts []string

	if cell.Style.Fg != nil {
		r, g, b, _ := cell.Style.Fg.RGBA()
		parts = append(parts, fmt.Sprintf("color:rgb(%d,%d,%d)", r>>8, g>>8, b>>8))
	}
	if cell.Style.Bg != nil {
		r, g, b, _ := cell.Style.Bg.RGBA()
		parts = append(parts, fmt.Sprintf("background:rgb(%d,%d,%d)", r>>8, g>>8, b>>8))
	}
	if cell.Style.Attrs&cellbuf.BoldAttr != 0 {
		parts = append(parts, "font-weight:bold")
	}
	if cell.Style.Attrs&cellbuf.FaintAttr != 0 {
		parts = append(parts, "opacity:0.5")
	}
	if cell.Style.Attrs&cellbuf.ItalicAttr != 0 {
		parts = append(parts, "font-style:italic")
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";")
}

// --- Snapshot renderers for new components ---

func (sc *snapContext) renderRadioSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	value := fmt.Sprint(sc.evalProp(vn, "value", ""))
	options, _ := sc.evalProp(vn, "options", []any{}).([]any)
	var parts []string
	for _, opt := range options {
		s := fmt.Sprint(opt)
		if s == value {
			parts = append(parts, "(•) "+s)
		} else {
			parts = append(parts, "( ) "+s)
		}
	}
	sc.focusIndex++
	return style.Render("  " + strings.Join(parts, "  "))
}

func (sc *snapContext) renderToggleSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	checked, _ := sc.evalProp(vn, "checked", false).(bool)
	label := fmt.Sprint(sc.evalProp(vn, "label", ""))
	sc.focusIndex++
	mark := "[OFF]"
	if checked {
		mark = "[ON] "
	}
	return style.Render("  " + mark + " " + label)
}

func (sc *snapContext) renderSelectSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	value := fmt.Sprint(sc.evalProp(vn, "value", ""))
	placeholder := fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
	sc.focusIndex++
	display := value
	if display == "" {
		display = placeholder
	}
	return style.Render("  [▼ " + display + "]")
}

func (sc *snapContext) renderTextareaSnap(vn *ast.VisualNode) string {
	value := fmt.Sprint(sc.evalProp(vn, "value", ""))
	placeholder := fmt.Sprint(sc.evalProp(vn, "placeholder", ""))
	sc.focusIndex++
	display := value
	if display == "" {
		display = placeholder
	}
	style := sc.renderer.NewStyle().Foreground(lipgloss.Color("#585b70"))
	return style.Render(display)
}

func (sc *snapContext) renderProgressSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	value := 0.0
	if v := sc.evalProp(vn, "value", 0.0); v != nil {
		switch n := v.(type) {
		case float64:
			value = n
		case int:
			value = float64(n)
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
	ratio := value / maxVal
	if ratio > 1 {
		ratio = 1
	}
	if ratio < 0 {
		ratio = 0
	}
	width := 20
	filled := int(float64(width) * ratio)
	return style.Render("[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]")
}

func (sc *snapContext) renderTabsSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	selected := 0
	if v := sc.evalProp(vn, "selected", 0); v != nil {
		if n, ok := v.(int); ok {
			selected = n
		}
	}
	var tabs []string
	for i, item := range items {
		s := fmt.Sprint(item)
		if i == selected {
			tabs = append(tabs, "["+s+"]")
		} else {
			tabs = append(tabs, " "+s+" ")
		}
	}
	return style.Render(strings.Join(tabs, " "))
}

func (sc *snapContext) renderModalSnap(vn *ast.VisualNode) string {
	open, _ := sc.evalProp(vn, "open", false).(bool)
	if !open {
		return ""
	}
	title := fmt.Sprint(sc.evalProp(vn, "title", ""))
	style := sc.buildStyle(vn)
	var children []string
	if title != "" {
		children = append(children, title)
	}
	for _, child := range vn.Children {
		s := sc.renderNode(child)
		if s != "" {
			children = append(children, s)
		}
	}
	return style.Border(lipgloss.RoundedBorder()).Render(lipgloss.JoinVertical(lipgloss.Left, children...))
}

func (sc *snapContext) renderConditionalSnap(vn *ast.VisualNode) string {
	open, _ := sc.evalProp(vn, "open", false).(bool)
	if !open {
		return ""
	}
	var children []string
	for _, child := range vn.Children {
		s := sc.renderNode(child)
		if s != "" {
			children = append(children, s)
		}
	}
	return sc.buildStyle(vn).Render(lipgloss.JoinVertical(lipgloss.Left, children...))
}

func (sc *snapContext) renderAccordionSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	expanded, _ := sc.evalProp(vn, "expanded", []any{}).([]any)
	expandedSet := map[string]bool{}
	for _, e := range expanded {
		expandedSet[fmt.Sprint(e)] = true
	}
	var parts []string
	for i, item := range items {
		if expandedSet[fmt.Sprint(i)] {
			parts = append(parts, "▼ "+fmt.Sprint(item))
		} else {
			parts = append(parts, "▶ "+fmt.Sprint(item))
		}
	}
	return style.Render(strings.Join(parts, "\n"))
}

func (sc *snapContext) renderSplitviewSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	if len(vn.Children) < 2 {
		if len(vn.Children) == 1 {
			return sc.renderNode(vn.Children[0])
		}
		return ""
	}
	left := sc.renderNode(vn.Children[0])
	right := sc.renderNode(vn.Children[1])
	direction := fmt.Sprint(sc.evalProp(vn, "direction", "horizontal"))
	if direction == "vertical" {
		return style.Render(lipgloss.JoinVertical(lipgloss.Left, left, right))
	}
	return style.Render(lipgloss.JoinHorizontal(lipgloss.Top, left, " │ ", right))
}

func (sc *snapContext) renderTableSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	columns, _ := sc.evalProp(vn, "columns", []any{}).([]any)
	rows, _ := sc.evalProp(vn, "rows", []any{}).([]any)
	var lines []string
	var header []string
	for _, col := range columns {
		header = append(header, fmt.Sprint(col))
	}
	headerLine := strings.Join(header, " | ")
	lines = append(lines, headerLine)
	lines = append(lines, strings.Repeat("─", len(headerLine)))
	for _, row := range rows {
		if cells, ok := row.([]any); ok {
			var cellStrs []string
			for _, c := range cells {
				cellStrs = append(cellStrs, fmt.Sprint(c))
			}
			lines = append(lines, strings.Join(cellStrs, " | "))
		}
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (sc *snapContext) renderTreeSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	var lines []string
	for _, item := range items {
		lines = append(lines, "▶ "+fmt.Sprint(item))
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (sc *snapContext) renderMenuSnap(vn *ast.VisualNode) string {
	open, _ := sc.evalProp(vn, "open", false).(bool)
	if !open {
		return ""
	}
	style := sc.buildStyle(vn)
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	var lines []string
	for _, item := range items {
		lines = append(lines, "  "+fmt.Sprint(item))
	}
	return style.Border(lipgloss.NormalBorder()).Render(strings.Join(lines, "\n"))
}

func (sc *snapContext) renderMenubarSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	items, _ := sc.evalProp(vn, "items", []any{}).([]any)
	var tabs []string
	for _, item := range items {
		tabs = append(tabs, "["+fmt.Sprint(item)+"]")
	}
	return style.Render(strings.Join(tabs, " "))
}

func (sc *snapContext) renderCardSnap(vn *ast.VisualNode) string {
	style := sc.buildStyle(vn)
	var children []string
	for _, child := range vn.Children {
		s := sc.renderNode(child)
		if s != "" {
			children = append(children, s)
		}
	}
	return style.Border(lipgloss.RoundedBorder()).Render(lipgloss.JoinVertical(lipgloss.Left, children...))
}
