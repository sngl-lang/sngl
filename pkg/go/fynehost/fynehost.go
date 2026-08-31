// Package fynehost renders a SNGL program with Fyne, as a snglhost.Host.
//
// It is the library half of a generated worker: the worker is a main() that
// opens a window, and this is what its ops do. It lives under pkg/ because
// that worker is built inside the user's own module and imports it from there.
//
// Nothing in the compiler imports this package, and nothing may -- linking Fyne
// into `sngl` would make the compiler a CGo/OpenGL binary. codegen/platform/fyne
// keeps Fyne at arm's length by naming it only in string literals, and
// keep_test_deps.go exists to stop `go mod tidy` noticing. See
// TestTheCompilerDoesNotLinkFyne.
package fynehost

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// Widget is how one SNGL element is built and driven.
//
// It is the runtime twin of the Spec record in fyne.sngl, and carries less:
// a Spec must also say the Go *type* and the callback *signature* so the
// emitter can print them, while a host holds the value and reads both off it by
// reflection. What cannot be recovered is the constructor -- Go will not turn
// "widget.NewLabel" into a symbol -- so that is the one thing a registry is
// for, and a generated worker emits this table from the Specs in scope.
type Widget struct {
	// New is the constructor, as a function value.
	New any
	// Setters maps a prop to the method that assigns it, without the receiver.
	// A prop absent here has no surface to reach and is dropped, as the emitter
	// drops it.
	Setters map[string]string
	// Handlers maps a declared event to the callback field it is assigned to.
	Handlers map[string]string
	// Add is the method a container attaches a child with; Content the field a
	// single-child wrapper assigns one to. Which applies is decided by Content
	// being set, because that is the case with no method to call.
	Add     string
	Content string
}

// Registry maps a SNGL element name to how it is built.
//
// Hand-written here for the elements a first window needs. A generated worker
// replaces it wholesale, emitted from the Specs its program can reach -- which
// is what makes a third-party widget a rebuild rather than an impossibility.
type Registry map[string]Widget

// Host renders into a Fyne container. It is not safe for concurrent use and
// expects to be driven from the goroutine that owns the Fyne loop.
type Host struct {
	reg   Registry
	nodes map[snglhost.Key]*mounted
	roots []*mounted
	// OnEvent is called when a widget's own callback fires. A worker forwards
	// it back over the wire; a test reads it directly.
	OnEvent func(key snglhost.Key, event string)
	// Root is the container the tree is rendered into.
	Root *fyne.Container
	// Unsupported records elements the registry has no entry for, so a worker
	// can report what it could not build rather than rendering a hole in
	// silence.
	Unsupported []string

	depth int
	err   error
}

type mounted struct {
	key      snglhost.Key
	name     string
	obj      fyne.CanvasObject
	spec     Widget
	parent   snglhost.Key
	children []*mounted
	// orphans holds objects mounted beneath a widget that is not a container.
	// They are tracked so Remove stays correct, and go unrendered -- which is
	// what the declaration said would happen: a widget declares no slot.
	orphans []fyne.CanvasObject
}

// New returns a Host rendering into a fresh vertical container.
func New(reg Registry) *Host {
	return &Host{
		reg:   reg,
		nodes: map[snglhost.Key]*mounted{},
		Root:  container.NewVBox(),
	}
}

func (h *Host) Begin() { h.depth++ }

func (h *Host) End() error {
	h.depth--
	if h.depth > 0 {
		return nil
	}
	err := h.err
	h.err = nil
	h.Root.Refresh()
	return err
}

func (h *Host) Create(d snglhost.NodeDesc, parent snglhost.Key, index int) error {
	spec, ok := h.reg[d.Name]
	if !ok {
		// Not an error: a program may name an element this worker was not
		// built for, and reporting it beats rendering a hole silently.
		h.Unsupported = append(h.Unsupported, d.Name)
		return nil
	}
	obj, err := construct(spec)
	if err != nil {
		return fmt.Errorf("%s: %w", d.Name, err)
	}
	m := &mounted{key: d.Key, name: d.Name, obj: obj, spec: spec, parent: parent}
	h.nodes[d.Key] = m
	for _, p := range d.Props {
		if err := applyProp(m, p.Name, p.Value); err != nil {
			return fmt.Errorf("%s.%s: %w", d.Name, p.Name, err)
		}
	}
	if err := h.bind(m, d.Events); err != nil {
		return err
	}
	return h.insert(m, parent, index)
}

func (h *Host) Remove(key snglhost.Key) error {
	m, ok := h.nodes[key]
	if !ok {
		return nil // an unsupported element was never mounted
	}
	h.detach(m)
	var forget func(*mounted)
	forget = func(x *mounted) {
		delete(h.nodes, x.key)
		for _, c := range x.children {
			forget(c)
		}
	}
	forget(m)
	return nil
}

func (h *Host) Move(key, parent snglhost.Key, index int) error {
	m, ok := h.nodes[key]
	if !ok {
		return nil
	}
	h.detach(m)
	return h.insert(m, parent, index)
}

func (h *Host) SetProp(key snglhost.Key, prop string, v any) error {
	m, ok := h.nodes[key]
	if !ok {
		return nil
	}
	return applyProp(m, prop, v)
}

func (h *Host) Rebind(key snglhost.Key, events []string) error {
	m, ok := h.nodes[key]
	if !ok {
		return nil
	}
	return h.bind(m, events)
}

// Object returns the Fyne widget mounted for a key, for a test or an inspector.
func (h *Host) Object(key snglhost.Key) (fyne.CanvasObject, bool) {
	m, ok := h.nodes[key]
	if !ok {
		return nil, false
	}
	return m.obj, true
}

// Fire invokes a widget's own callback, the way a click would. This is what
// makes an interpreted run the same kind of target as a compiled one: the
// event goes through the widget rather than around it.
func (h *Host) Fire(key snglhost.Key, event string) error {
	m, ok := h.nodes[key]
	if !ok {
		return fmt.Errorf("no widget at %s", key)
	}
	field, ok := m.spec.Handlers[event]
	if !ok {
		return fmt.Errorf("%s declares no @%s", m.name, event)
	}
	f := reflect.ValueOf(m.obj).Elem().FieldByName(field)
	if !f.IsValid() || f.IsNil() {
		return fmt.Errorf("%s.%s is not bound", m.name, field)
	}
	args := make([]reflect.Value, f.Type().NumIn())
	for i := range args {
		args[i] = reflect.Zero(f.Type().In(i))
	}
	f.Call(args)
	return nil
}

// construct calls the registered constructor. A container's is variadic and
// takes nothing: children arrive later through Add.
func construct(w Widget) (obj fyne.CanvasObject, err error) {
	fv := reflect.ValueOf(w.New)
	if !fv.IsValid() || fv.Kind() != reflect.Func {
		return nil, fmt.Errorf("no constructor registered")
	}
	ft := fv.Type()
	fixed := ft.NumIn()
	if ft.IsVariadic() {
		fixed--
	}
	args := make([]reflect.Value, fixed)
	for i := range args {
		args[i] = reflect.Zero(ft.In(i))
	}
	// A constructor may dereference an argument it was handed nothing for --
	// canvas.NewImageFromURI(nil) does -- so a bad registry entry is reported
	// rather than taking the process down.
	defer func() {
		if r := recover(); r != nil {
			obj, err = nil, fmt.Errorf("constructor panicked: %v", r)
		}
	}()
	out, ok := fv.Call(args)[0].Interface().(fyne.CanvasObject)
	if !ok {
		return nil, fmt.Errorf("constructor did not return a CanvasObject")
	}
	return out, nil
}

// applyProp assigns through the declared setter. The method's own parameter
// type is what the value is converted to, so nothing here names a Fyne type.
func applyProp(m *mounted, prop string, v any) error {
	method, ok := m.spec.Setters[prop]
	if !ok {
		return nil // no surface to reach; the emitter drops it too
	}
	fn := reflect.ValueOf(m.obj).MethodByName(method)
	if !fn.IsValid() {
		return fmt.Errorf("%T has no method %s", m.obj, method)
	}
	want := fn.Type().In(0)
	rv := reflect.ValueOf(v)
	if v == nil {
		rv = reflect.Zero(want)
	} else if !rv.Type().AssignableTo(want) {
		if !rv.CanConvert(want) {
			return fmt.Errorf("cannot pass %T to %s(%s)", v, method, want)
		}
		rv = rv.Convert(want)
	}
	fn.Call([]reflect.Value{rv})
	return nil
}

// bind assigns a closure to each declared callback field. The field's own type
// is the signature, so MakeFunc needs nothing the registry does not already
// say -- which is why a Spec's Signature is an emitter's concern and not a
// host's.
func (h *Host) bind(m *mounted, events []string) error {
	for event, field := range m.spec.Handlers {
		f := reflect.ValueOf(m.obj).Elem().FieldByName(field)
		if !f.IsValid() || !f.CanSet() {
			return fmt.Errorf("%s has no settable field %s", m.name, field)
		}
		if !contains(events, event) {
			f.Set(reflect.Zero(f.Type()))
			continue
		}
		key, name := m.key, event
		f.Set(reflect.MakeFunc(f.Type(), func([]reflect.Value) []reflect.Value {
			if h.OnEvent != nil {
				h.OnEvent(key, name)
			}
			return nil
		}))
	}
	return nil
}

func (h *Host) insert(m *mounted, parent snglhost.Key, index int) error {
	m.parent = parent
	siblings, objs := h.slotsOf(parent)
	if index < 0 || index > len(*siblings) {
		index = len(*siblings)
	}
	*siblings = append(*siblings, nil)
	copy((*siblings)[index+1:], (*siblings)[index:])
	(*siblings)[index] = m

	*objs = append(*objs, nil)
	copy((*objs)[index+1:], (*objs)[index:])
	(*objs)[index] = m.obj
	return nil
}

func (h *Host) detach(m *mounted) {
	siblings, objs := h.slotsOf(m.parent)
	for i, c := range *siblings {
		if c.key == m.key {
			*siblings = append((*siblings)[:i], (*siblings)[i+1:]...)
			*objs = append((*objs)[:i], (*objs)[i+1:]...)
			return
		}
	}
}

// slotsOf returns the child list and the Fyne object list of a parent, kept in
// step so an index means the same thing in both.
func (h *Host) slotsOf(parent snglhost.Key) (*[]*mounted, *[]fyne.CanvasObject) {
	if parent == (snglhost.Key{}) {
		return &h.roots, &h.Root.Objects
	}
	p, ok := h.nodes[parent]
	if !ok {
		return &h.roots, &h.Root.Objects
	}
	if c, ok := p.obj.(*fyne.Container); ok {
		return &p.children, &c.Objects
	}
	// A widget that is not a container has nowhere to put a child. Keep the
	// bookkeeping so Remove still works, and let the object go unrendered.
	return &p.children, &p.orphans
}

func contains(xs []string, x string) bool {
	return slices.Contains(xs, x)
}

// Tree renders what is mounted, in snglhost's format, so a Fyne host can be
// compared against the session that drove it.
func (h *Host) Tree() string {
	var b strings.Builder
	var walk func([]*mounted, int)
	walk = func(nodes []*mounted, depth int) {
		for _, n := range nodes {
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", depth), n.name)
			walk(n.children, depth+1)
		}
	}
	walk(h.roots, 0)
	return b.String()
}
