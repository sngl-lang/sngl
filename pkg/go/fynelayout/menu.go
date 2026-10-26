package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// MenuEntry is one entry of a window's main menu, sngl:ui's menuItem, holding
// the entries of its submenu as its children. It is drawn nowhere: it stands
// hidden among the window's content, where the box gives it no room, and the
// Toplevel builds its main menu from the entries it holds. A render slot
// inserts and removes entries as it does any node, and InsertBefore, Remove
// and Add tell the window; an entry's own children are a box whose layout is
// called as they move, and its setters say when it changed. OnSelect is its
// `@select`.
type MenuEntry struct {
	*fyne.Container
	label    string
	disabled bool
	checked  bool
	OnSelect func()
	// host is the window showing the entry, which builds its menu again.
	host *Toplevel
}

// NewMenuEntry makes an entry with no label.
func NewMenuEntry() *MenuEntry {
	e := &MenuEntry{}
	e.Container = container.New(&menuLayout{moved: e.changed})
	e.Container.Hide()
	return e
}

// menuLayout lays nothing out: it is called as an entry's children are added
// and removed, which is when the menu holding them is built again.
type menuLayout struct{ moved func() }

func (l *menuLayout) Layout([]fyne.CanvasObject, fyne.Size) { l.moved() }

func (l *menuLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

func (e *MenuEntry) entries() []*MenuEntry { return entriesIn(e.Container) }

func entriesIn(c *fyne.Container) []*MenuEntry {
	var out []*MenuEntry
	for _, o := range c.Objects {
		if e, ok := o.(*MenuEntry); ok {
			out = append(out, e)
		}
	}
	return out
}

func (e *MenuEntry) adopt(t *Toplevel) {
	e.host = t
	for _, k := range e.entries() {
		k.adopt(t)
	}
}

func (e *MenuEntry) changed() {
	if e.host != nil {
		e.host.applyMenu()
	}
}

// SetLabel sets what the entry shows.
func (e *MenuEntry) SetLabel(s string) {
	if e.label != s {
		e.label = s
		e.changed()
	}
}

// SetDisabled sets whether the entry can be picked.
func (e *MenuEntry) SetDisabled(v bool) {
	if e.disabled != v {
		e.disabled = v
		e.changed()
	}
}

// SetChecked sets whether the entry shows a check.
func (e *MenuEntry) SetChecked(v bool) {
	if e.checked != v {
		e.checked = v
		e.changed()
	}
}

// Select is the entry picked.
func (e *MenuEntry) Select() {
	if !e.disabled && e.OnSelect != nil {
		e.OnSelect()
	}
}

// toplevels is the window whose content each box is, so an entry a slot
// inserts there reaches the window's menu.
var toplevels = map[*fyne.Container]*Toplevel{}

// menuMoved tells the window whose content c is that an entry or a Back came
// or went.
func menuMoved(c *fyne.Container, o fyne.CanvasObject) {
	t := toplevels[c]
	if t == nil {
		return
	}
	switch o.(type) {
	case *MenuEntry:
		t.applyMenu()
	case *Back:
		t.applyBack()
	}
}

// Back is a window's way back, while it can go back: drawn nowhere where it
// is written, among the window's content, it has the window show a back
// button over the content, which runs its OnPress.
type Back struct {
	fyne.CanvasObject
	OnPress func()
}

// NewBack makes a way back.
func NewBack() *Back {
	return &Back{CanvasObject: Nothing()}
}

// Press is the back button pressed.
func (b *Back) Press() {
	if b.OnPress != nil {
		b.OnPress()
	}
}

// applyBack shows the back button while a Back is among the content, running
// the first one's handler.
func (t *Toplevel) applyBack() {
	var back *Back
	for _, o := range t.Container.Objects {
		if b, ok := o.(*Back); ok {
			back = b
			break
		}
	}
	if t.back == nil {
		if back == nil {
			return
		}
		t.back = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), nil)
		t.back.Importance = widget.LowImportance
		t.Container.Objects = append([]fyne.CanvasObject{container.NewHBox(t.back)}, t.Container.Objects...)
		t.Container.Refresh()
	}
	if back == nil {
		t.back.OnTapped = nil
		t.back.Hide()
		return
	}
	t.back.OnTapped = back.Press
	t.back.Show()
}
