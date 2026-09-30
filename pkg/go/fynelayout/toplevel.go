package fynelayout

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// Toplevel is what a `ui.window` is on fyne: a node holding its content in a
// box, which the program attaches to the application (App) like any node to
// its parent. Attaching one creates the fyne.Window showing the box, and puts
// it on screen when it is visible; detaching one closes that window.
//
// So the window lives exactly as long as the node does in the tree: a window
// under `if details` is created when the condition turns true and closed,
// content and all, when it turns false. Hiding without closing is `visible`.
type Toplevel struct {
	*fyne.Container
	title   string
	visible bool
	w       fyne.Window
	// attached is whether the node is the application's child.
	attached bool
	// OnVisible is where the window reports a change of `visible` it made
	// itself: the window manager's close, or a start with nothing on screen.
	OnVisible func(bool)
	// OnClosed is the window's `@closed`, run after a window manager's close
	// has been reported as `visible = false`.
	OnClosed func()
}

// App is the parent a node at the root of the package body is attached to.
// It is no container a window shows: InsertBefore, SlotAnchor and Remove
// answer for the application when they are handed it.
var App = container.NewWithoutLayout()

var (
	app fyne.App
	// started is whether the loop is running: a window attached before then
	// is created when it starts.
	started bool
	// hidden is a start with no window on screen -- `@run` never called
	// `run` -- which a window's close does not end.
	hidden   bool
	attached []*Toplevel
	// anchor is a window never shown. fyne's driver quits when its last
	// window closes, and a window the tree destroys, or a start with none on
	// screen, is not the program ending.
	anchor fyne.Window
)

// NewToplevel makes a toplevel and its content box. The window is made when
// the node is attached to the application and the loop is running.
func NewToplevel() *Toplevel {
	return &Toplevel{Container: container.NewVBox(), visible: true}
}

// SetTitle sets the title the window shows.
func (t *Toplevel) SetTitle(s string) {
	t.title = s
	if t.w != nil {
		t.w.SetTitle(s)
	}
}

// SetVisible shows or hides the window. The program writing the value the
// window already has -- its own report coming back through the binding --
// changes nothing.
func (t *Toplevel) SetVisible(v bool) {
	if t.visible == v {
		return
	}
	t.visible = v
	if t.w == nil {
		return
	}
	if v {
		t.w.Show()
	} else {
		t.w.Hide()
	}
}

// Visible is the value SetVisible last wrote or the window last reported.
func (t *Toplevel) Visible() bool { return t.visible }

// RequestClose closes the window the way the window manager's close button
// does.
func (t *Toplevel) RequestClose() {
	if t.w != nil {
		t.closeRequested()
	}
}

func (t *Toplevel) create() {
	if t.w != nil {
		return
	}
	t.w = app.NewWindow(t.title)
	// fyne lays SetContent's tree out against the current size, so the
	// content goes in before the resize.
	t.w.SetContent(t.Container)
	t.w.Resize(fyne.NewSize(480, 640))
	t.w.SetCloseIntercept(t.closeRequested)
}

func (t *Toplevel) report(v bool) {
	if t.visible == v {
		return
	}
	t.visible = v
	if t.OnVisible != nil {
		t.OnVisible(v)
	}
}

// closeRequested is the window manager's close. It is reported as the window
// going off screen, and then as `@closed`; what the handlers do decides what
// exists. A close that leaves nothing on screen ends the program, unless it
// started with nothing on screen.
func (t *Toplevel) closeRequested() {
	if t.w != nil {
		t.w.Hide()
	}
	t.report(false)
	if t.OnClosed != nil {
		t.OnClosed()
	}
	if hidden {
		return
	}
	for _, x := range attached {
		if x.w != nil && x.visible {
			return
		}
	}
	app.Quit()
}

// AppAttach makes o, a Toplevel, a child of the application. Before the loop
// starts it is created when the loop does; after, it is created at once, and
// put on screen when it is visible.
func AppAttach(o fyne.CanvasObject) {
	t, ok := o.(*Toplevel)
	if !ok || t.attached {
		return
	}
	t.attached = true
	attached = append(attached, t)
	if started {
		t.create()
		if t.visible {
			t.w.Show()
		}
	}
}

// AppDetach takes o out of the application, and closes its window.
func AppDetach(o fyne.CanvasObject) {
	t, ok := o.(*Toplevel)
	if !ok || !t.attached {
		return
	}
	t.attached = false
	attached = slices.DeleteFunc(attached, func(x *Toplevel) bool { return x == t })
	if t.w != nil {
		w := t.w
		t.w = nil
		w.SetContent(container.NewStack())
		w.Close()
	}
}

// Remove takes o out of c, which a render slot does to each entry before it
// renders again: detaching a toplevel when c is the application.
func Remove(c *fyne.Container, o fyne.CanvasObject) {
	if c == App {
		AppDetach(o)
		return
	}
	c.Remove(o)
}

// RunWindows runs the loop over the toplevels attached so far and those
// attached later. show puts the visible ones on screen at start; without it
// the loop runs with nothing on screen, each window reporting itself hidden,
// until one is opened or attached.
func RunWindows(a fyne.App, show bool) {
	app = a
	hidden = !show
	anchor = app.NewWindow("")
	started = true
	for _, t := range slices.Clone(attached) {
		t.create()
		switch {
		case show && t.visible:
			t.w.Show()
		case !show:
			t.report(false)
		}
	}
	app.Run()
}
