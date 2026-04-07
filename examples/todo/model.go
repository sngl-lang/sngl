package ui

import (
	"fmt"

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

type Todo struct {
	Text string
	Done bool
}

// Model holds the state for this SNGL UI.
type Model struct {
	newTodo string
	todos   []Todo

	entry0 *widget.Entry

	widget0 *widget.Label
	widget1 *widget.Button
	forBox0 *fyne.Container
	widget2 *widget.Label
	widget3 *widget.Button
}

// New creates a Model with default values.
func New() *Model {
	m := &Model{
		newTodo: "",
		todos:   nil,
	}
	m.entry0 = widget.NewEntry()
	m.entry0.SetPlaceHolder("Buy eggs")
	m.entry0.SetText(fmt.Sprint(m.newTodo))
	m.entry0.OnChanged = func(s string) {
		m.newTodo = s
		m.doRefresh()
	}
	return m
}

func (m *Model) doRefresh() {
	m.updateFor0()
}

func (m *Model) status() string {
	return (("Todo List (" + fmt.Sprint(len(m.todos))) + " items)")
}

func (m *Model) Status() {
	return (("Todo List (" + fmt.Sprint(len(m.todos))) + " items)")
}

func (m *Model) NewTodo() string {
	return m.newTodo
}

func (m *Model) SetNewTodo(v string) {
	m.newTodo = v
	m.entry0.SetText(v)
}

func (m *Model) Todos() []Todo {
	return m.todos
}

func (m *Model) SetTodos(v []Todo) {
	m.todos = v
	m.updateFor0()
}

// BuildUI creates the widget tree. Call once; widgets are updated selectively.
func (m *Model) BuildUI() fyne.CanvasObject {
	var content fyne.CanvasObject
	var contentChildren []fyne.CanvasObject
	var contentC0 fyne.CanvasObject
	var contentC0Slot0 fyne.CanvasObject
	m.widget0 = widget.NewLabel(fmt.Sprint(m.status()))
	contentC0Slot0 = m.widget0
	if contentC0Slot0 != nil {
		contentC0 = contentC0Slot0
	}
	var contentC0Slot1 fyne.CanvasObject
	var contentC0Slot1Children []fyne.CanvasObject
	var contentC0Slot1C0 fyne.CanvasObject
	var contentC0Slot1C0Slot0 fyne.CanvasObject
	contentC0Slot1C0Slot0 = m.entry0
	if contentC0Slot1C0Slot0 != nil {
		contentC0Slot1C0 = contentC0Slot1C0Slot0
	}
	var contentC0Slot1C0Slot1 fyne.CanvasObject
	m.widget1 = widget.NewButton(fmt.Sprint("Add"))
	contentC0Slot1C0Slot1 = m.widget1
	// TODO: wire click event on persistent widget
	if contentC0Slot1C0Slot1 != nil {
		contentC0Slot1C0 = contentC0Slot1C0Slot1
	}
	if contentC0Slot1C0 != nil {
		contentC0Slot1Children = append(contentC0Slot1Children, contentC0Slot1C0)
	}
	contentC0Slot1 = container.NewHBox(contentC0Slot1Children...)
	if contentC0Slot1 != nil {
		contentC0 = contentC0Slot1
	}
	var contentC0Slot2 fyne.CanvasObject
	var contentC0Slot2Children []fyne.CanvasObject
	var contentC0Slot2C0 fyne.CanvasObject
	var contentC0Slot2C0Slot0 fyne.CanvasObject
	var contentC0Slot2C0Slot0Items []fyne.CanvasObject
	for index, item := range m.todos {
		_ = index
		var contentC0Slot2C0Slot0Item fyne.CanvasObject
		m.widget2 = widget.NewLabel(fmt.Sprint(item.Text))
		contentC0Slot2C0Slot0Item = m.widget2
		if contentC0Slot2C0Slot0Item != nil {
			contentC0Slot2C0Slot0Items = append(contentC0Slot2C0Slot0Items, contentC0Slot2C0Slot0Item)
		}
	}
	m.forBox0 = container.NewVBox(contentC0Slot2C0Slot0Items...)
	contentC0Slot2C0Slot0 = m.forBox0
	if contentC0Slot2C0Slot0 != nil {
		contentC0Slot2C0 = contentC0Slot2C0Slot0
	}
	if contentC0Slot2C0 != nil {
		contentC0Slot2Children = append(contentC0Slot2Children, contentC0Slot2C0)
	}
	contentC0Slot2 = container.NewVBox(contentC0Slot2Children...)
	if contentC0Slot2 != nil {
		contentC0 = contentC0Slot2
	}
	var contentC0Slot3 fyne.CanvasObject
	m.widget3 = widget.NewButton(fmt.Sprint("Remove"))
	contentC0Slot3 = m.widget3
	// TODO: wire click event on persistent widget
	if contentC0Slot3 != nil {
		contentC0 = contentC0Slot3
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

func (m *Model) updateFor0() {
	var items []fyne.CanvasObject
	for index, item := range m.todos {
		_ = index
		_ = item
		items = append(items, widget.NewLabel(fmt.Sprint(item)))
	}
	m.forBox0.Objects = items
	m.forBox0.Refresh()
}
