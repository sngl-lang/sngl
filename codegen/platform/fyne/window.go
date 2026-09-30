package fyne

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `open` and `close` show and hide the snglWindow the Model keeps
// under the window's `#id`: the receiver is the handle, which the Go context
// already spells as that field.
func init() {
	codegen.RegisterPlatformIntrinsic("fyne", codegen.WindowOpenIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Open()", nil
	})
	// A window that comes and goes with a condition is mounted and unmounted
	// by the effect passWindowLifetimes leaves in its place.
	codegen.RegisterPlatformIntrinsic("fyne", lower.WindowMountIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Mount()", nil
	})
	codegen.RegisterPlatformIntrinsic("fyne", lower.WindowUnmountIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Unmount()", nil
	})
	codegen.RegisterPlatformIntrinsic("fyne", codegen.WindowCloseIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Close()", nil
	})
}

// windowCloseMethod is the Model method a window's `@close` becomes.
func windowCloseMethod(field string) string {
	return "__" + strings.TrimPrefix(field, "__") + "_close"
}

// emitIRWindowsCode is BuildUI for a program holding window records: each
// window's content is built by its own method and wrapped in the snglWindow
// that shows it, and every window is mounted before the first settles run.
// BuildUI returns the first window's content, which is what a test or a
// snapshot takes.
func emitIRWindowsCode(b *strings.Builder, wins []codegen.HostWindow, windowCodes []string, mounts *strings.Builder, gc *golang.GoIRContext) {
	b.WriteString(windowRuntime)
	b.WriteString("// BuildUI creates every window's widget tree. Call once.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")
	fmt.Fprintf(b, "\tif m.%s != nil {\n\t\treturn m.%s.content\n\t}\n", wins[0].Field, wins[0].Field)
	for _, hw := range wins {
		fmt.Fprintf(b, "\tm.%s = &snglWindow{", hw.Field)
		if t := hw.Window.Prop(ir.WindowTitle); t != nil {
			fmt.Fprintf(b, "title: %s, ", gc.EvalExpr(t))
		}
		fmt.Fprintf(b, "content: m.%s()", windowBuildFunc(hw.Field))
		if hw.Close != nil {
			fmt.Fprintf(b, ", onClose: m.%s", windowCloseMethod(hw.Field))
		}
		b.WriteString("}\n")
		if !hw.Lifetime {
			fmt.Fprintf(b, "\tm.%s.Mount()\n", hw.Field)
		}
	}
	b.WriteString(mounts.String())
	fmt.Fprintf(b, "\treturn m.%s.content\n", wins[0].Field)
	b.WriteString("}\n\n")
	for _, code := range windowCodes {
		b.WriteString(code)
	}
}

// emitIRWindowsMain is the entry point of a program holding window records.
// The tree is built and every window mounted first -- the first settle
// included -- and snglRunWindows then creates the fyne windows. `run` is what
// puts them on screen: a program that wrote `@run` and never called it runs
// the loop with none shown.
func emitIRWindowsMain(b *strings.Builder, pkg *ir.Package) {
	b.WriteString("func main() {\n")
	b.WriteString("\tsnglApp = app.New()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.BuildUI()\n")
	if pkg.RemoteSettle != nil {
		// Through DoAndWait because a settle arrives on the fetch's goroutine
		// and Fyne's widgets belong to the main one.
		fmt.Fprintf(b, "\tremote.Default.OnSettle(func() { fyne.DoAndWait(m.%s) })\n", pkg.RemoteSettle.Name)
	}
	switch {
	case pkg.Run == nil:
		b.WriteString("\tsnglRunWindows(true)\n")
	case len(pkg.Run.Block) == 0:
		// An empty handler is emitted as no function at all, and never calls
		// run.
		b.WriteString("\tsnglRunWindows(false)\n")
	default:
		b.WriteString("\tran := false\n")
		fmt.Fprintf(b, "\t%s(os.Args[1:], func() {\n", golang.ModelCallee(pkg, pkg.Run, "m"))
		b.WriteString("\t\tran = true\n")
		b.WriteString("\t\tsnglRunWindows(true)\n")
		b.WriteString("\t})\n")
		b.WriteString("\tif !ran {\n")
		b.WriteString("\t\tsnglRunWindows(false)\n")
		b.WriteString("\t}\n")
	}
	if pkg.Teardown != nil {
		fmt.Fprintf(b, "\tm.%s()\n", pkg.Teardown.Name)
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}

// windowRuntime is the window record a program holding several windows
// keeps, written into the program rather than a runtime package because it
// is a dozen lines over fyne's own API.
//
// The content outlives the fyne window around it: Mount creates a window
// showing it and Unmount closes that window, while the content keeps
// receiving the program's updates in between. fyne's driver quits when its
// last window is destroyed, so a window that is never shown keeps it running
// through a moment with none -- which a window the tree destroys, or a start
// with nothing on screen, is.
const windowRuntime = `// snglWindow is one window of the program: the fyne window the host shows,
// around a widget tree the program builds once.
type snglWindow struct {
	title   string
	content fyne.CanvasObject
	onClose func()
	w       fyne.Window
	mounted bool
	open    bool
}

var (
	snglApp     fyne.App
	snglStarted bool
	snglHidden  bool
	snglWindows []*snglWindow
	snglAnchor  fyne.Window
)

// Mount says the window exists. Before the loop starts it is created when the
// loop does; after, it is created and put on screen at once.
func (w *snglWindow) Mount() {
	if w.mounted {
		return
	}
	w.mounted = true
	snglWindows = append(snglWindows, w)
	if snglStarted {
		w.create()
		w.Open()
	}
}

// Unmount closes the fyne window. The content is kept for a later Mount.
func (w *snglWindow) Unmount() {
	if !w.mounted {
		return
	}
	w.mounted = false
	w.open = false
	for i, x := range snglWindows {
		if x == w {
			snglWindows = append(snglWindows[:i], snglWindows[i+1:]...)
			break
		}
	}
	if w.w != nil {
		w.w.SetContent(container.NewStack())
		w.w.Close()
		w.w = nil
	}
}

// Open puts a mounted window on screen.
func (w *snglWindow) Open() {
	w.open = true
	if w.w != nil {
		w.w.Show()
	}
}

// Close takes the window off screen; Open brings it back as it was.
func (w *snglWindow) Close() {
	w.open = false
	if w.w != nil {
		w.w.Hide()
	}
}

func (w *snglWindow) create() {
	if w.w != nil {
		return
	}
	w.w = snglApp.NewWindow(w.title)
	// fyne lays SetContent's tree out against the current size, so the
	// content goes in before the resize.
	w.w.SetContent(w.content)
	w.w.Resize(fyne.NewSize(480, 640))
	w.w.SetCloseIntercept(w.closeRequested)
}

// closeRequested is the window manager's close. The handler decides what it
// means; with none, the window hides. Either way a close that leaves nothing
// on screen ends the program, unless it started with nothing on screen.
func (w *snglWindow) closeRequested() {
	if w.onClose != nil {
		w.onClose()
	} else {
		w.Close()
	}
	if snglHidden {
		return
	}
	for _, x := range snglWindows {
		if x.open {
			return
		}
	}
	snglApp.Quit()
}

// snglRunWindows runs the loop over the windows mounted so far and those
// mounted later. show puts the mounted ones on screen at start.
func snglRunWindows(show bool) {
	snglHidden = !show
	snglAnchor = snglApp.NewWindow("")
	snglStarted = true
	for _, w := range append([]*snglWindow(nil), snglWindows...) {
		w.create()
		if show || w.open {
			w.Open()
		}
	}
	snglApp.Run()
}

`
