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

// Widget is how one SNGL element is built and driven: the runtime twin of a
// Spec in fyne.sngl. The constructor is the only part reflection cannot
// recover, which is what a registry is for.
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
type Registry map[string]Widget

// Host renders into a Fyne container. It is not safe for concurrent use and
// expects to be driven from the goroutine that owns the Fyne loop.
type Host struct {
	reg   Registry
	nodes map[snglhost.Key]*mounted
	// missing is every key with no widget behind it: an element this worker was
	// not built for, and everything under it.
	missing map[snglhost.Key]bool
	roots   []*mounted
	// OnEvent is called when a widget's own callback fires. A worker forwards
	// it back over the wire; a test reads it directly.
	OnEvent func(key snglhost.Key, event string)
	// Root is the container the tree is rendered into.
	Root *fyne.Container
	// Unsupported records elements the registry has no entry for. A worker
	// reports them: a window that silently renders less than the program says
	// is worse than one that says what it dropped.
	Unsupported []string
	// OnUnsupported is called once per element name the registry lacks.
	OnUnsupported func(name string)
	seenMissing   map[string]bool

	depth int
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
		reg:         reg,
		nodes:       map[snglhost.Key]*mounted{},
		missing:     map[snglhost.Key]bool{},
		seenMissing: map[string]bool{},
		Root:        container.NewVBox(),
	}
}

func (h *Host) Begin() { h.depth++ }

func (h *Host) End() error {
	h.depth--
	if h.depth > 0 {
		return nil
	}
	h.Root.Refresh()
	return nil
}

func (h *Host) Create(d snglhost.NodeDesc, parent snglhost.Key, index int) error {
	if h.missing[parent] {
		// Its parent could not be built, so there is nowhere for this to go.
		// Without this it would fall through to the root and the whole subtree
		// of an unknown container would appear at top level.
		h.missing[d.Key] = true
		return nil
	}
	spec, ok := h.reg[d.Name]
	if !ok {
		// Not an error: a program may name an element this worker was not built
		// for, and reporting it beats rendering a hole silently.
		h.missing[d.Key] = true
		if !h.seenMissing[d.Name] {
			h.seenMissing[d.Name] = true
			h.Unsupported = append(h.Unsupported, d.Name)
			if h.OnUnsupported != nil {
				h.OnUnsupported(d.Name)
			}
		}
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

// SetOnEvent satisfies snglhost.EventReporter, so a worker forwards what a
// viewer does back over the wire without this package knowing there is one.
func (h *Host) SetOnEvent(fn func(key snglhost.Key, event string)) { h.OnEvent = fn }

// Object returns the Fyne widget mounted for a key, for a test or an inspector.
func (h *Host) Object(key snglhost.Key) (fyne.CanvasObject, bool) {
	m, ok := h.nodes[key]
	if !ok {
		return nil, false
	}
	return m.obj, true
}

// Fire invokes a widget's own callback, the way a click would -- through the
// widget rather than around it, as a compiled target does.
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
// is the signature, so MakeFunc needs nothing the registry does not say.
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
	return h.setContent(parent)
}

func (h *Host) detach(m *mounted) {
	siblings, objs := h.slotsOf(m.parent)
	for i, c := range *siblings {
		if c.key == m.key {
			*siblings = append((*siblings)[:i], (*siblings)[i+1:]...)
			*objs = append((*objs)[:i], (*objs)[i+1:]...)
			_ = h.setContent(m.parent)
			return
		}
	}
}

// slotsOf returns the child list and the Fyne object list of a parent, kept in
// step so an index means the same thing in both.
//
// A wrapper takes its single child through a field rather than a list, which
// the registry names in Content -- see setContent, called after the lists are
// updated. Its slice here is bookkeeping so Remove and Move stay correct.
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
	return &p.children, &p.orphans
}

// setContent assigns a wrapper's single child to the field its registry entry
// names. A container's children are its Objects slice and need nothing; a
// wrapper's are one field, and without this a Scroll rendered empty.
func (h *Host) setContent(parent snglhost.Key) error {
	p, ok := h.nodes[parent]
	if !ok || p.spec.Content == "" {
		return nil
	}
	f := reflect.ValueOf(p.obj).Elem().FieldByName(p.spec.Content)
	if !f.IsValid() || !f.CanSet() {
		return fmt.Errorf("%s has no settable field %s", p.name, p.spec.Content)
	}
	if len(p.children) == 0 {
		f.Set(reflect.Zero(f.Type()))
	} else {
		child := reflect.ValueOf(p.children[0].obj)
		if !child.Type().AssignableTo(f.Type()) {
			return fmt.Errorf("cannot put a %T in %s.%s", p.children[0].obj, p.name, p.spec.Content)
		}
		f.Set(child)
	}
	if w, ok := p.obj.(fyne.Widget); ok {
		w.Refresh()
	}
	return nil
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
