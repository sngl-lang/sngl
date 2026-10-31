package fynelayout

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Tabs is a `ui.tabs` on fyne: a widget drawing an AppTabs, Bar, with an item
// per TabPage its Container holds. That box is drawn nowhere: a render slot
// inserts and removes pages there as it does any node, and its layout, run as
// they move, makes the AppTabs' items theirs.
type Tabs struct {
	widget.BaseWidget
	Container *fyne.Container
	Bar       *container.AppTabs
	// selected is the program's, which the AppTabs shows.
	selected int
	// quiet is set while this package changes the item shown, which the
	// AppTabs reports as it would the user's.
	quiet bool
}

func NewTabs() *Tabs {
	t := &Tabs{Bar: container.NewAppTabs()}
	t.Container = container.New(&tabsLayout{t})
	t.Bar.OnSelected = t.chosen
	t.ExtendBaseWidget(t)
	return t
}

func (t *Tabs) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.Bar) }

func (t *Tabs) Add(o fyne.CanvasObject) { t.Container.Add(o) }

// SetSelected is the page shown, by its index among the pages.
func (t *Tabs) SetSelected(k int) {
	t.selected = k
	t.show()
}

type tabsLayout struct{ t *Tabs }

func (l *tabsLayout) Layout([]fyne.CanvasObject, fyne.Size) { l.t.sync() }

func (l *tabsLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (t *Tabs) pages() []*TabPage {
	var out []*TabPage
	for _, o := range t.Container.Objects {
		if p, ok := o.(*TabPage); ok {
			out = append(out, p)
		}
	}
	return out
}

func (t *Tabs) sync() {
	pages := t.pages()
	items := make([]*container.TabItem, len(pages))
	for i, p := range pages {
		items[i] = p.item
		p.host = t
	}
	if !slices.Equal(items, t.Bar.Items) {
		t.quiet = true
		t.Bar.SetItems(items)
		t.quiet = false
	}
	for i, p := range pages {
		switch {
		case p.disabled && !p.item.Disabled():
			t.Bar.DisableIndex(i)
		case !p.disabled && p.item.Disabled():
			t.Bar.EnableIndex(i)
		}
	}
	t.show()
}

func (t *Tabs) show() {
	if t.selected < 0 || t.selected >= len(t.Bar.Items) || t.Bar.SelectedIndex() == t.selected {
		return
	}
	t.quiet = true
	t.Bar.SelectIndex(t.selected)
	t.quiet = false
}

func (t *Tabs) chosen(item *container.TabItem) {
	if t.quiet {
		return
	}
	k := slices.Index(t.Bar.Items, item)
	if k < 0 || k == t.selected {
		return
	}
	t.selected = k
	if p := t.pages()[k]; p.OnChange != nil {
		p.OnChange()
	}
}

// TabPage is one tab of a Tabs: its panel, Box, which the AppTabs shows while
// it is selected, titled with its label. What the tabs' box holds is the page
// itself, a hidden rectangle. OnChange is its `@change`, the user choosing it.
type TabPage struct {
	fyne.CanvasObject
	Box      *fyne.Container
	OnChange func()
	item     *container.TabItem
	disabled bool
	host     *Tabs
}

func NewTabPage() *TabPage {
	r := canvas.NewRectangle(color.Transparent)
	r.Hide()
	p := &TabPage{CanvasObject: r, Box: NewVBox()}
	p.item = container.NewTabItem("", p.Box)
	return p
}

func (p *TabPage) Add(o fyne.CanvasObject) {
	Adopt(p.Box, o)
	p.Box.Add(o)
}

func (p *TabPage) SetTitle(title string) {
	p.item.Text = title
	if p.host != nil {
		p.host.Bar.Refresh()
	}
}

func (p *TabPage) SetDisabled(disabled bool) {
	p.disabled = disabled
	if p.host != nil {
		p.host.sync()
	}
}
