package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fyneplat "git.duckfam.us/jonathan/sngl/codegen/platform/fyne"
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

// TestEveryOverriddenComponentIsInTheRegistry: the platform declares a fyne
// body for each of these, so a window must be able to build each. One missing
// is a component that checks, generates and runs on a compiled target while
// silently rendering nothing when interpreted.
func TestEveryOverriddenComponentIsInTheRegistry(t *testing.T) {
	src, err := Generate()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	text := string(src)
	for _, name := range fyneplat.StdlibComponents {
		if !strings.Contains(text, "\t\t\""+name+"\": {") {
			t.Errorf("%s has a fyne override but no registry entry", name)
		}
	}
}

// TestASetterIsKeyedByThePropACallerWrites guards the rename the generator has
// to follow. A Spec's setters are keyed by the primitive's vocabulary, and a
// host is handed the component's own prop names -- so `text` taking `value` is
// the case that fails silently if the override is not read.
func TestASetterIsKeyedByThePropACallerWrites(t *testing.T) {
	src, err := Generate()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	text := string(src)
	for _, want := range []string{
		`"text": {`,
		`Setters: map[string]string{"value": "SetText"}`, // ui.text writes value=
		`Setters: map[string]string{"label": "SetText"}`, // ui.chip writes label=
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated registry is missing %s", want)
		}
	}
	// And the primitive's own name must not survive as a key for ui.text.
	if strings.Contains(text, "\t\t\"text\": {\n\t\t\tNew:     widget.NewLabel,\n\t\t\tSetters: map[string]string{\"text\":") {
		t.Error("ui.text is keyed by the primitive's prop name; the override rename was not followed")
	}
}
