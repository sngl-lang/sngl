package fyne

import "duckfam.us/sngl/ir"

// fyne.sngl's Tabs and TabPage, fynelayout's AppTabs over the pages its box
// holds and one page of it. Neither is the box a render slot renders into: a
// slot of pages inserts into the tabs' Container, and a panel's renders into
// the page's Box.
const (
	tabsTag    = "Tabs"
	tabPageTag = "TabPage"
)

func tabsSpecFor(tag string, props map[string]ir.Expr) *fyneSpec {
	sp := &fyneSpec{CtorProps: props, Currents: map[string]string{}, Setters: map[string]string{}, Handlers: map[string]fyneHandler{}, Add: "Add"}
	switch tag {
	case tabsTag:
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewTabs"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*Tabs"}
		sp.slotBox = "Container"
		sp.Setters["selected"] = "SetSelected"
	case tabPageTag:
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewTabPage"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*TabPage"}
		sp.slotBox = "Box"
		sp.Setters["title"] = "SetTitle"
		sp.Setters["disabled"] = "SetDisabled"
		sp.Handlers["change"] = fyneHandler{Field: "OnChange", Signature: "func()"}
	default:
		return nil
	}
	return sp
}
