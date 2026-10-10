//go:build !js

package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
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
	"strings"
	"unsafe"

	"duckfam.us/sngl/pkg/go/gtk4rt"
)

func newApp() gtk4rt.Handle {
	app := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)
	C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, nil)
	return gtk4rt.Handle(unsafe.Pointer(app))
}

// shown is the texts a window's box shows, in order.
func shown(box gtk4rt.Handle) string {
	var got []string
	widgetTexts(unsafe.Pointer(box), &got)
	return strings.Join(got, ",")
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

const multiWindowDriver = `package main

import (
	"testing"

	"duckfam.us/sngl/pkg/go/gtk4rt"
)

func TestEachWindowKeepsItsRows(t *testing.T) {
	m := New()
	m.BuildUI(newApp())
	check := func(when string, box gtk4rt.Handle, want string) {
		t.Helper()
		if got := shown(box); got != want {
			t.Fatalf("%s: shows %q, want %q", when, got, want)
		}
	}
	check("one built", m.one, "one head,a 0,b 0,one foot,more")
	check("two built", m.two, "two head,x,two foot,note")
	m.moreClick()
	check("one, after pushing in one", m.one, "one head,a 0,b 0,c 0,one foot,more")
	check("two, written from one", m.two, "two head,x,z,two foot,note")
	m.noteClick()
	check("two, after pushing in two", m.two, "two head,x,z,y,two foot,note")
	check("one, untouched by two", m.one, "one head,a 0,b 0,c 0,one foot,more")
}
`

// Each window renders the loop at the top of its body into its own box, between
// its own siblings, and a push in either window reaches every window that
// renders what it wrote.
func TestMultiWindowRootSlotsRun(t *testing.T) {
	skipWithoutGIR(t)
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	files := generateGTK4FilesBuilt(t, multiWindowRootSlotSrc)
	tmp, err := os.MkdirTemp(".", "_gtk4-multi-window-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	files["entry.go"] = "package main\n\nfunc main() {}\n"
	files["probe.go"] = multiWindowProbe
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
}
