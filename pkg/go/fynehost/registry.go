package fynehost

import (
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Default is the registry for the elements a first window needs.
//
// Hand-written, and deliberately small. A generated worker emits this table
// from the Specs its program can reach, which is what makes a widget the
// compiler never heard of a rebuild rather than an impossibility -- and it is
// also why the entries carry no Go type or callback signature: those exist in a
// Spec so an emitter can print them, and a host reads both off the value.
//
// The names are SNGL's, not Fyne's. An interpreted program is checked without
// platform overrides inlined, so what reaches a host is `text`, not `Label`.
func Default() Registry {
	return Registry{
		"text": {
			New:     widget.NewLabel,
			Setters: map[string]string{"value": "SetText"},
		},
		"button": {
			New:      widget.NewButton,
			Setters:  map[string]string{"text": "SetText"},
			Handlers: map[string]string{"click": "OnTapped"},
		},
		"input": {
			New: widget.NewEntry,
			Setters: map[string]string{
				"value":       "SetText",
				"placeholder": "SetPlaceHolder",
			},
			Handlers: map[string]string{"input": "OnChanged"},
		},
		"checkbox": {
			New: widget.NewCheck,
			Setters: map[string]string{
				"label":   "SetText",
				"checked": "SetChecked",
			},
			Handlers: map[string]string{"change": "OnChanged"},
		},
		"vbox": {New: container.NewVBox, Add: "Add"},
		"hbox": {New: container.NewHBox, Add: "Add"},
	}
}
