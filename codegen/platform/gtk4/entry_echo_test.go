//go:build !js

package gtk4

import (
	"strings"
	"testing"
)

// The input lives in a runtime instance, whose changed handler writes the
// bound value back to the entry; the root model's does not. The canvas keeps
// the file on the cgo path.
const entryEchoSrc = `
import draw "sngl:ui/draw"
import ui "sngl:ui"

component Search ui.node {
    var search = ""

    ui.vbox {
        ui.input #box(:value=search)
        ui.text(value=search)
    }
}

ui.window {
    ui.vbox {
        draw.canvas(width=10px, height=10px) {
            draw.circle(cx=5.0, cy=5.0, r=2.0)
        }
        for var i = [1] {
            Search()
        }
    }
}
`

const entryEchoProbe = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import "unsafe"

func probeInit() { C.gtk_init() }

func find(w *C.GtkWidget, typ string) *C.GtkWidget {
	for c := C.gtk_widget_get_first_child(w); c != nil; c = C.gtk_widget_get_next_sibling(c) {
		if C.GoString(C.g_type_name_from_instance((*C.GTypeInstance)(unsafe.Pointer(c)))) == typ {
			return c
		}
		if f := find(c, typ); f != nil {
			return f
		}
	}
	return nil
}

func typeInto(root unsafe.Pointer, text string) {
	t := C.CString(text)
	defer C.free(unsafe.Pointer(t))
	pos := C.int(-1)
	C.gtk_editable_insert_text((*C.GtkEditable)(unsafe.Pointer(find((*C.GtkWidget)(root), "GtkEntry"))), t, -1, &pos)
}

func labelText(root unsafe.Pointer) string {
	return C.GoString(C.gtk_label_get_text((*C.GtkLabel)(unsafe.Pointer(find((*C.GtkWidget)(root), "GtkLabel")))))
}
`

const entryEchoDriver = `package main

import (
	"testing"
	"unsafe"
)

func TestTypingUpdatesOnce(t *testing.T) {
	probeInit()
	m := New()
	m.buildWidgetTree()
	typeInto(unsafe.Pointer(m.__root), "a")
	if got := labelText(unsafe.Pointer(m.__root)); got != "a" {
		t.Fatalf("label reads %q, want %q", got, "a")
	}
}
`

// gtk_editable_set_text deletes and reinserts, firing "changed" for each, so
// an entry's changed handler that writes its text back through it re-entered
// itself until the stack overflowed.
func TestTypingIntoATwoWayEntryDoesNotRecurse(t *testing.T) {
	withCgo(t)
	files := goFiles(generateGTK4FilesBuilt(t, entryEchoSrc))
	if !strings.Contains(files["model.go"], "C.gtk_entry_new") {
		t.Fatalf("expected the cgo path:\n%s", files["model.go"])
	}
	files["entry.go"] = "package main\n\nfunc main() {}\n"
	files["probe.go"] = entryEchoProbe
	files["echo_test.go"] = entryEchoDriver
	if out, ok := runGo(t, "gtk4-entry-echo-", files, true, "test", "-count=1", "."); !ok {
		t.Errorf("%s\n--- model.go ---\n%s", out, files["model.go"])
	}
}
