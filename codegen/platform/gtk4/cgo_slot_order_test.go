//go:build !js

package gtk4

import (
	"strings"
	"testing"
)

const cgoSlotOrderSrc = `
import . "sngl:ui"

var on = true

window {
    vbox {
        if on {
            text(value="first")
        }
        text(value="second")
        card {
            if on {
                text(value="carded")
            }
        }
        scroll {
            if on {
                text(value="scrolled")
            }
        }
        toggle(checked=on)
        button #flip(text="flip", @click { on = !on })
    }
}
`

const cgoSlotOrderProbe = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import "unsafe"

func probeInit() { C.gtk_init() }

func labelTexts(w unsafe.Pointer, out *[]string) {
	for c := C.gtk_widget_get_first_child((*C.GtkWidget)(w)); c != nil; c = C.gtk_widget_get_next_sibling(c) {
		if C.gtk_widget_get_visible(c) == 0 {
			continue
		}
		switch C.GoString(C.g_type_name_from_instance((*C.GTypeInstance)(unsafe.Pointer(c)))) {
		case "GtkLabel":
			*out = append(*out, C.GoString(C.gtk_label_get_text((*C.GtkLabel)(unsafe.Pointer(c)))))
		case "GtkButton":
		default:
			labelTexts(unsafe.Pointer(c), out)
		}
	}
}
`

const cgoSlotOrderDriver = `package main

import (
	"strings"
	"testing"
	"unsafe"
)

func TestASlotReRendersInPlace(t *testing.T) {
	probeInit()
	m := New()
	m.buildWidgetTree()
	m.flipClick()
	m.flipClick()
	var got []string
	labelTexts(unsafe.Pointer(m.__root), &got)
	if want := "first,second,carded,scrolled"; strings.Join(got, ",") != want {
		t.Fatalf("rendered %q, want %q", strings.Join(got, ","), want)
	}
}
`

// A render slot on the cgo path re-renders before its anchor, as it does
// wrapped, rather than after every sibling written below it.
func TestCgoSlotReRendersInPlace(t *testing.T) {
	withCgo(t)
	files := goFiles(generateGTK4FilesBuilt(t, cgoSlotOrderSrc))
	// The widgets are C pointers on the cgo path. gtk4rt is still reached
	// for its own primitives -- the scroll is gtk4rt's -- as it is for an
	// image, so its name alone does not say which path the file took.
	if !strings.Contains(files["model.go"], "*C.GtkBox") {
		t.Fatalf("expected the cgo path:\n%s", files["model.go"])
	}
	files["entry.go"] = "package main\n\nfunc main() {}\n"
	files["probe.go"] = cgoSlotOrderProbe
	files["order_test.go"] = cgoSlotOrderDriver
	if out, ok := runGo(t, "gtk4-cgo-slot-order-", files, true, "test", "-count=1", "."); !ok {
		t.Errorf("%s\n--- model.go ---\n%s", out, files["model.go"])
	}
}
