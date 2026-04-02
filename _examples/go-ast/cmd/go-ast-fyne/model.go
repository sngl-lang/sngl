package main

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

type Color struct {
	R int
	G int
	B int
	A int
}

// Model holds the state for this SNGL UI.
type Model struct {
	source     string
	file       ast.File
	parseError string
	Parse      func(string) // extern

	entry0 *widget.Entry

	btn0    *widget.Button
	ifBox0  *fyne.Container
	label0  *widget.Label
	forBox1 *fyne.Container
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
	m.updateLabel0()
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
	m.updateLabel0()
	m.updateIf0()
}

// BuildUI creates the widget tree. Call once; widgets are updated selectively.
func (m *Model) BuildUI() fyne.CanvasObject {
	var content fyne.CanvasObject
	var contentChildren []fyne.CanvasObject
	var contentC0 fyne.CanvasObject
	contentC0 = widget.NewLabelWithStyle(fmt.Sprint("Go AST Explorer"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	if contentC0 != nil {
		contentChildren = append(contentChildren, contentC0)
	}
	var contentC1 fyne.CanvasObject
	contentC1 = widget.NewLabel(fmt.Sprint("Enter Go source code and click Parse to see the AST."))
	if contentC1 != nil {
		contentChildren = append(contentChildren, contentC1)
	}
	var contentC2 fyne.CanvasObject
	contentC2 = m.entry0
	if contentC2 != nil {
		contentChildren = append(contentChildren, contentC2)
	}
	var contentC3 fyne.CanvasObject
	m.btn0 = widget.NewButton("Parse AST", func() {
		m.Parse(m.source)
		m.doRefresh()
	})
	contentC3 = m.btn0
	if contentC3 != nil {
		contentChildren = append(contentChildren, contentC3)
	}
	var contentC4 fyne.CanvasObject
	var contentC4Inner fyne.CanvasObject
	m.label0 = widget.NewLabel(fmt.Sprint(m.parseError))
	contentC4Inner = m.label0
	if contentC4Inner == nil {
		contentC4Inner = widget.NewLabel("")
	}
	m.ifBox0 = container.NewStack(contentC4Inner)
	if !(m.parseError != "") {
		m.ifBox0.Hide()
	}
	contentC4 = m.ifBox0
	if contentC4 != nil {
		contentChildren = append(contentChildren, contentC4)
	}
	var contentC5 fyne.CanvasObject
	var contentC5Children []fyne.CanvasObject
	var contentC5C0 fyne.CanvasObject
	var contentC5C0Items []fyne.CanvasObject
	for _, decl := range m.file.Decls {
		var contentC5C0Item fyne.CanvasObject
		contentC5C0Item = widget.NewLabel(fmt.Sprint(decl))
		if contentC5C0Item != nil {
			contentC5C0Items = append(contentC5C0Items, contentC5C0Item)
		}
	}
	m.forBox1 = container.NewVBox(contentC5C0Items...)
	contentC5C0 = m.forBox1
	if contentC5C0 != nil {
		contentC5Children = append(contentC5Children, contentC5C0)
	}
	contentC5 = container.NewVBox(contentC5Children...)
	if contentC5 != nil {
		contentChildren = append(contentChildren, contentC5)
	}
	content = container.NewVBox(contentChildren...)
	content = container.NewPadded(content)
	if content == nil {
		content = widget.NewLabel("")
	}
	return content
}

func (m *Model) updateLabel0() {
	m.label0.SetText(fmt.Sprint(m.parseError))
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
