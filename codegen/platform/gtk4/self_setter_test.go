//go:build !js

package gtk4

import (
	"strings"
	"testing"
)

// The button's click writes the state its own label reads. The canvas keeps
// the file on the cgo path.
const selfSetterSrc = `
import draw "sngl:ui/draw"
import ui "sngl:ui"

var n = 0

ui.window {
    ui.vbox {
        draw.canvas(width=10px, height=10px) {
            draw.circle(cx=5.0, cy=5.0, r=2.0)
        }
        ui.button(text="{n}", @click {
            n = n + 1
        })
    }
}
`

const selfSetterProbe = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

// gtk_widget_activate would show the press and emit clicked from a timeout.
static void click(GtkWidget *b) { g_signal_emit_by_name(b, "clicked"); }
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

func clickButton(root unsafe.Pointer) {
	C.click(find((*C.GtkWidget)(root), "GtkButton"))
}

func buttonLabel(root unsafe.Pointer) string {
	return C.GoString(C.gtk_button_get_label((*C.GtkButton)(unsafe.Pointer(find((*C.GtkWidget)(root), "GtkButton")))))
}
`

const selfSetterDriver = `package main

import (
	"testing"
	"unsafe"
)

func TestClickUpdatesOwnLabel(t *testing.T) {
	probeInit()
	m := New()
	m.buildWidgetTree()
	clickButton(unsafe.Pointer(m.__root))
	if got := buttonLabel(unsafe.Pointer(m.__root)); got != "1" {
		t.Fatalf("label reads %q, want %q", got, "1")
	}
}
`

// dropSelfSetterCalls used to drop every setter on a handler's own node, so
// the click counted and the label showed 0.
func TestAClickUpdatesTheButtonsOwnLabel(t *testing.T) {
	files := goFiles(generateGTK4FilesBuilt(t, selfSetterSrc))
	if !strings.Contains(files["model.go"], "import \"C\"") {
		t.Fatalf("expected the cgo path:\n%s", files["model.go"])
	}
	files["entry.go"] = "package main\n\nfunc main() {}\n"
	files["probe.go"] = selfSetterProbe
	files["self_test.go"] = selfSetterDriver
	if out, ok := runGo(t, "gtk4-self-setter-", files, true, "test", "-count=1", "."); !ok {
		t.Errorf("%s\n--- model.go ---\n%s", out, files["model.go"])
	}
}
