package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// The widgets that take focus on this platform: the Fyne widget each embeds,
// which a key reaches through it. fyne's driver hands a key to the focused
// widget and to no one else, so the window can hear a key before the widget
// does only if the widget asks it: each of these hands a key it is given to
// its window's `@key` first (key.go), and to the widget it embeds only when
// the window did not stop it. That is all they change: the embedded widget's
// own methods ask the embedded widget, so it is drawn, tapped and changed as
// it would be on its own.
//
// A button, check or select written `focusable=false` is one of these with
// noTab set, which Tab passes over. fyne's focus manager focuses no widget
// that reports itself disabled, whether moving focus with Tab or asked to by
// a tap, so it reports itself disabled to that manager. A button takes no
// focus when tapped in any case, and a check or a select tapped takes none
// here, so a click leaves the keys to the window.
//
// While a stopped Tab is held with nothing focused, every one of them reports
// itself disabled, so the focus manager the driver then asks to move focus
// finds nothing to move it to (holdTab).

// Button is a widget.Button whose keys its window hears first.
type Button struct {
	widget.Button
	noTab bool
}

// NewButton is widget.NewButton's Button.
func NewButton(label string, tapped func()) *Button {
	b := &Button{}
	b.Text, b.OnTapped = label, tapped
	b.ExtendBaseWidget(b)
	return b
}

// NewNoTabButton is a Button Tab passes over.
func NewNoTabButton(label string, tapped func()) *Button {
	b := NewButton(label, tapped)
	b.noTab = true
	return b
}

// Disabled is what the focus manager asks.
func (b *Button) Disabled() bool { return b.noTab || tabHeld || b.Button.Disabled() }

func (b *Button) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(b, ev, false) {
		b.Button.TypedKey(ev)
	}
}

func (b *Button) TypedRune(r rune) {
	if !typedRune(b, r, false) {
		b.Button.TypedRune(r)
	}
}

func (b *Button) TypedShortcut(s fyne.Shortcut) { typedShortcut(b, s, false, nil) }

func (b *Button) AcceptsTab() bool { return acceptsTab(b, false, false) }

// Check is a widget.Check whose keys its window hears first.
type Check struct {
	widget.Check
	noTab bool
}

// NewCheck is widget.NewCheck's Check.
func NewCheck(label string, changed func(bool)) *Check {
	c := &Check{}
	c.Text, c.OnChanged = label, changed
	c.ExtendBaseWidget(c)
	return c
}

// NewNoTabCheck is a Check Tab passes over.
func NewNoTabCheck(label string, changed func(bool)) *Check {
	c := NewCheck(label, changed)
	c.noTab = true
	return c
}

// Disabled is what the focus manager asks.
func (c *Check) Disabled() bool { return c.noTab || tabHeld || c.Check.Disabled() }

func (c *Check) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(c, ev, false) {
		c.Check.TypedKey(ev)
	}
}

func (c *Check) TypedRune(r rune) {
	if !typedRune(c, r, false) {
		c.Check.TypedRune(r)
	}
}

func (c *Check) TypedShortcut(s fyne.Shortcut) { typedShortcut(c, s, false, nil) }

func (c *Check) AcceptsTab() bool { return acceptsTab(c, false, false) }

// Select is a widget.Select whose keys its window hears first.
type Select struct {
	widget.Select
	noTab bool
}

// NewSelect is widget.NewSelect's Select.
func NewSelect(options []string, changed func(string)) *Select {
	s := &Select{}
	s.Options, s.OnChanged = options, changed
	s.ExtendBaseWidget(s)
	return s
}

// NewNoTabSelect is a Select Tab passes over.
func NewNoTabSelect(options []string, changed func(string)) *Select {
	s := NewSelect(options, changed)
	s.noTab = true
	return s
}

// Disabled is what the focus manager asks.
func (s *Select) Disabled() bool { return s.noTab || tabHeld || s.Select.Disabled() }

func (s *Select) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(s, ev, false) {
		s.Select.TypedKey(ev)
	}
}

func (s *Select) TypedRune(r rune) {
	if !typedRune(s, r, false) {
		s.Select.TypedRune(r)
	}
}

func (s *Select) TypedShortcut(sc fyne.Shortcut) { typedShortcut(s, sc, false, nil) }

func (s *Select) AcceptsTab() bool { return acceptsTab(s, false, false) }

// Hyperlink is a widget.Hyperlink whose keys its window hears first.
type Hyperlink struct {
	widget.Hyperlink
}

// NewHyperlink is widget.NewHyperlink's Hyperlink, with no URL.
func NewHyperlink(text string) *Hyperlink {
	h := &Hyperlink{}
	h.Text = text
	h.ExtendBaseWidget(h)
	return h
}

// Disabled is what the focus manager asks.
func (h *Hyperlink) Disabled() bool { return tabHeld }

func (h *Hyperlink) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(h, ev, false) {
		h.Hyperlink.TypedKey(ev)
	}
}

func (h *Hyperlink) TypedRune(r rune) {
	if !typedRune(h, r, false) {
		h.Hyperlink.TypedRune(r)
	}
}

func (h *Hyperlink) TypedShortcut(s fyne.Shortcut) { typedShortcut(h, s, false, nil) }

func (h *Hyperlink) AcceptsTab() bool { return acceptsTab(h, false, false) }

// Entry is a widget.Entry, single-line or multiline, whose keys its window
// hears first, as keys pressed while editing. A field keeps its focus when
// written `focusable=false`, since a disabled report refuses its paste.
type Entry struct {
	widget.Entry
}

// NewEntry is widget.NewEntry's Entry.
func NewEntry() *Entry {
	e := &Entry{}
	e.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	e.ExtendBaseWidget(e)
	return e
}

// NewMultiLineEntry is widget.NewMultiLineEntry's Entry.
func NewMultiLineEntry() *Entry {
	e := NewEntry()
	e.MultiLine = true
	return e
}

// Disabled is what the focus manager asks, and the driver before it hands a
// disabled field any shortcut but Copy.
func (e *Entry) Disabled() bool { return tabHeld || e.Entry.Disabled() }

func (e *Entry) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(e, ev, true) {
		e.Entry.TypedKey(ev)
	}
}

func (e *Entry) TypedRune(r rune) {
	if !typedRune(e, r, true) {
		e.Entry.TypedRune(r)
	}
}

func (e *Entry) TypedShortcut(s fyne.Shortcut) { typedShortcut(e, s, true, e.Entry.TypedShortcut) }

func (e *Entry) AcceptsTab() bool { return acceptsTab(e, true, e.Entry.AcceptsTab()) }

// DateEntry is a widget.DateEntry whose keys its window hears first, as keys
// pressed while editing.
type DateEntry struct {
	widget.DateEntry
}

// NewDateEntry is widget.NewDateEntry's DateEntry.
func NewDateEntry() *DateEntry {
	d := &DateEntry{}
	d.ExtendBaseWidget(d)
	d.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	return d
}

// Disabled is what the focus manager asks.
func (d *DateEntry) Disabled() bool { return tabHeld || d.DateEntry.Disabled() }

func (d *DateEntry) TypedKey(ev *fyne.KeyEvent) {
	if !typedKey(d, ev, true) {
		d.DateEntry.TypedKey(ev)
	}
}

func (d *DateEntry) TypedRune(r rune) {
	if !typedRune(d, r, true) {
		d.DateEntry.TypedRune(r)
	}
}

func (d *DateEntry) TypedShortcut(s fyne.Shortcut) {
	typedShortcut(d, s, true, d.DateEntry.TypedShortcut)
}

func (d *DateEntry) AcceptsTab() bool { return acceptsTab(d, true, d.DateEntry.AcceptsTab()) }

// The interfaces the driver asks a focused widget for.
type keyed interface {
	fyne.Focusable
	fyne.Shortcutable
	fyne.Tabbable
	Disabled() bool
}

var (
	_ keyed = (*Button)(nil)
	_ keyed = (*Check)(nil)
	_ keyed = (*Select)(nil)
	_ keyed = (*Hyperlink)(nil)
	_ keyed = (*Entry)(nil)
	_ keyed = (*DateEntry)(nil)
)
