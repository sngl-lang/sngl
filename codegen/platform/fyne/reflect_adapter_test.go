package fyne

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.duckfam.us/jonathan/sngl/pkg/go/fynelayout"
)

// This file is the spike for an interpreted fyne host: proof that a widget can
// be built and driven from the Spec records in fyne.sngl alone, with no
// generated Go. It lives in a _test.go because the compiler's own binary must
// stay pure Go -- see keep_test_deps.go for why nothing here imports fyne
// outside a test.
//
// The claim under test is that the ONLY thing an interpreter cannot recover at
// runtime is the constructor: Go will not turn the string
// "fyne.io/fyne/v2/widget.NewLabel" into a symbol. Everything else -- the
// widget's type, its setter methods, its callback fields and their exact
// signatures -- is readable off the constructed value by reflection.

// fyneCtors is the irreducible generated artifact: Spec.New spelled as
// path.Name, bound to the function value. One identifier per entry, no wrapper
// closure. In the real adapter this file is written by go:generate from the
// Specs; here it is hand-written so the spike can check whether the shape
// holds at all.
var fyneCtors = map[string]any{
	"fyne.io/fyne/v2/widget.NewLabel":             widget.NewLabel,
	"fyne.io/fyne/v2/widget.NewButton":            widget.NewButton,
	"fyne.io/fyne/v2/widget.NewCheck":             widget.NewCheck,
	"fyne.io/fyne/v2/widget.NewEntry":             widget.NewEntry,
	"fyne.io/fyne/v2/widget.NewMultiLineEntry":    widget.NewMultiLineEntry,
	"fyne.io/fyne/v2/widget.NewHyperlink":         widget.NewHyperlink,
	"fyne.io/fyne/v2/widget.NewSelect":            widget.NewSelect,
	"fyne.io/fyne/v2/widget.NewProgressBar":       widget.NewProgressBar,
	"fyne.io/fyne/v2/widget.NewSlider":            widget.NewSlider,
	"fyne.io/fyne/v2/canvas.NewImageFromResource": canvas.NewImageFromResource,
	"fyne.io/fyne/v2/layout.NewSpacer":            layout.NewSpacer,
	"fyne.io/fyne/v2/container.NewVBox":           container.NewVBox,
	"fyne.io/fyne/v2/container.NewHBox":           container.NewHBox,
	"fyne.io/fyne/v2/container.NewVScroll":        container.NewVScroll,
	// The window's Toplevel is this platform's own runtime type, not a Fyne
	// widget, and is held to the same reflection as the rest.
	"git.duckfam.us/jonathan/sngl/pkg/go/fynelayout.NewToplevel": fynelayout.NewToplevel,
}

// allSpecs decodes every Spec the fyne overrides reach, keyed by the stdlib
// component whose override reached it. Reuses the walk
// TestEveryFyneBodyLowersToADeclaredWidget already established.
func allSpecs(t *testing.T) map[string]*fyneSpec {
	t.Helper()
	pkg := checkAllComponents(t)
	out := map[string]*fyneSpec{}
	for _, comp := range OverriddenComponents(pkg) {
		for _, n := range fynePrimitiveNodes(comp.PlatformOverrides["fyne"].Stmts) {
			sp, err := specFromProps(n.Name, nodeProps(n))
			if err != nil {
				t.Fatalf("%s: %v", comp.Name, err)
			}
			out[comp.Name] = sp
		}
	}
	if len(out) == 0 {
		t.Fatal("no specs decoded; the override walk found nothing")
	}
	return out
}

// TestEverySpecCtorIsInTheAdapterTable is the gate that keeps the table honest:
// a widget added to fyne.sngl fails here until the table names its
// constructor. This is the whole maintenance burden of the interpreted host.
func TestEverySpecCtorIsInTheAdapterTable(t *testing.T) {
	for name, sp := range allSpecs(t) {
		key := sp.New.Path + "." + sp.New.Name
		if _, ok := fyneCtors[key]; !ok {
			t.Errorf("%s: no adapter entry for %s", name, key)
		}
	}
}

// TestSpecGoTypeNameMatchesTheCtorReturn checks the Spec's declared GoType
// against what the constructor actually returns -- by NAME only.
//
// The path cannot be checked: `container.Scroll` is a type ALIAS for
// `internal/widget.Scroll`, and reflection reports the underlying type's
// package, so an alias reads as a mismatch. That is the one piece of Spec data
// reflection genuinely cannot verify. It costs nothing, because GoType exists
// only so the emitter can print a type annotation -- an interpreted host holds
// the value and never needs the name.
func TestSpecGoTypeNameMatchesTheCtorReturn(t *testing.T) {
	for name, sp := range allSpecs(t) {
		fn, ok := fyneCtors[sp.New.Path+"."+sp.New.Name]
		if !ok {
			continue // reported by the table test
		}
		got := reflect.TypeOf(fn).Out(0)
		if got.Kind() == reflect.Pointer {
			got = got.Elem()
		}
		want := strings.TrimPrefix(sp.GoType.Name, "*")
		if got.Name() != want {
			t.Errorf("%s: Spec GoType names %s, ctor returns %s", name, want, got.Name())
		}
		if got.PkgPath() != sp.GoType.Path {
			t.Logf("%s: %s is an alias -- Spec says %s, reflect says %s",
				name, want, sp.GoType.Path, got.PkgPath())
		}
	}
}

// TestEverySpecCtorAcceptsItsOwnArgs calls each constructor with the arguments
// its own Spec supplies. This is not an interpreter question: the emitter
// splices those same arguments into generated Go, so a ctor that rejects them
// is a program that panics at startup on every target.
func TestEverySpecCtorAcceptsItsOwnArgs(t *testing.T) {
	for name, sp := range allSpecs(t) {
		if _, err := construct(sp); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestEverySpecSetterAndHandlerResolves is the check that replaces the
// compile-time guarantee codegen gives. A Setter naming a method that does not
// exist, or a Handler naming a missing field, is caught here rather than in a
// user's build.
func TestEverySpecSetterAndHandlerResolves(t *testing.T) {
	withDriver(t)
	for name, sp := range allSpecs(t) {
		w, err := construct(sp)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		wt := reflect.TypeOf(w)
		for prop, method := range sp.Setters {
			m, ok := wt.MethodByName(method)
			if !ok {
				t.Errorf("%s: setter %q -> %s.%s does not exist", name, prop, wt, method)
				continue
			}
			// One value in, nothing out: the shape every value prop assigns
			// through. Receiver counts as arg 0.
			if m.Type.NumIn() != 2 {
				t.Errorf("%s: setter %s.%s takes %d args, want 1", name, wt, method, m.Type.NumIn()-1)
			}
		}
		if len(sp.Handlers) == 0 {
			continue
		}
		if wt.Kind() != reflect.Pointer || wt.Elem().Kind() != reflect.Struct {
			t.Errorf("%s: declares handlers but %s is not a pointer to struct", name, wt)
			continue
		}
		for event, h := range sp.Handlers {
			f, ok := wt.Elem().FieldByName(h.Field)
			if !ok {
				t.Errorf("%s: handler %q -> %s.%s does not exist", name, event, wt, h.Field)
				continue
			}
			if f.Type.Kind() != reflect.Func {
				t.Errorf("%s: handler field %s.%s is %s, not a func", name, wt, h.Field, f.Type)
			}
		}
	}
}

// TestSignatureIsRecoverableFromTheGoType is the finding that makes the
// interpreted adapter smaller than the emitter: fyneHandler.Signature exists so
// the emitter can PRINT a Go func literal. Reflection does not need it -- the
// field's own type is the signature, exactly.
func TestSignatureIsRecoverableFromTheGoType(t *testing.T) {
	checked := 0
	for name, sp := range allSpecs(t) {
		w, err := construct(sp)
		if err != nil {
			continue
		}
		wt := reflect.TypeOf(w)
		if wt.Kind() != reflect.Pointer {
			continue
		}
		for event, h := range sp.Handlers {
			f, ok := wt.Elem().FieldByName(h.Field)
			if !ok {
				continue
			}
			if got := renderFuncSig(f.Type, h.Param); got != h.Signature {
				t.Errorf("%s/%s: reflected signature %q, Spec says %q", name, event, got, h.Signature)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no handler signatures compared; the walk found nothing")
	}
}

// TestDriveAWidgetFromSpecDataAlone is the end-to-end: build, set a prop, bind
// a callback and fire it, using nothing but the Spec and reflection. This is
// what the interpreted host's Create/SetProp/Bind ops reduce to.
func TestDriveAWidgetFromSpecDataAlone(t *testing.T) {
	withDriver(t)
	sp := allSpecs(t)["button"]
	if sp == nil {
		t.Fatal("no spec for button")
	}
	w, err := construct(sp)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}

	// SetProp("text", "Click me")
	if err := setProp(w, sp, "text", "Click me"); err != nil {
		t.Fatalf("setProp: %v", err)
	}
	if got := reflect.ValueOf(w).Elem().FieldByName("Text").String(); got != "Click me" {
		t.Errorf("Text = %q, want %q", got, "Click me")
	}

	// Bind("click", handler) then invoke the widget's own callback, which is
	// what c.<id>.click() must do for the interpreter to be the sixth target
	// driving a real event rather than a synthetic one.
	fired := 0
	if err := bind(w, sp, "click", func([]reflect.Value) []reflect.Value {
		fired++
		return nil
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	reflect.ValueOf(w).Elem().FieldByName("OnTapped").Call(nil)
	if fired != 1 {
		t.Errorf("handler fired %d times, want 1", fired)
	}
}

// withDriver installs a fyne app for the duration of the test.
//
// A widget is a plain struct until something touches it: SetText calls Refresh,
// which asks the cache for a renderer, which needs a driver. So an interpreted
// host cannot be a library that merely builds widgets -- it must own a fyne app
// and run on its thread, which is why Host.Run owns the event loop rather than
// being called into.
func withDriver(t *testing.T) {
	t.Helper()
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
}

// --- the adapter itself: everything below is Spec-driven, nothing names a
// fyne type.

// construct builds the widget from Spec.New and Spec.Args. Args carry either a
// Prop (supplied by the instantiation, zero here) or Raw Go source, which is
// the fallback when a constructor needs an argument the caller omitted.
func construct(sp *fyneSpec) (w any, err error) {
	fn, ok := fyneCtors[sp.New.Path+"."+sp.New.Name]
	if !ok {
		return nil, fmt.Errorf("no adapter entry for %s.%s", sp.New.Path, sp.New.Name)
	}
	fv := reflect.ValueOf(fn)
	ft := fv.Type()

	// A container ctor is variadic (`NewVBox(objects ...CanvasObject)`) and its
	// Spec supplies nothing: children arrive later through Add. So the Spec's
	// args fill the fixed positions only.
	fixed := ft.NumIn()
	if ft.IsVariadic() {
		fixed--
	}
	if len(sp.Args) != fixed {
		return nil, fmt.Errorf("%s takes %d fixed args, Spec supplies %d", sp.New.Name, fixed, len(sp.Args))
	}
	args := make([]reflect.Value, len(sp.Args))
	for i, a := range sp.Args {
		v, aerr := argValue(a, ft.In(i))
		if aerr != nil {
			return nil, fmt.Errorf("arg %d: %w", i, aerr)
		}
		args[i] = v
	}

	// A ctor that dereferences one of its own arguments panics rather than
	// returning an error, and one Spec does exactly that. Recovering here is
	// what turns it into a reportable fact instead of a dead test binary.
	defer func() {
		if r := recover(); r != nil {
			w, err = nil, fmt.Errorf("%s panics on the args its own Spec supplies (%v): %v",
				sp.New.Name, sp.Args, r)
		}
	}()
	return fv.Call(args)[0].Interface(), nil
}

// argValue evaluates one ctor argument. A Prop arg is the instantiation's
// business and arrives as the type's zero here. A Raw arg is Go source spliced
// verbatim by the emitter; reflection cannot evaluate Go, so only the literal
// forms the Specs actually use are accepted -- anything else is a Spec the
// interpreter cannot honour, and saying so is the point.
func argValue(a fyneArg, want reflect.Type) (reflect.Value, error) {
	if a.Prop != "" || a.Raw == "" || a.Raw == "nil" {
		return reflect.Zero(want), nil
	}
	if s, err := strconv.Unquote(a.Raw); err == nil {
		v := reflect.ValueOf(s)
		if !v.Type().AssignableTo(want) {
			return reflect.Value{}, fmt.Errorf("Raw %s is not assignable to %s", a.Raw, want)
		}
		return v, nil
	}
	return reflect.Value{}, fmt.Errorf("Raw %q is Go source the interpreter cannot evaluate", a.Raw)
}

func setProp(w any, sp *fyneSpec, prop string, val any) error {
	method, ok := sp.Setters[prop]
	if !ok {
		return nil // no fyne surface to reach; the emitter drops it too
	}
	m := reflect.ValueOf(w).MethodByName(method)
	if !m.IsValid() {
		return fmt.Errorf("%T has no method %s", w, method)
	}
	want := m.Type().In(0)
	v := reflect.ValueOf(val)
	if !v.Type().AssignableTo(want) {
		if !v.CanConvert(want) {
			return fmt.Errorf("cannot pass %T to %s(%s)", val, method, want)
		}
		v = v.Convert(want)
	}
	m.Call([]reflect.Value{v})
	return nil
}

// bind assigns fn to the widget's callback field. The field's own type is the
// signature, so MakeFunc needs nothing from the Spec beyond the field name.
func bind(w any, sp *fyneSpec, event string, fn func([]reflect.Value) []reflect.Value) error {
	h, ok := sp.Handlers[event]
	if !ok {
		return fmt.Errorf("no handler declared for %q", event)
	}
	f := reflect.ValueOf(w).Elem().FieldByName(h.Field)
	if !f.IsValid() {
		return fmt.Errorf("%T has no field %s", w, h.Field)
	}
	f.Set(reflect.MakeFunc(f.Type(), fn))
	return nil
}

// renderFuncSig prints a reflect func type the way fyne.sngl's Signature
// spells it, so the two can be compared.
func renderFuncSig(ft reflect.Type, param string) string {
	var b strings.Builder
	b.WriteString("func(")
	for i := 0; i < ft.NumIn(); i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		if param != "" {
			b.WriteString(param + " ")
		}
		b.WriteString(ft.In(i).String())
	}
	b.WriteString(")")
	return b.String()
}
