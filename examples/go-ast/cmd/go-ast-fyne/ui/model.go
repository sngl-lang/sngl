package ui

import (
	"fmt"
	"go/ast"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

var _ = fmt.Sprint
var _ fyne.CanvasObject
var _ = container.NewVBox
var _ = layout.NewSpacer
var _ = widget.NewLabel

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// Model holds the state for this SNGL UI.
type Model struct {
	source     string
	file       ast.File
	parseError string
	Parse      func(string) // extern

	entry0 *widget.Entry

	widget0 *widget.Label
	widget1 *widget.Label
	widget2 *widget.Button
	ifBox0  *fyne.Container
	widget3 *widget.Label
	forBox1 *fyne.Container
	widget4 *widget.Label
}

// New creates a Model with default values.
func New() *Model {
	m := &Model{
		source:     "package main",
		file:       ast.File{},
		parseError: "",
	}
	m.entry0 = widget.NewMultiLineEntry()
	m.entry0.SetPlaceHolder("Enter Go code...")
	m.entry0.SetText(fmt.Sprint(m.source))
	m.entry0.OnChanged = func(s string) {
		m.source = s
		m.doRefresh()
	}
	m.entry0.SetMinRowsVisible(10)
	return m
}

func (m *Model) doRefresh() {
	m.updateIf0()
	m.updateFor1()
}

func (m *Model) Source() string {
	return m.source
}

func (m *Model) SetSource(v string) {
	m.source = v
	m.entry0.SetText(v)
}

func (m *Model) File() ast.File {
	return m.file
}

func (m *Model) SetFile(v ast.File) {
	m.file = v
	m.updateFor1()
}

func (m *Model) ParseError() string {
	return m.parseError
}

func (m *Model) SetParseError(v string) {
	m.parseError = v
	m.updateIf0()
}

// BuildUI creates the widget tree. Call once; widgets are updated selectively.
func (m *Model) BuildUI() fyne.CanvasObject {
	var content fyne.CanvasObject
	var contentChildren []fyne.CanvasObject
	var contentC0 fyne.CanvasObject
	var contentC0Slot0 fyne.CanvasObject
	m.widget0 = widget.NewLabel(fmt.Sprint("Go AST Explorer"))
	contentC0Slot0 = m.widget0
	if contentC0Slot0 != nil {
		contentC0 = contentC0Slot0
	}
	var contentC0Slot1 fyne.CanvasObject
	m.widget1 = widget.NewLabel(fmt.Sprint("Enter Go source code and click Parse to see the AST."))
	contentC0Slot1 = m.widget1
	if contentC0Slot1 != nil {
		contentC0 = contentC0Slot1
	}
	var contentC0Slot2 fyne.CanvasObject
	contentC0Slot2 = m.entry0
	if contentC0Slot2 != nil {
		contentC0 = contentC0Slot2
	}
	var contentC0Slot3 fyne.CanvasObject
	m.widget2 = widget.NewButton(fmt.Sprint("Parse AST"), nil)
	contentC0Slot3 = m.widget2
	// TODO: wire click event on persistent widget
	if contentC0Slot3 != nil {
		contentC0 = contentC0Slot3
	}
	var contentC0Slot4 fyne.CanvasObject
	var contentC0Slot4Inner fyne.CanvasObject
	m.widget3 = widget.NewLabel(fmt.Sprint(m.parseError))
	contentC0Slot4Inner = m.widget3
	if contentC0Slot4Inner == nil {
		contentC0Slot4Inner = widget.NewLabel("")
	}
	m.ifBox0 = container.NewStack(contentC0Slot4Inner)
	if !(m.parseError != "") {
		m.ifBox0.Hide()
	}
	contentC0Slot4 = m.ifBox0
	if contentC0Slot4 != nil {
		contentC0 = contentC0Slot4
	}
	var contentC0Slot5 fyne.CanvasObject
	var contentC0Slot5Children []fyne.CanvasObject
	var contentC0Slot5C0 fyne.CanvasObject
	var contentC0Slot5C0Slot0 fyne.CanvasObject
	var contentC0Slot5C0Slot0Items []fyne.CanvasObject
	for _, decl := range m.file.Decls {
		var contentC0Slot5C0Slot0Item fyne.CanvasObject
		m.widget4 = widget.NewLabel(fmt.Sprint(decl))
		contentC0Slot5C0Slot0Item = m.widget4
		if contentC0Slot5C0Slot0Item != nil {
			contentC0Slot5C0Slot0Items = append(contentC0Slot5C0Slot0Items, contentC0Slot5C0Slot0Item)
		}
	}
	m.forBox1 = container.NewVBox(contentC0Slot5C0Slot0Items...)
	contentC0Slot5C0Slot0 = m.forBox1
	if contentC0Slot5C0Slot0 != nil {
		contentC0Slot5C0 = contentC0Slot5C0Slot0
	}
	if contentC0Slot5C0 != nil {
		contentC0Slot5Children = append(contentC0Slot5Children, contentC0Slot5C0)
	}
	contentC0Slot5 = container.NewVBox(contentC0Slot5Children...)
	if contentC0Slot5 != nil {
		contentC0 = contentC0Slot5
	}
	if contentC0 != nil {
		contentChildren = append(contentChildren, contentC0)
	}
	content = container.NewVBox(contentChildren...)
	if content == nil {
		content = widget.NewLabel("")
	}
	return content
}

func (m *Model) updateIf0() {
	if m.parseError != "" {
		m.ifBox0.Show()
	} else {
		m.ifBox0.Hide()
	}
}

func (m *Model) updateFor1() {
	var items []fyne.CanvasObject
	for _, decl := range m.file.Decls {
		_ = decl
		items = append(items, widget.NewLabel(fmt.Sprint(decl)))
	}
	m.forBox1.Objects = items
	m.forBox1.Refresh()
}
