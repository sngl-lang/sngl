package consteval

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// flush runs Emit for each key and returns the document Flush wrote.
func flush(t *testing.T, emit func()) string {
	t.Helper()
	reset()
	out := filepath.Join(t.TempDir(), "out.sngl")
	t.Setenv(OutEnv, out)
	emit()
	if err := Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading %s: %v", out, err)
	}
	return string(data)
}

// resultLine returns the value recorded under key, or "" if the key
// was omitted.
func resultLine(doc, key string) string {
	for line := range strings.SplitSeq(doc, "\n") {
		if rest, ok := strings.CutPrefix(line, key+Sep); ok {
			return rest
		}
	}
	return ""
}

// itemRef is how a value of item names its own type: the package reflect
// reports for it, and the name in that package.
const itemRef = `import("go://git.duckfam.us/jonathan/sngl/pkg/go/consteval").item`

type item struct {
	Name  string
	Value int
	skip  string //nolint:unused // asserts unexported fields are omitted
}

type celsius float64

// A Marshaler hands back bytes it may still own; the encoder must copy them.
func (c celsius) MarshalSNGL() ([]byte, error) { return []byte("42degC"), nil }

type notMine struct{ N int }

func TestEncode(t *testing.T) {
	cases := []struct {
		key  string
		val  any
		want string
	}{
		{"s", "hi", `"hi"`},
		{"esc", "a\"b\\c\nd{e}", `"a\"b\\c\nd\{e\}"`},
		{"unicode", "héllo — ☃", `"héllo — ☃"`},
		{"ctl", "a\x01b", `"a\x01b"`},
		{"i", 7, "7"},
		{"neg", -7, "-7"},
		{"u", uint16(7), "7"},
		{"f", 2.5, "2.5"},
		{"f32", float32(0.1), "0.1"},
		// A float that lands on a whole number stays a float: written as `2`
		// it reads as an int, which is the loss the checked-results path was
		// built to stop.
		{"fwhole", 2.0, "2.0"},
		{"b", true, "true"},
		{"nilptr", (*item)(nil), "null"},
		{"nilslice", []string(nil), "null"},
		{"emptyslice", []string{}, "[]"},
		{"list", []int{1, 2, 3}, "[1, 2, 3]"},
		{"nested", [][]int{{1}, {2}}, "[[1], [2]]"},
		// The go:// importer types []byte as list<int>, so a base64 string
		// could never check against the declared type.
		{"bytes", []byte("hi"), "[104, 105]"},
		{"m", map[string]int{"b": 2, "a": 1}, `{"a" = 1, "b" = 2}`},
		{"mint", map[int]string{2: "b", 1: "a"}, `{1 = "a", 2 = "b"}`},
		{"nilmap", map[string]int(nil), "null"},
		{"st", item{Name: "alpha", Value: 1, skip: "x"}, itemRef + `{Name = "alpha", Value = 1}`},
		{"stlist", []item{{Name: "a"}}, `[` + itemRef + `{Name = "a", Value = 0}]`},
		{"ptr", &item{Name: "p"}, itemRef + `{Name = "p", Value = 0}`},
		{"anon", struct{ A int }{3}, `{A = 3}`},
		{"marshaler", celsius(1), "42degC"},
		{"dur", 1500 * time.Millisecond, "1500ms"},
		{"durfrac", 1500 * time.Microsecond, "1.5ms"},
		{"tim", time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC), `"2026-03-12T10:00:00Z"`},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			doc := flush(t, func() { Emit(tc.key, tc.val) })
			if got := resultLine(doc, tc.key); got != tc.want {
				t.Errorf("Emit(%v) = %s, want %s", tc.val, got, tc.want)
			}
		})
	}
}

// A type the emitting code does not own gets its form from Register.
func TestRegister(t *testing.T) {
	// The generic form infers the type and hands the callback a notMine, not
	// an any it has to assert.
	Register(func(v notMine) ([]byte, error) {
		return AppendQuote(nil, "n"+strconv.Itoa(v.N)), nil
	})
	doc := flush(t, func() { Emit("reg", notMine{N: 3}) })
	if got := resultLine(doc, "reg"); got != `"n3"` {
		t.Errorf("registered encoder not used: got %s", got)
	}
}

// A failed key is absent from the document, which is how the compiler learns
// the value could not be produced.
func TestFailOmitsKey(t *testing.T) {
	doc := flush(t, func() {
		Emit("ok", 1)
		Fail("bad", errors.New("nope"))
	})
	if resultLine(doc, "ok") != "1" {
		t.Error("surviving key lost")
	}
	if strings.Contains(doc, "bad") {
		t.Errorf("failed key present in document:\n%s", doc)
	}
}

// A value that points at itself has no SNGL form. Without the depth cap the
// encoder overflows the stack, which the generated program's recover cannot
// catch, so the round writes no results file at all.
func TestCyclicValueFailsOneKey(t *testing.T) {
	type node struct {
		Name string
		Next *node
	}
	n := &node{Name: "a"}
	n.Next = n
	doc := flush(t, func() {
		Emit("cyc", n)
		Emit("ok", "yes")
	})
	if strings.Contains(doc, "cyc") {
		t.Errorf("cyclic value emitted:\n%s", doc)
	}
	if resultLine(doc, "ok") != `"yes"` {
		t.Error("cyclic value took down an unrelated key")
	}
}

// A value with no SNGL form fails only its own key.
func TestUnencodableValueFailsOneKey(t *testing.T) {
	doc := flush(t, func() {
		Emit("fn", func() {})
		Emit("ok", "yes")
	})
	if strings.Contains(doc, "fn") {
		t.Errorf("unencodable value emitted:\n%s", doc)
	}
	if resultLine(doc, "ok") != `"yes"` {
		t.Error("unencodable value took down an unrelated key")
	}
}

func TestFlushWithoutOutEnv(t *testing.T) {
	reset()
	t.Setenv(OutEnv, "")
	if err := Flush(); err == nil {
		t.Fatal("Flush must fail when " + OutEnv + " is unset")
	}
}

// shared hands out the same buffer every time, as a Marshaler is allowed to.
type shared struct{ buf []byte }

func (s *shared) MarshalSNGL() ([]byte, error) { return s.buf, nil }

// The encoder must copy what a Marshaler returns: a later write through the
// caller's own slice cannot be allowed to rewrite an already-emitted value.
func TestMarshalerBufferIsNotRetained(t *testing.T) {
	s := &shared{buf: []byte(`"one"`)}
	doc := flush(t, func() {
		Emit("a", s)
		copy(s.buf, `"two"`)
		Emit("b", s)
	})
	if got := resultLine(doc, "a"); got != `"one"` {
		t.Errorf("first value became %s: the Marshaler's slice was retained", got)
	}
	if got := resultLine(doc, "b"); got != `"two"` {
		t.Errorf("second value = %s, want %q", got, `"two"`)
	}
}

// SNGL has no exponent syntax, so a value Go would print as 1e+30 has to be
// written out in full — and one that lands on a whole number still needs a
// fractional part, or the literal reads as an int.
func TestFloatsAvoidExponentSyntax(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{1e30, "1000000000000000000000000000000.0"},
		{1e-9, "0.000000001"},
		{0.1, "0.1"},
		{float32(0.1), "0.1"},
		{-2.5, "-2.5"},
		{0.0, "0.0"},
		{1e21, "1000000000000000000000.0"},
	} {
		got, err := Encode(tc.v)
		if err != nil {
			t.Errorf("Encode(%v): %v", tc.v, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("Encode(%v) = %s, want %s", tc.v, got, tc.want)
		}
		if strings.ContainsAny(string(got), "eE") {
			t.Errorf("Encode(%v) = %s, which SNGL cannot lex", tc.v, got)
		}
	}
}

// prettyPrinted spans several lines, as a Marshaler returning SNGL source is
// free to.
type prettyPrinted struct{}

func (prettyPrinted) MarshalSNGL() ([]byte, error) { return []byte("{\n  a = 1,\n}"), nil }

// The record is one line. A value written over several would arrive as records
// with no key in them, failing the whole batch with a message about
// corruption; rejecting it at Emit costs the one key and names it.
func TestMultiLineValueFailsOneKey(t *testing.T) {
	doc := flush(t, func() {
		Emit("multi", prettyPrinted{})
		Emit("ok", "yes")
	})
	if strings.Contains(doc, "multi") {
		t.Errorf("a multi-line value was written:\n%s", doc)
	}
	if resultLine(doc, "ok") != `"yes"` {
		t.Error("a multi-line value took down an unrelated key")
	}
	if failures["multi"] == nil {
		t.Error("the offending key was not recorded as a failure")
	}
}
