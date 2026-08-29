package sngltest_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/sngltest"
	"git.duckfam.us/jonathan/sngl/sngltest/testdata/marshalpkg"
)

// fake stands in for *testing.T so a check that is supposed to fail can be
// read rather than failing this test.
type fake struct{ msgs []string }

func (f *fake) Errorf(format string, args ...any) {
	f.msgs = append(f.msgs, fmt.Sprintf(format, args...))
}

func (f *fake) Helper() {}

// only returns the one message the check reported.
func (f *fake) only(t *testing.T) string {
	t.Helper()
	if len(f.msgs) != 1 {
		t.Fatalf("reported %d failures, want 1: %q", len(f.msgs), f.msgs)
	}
	return f.msgs[0]
}

// wants asserts the message carries every fragment a reader needs to act on
// it. A failure that does not name both halves sends the reader to the wrong
// one.
func wants(t *testing.T, msg string, fragments ...string) {
	t.Helper()
	for _, f := range fragments {
		if !strings.Contains(msg, f) {
			t.Errorf("message does not mention %q:\n%s", f, msg)
		}
	}
}

// A struct with no marshalling of its own: the encoder walks it by reflection
// and the importer declares it, so the halves agree by construction.
func TestPlainStruct(t *testing.T) {
	sngltest.CheckMarshal(t, marshalpkg.Item{Name: "alpha", Value: 1})
}

// A MarshalSNGL that writes what the importer types the receiver as.
func TestAgreeingMarshaler(t *testing.T) {
	sngltest.CheckMarshal(t, marshalpkg.Tag("news"), marshalpkg.Tag(`quote"brace{`))
}

// The registered pair: consteval.Register writes `250ms` and
// golang.RegisterType types it as the duration unit. Neither half is in this
// package or the type's own, which is the case a user cannot inspect.
func TestRegisteredType(t *testing.T) {
	sngltest.CheckMarshal(t, 250*time.Millisecond, 1500*time.Microsecond, time.Duration(0))
}

// The disagreement this package exists to catch: base64 is a string, and the
// importer makes list<int> of a []byte. The message has to name both halves,
// or the reader goes and looks at the wrong one.
func TestMismatchedMarshaler(t *testing.T) {
	f := &fake{}
	sngltest.CheckMarshal[marshalpkg.Blob](f, marshalpkg.Blob{1, 2})
	msg := f.only(t)
	wants(t, msg,
		"marshalpkg.Blob",                        // the type
		`"AQI="`,                                 // what the marshaller wrote
		"Blob.MarshalSNGL",                       // which half wrote it
		"list<int>",                              // what the importer made of it
		"codegen/scheme/golang",                  // which half decided that
		"not assignable",                         // the checker's own verdict
		"golang.RegisterType",                    // the other way out
		"sngl/sngltest/testdata/marshalpkg.Blob", // the key it takes
	)
}

// A value is checked on its own: a table does not stop at the first failure.
func TestEachValueIsReported(t *testing.T) {
	f := &fake{}
	sngltest.CheckMarshal(f, marshalpkg.Blob{1}, marshalpkg.Blob{2})
	if len(f.msgs) != 2 {
		t.Errorf("two bad values reported %d failures, want 2", len(f.msgs))
	}
}

// A type the importer models as nothing fails before any value is encoded:
// whatever it marshals to, no declaration can mention it.
func TestUnmodelledType(t *testing.T) {
	f := &fake{}
	sngltest.CheckType[marshalpkg.Chan](f)
	wants(t, f.only(t), "marshalpkg.Chan", "dyn", "unusable", "golang.RegisterType")
}

// A type with no declaration behind it cannot be resolved through the
// importer at all, and says so rather than guessing at a mapping.
func TestUnnamedType(t *testing.T) {
	f := &fake{}
	sngltest.CheckType[map[string]int](f)
	wants(t, f.only(t), "map[string]int", "has no name", "type X map[string]int")
}

// The mapping half alone, for a type whose values are awkward to build.
func TestCheckTypePasses(t *testing.T) {
	sngltest.CheckType[marshalpkg.Item](t)
	sngltest.CheckType[[]marshalpkg.Item](t)
	sngltest.CheckType[*marshalpkg.Item](t)
}

// Encode hands back the source the compiler would have parsed, for a golden
// test over the exact text.
func TestEncode(t *testing.T) {
	const itemRef = `import("go:git.duckfam.us/jonathan/sngl/sngltest/testdata/marshalpkg").Item`
	if got := sngltest.Encode(t, marshalpkg.Item{Name: "alpha", Value: 1}); got != itemRef+`{Name = "alpha", Value = 1}` {
		t.Errorf("Encode() = %s", got)
	}
	if got := sngltest.Encode(t, marshalpkg.Tag("news")); got != `"news"` {
		t.Errorf("Encode() = %s", got)
	}
}
