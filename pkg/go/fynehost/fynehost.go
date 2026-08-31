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

// spec is the Spec record a primitive was instantiated with, as it arrives on
// the node. The platform's fyne.sngl declares it and the interpreter hands it
// over untouched, so nothing here decides which widget a component becomes.
type spec struct {
	// ctor is the constructor's key, "path.Name", looked up in Ctors.
	ctor string
	// setters maps a prop to the method that assigns it; handlers a declared
	// event to its callback field.
	setters  map[string]string
	handlers map[string]string
	// add is a container's attach method, content a wrapper's child field.
	add, content string
}

// SpecProp is the prop a primitive carries its Spec in. Renaming it here means
// renaming it in fyne.sngl.
const SpecProp = "spec"

// decodeSpec reads the Spec off a node's props. A node without one is not a
// widget this platform declared -- a canvas or a drawing shape, say -- and
// there is nothing to build.
func decodeSpec(props []snglhost.PropVal) (spec, bool) {
	var sp spec
	raw, ok := fieldOf(props, SpecProp)
	if !ok {
		return sp, false
	}
	rec, ok := raw.(snglhost.WireStruct)
	if !ok {
		return sp, false
	}
	if n, ok := structField(rec, "new").(snglhost.WireStruct); ok {
		sp.ctor = str(structField(n, "path")) + "." + str(structField(n, "name"))
	}
	sp.add = str(structField(rec, "add"))
	sp.content = str(structField(rec, "content"))
	sp.setters = pairs(structField(rec, "setters"), "prop", "call")
	sp.handlers = pairs(structField(rec, "handlers"), "on", "field")
	return sp, sp.ctor != "."
}

func fieldOf(props []snglhost.PropVal, name string) (any, bool) {
	for _, p := range props {
		if p.Name == name {
			return p.Value, true
		}
	}
	return nil, false
}

func structField(w snglhost.WireStruct, name string) any {
	for _, f := range w.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}

// pairs reads a list of two-field records into a map, which is how a Spec
// spells its setters and handlers.
func pairs(v any, keyField, valField string) map[string]string {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, el := range list {
		rec, ok := el.(snglhost.WireStruct)
		if !ok {
			continue
		}
		if k := str(structField(rec, keyField)); k != "" {
			out[k] = str(structField(rec, valField))
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// Host renders into a Fyne container. It is not safe for concurrent use and
// expects to be driven from the goroutine that owns the Fyne loop.
type Host struct {
	// ctors is the only thing this host is told in advance: a constructor
	// cannot be recovered from the string a Spec names it by.
	ctors map[string]any
	nodes map[snglhost.Key]*mounted
	// missing is every key with no widget behind it: an element this worker was
	// not built for, and everything under it.
	missing map[snglhost.Key]bool
	roots   []*mounted
	// OnEvent is called when a widget's own callback fires. A worker forwards
	// it back over the wire; a test reads it directly.
	OnEvent func(key snglhost.Key, event string, args []any)
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
	spec     spec
	parent   snglhost.Key
	children []*mounted
	// orphans holds objects mounted beneath a widget that is neither a
	// container nor a wrapper. Tracked so Remove stays correct; unrendered,
	// because the Spec names nowhere to put them.
	orphans []fyne.CanvasObject
}

// New returns a Host rendering into a fresh vertical container.
func New(ctors map[string]any) *Host {
	return &Host{
		ctors:       ctors,
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
	sp, ok := decodeSpec(d.Props)
	if ok {
		if _, known := h.ctors[sp.ctor]; !known {
			ok = false
		}
	}
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
	obj, err := construct(h.ctors[sp.ctor])
	if err != nil {
		return fmt.Errorf("%s: %w", d.Name, err)
	}
	m := &mounted{key: d.Key, name: d.Name, obj: obj, spec: sp, parent: parent}
	h.nodes[d.Key] = m
	for _, p := range d.Props {
		if p.Name == SpecProp {
			continue // metadata, not a value the widget takes
		}
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
func (h *Host) SetOnEvent(fn func(key snglhost.Key, event string, args []any)) { h.OnEvent = fn }

// Object returns the Fyne widget mounted for a key, for a test or an inspector.
func (h *Host) Object(key snglhost.Key) (fyne.CanvasObject, bool) {
	m, ok := h.nodes[key]
	if !ok {
		return nil, false
	}
	return m.obj, true
}

// Fire invokes a widget's own callback, the way a click would -- through the
// widget rather than around it, as a compiled target does. Args are what the
// widget would have reported; a zero value stands in for each it omits.
func (h *Host) Fire(key snglhost.Key, event string, args ...any) error {
	m, ok := h.nodes[key]
	if !ok {
		return fmt.Errorf("no widget at %s", key)
	}
	field, ok := m.spec.handlers[event]
	if !ok {
		return fmt.Errorf("%s declares no @%s", m.name, event)
	}
	f := reflect.ValueOf(m.obj).Elem().FieldByName(field)
	if !f.IsValid() || f.IsNil() {
		return fmt.Errorf("%s.%s is not bound", m.name, field)
	}
	in := make([]reflect.Value, f.Type().NumIn())
	for i := range in {
		if i < len(args) && args[i] != nil {
			v := reflect.ValueOf(args[i])
			if v.Type().AssignableTo(f.Type().In(i)) {
				in[i] = v
				continue
			}
		}
		in[i] = reflect.Zero(f.Type().In(i))
	}
	f.Call(in)
	return nil
}

// construct calls the registered constructor. A container's is variadic and
// takes nothing: children arrive later through Add.
func construct(ctor any) (obj fyne.CanvasObject, err error) {
	fv := reflect.ValueOf(ctor)
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
	method, ok := m.spec.setters[prop]
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
	for event, field := range m.spec.handlers {
		f := reflect.ValueOf(m.obj).Elem().FieldByName(field)
		if !f.IsValid() || !f.CanSet() {
			return fmt.Errorf("%s has no settable field %s", m.name, field)
		}
		if !contains(events, event) {
			f.Set(reflect.Zero(f.Type()))
			continue
		}
		key, name := m.key, event
		f.Set(reflect.MakeFunc(f.Type(), func(in []reflect.Value) []reflect.Value {
			if h.OnEvent != nil {
				// The callback's own arguments are the payload: OnChanged(s)
				// hands over the text the field now holds, which is what a
				// two-way binding assigns.
				args := make([]any, len(in))
				for i, v := range in {
					args[i] = v.Interface()
				}
				h.OnEvent(key, name, args)
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
	if !ok || p.spec.content == "" {
		return nil
	}
	f := reflect.ValueOf(p.obj).Elem().FieldByName(p.spec.content)
	if !f.IsValid() || !f.CanSet() {
		return fmt.Errorf("%s has no settable field %s", p.name, p.spec.content)
	}
	if len(p.children) == 0 {
		f.Set(reflect.Zero(f.Type()))
	} else {
		child := reflect.ValueOf(p.children[0].obj)
		if !child.Type().AssignableTo(f.Type()) {
			return fmt.Errorf("cannot put a %T in %s.%s", p.children[0].obj, p.name, p.spec.content)
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
