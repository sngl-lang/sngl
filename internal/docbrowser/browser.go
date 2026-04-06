// Package docbrowser provides an embedded TUI documentation browser.
package docbrowser

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/docs"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	tierStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	propStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

// Model is the bubbletea model for the doc browser.
type Model struct {
	components []docs.Component
	selected   int
	scroll     int
	width      int
	height     int
}

// New creates a doc browser model.
func New() Model {
	return Model{
		components: docs.Components(),
	}
}

// Run launches the TUI doc browser.
func Run() error {
	p := tea.NewProgram(New())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch {
		case msg.Code == 'q' || (msg.Code == 'c' && msg.Mod == tea.ModCtrl):
			return m, tea.Quit
		case msg.Code == 'j' || msg.Code == tea.KeyDown:
			if m.selected < len(m.components)-1 {
				m.selected++
				if m.selected-m.scroll >= m.height-4 {
					m.scroll++
				}
			}
		case msg.Code == 'k' || msg.Code == tea.KeyUp:
			if m.selected > 0 {
				m.selected--
				if m.selected < m.scroll {
					m.scroll--
				}
			}
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	if len(m.components) == 0 {
		v := tea.NewView("No components found")
		v.AltScreen = true
		return v
	}

	sidebarWidth := 28
	if m.width > 100 {
		sidebarWidth = 32
	}

	// Sidebar
	var sidebar strings.Builder
	sidebar.WriteString(titleStyle.Render("SNGL Components") + "\n\n")

	currentTier := ""
	for i, comp := range m.components {
		if i < m.scroll {
			continue
		}
		if comp.Tier != currentTier {
			currentTier = comp.Tier
			sidebar.WriteString(tierStyle.Render(currentTier) + "\n")
		}
		if i == m.selected {
			sidebar.WriteString(selStyle.Render("> "+comp.Name) + "\n")
		} else {
			sidebar.WriteString("  " + comp.Name + "\n")
		}
	}

	// Detail pane
	comp := m.components[m.selected]
	var detail strings.Builder

	detail.WriteString(titleStyle.Render(comp.Name) + "\n")
	detail.WriteString(dimStyle.Render("["+comp.Tier+"]") + "\n\n")

	doc := comp.Doc
	if idx := strings.Index(doc, " Example:"); idx > 0 {
		doc = strings.TrimSpace(doc[:idx])
	}
	if doc != "" {
		detail.WriteString(doc + "\n\n")
	}

	if len(comp.Props) > 0 {
		detail.WriteString(titleStyle.Render("Properties") + "\n")
		for _, p := range comp.Props {
			detail.WriteString(propStyle.Render(fmt.Sprintf("  %-16s", p.Name)))
			detail.WriteString(dimStyle.Render(p.Type))
			if p.Doc != "" {
				detail.WriteString("  " + p.Doc)
			}
			detail.WriteString("\n")
		}
		detail.WriteString("\n")
	}

	if len(comp.Events) > 0 {
		detail.WriteString(titleStyle.Render("Events") + "\n")
		for _, e := range comp.Events {
			detail.WriteString(propStyle.Render(fmt.Sprintf("  %-16s", e.Name)))
			detail.WriteString(dimStyle.Render(e.PayloadType) + "\n")
		}
		detail.WriteString("\n")
	}

	detail.WriteString(dimStyle.Render("Children: "+comp.Children) + "\n")

	if comp.Example != "" {
		detail.WriteString("\n" + titleStyle.Render("Example") + "\n")
		detail.WriteString(dimStyle.Render(comp.Example) + "\n")
	}

	// Layout
	sidebarStr := lipgloss.NewStyle().Width(sidebarWidth).Render(sidebar.String())
	detailStr := lipgloss.NewStyle().PaddingLeft(2).Render(detail.String())

	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebarStr, detailStr)
	footer := dimStyle.Render("↑/↓ navigate  q quit")

	v := tea.NewView(content + "\n\n" + footer)
	v.AltScreen = true
	return v
}
