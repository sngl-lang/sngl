package gencache

import (
	"reflect"
	"strings"
	"testing"
)

type valueInner struct {
	Name  string
	Count int
}

type valueOuter struct {
	Title   string
	Neg     int
	Big     uint64
	Ratio   float64
	On      bool
	Tags    []string
	Empty   []int
	Nested  valueInner
	Ptr     *valueInner
	NilPtr  *valueInner
	ByName  map[string]*valueInner
	Members map[string]string
}

// What EncodeValue writes, DecodeValue reads back into the same Go value --
// strings that need escaping, negative and unsigned numbers, nil pointers and
// slices, and maps of pointers included.
func TestValueRoundTrip(t *testing.T) {
	want := valueOuter{
		Title:   `a "quoted" {brace} \ and` + "\nnewline",
		Neg:     -42,
		Big:     1 << 63,
		Ratio:   0.125,
		On:      true,
		Tags:    []string{"x", "y"},
		Nested:  valueInner{Name: "n", Count: 3},
		Ptr:     &valueInner{Name: "p"},
		ByName:  map[string]*valueInner{"b": {Name: "b", Count: 2}, "a": {Name: "a"}},
		Members: map[string]string{"vertical": "GTK_ORIENTATION_VERTICAL"},
	}
	src, err := EncodeValue(want)
	if err != nil {
		t.Fatal(err)
	}
	var got valueOuter
	if err := DecodeValue(src, &got); err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip:\ngot  %#v\nwant %#v\nvia  %s", got, want, src)
	}
}

// A field the Go type does not have is an error rather than a field dropped:
// a stored value written for a different shape is one to produce again.
func TestValueUnknownFieldIsAnError(t *testing.T) {
	var got valueInner
	err := DecodeValue([]byte(`valueInner{Name = "x", Gone = 1}`), &got)
	if err == nil || !strings.Contains(err.Error(), "Gone") {
		t.Errorf("err = %v, want one naming the unknown field", err)
	}
}
