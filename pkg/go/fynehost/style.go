package fynehost

import (
	"reflect"
	"strings"

	fyne "fyne.io/fyne/v2"

	"duckfam.us/sngl/pkg/go/fynelayout"
	"duckfam.us/sngl/pkg/go/snglcolor"
	"duckfam.us/sngl/pkg/go/snglhost"
)

// LookProp is the primitive prop carrying the fynelayout.Look the platform's
// style.sngl built from the node's Style.
const LookProp = "look"

// boxed is what a node is mounted as: the widget, or the widget inside the Box
// showing a look that asks for anything.
func boxed(obj fyne.CanvasObject, props []snglhost.PropVal) (fyne.CanvasObject, *fynelayout.Box) {
	if v, ok := fieldOf(props, LookProp); !ok || decodeLook(v) == (fynelayout.Look{}) {
		return obj, nil
	}
	b := fynelayout.NewBox(obj)
	return b, b
}

// decodeLook reads a Look record off the wire. It crosses with its SNGL
// field names, which are the Go ones spelled lower.
func decodeLook(v any) fynelayout.Look {
	var l fynelayout.Look
	rec, ok := v.(snglhost.WireStruct)
	if !ok {
		return l
	}
	lv := reflect.ValueOf(&l).Elem()
	for _, f := range rec.Fields {
		dst := lv.FieldByName(strings.ToUpper(f.Name[:1]) + f.Name[1:])
		if !dst.IsValid() {
			continue
		}
		setWire(dst, f.Value)
	}
	return l
}

func setWire(dst reflect.Value, v any) {
	switch dst.Interface().(type) {
	case snglcolor.Color:
		rec, ok := v.(snglhost.WireStruct)
		if !ok {
			return
		}
		dst.Set(reflect.ValueOf(snglcolor.Color{
			R: int(num(structField(rec, "r"))),
			G: int(num(structField(rec, "g"))),
			B: int(num(structField(rec, "b"))),
			A: int(num(structField(rec, "a"))),
		}))
	case bool:
		b, _ := v.(bool)
		dst.SetBool(b)
	case int:
		dst.SetInt(int64(num(v)))
	case float32:
		dst.SetFloat(float64(num(v)))
	}
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// rebox mounts m inside a Box where its parent held the widget itself, for a
// look that asked for nothing when m was created.
func (h *Host) rebox(m *mounted) error {
	siblings, _ := h.slotsOf(m.parent)
	index := len(*siblings)
	for i, c := range *siblings {
		if c.key == m.key {
			index = i
		}
	}
	h.detach(m)
	m.box = fynelayout.NewBox(m.obj)
	m.view = m.box
	return h.insert(m, m.parent, index)
}
