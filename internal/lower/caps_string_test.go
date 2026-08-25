package lower

import (
	"reflect"
	"strings"
	"testing"
)

// Caps.String names the enabled lowering passes; it is what `dump --stage
// lowered` and the pipeline log report. It is a hand-written run of ifs, one
// per field, so a new capability is reported only if someone remembers to add
// one — and ReactiveCanvas had been missed, so every dump silently understated
// which passes ran.
//
// Setting every field and checking each name appears turns that into a failure
// that names the missing field.
func TestCapsStringNamesEveryField(t *testing.T) {
	rv := reflect.New(reflect.TypeFor[Caps]()).Elem()
	for _, field := range rv.Fields() {
		if field.Kind() == reflect.Bool {
			field.SetBool(true)
		}
	}
	all := rv.Interface().(Caps)
	got := all.String()
	parts := strings.Split(got, ",")
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		seen[p] = true
	}

	rt := reflect.TypeFor[Caps]()
	for f := range rt.Fields() {
		if f.Type.Kind() != reflect.Bool {
			continue
		}
		if !seen[f.Name] {
			t.Errorf("Caps.String() omits %s\n"+
				"\tEvery capability must name itself, or a dump reports the wrong\n"+
				"\tset of passes. Add an arm for it in caps.go.", f.Name)
		}
	}
}
