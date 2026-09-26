//go:build !js

package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rootSlotOrderSrc renders loops at the top of a window body and at the top of
// a component body built at run time, each between static siblings.
const rootSlotOrderSrc = `
import ui "sngl:ui"

component Row(label string) ui.node {
    var n = 0
    ui.button(text="{label} {n}", @click { n += 1 })
}

component Card(xs list<string>) ui.node {
    var open = true
    ui.text(value="[")
    for var x = xs {
        ui.text(value=x)
    }
    ui.text(value="|")
    if open {
        ui.text(value="open")
    }
    ui.text(value="]")
}

ui.window {
    var items = ["a", "b"]
    ui.text(value="header")
    for var it = items {
        Row(label=it)
    }
    ui.text(value="mid")
    for var it = items {
        Card(xs=[it])
    }
    ui.text(value="footer")
    ui.button #more(text="more", @click { items.push("c") })
}
`

const rootSlotOrderProbe = `package main

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
*/
import "C"

import "unsafe"

func probeInit() { C.gtk_init() }

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

const rootSlotOrderDriver = `package main

import (
	"strings"
	"testing"
	"unsafe"
)

func rendered(m *Model) string {
	var got []string
	widgetTexts(unsafe.Pointer(m.__root), &got)
	return strings.Join(got, ",")
}

func TestRootSlotsKeepTheirPlace(t *testing.T) {
	probeInit()
	m := New()
	m.buildWidgetTree()
	if want := "header,a 0,b 0,mid,[,a,|,open,],[,b,|,open,],footer,more"; rendered(m) != want {
		t.Fatalf("built %q, want %q", rendered(m), want)
	}
	m.moreClick()
	if want := "header,a 0,b 0,c 0,mid,[,a,|,open,],[,b,|,open,],[,c,|,open,],footer,more"; rendered(m) != want {
		t.Fatalf("after a push, rendered %q, want %q", rendered(m), want)
	}
}
`

// A reactive slot at the top of a body renders between the siblings it was
// written between, rather than above all of them. The toggle has no gtk4rt
// wrapper, which puts the second variant on the cgo path.
func TestRootSlotsKeepTheirPlaceRuns(t *testing.T) {
	skipWithoutGIR(t)
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("gtk4 needs an X11/Wayland display")
	}
	for name, src := range map[string]string{
		"wrapped": rootSlotOrderSrc,
		"cgo":     strings.Replace(rootSlotOrderSrc, "    ui.text(value=\"footer\")", "    ui.toggle(checked=true)\n    ui.text(value=\"footer\")", 1),
	} {
		t.Run(name, func(t *testing.T) {
			files := generateGTK4FilesBuilt(t, src)
			if cgo := !strings.Contains(files["model.go"], "gtk4rt."); cgo != (name == "cgo") {
				t.Fatalf("expected the %s path:\n%s", name, files["model.go"])
			}
			tmp, err := os.MkdirTemp(".", "_gtk4-root-slot-order-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)
			files["entry.go"] = "package main\n\nfunc main() {}\n"
			files["probe.go"] = rootSlotOrderProbe
			files["order_test.go"] = rootSlotOrderDriver
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
