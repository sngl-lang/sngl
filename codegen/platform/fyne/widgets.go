package fyne

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// intrinsicPrefix is the namespace every fyne widget declaration's
// #[intrinsic] id carries. The prefix is the platform whose codegen answers
// to the id, so a fyne primitive can never collide with a stdlib intrinsic or
// with another platform's.
const intrinsicPrefix = "fyne:"

// ctorArg is one argument the Go constructor is called with at create time,
// before any prop value is known. Fyne constructors take their content up
// front; the sequence this platform emits sets it afterwards, so each is
// called with the zero of what it wants.
type ctorArg int

const (
	// argEmptyString is `""` — a label's text, a button's caption.
	argEmptyString ctorArg = iota
	// argNil is `nil` — a callback, an option list, a scroll child, an image URI.
	argNil
)

func (a ctorArg) expr() ir.Expr {
	switch a {
	case argEmptyString:
		return &ir.Literal{Type: ir.TypString, Raw: ""}
	default:
		return &ir.Literal{Type: ir.TypNull}
	}
}

// fyneEvent is the Fyne callback field one declared event is assigned to.
//
// Signature is the Go func literal type the field takes, which is Fyne's and
// not the SNGL event's: `OnChanged` on an Entry is `func(string)` where the
// SNGL `@input` carries an InputEvent. BindParam names that parameter so the
// promoted handler's two-way write-back can read the value straight from it
// (`m.name = s`) rather than off an event struct the closure never receives.
type fyneEvent struct {
	Field     string
	Signature string
	BindParam string
}

// fyneWidget is what one #[intrinsic("fyne:<Name>")] declaration means to the
// Go emitter: the constructor to call, the Go type it returns, and the setter
// or callback field behind each prop and event the declaration exposes.
//
// A prop or event absent from these maps has no Fyne surface to reach, so the
// assignment or attachment is dropped. That is the whole of the platform's
// knowledge of Fyne — the declarations in lib/platforms/fyne/fyne.sngl are the
// contract, and this table is how each id is emitted.
type fyneWidget struct {
	GoFn   string
	GoType string
	// CtorArgs are the arguments GoFn is called with. See ctorArg.
	CtorArgs []ctorArg
	// Props maps a declared prop to the setter method it becomes, without the
	// receiver: "SetText" emits `w.SetText(v)`.
	Props map[string]string
	// Events maps a declared event to the callback field it is assigned to.
	Events map[string]fyneEvent
	// SingleChild marks a container that holds its child in a `Content` field
	// rather than through an `Add` method (container.Scroll).
	SingleChild bool
}

// fyneWidgets is the platform's dispatch table, keyed by the intrinsic id its
// declaration carries. Every lookup goes through widgetFor, so nothing in this
// package is keyed on a component name.
var fyneWidgets = map[string]*fyneWidget{
	"Label": {
		GoFn:     "widget.NewLabel",
		GoType:   "*widget.Label",
		CtorArgs: []ctorArg{argEmptyString},
		Props:    map[string]string{"text": "SetText"},
	},
	"Button": {
		GoFn:     "widget.NewButton",
		GoType:   "*widget.Button",
		CtorArgs: []ctorArg{argEmptyString, argNil},
		Props:    map[string]string{"text": "SetText"},
		Events: map[string]fyneEvent{
			"click": {Field: "OnTapped", Signature: "func()"},
		},
	},
	"Entry": {
		GoFn:   "widget.NewEntry",
		GoType: "*widget.Entry",
		Props: map[string]string{
			"text":        "SetText",
			"placeholder": "SetPlaceHolder",
		},
		Events: map[string]fyneEvent{
			"input": {Field: "OnChanged", Signature: "func(s string)", BindParam: "s"},
		},
	},
	"MultiLineEntry": {
		GoFn:   "widget.NewMultiLineEntry",
		GoType: "*widget.Entry",
		Props: map[string]string{
			"text":        "SetText",
			"placeholder": "SetPlaceHolder",
			"rows":        "SetMinRowsVisible",
		},
		Events: map[string]fyneEvent{
			"input": {Field: "OnChanged", Signature: "func(s string)", BindParam: "s"},
		},
	},
	"Check": {
		GoFn:     "widget.NewCheck",
		GoType:   "*widget.Check",
		CtorArgs: []ctorArg{argEmptyString, argNil},
		Props: map[string]string{
			"text":    "SetText",
			"checked": "SetChecked",
		},
		Events: map[string]fyneEvent{
			"change": {Field: "OnChanged", Signature: "func(b bool)", BindParam: "b"},
		},
	},
	"Select": {
		GoFn:     "widget.NewSelect",
		GoType:   "*widget.Select",
		CtorArgs: []ctorArg{argNil, argNil},
		Props:    map[string]string{"selected": "SetSelected"},
		Events: map[string]fyneEvent{
			"change": {Field: "OnChanged", Signature: "func(s string)", BindParam: "s"},
		},
	},
	"Hyperlink": {
		GoFn:     "widget.NewHyperlink",
		GoType:   "*widget.Hyperlink",
		CtorArgs: []ctorArg{argEmptyString, argNil},
	},
	"Image": {
		GoFn:     "canvas.NewImageFromURI",
		GoType:   "*canvas.Image",
		CtorArgs: []ctorArg{argNil},
	},
	"Spacer": {
		GoFn:   "layout.NewSpacer",
		GoType: "fyne.CanvasObject",
	},
	"VBox": {
		GoFn:   "container.NewVBox",
		GoType: "*fyne.Container",
	},
	"HBox": {
		GoFn:   "container.NewHBox",
		GoType: "*fyne.Container",
	},
	"VScroll": {
		GoFn:        "container.NewVScroll",
		GoType:      "*container.Scroll",
		CtorArgs:    []ctorArg{argNil},
		SingleChild: true,
	},
}

// widgetFor is the one lookup: a component's #[intrinsic] id, minus this
// platform's namespace, indexes fyneWidgets. A component with no id, or one
// namespaced to another platform, is not a fyne widget.
func widgetFor(comp *ir.Component) *fyneWidget {
	if comp == nil {
		return nil
	}
	id, ok := strings.CutPrefix(comp.Intrinsic, intrinsicPrefix)
	if !ok {
		return nil
	}
	return fyneWidgets[id]
}

// ctorImports are the Go import paths a widget's constructor and Go type name
// between them, resolved through fyneFrameworkPkgs. Nothing in fyne.sngl
// declares an import: the two Go spellings say which packages the emitted file
// needs, and the Go type's is the one gc cannot see for itself (it reaches the
// output as a Model field's type string rather than as an evaluated call).
func (w *fyneWidget) ctorImports() []string {
	var out []string
	for _, sel := range []string{selectorOf(w.GoFn), selectorOf(w.GoType)} {
		if p := fyneImportPath(sel); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// selectorOf is the package selector in a Go spelling like "*widget.Label" or
// "container.NewVScroll", or "" when there is none.
func selectorOf(goName string) string {
	sel, _, ok := strings.Cut(strings.TrimLeft(goName, "*[]"), ".")
	if !ok {
		return ""
	}
	return sel
}
