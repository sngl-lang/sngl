package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
)

// stubGC builds a GoIRContext suitable for translator unit tests —
// no real package context, just enough machinery to emit Go strings.
func stubGC() *golang.GoIRContext {
	return golang.NewIRContext(nil)
}

func TestFyneTranslator_OnCreateNode_Text(t *testing.T) {
	var fields []string
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(name, goType string) {
		fields = append(fields, name+" "+goType)
	})
	got := tr.OnCreateNode("__n0", "text")
	if !strings.Contains(got, "m.__n0 = widget.NewLabel(") {
		t.Errorf("expected widget.NewLabel constructor; got: %s", got)
	}
	want := "__n0 *widget.Label"
	if len(fields) != 1 || fields[0] != want {
		t.Errorf("expected field registration %q; got: %v", want, fields)
	}
}

func TestFyneTranslator_OnCreateNode_UnknownTag(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnCreateNode("__n0", "wibble")
	if got != "" {
		t.Errorf("expected empty emission for unknown tag; got: %s", got)
	}
}

func TestFyneTranslator_OnAppendChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnAppendChild("parent", "__n0")
	want := "parent.Add(m.__n0)\n"
	if got != want {
		t.Errorf("OnAppendChild: got %q, want %q", got, want)
	}
}

func TestFyneTranslator_OnRemoveChild(t *testing.T) {
	tr := newFyneTranslator(stubGC(), platformBlueprints(), func(_, _ string) {})
	got := tr.OnRemoveChild("parent", "__entry")
	want := "parent.Remove(__entry)\n"
	if got != want {
		t.Errorf("OnRemoveChild: got %q, want %q", got, want)
	}
}
