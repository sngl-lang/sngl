package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	fyneplat "duckfam.us/sngl/codegen/platform/fyne"
)

const generated = "../../../pkg/go/fynehost/registry_gen.go"

// TestTheCommittedRegistryIsCurrent is the whole reason to generate it. A
// hand-edited or stale registry is a widget fyne.sngl declares that no window
// can build, and nothing else would notice: the platform's own tests check the
// Specs, and the host's tests check the widgets they happen to name.
func TestTheCommittedRegistryIsCurrent(t *testing.T) {
	want, err := Generate()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	got, err := os.ReadFile(filepath.FromSlash(generated))
	if err != nil {
		t.Fatalf("reading the committed registry: %v", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("pkg/go/fynehost/registry_gen.go is out of date; run `go generate ./pkg/go/fynehost/`")
	}
}

// TestEveryOverriddenComponentsConstructorIsPresent: fyne.sngl declares a body
// for each of these and each body names a constructor. One missing is a
// component that checks, generates and runs on a compiled target while silently
// rendering nothing when interpreted.
//
// It is the constructors that are checked, not the component names: which
// widget a component becomes rides on the node in its Spec, and the ctor table
// is the only thing a host is told in advance.
func TestEveryOverriddenComponentsConstructorIsPresent(t *testing.T) {
	src, err := Generate()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	pkg, err := probe()
	if err != nil {
		t.Fatalf("checking the probe: %v", err)
	}
	specs, err := fyneplat.WidgetSpecsFrom(pkg)
	if err != nil {
		t.Fatalf("decoding specs: %v", err)
	}
	if len(specs) < 30 {
		t.Fatalf("only %d specs decoded; this test is checking nothing", len(specs))
	}
	text := string(src)
	for _, sp := range specs {
		if sp.CtorPath == "" {
			continue
		}
		key := sp.CtorPath + "." + sp.CtorName
		if !strings.Contains(text, strconv.Quote(key)) {
			t.Errorf("%s is built by %s, which is not in the table", sp.Element, key)
		}
	}
}

// TestTheTableIsConstructorsAndNothingElse guards what the move to Spec-driven
// hosts bought: a setter or a callback field appearing here would mean a host
// was told something the node already carries.
func TestTheTableIsConstructorsAndNothingElse(t *testing.T) {
	src, err := Generate()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, leaked := range []string{"SetText", "OnTapped", "OnChanged", "Setters", "Handlers"} {
		if strings.Contains(string(src), leaked) {
			t.Errorf("the generated table mentions %q; that rides on the Spec, not here", leaked)
		}
	}
}
