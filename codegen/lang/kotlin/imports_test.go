package kotlin

import (
	"slices"
	"strings"
	"testing"
)

// Name hands out each simple name once: the first import of it keeps it, a
// later one of another declaration is aliased, and one the file declares is
// never taken by an import at all.
func TestNameAliasesASecondClaim(t *testing.T) {
	kc := NewIRContext(nil)
	kc.Reserve("Card")
	if got := kc.Name("androidx.compose.material3.Text"); got != "Text" {
		t.Errorf("first Text = %q, want Text", got)
	}
	if got := kc.Name("androidx.compose.material3.Text"); got != "Text" {
		t.Errorf("Text again = %q, want the same spelling", got)
	}
	if got := kc.Name("com.example.Text"); got != "Text_2" {
		t.Errorf("second Text = %q, want Text_2", got)
	}
	if got := kc.Name("androidx.compose.material3.Card"); got != "Card_2" {
		t.Errorf("Card beside a declared Card = %q, want Card_2", got)
	}
	kc.RequireImport("androidx.compose.ui.graphics.Color as ComposeColor")
	if got := kc.Name("androidx.compose.ui.graphics.Color"); got != "ComposeColor" {
		t.Errorf("Color after its aliased import = %q, want ComposeColor", got)
	}
	want := []string{
		"androidx.compose.material3.Text",
		"com.example.Text as Text_2",
		"androidx.compose.material3.Card as Card_2",
		"androidx.compose.ui.graphics.Color as ComposeColor",
	}
	if got := kc.Imports(); !slices.Equal(got, want) {
		t.Errorf("imports = %q, want %q", got, want)
	}
}

// A caller that requires an import spells the name bare, so one whose name is
// already another's is refused rather than emitted to be read as the other.
func TestRequireImportRefusesATakenName(t *testing.T) {
	for _, tc := range []struct{ name, claim string }{
		{"another import", "com.example.Text"},
		{"a declaration", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kc := NewIRContext(nil)
			if tc.claim == "" {
				kc.Reserve("Text")
			} else {
				kc.RequireImport(tc.claim)
			}
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(r.(string), "spell it through Name") {
					t.Errorf("recover() = %v, want the refusal", r)
				}
			}()
			kc.RequireImport("androidx.compose.material3.Text")
		})
	}
}
