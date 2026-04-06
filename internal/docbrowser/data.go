package docbrowser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/docs"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// initComponents provides the component data for the doc browser.
func initComponents() []docs.Component {
	return docs.Components()
}

// --- Styles ---

var (
	subtle    = lipgloss.Color("#666")
	highlight = lipgloss.Color("#7ee787")
	accent    = lipgloss.Color("#58a6ff")
	dimmed    = lipgloss.Color("#555")
	warning   = lipgloss.Color("#d29922")

	sidebarStyle = lipgloss.NewStyle().
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(dimmed).
			PaddingRight(2).
			PaddingLeft(1)

	detailStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			PaddingRight(1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ccc"))

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(warning).
			PaddingTop(1)

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accent)

	propNameStyle = lipgloss.NewStyle().
			Foreground(highlight)

	propTypeStyle = lipgloss.NewStyle().
			Foreground(subtle)

	docStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#aaa"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#fff")).
			Background(lipgloss.Color("#333")).
			Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtle)

	kindStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888"))
)

// --- Nav Item ---

type navItem struct {
	section   string          // section header text (if this is a header row)
	component *docs.Component // stdlib component (mutually exclusive with decl)
	decl      *declEntry      // user package declaration
}

func (n navItem) isHeader() bool { return n.section != "" }
func (n navItem) name() string {
	if n.component != nil {
		return n.component.Name
	}
	if n.decl != nil {
		return n.decl.Name
	}
	return n.section
}

type declEntry struct {
	Name   string
	Kind   string // "component", "struct", "enum", "func", "var", "const"
	Doc    string
	Decl   any    // underlying AST node
	Source *ast.Document
}

// --- Browser Model ---

type browser struct {
	Model
	title     string
	flatItems []navItem
	scroll    int
	detScroll int
}

func (b browser) Init() tea.Cmd {
	return nil
}

func (b browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.width = msg.Width
		b.height = msg.Height
	case tea.KeyPressMsg:
		switch {
		case msg.Code == tea.KeyUp || msg.Code == 'k':
			b.moveUp()
			b.detScroll = 0
		case msg.Code == tea.KeyDown || msg.Code == 'j':
			b.moveDown()
			b.detScroll = 0
		case msg.Code == tea.KeyPgUp:
			b.detScroll -= 10
			if b.detScroll < 0 {
				b.detScroll = 0
			}
		case msg.Code == tea.KeyPgDown:
			b.detScroll += 10
		case msg.Code == 'q':
			return b, tea.Quit
		case msg.Code == 'c' && msg.Mod == tea.ModCtrl:
			return b, tea.Quit
		}
	}
	return b, nil
}

func (b *browser) moveUp() {
	idx := b.selectedIndex - 1
	for idx >= 0 && b.flatItems[idx].isHeader() {
		idx--
	}
	if idx >= 0 {
		b.selectedIndex = idx
	}
	b.ensureVisible()
}

func (b *browser) moveDown() {
	idx := b.selectedIndex + 1
	for idx < len(b.flatItems) && b.flatItems[idx].isHeader() {
		idx++
	}
	if idx < len(b.flatItems) {
		b.selectedIndex = idx
	}
	b.ensureVisible()
}

func (b *browser) ensureVisible() {
	maxVis := b.height - 4
	if maxVis < 1 {
		maxVis = 1
	}
	if b.selectedIndex < b.scroll {
		b.scroll = b.selectedIndex
	}
	if b.selectedIndex >= b.scroll+maxVis {
		b.scroll = b.selectedIndex - maxVis + 1
	}
}

func (b browser) View() tea.View {
	if b.width == 0 {
		return tea.NewView("Loading...")
	}

	sw := 30
	if sw > b.width/3 {
		sw = b.width / 3
	}
	dw := b.width - sw - 4

	sidebar := b.renderSidebar(sw)
	detail := b.renderDetail(dw)

	content := lipgloss.JoinHorizontal(lipgloss.Top,
		sidebarStyle.Width(sw).Height(b.height-1).Render(sidebar),
		detailStyle.Width(dw).Render(detail),
	)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (b browser) renderSidebar(width int) string {
	var lines []string
	lines = append(lines, headerStyle.Render(b.title))
	lines = append(lines, "")

	maxVis := b.height - 4
	if maxVis < 1 {
		maxVis = 1
	}

	vis := 0
	for i, item := range b.flatItems {
		if i < b.scroll {
			continue
		}
		if vis >= maxVis {
			break
		}
		vis++

		if item.isHeader() {
			lines = append(lines, sectionStyle.Width(width-2).Render(strings.ToUpper(item.section)))
			continue
		}

		name := item.name()
		if item.decl != nil {
			// Show kind indicator for user decls
			kind := kindStyle.Render(item.decl.Kind[:1])
			if i == b.selectedIndex {
				lines = append(lines, selectedStyle.Render("▸ ")+kind+" "+selectedStyle.Render(name))
			} else {
				lines = append(lines, normalStyle.Render("  ")+kind+" "+normalStyle.Render(name))
			}
		} else {
			if i == b.selectedIndex {
				lines = append(lines, selectedStyle.Render("▸ "+name))
			} else {
				lines = append(lines, normalStyle.Render("  "+name))
			}
		}
	}

	lines = append(lines, helpStyle.Render("↑↓ navigate  pgup/pgdn scroll  q quit"))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (b browser) renderDetail(width int) string {
	if b.selectedIndex < 0 || b.selectedIndex >= len(b.flatItems) {
		return docStyle.Render("Select an item")
	}
	item := b.flatItems[b.selectedIndex]

	var sections []string

	if item.component != nil {
		sections = renderComponentDetail(item.component, width)
	} else if item.decl != nil {
		sections = renderDeclDetail(item.decl, width)
	} else {
		return docStyle.Render("Select an item")
	}

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Apply scroll
	lines := strings.Split(content, "\n")
	if b.detScroll > 0 && b.detScroll < len(lines) {
		lines = lines[b.detScroll:]
	}
	maxLines := b.height - 2
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	return strings.Join(lines, "\n")
}

// --- Detail Renderers ---

func renderComponentDetail(comp *docs.Component, width int) []string {
	var s []string

	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fff")).Render(comp.Name)
	tier := lipgloss.NewStyle().Foreground(warning).Render("[" + comp.Tier + "]")
	s = append(s, name+"  "+tier)

	if comp.Doc != "" {
		s = append(s, docStyle.Render(wrapText(comp.Doc, width)))
	}

	if comp.Children != "none" {
		s = append(s, labelStyle.Render("Children: ")+comp.Children)
	}

	if len(comp.Props) > 0 {
		s = append(s, "", labelStyle.Render("Properties"))
		for _, p := range comp.Props {
			line := "  " + propNameStyle.Render(p.Name) + " " + propTypeStyle.Render(p.Type)
			if p.Doc != "" {
				line += "  " + docStyle.Render(p.Doc)
			}
			s = append(s, line)
		}
	}

	if len(comp.Events) > 0 {
		s = append(s, "", labelStyle.Render("Events"))
		for _, e := range comp.Events {
			line := "  " + propNameStyle.Render("@"+e.Name)
			if e.PayloadType != "" {
				line += " " + propTypeStyle.Render(e.PayloadType)
			}
			s = append(s, line)
		}
	}

	if comp.Example != "" {
		s = append(s, "", labelStyle.Render("Example"))
		for _, line := range strings.Split(comp.Example, "\n") {
			s = append(s, "  "+docStyle.Render(line))
		}
	}

	return s
}

func renderDeclDetail(d *declEntry, width int) []string {
	var s []string

	kind := kindStyle.Render(d.Kind)
	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fff")).Render(d.Name)
	s = append(s, kind+" "+name)

	if d.Doc != "" {
		s = append(s, docStyle.Render(wrapText(d.Doc, width)))
	}

	switch decl := d.Decl.(type) {
	case *ast.Component:
		if len(decl.Params) > 0 {
			s = append(s, "", labelStyle.Render("Parameters"))
			for _, p := range decl.Params {
				pType := p.Default.TypeHint
				if pType == "" {
					pType = "any"
				}
				line := "  " + propNameStyle.Render(p.Name) + " " + propTypeStyle.Render(pType)
				s = append(s, line)
			}
		}
		if len(decl.EventDecls) > 0 {
			s = append(s, "", labelStyle.Render("Events"))
			for _, e := range decl.EventDecls {
				line := "  " + propNameStyle.Render("@"+e.Name)
				if e.PayloadType != "" {
					line += " " + propTypeStyle.Render(e.PayloadType)
				}
				s = append(s, line)
			}
		}
		if decl.ChildrenType != "" {
			s = append(s, "", labelStyle.Render("Children: ")+decl.ChildrenType)
		}

	case *ast.StructDef:
		if len(decl.Fields) > 0 {
			s = append(s, "", labelStyle.Render("Fields"))
			for _, f := range decl.Fields {
				s = append(s, "  "+propNameStyle.Render(f.Name)+" "+propTypeStyle.Render(f.Type))
			}
		}

	case *ast.EnumDef:
		if len(decl.Values) > 0 {
			s = append(s, "", labelStyle.Render("Values"))
			for _, v := range decl.Values {
				s = append(s, "  "+propNameStyle.Render(v))
			}
		}

	case *ast.FuncDef:
		var sig strings.Builder
		sig.WriteString("func " + d.Name + "(")
		for i, p := range decl.Params {
			if i > 0 {
				sig.WriteString(", ")
			}
			sig.WriteString(p.Name)
			if p.Type != "" {
				sig.WriteString(" " + p.Type)
			}
		}
		sig.WriteString(")")
		if decl.ReturnType != "" {
			sig.WriteString(" " + decl.ReturnType)
		}
		s = append(s, "", propTypeStyle.Render(sig.String()))

	case *ast.Data:
		if decl.IsFunc {
			var sig strings.Builder
			sig.WriteString("func(")
			sig.WriteString(strings.Join(decl.ParamTypes, ", "))
			sig.WriteString(")")
			if decl.ReturnType != "" {
				sig.WriteString(" " + decl.ReturnType)
			}
			s = append(s, "", propTypeStyle.Render(sig.String()))
		} else if decl.Init.TypeHint != "" {
			s = append(s, "", labelStyle.Render("Type: ")+propTypeStyle.Render(decl.Init.TypeHint))
		}

	case *ast.Const:
		if decl.Init.TypeHint != "" {
			s = append(s, "", labelStyle.Render("Type: ")+propTypeStyle.Render(decl.Init.TypeHint))
		}
		if decl.Init.Literal != nil {
			s = append(s, labelStyle.Render("Value: ")+fmt.Sprint(decl.Init.Literal))
		}
	}

	return s
}

// --- Helpers ---

func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	var line string
	for _, w := range words {
		if line == "" {
			line = w
		} else if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func parseDir(dir string) (*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var doc *ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		d, err := parser.Parse(e.Name(), f)
		f.Close()
		if err != nil {
			continue
		}
		if doc == nil {
			doc = d
		} else {
			doc.Components = append(doc.Components, d.Components...)
			doc.Structs = append(doc.Structs, d.Structs...)
			doc.Enums = append(doc.Enums, d.Enums...)
			doc.Data = append(doc.Data, d.Data...)
			doc.Functions = append(doc.Functions, d.Functions...)
			doc.Consts = append(doc.Consts, d.Consts...)
			doc.Comments = append(doc.Comments, d.Comments...)
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("no .sngl files in %s", dir)
	}
	return doc, nil
}

func buildDeclItems(pd *checker.PackageDocs, doc *ast.Document) []navItem {
	var items []navItem

	add := func(section string, decls []checker.DeclInfo, kind string) {
		if len(decls) == 0 {
			return
		}
		items = append(items, navItem{section: section})
		for i := range decls {
			d := &decls[i]
			items = append(items, navItem{decl: &declEntry{
				Name:   d.Name,
				Kind:   kind,
				Doc:    d.Doc,
				Decl:   d.Decl,
				Source: doc,
			}})
		}
	}

	// Separate user components from platform overrides
	var userComps, overrides []checker.DeclInfo
	for _, d := range pd.Components {
		if strings.HasPrefix(d.Name, "sngl.") {
			overrides = append(overrides, d)
		} else {
			userComps = append(userComps, d)
		}
	}

	add("Components", userComps, "component")
	add("Structs", pd.Structs, "struct")
	add("Enums", pd.Enums, "enum")
	add("Functions", pd.Functions, "func")
	add("Data", pd.Data, "var")
	add("Constants", pd.Consts, "const")
	if len(overrides) > 0 {
		add("Platform Overrides", overrides, "component")
	}

	return items
}

func buildStdlibItems() []navItem {
	tiers := docs.ComponentsByTier()
	var items []navItem
	for _, t := range tiers {
		items = append(items, navItem{section: t.Name})
		for i := range t.Components {
			c := t.Components[i]
			items = append(items, navItem{component: &c})
		}
	}
	return items
}

func firstSelectable(items []navItem) int {
	for i, item := range items {
		if !item.isHeader() {
			return i
		}
	}
	return 0
}

// --- Public API ---

// Run launches the doc browser with stdlib only.
func Run() error {
	return RunWithDir("")
}

// RunWithDir launches the doc browser. If dir is non-empty and contains .sngl
// files, those declarations are shown first, followed by stdlib components.
func RunWithDir(dir string) error {
	var items []navItem
	title := "SNGL Stdlib"

	if dir != "" {
		if doc, err := parseDir(dir); err == nil {
			pd := checker.ExtractPackageDocs(doc)
			items = buildDeclItems(pd, doc)
			abs, _ := filepath.Abs(dir)
			title = filepath.Base(abs)
			// Add stdlib as a separate section
			items = append(items, navItem{section: "─── Stdlib ───"})
			items = append(items, buildStdlibItems()...)
		}
	}

	if len(items) == 0 {
		items = buildStdlibItems()
	}

	m := browser{
		Model:     New(),
		title:     title,
		flatItems: items,
	}
	m.selectedIndex = firstSelectable(items)

	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
