package fyne

import "duckfam.us/sngl/ir"

// fyne.sngl's MenuEntry and Back, which are this platform's runtime and not
// Fyne widgets: a window's main menu is the entries among its content, each
// holding its submenu's, which the Toplevel builds the menu from, and a Back
// among it is the window's back button.
const (
	menuEntryTag = "MenuEntry"
	backTag      = "Back"
)

func menuSpecFor(tag string, props map[string]ir.Expr) *fyneSpec {
	sp := &fyneSpec{CtorProps: props, Currents: map[string]string{}, Setters: map[string]string{}, Handlers: map[string]fyneHandler{}, Add: "Add", slotBox: "Container"}
	switch tag {
	case menuEntryTag:
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewMenuEntry"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*MenuEntry"}
		sp.Setters["label"] = "SetLabel"
		sp.Setters["disabled"] = "SetDisabled"
		sp.Setters["checked"] = "SetChecked"
		sp.Handlers["select"] = fyneHandler{Field: "OnSelect", Signature: "func()"}
	case backTag:
		sp.Add, sp.slotBox = "", ""
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewBack"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*Back"}
		sp.Handlers["press"] = fyneHandler{Field: "OnPress", Signature: "func()"}
	default:
		return nil
	}
	return sp
}
