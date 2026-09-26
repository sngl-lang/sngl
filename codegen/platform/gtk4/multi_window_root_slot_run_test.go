//go:build !js

package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// multiWindowRootSlotSrc has two windows, each with a loop at the top of its
// body between static siblings.
const multiWindowRootSlotSrc = `
import ui "sngl:ui"

component Row(label string) ui.node {
    var n = 0
    ui.button(text="{label} {n}", @click { n += 1 })
}

var items = ["a", "b"]
var notes = ["x"]

ui.window #one(title="One") {
    ui.text(value="one head")
    for var it = items {
        Row(label=it)
    }
    ui.text(value="one foot")
    ui.button #more(text="more", @click {
        items.push("c")
        notes.push("z")
    })
}

ui.window #two(title="Two") {
    ui.text(value="two head")
    for var it = notes {
        ui.text(value=it)
    }
    ui.text(value="two foot")
    ui.button #note(text="note", @click { notes.push("y") })
}
`

const multiWindowProbe = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import (
	"slices"
	"strings"
	"unsafe"
)

func newApp() unsafe.Pointer {
	app := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, nil)
	return unsafe.Pointer(app)
}

// windowTexts reads each of app's windows as its title and the texts it
// shows, sorted, since GTK orders them by focus.
func windowTexts(app unsafe.Pointer) []string {
	var out []string
	for l := C.gtk_application_get_windows((*C.GtkApplication)(app)); l != nil; l = l.next {
		w := (*C.GtkWindow)(l.data)
		var got []string
		widgetTexts(unsafe.Pointer(C.gtk_window_get_child(w)), &got)
		out = append(out, C.GoString(C.gtk_window_get_title(w))+": "+strings.Join(got, ","))
	}
	slices.Sort(out)
	return out
}

func widgetTexts(w unsafe.Pointer, out *[]string) {
	for c := C.gtk_widget_get_first_child((*C.GtkWidget)(w)); c != nil; c = C.gtk_widget_get_next_sibling(c) {
		if C.gtk_widget_get_visible(c) == 0 {
			continue
		}
		switch C.GoString(C.g_type_name_from_instance((*C.GTypeInstance)(unsafe.Pointer(c)))) {
		case "GtkLabel":
			*out = append(*out, C.GoString(C.gtk_label_get_text((*C.GtkLabel)(unsafe.Pointer(c)))))
		case "GtkButton":
			*out = append(*out, C.GoString(C.gtk_button_get_label((*C.GtkButton)(unsafe.Pointer(c)))))
		default:
			widgetTexts(unsafe.Pointer(c), out)
		}
	}
}
`

const multiWindowBuildWrapped = `package main

import (
	"unsafe"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
)

func buildUI(m *Model, app unsafe.Pointer) { m.BuildUI(gtk4rt.Handle(app)) }
`

const multiWindowBuildCgo = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import "unsafe"

func buildUI(m *Model, app unsafe.Pointer) { m.BuildUI((*C.GtkApplication)(app)) }
`

const multiWindowDriver = `package main

import (
	"strings"
	"testing"
)

func TestEachWindowKeepsItsRows(t *testing.T) {
	app := newApp()
	m := New()
	buildUI(m, app)
	check := func(when, want string) {
		t.Helper()
		if got := strings.Join(windowTexts(app), " | "); got != want {
			t.Fatalf("%s: windows %q, want %q", when, got, want)
		}
	}
	check("built", "One: one head,a 0,b 0,one foot,more | Two: two head,x,two foot,note")
	m.moreClick()
	check("after pushing in one", "One: one head,a 0,b 0,c 0,one foot,more | Two: two head,x,z,two foot,note")
	m.noteClick()
	check("after pushing in two", "One: one head,a 0,b 0,c 0,one foot,more | Two: two head,x,z,y,two foot,note")
}
`

// Every window of a program is a window of its own, and each renders its
// top-level slot into its own root: its rows show between its own siblings,
// and a push reaches the window it belongs to. The toggle has no gtk4rt
// wrapper, which puts the second variant on the cgo path.
func TestMultiWindowRootSlotsRun(t *testing.T) {
	skipWithoutGIR(t)
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	for name, src := range map[string]string{
		"wrapped": multiWindowRootSlotSrc,
		"cgo":     strings.Replace(multiWindowRootSlotSrc, "    ui.text(value=\"two foot\")", "    ui.toggle(checked=true)\n    ui.text(value=\"two foot\")", 1),
	} {
		t.Run(name, func(t *testing.T) {
			files := generateGTK4FilesBuilt(t, src)
			if cgo := !strings.Contains(files["model.go"], "gtk4rt."); cgo != (name == "cgo") {
				t.Fatalf("expected the %s path:\n%s", name, files["model.go"])
			}
			tmp, err := os.MkdirTemp(".", "_gtk4-multi-window-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)
			files["entry.go"] = "package main\n\nfunc main() {}\n"
			files["probe.go"] = multiWindowProbe
			files["build.go"] = multiWindowBuildWrapped
			if name == "cgo" {
				files["build.go"] = multiWindowBuildCgo
			}
			files["windows_test.go"] = multiWindowDriver
			for name, src := range files {
				if filepath.Ext(name) != ".go" {
					continue
				}
				if err := os.WriteFile(filepath.Join(tmp, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("go", "test", "-count=1", ".")
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%v\n%s\n--- model.go ---\n%s", err, out, files["model.go"])
			}
		})
	}
}
