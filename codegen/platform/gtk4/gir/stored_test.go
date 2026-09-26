package gir

import (
	"reflect"
	"testing"
)

// Strip then Restore is the identity: everything Strip clears is derived from
// what it keeps.
func TestStripRestoreIsIdentity(t *testing.T) {
	want, err := ParseMinimal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseMinimal()
	if err != nil {
		t.Fatal(err)
	}
	got.Strip()
	for _, c := range got.Classes {
		for _, p := range c.Props {
			if p.IRType != nil {
				t.Fatalf("%s.%s kept its IRType through Strip", c.CType, p.Name)
			}
		}
	}
	got.Restore()
	if !reflect.DeepEqual(got, want) {
		t.Error("Restore did not rebuild what Strip cleared")
	}
}
