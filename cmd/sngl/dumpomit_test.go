package main

import (
	"encoding/json"
	"strings"
	"testing"

	"duckfam.us/sngl/ir"
)

func TestDumpTreeMapKeyedByDeclarationsIsOneOrder(t *testing.T) {
	a, b := &ir.Param{Name: "f"}, &ir.Param{Name: "f"}
	v := struct {
		Owned []*ir.Param
		Sites map[*ir.Param]string
	}{
		Owned: []*ir.Param{a, b},
		Sites: map[*ir.Param]string{a: "first", b: "second"},
	}
	var want string
	for i := range 64 {
		got, err := json.Marshal(dumpTree(v, nil, nil, false, false))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = string(got)
			if !strings.Contains(want, "first") || !strings.Contains(want, "second") {
				t.Fatalf("an entry is missing: %s", want)
			}
			continue
		}
		if string(got) != want {
			t.Fatalf("dump %d differs:\n%s\n%s", i, got, want)
		}
	}
	if !strings.Contains(want, `\"(id)\":1}"`) || !strings.Contains(want, `\"(id)\":2}"`) {
		t.Errorf("each key should name its parameter's id: %s", want)
	}
}

func TestDumpTreeMapWithManyLikeKeysIsOneOrder(t *testing.T) {
	f1, f2, g := &ir.Param{Name: "f"}, &ir.Param{Name: "f"}, &ir.Param{Name: "g"}
	x, y, z := &ir.Var{Name: "x"}, &ir.Var{Name: "y"}, &ir.Var{Name: "z"}
	v := struct {
		Owned []*ir.Param
		Held  map[*ir.Param]*ir.Var
	}{
		Owned: []*ir.Param{f1, f2, g},
		Held:  map[*ir.Param]*ir.Var{f1: x, f2: y, g: z},
	}
	var want string
	for i := range 200 {
		got, err := json.Marshal(dumpTree(v, nil, nil, false, false))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = string(got)
			continue
		}
		if string(got) != want {
			t.Fatalf("dump %d differs:\n%s\n%s", i, got, want)
		}
	}
}
