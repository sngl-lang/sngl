package consteval_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/pkg/js/consteval"
)

// run drives the runtime over the given JS emit statements and returns the
// results document it wrote plus what it printed to stderr.
func run(t *testing.T, body string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, consteval.RuntimeFile), consteval.Runtime, 0o644); err != nil {
		t.Fatal(err)
	}
	main := "import * as consteval from \"./" + consteval.RuntimeFile + "\";\n" + body + "\nawait consteval.flush();\n"
	if err := os.WriteFile(filepath.Join(dir, "main.mjs"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results.sngl")
	cmd := exec.Command("node", filepath.Join(dir, "main.mjs"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), consteval.OutEnv+"="+out)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(got), stderr.String()
}

// value asserts that emitting v writes want as its SNGL source.
func value(t *testing.T, js, want string) {
	t.Helper()
	doc, stderr := run(t, "consteval.emit(\"k\", "+js+");")
	line := "k\t" + want
	if !strings.Contains(doc, line) {
		t.Errorf("%s encoded as:\n%s\nwant %s\n%s", js, doc, line, stderr)
	}
}

// unrepresentable asserts that v costs its own key and says why.
func unrepresentable(t *testing.T, js, reason string) {
	t.Helper()
	doc, stderr := run(t, "consteval.emit(\"k\", "+js+");\nconsteval.emit(\"other\", 1);")
	if strings.Contains(doc, "k\t") {
		t.Errorf("%s was encoded as %s", js, doc)
	}
	if !strings.Contains(doc, "other\t1.0") {
		t.Errorf("a neighbouring key was lost with %s:\n%s", js, doc)
	}
	if !strings.Contains(stderr, reason) {
		t.Errorf("%s failed without saying %q:\n%s", js, reason, stderr)
	}
}

// The kinds of JS value that have a SNGL form, and the form each takes.
func TestEncodes(t *testing.T) {
	for _, tc := range []struct{ js, want string }{
		// Every JS number is a double and the importer types `number` as
		// float, so an integral one still crosses with a decimal point.
		{"1", "1.0"},
		{"1.5", "1.5"},
		{"-2.25", "-2.25"},
		// SNGL has no exponent syntax — `1e21` would lex as a unit literal —
		// so the point is moved rather than written as an exponent.
		{"1e21", "1000000000000000000000.0"},
		{"1e-7", "0.0000001"},
		{"-0", "-0.0"},
		{"42n", "42"},
		{"true", "true"},
		{`"hi"`, `"hi"`},
		// undefined and null are SNGL's one absence.
		{"undefined", "null"},
		{"null", "null"},
		{"[1, 2]", "[1.0, 2.0]"},
		{"new Set([1, 1, 2])", "[1.0, 2.0]"},
		// A plain object is an anonymous struct literal: `Object` is not a
		// type the importer declared, so the expected type is what names it.
		{"({a: 1, b: \"x\"})", `{a = 1.0, b = "x"}`},
		// A class instance names its constructor, as the Go runtime names the
		// struct type.
		{"new (class Item { constructor() { this.n = 1; } })()", "Item{n = 1.0}"},
		// A Map is the map literal, quoted keys — which is what keeps
		// `{a = 1}` free to mean a struct.
		{`new Map([["a", 1], ["b", 2]])`, `{"a" = 1.0, "b" = 2.0}`},
		// A datetime is a string-domain type, as it is on the Go side.
		{`new Date(Date.UTC(2020, 0, 2, 3, 4, 5))`, `"2020-01-02T03:04:05.000Z"`},
		// Braces are escaped: an unescaped one opens an interpolation.
		{`"a{b}c"`, `"a\{b\}c"`},
		{`"tab\there"`, `"tab\there"`},
		{`"\u0001"`, `"\x01"`},
		{`"héllo"`, `"héllo"`},
		// A boxed primitive is an object with no own enumerable properties, so
		// without unwrapping it would cross as an empty struct named Number.
		{"new Number(5)", "5.0"},
		{`new String("hi")`, `"hi"`},
		{"new Boolean(true)", "true"},
	} {
		t.Run(tc.js, func(t *testing.T) { value(t, tc.js, tc.want) })
	}
}

// A value with no SNGL form fails its own key and says why, rather than being
// guessed at or taking the batch down with it.
func TestUnrepresentable(t *testing.T) {
	for _, tc := range []struct{ name, js, reason string }{
		{"NaN", "NaN", "NaN"},
		{"Infinity", "Infinity", "Infinity"},
		{"bigint out of range", "2n ** 70n", "outside the range"},
		{"function", "(() => 1)", "function"},
		{"symbol", "Symbol(\"s\")", "symbol"},
		{"typed array", "new Uint8Array([1])", "binary buffer"},
		{"regexp", "/x/", "RegExp"},
		{"invalid date", "new Date(NaN)", "invalid Date"},
		{"non-identifier key", `({"a-b": 1})`, "not a SNGL identifier"},
		{"unpaired surrogate", `"\ud800"`, "surrogate"},
		// A self-referential value has no SNGL form; the depth cap makes that
		// this key's failure rather than a stack overflow that would lose the
		// whole results file.
		{"cycle", "(() => { const o = {}; o.self = o; return o; })()", "nests deeper"},
		{"getter that throws", "({ get a() { throw new Error(\"boom\"); } })", "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) { unrepresentable(t, tc.js, tc.reason) })
	}
}

// A type can choose its own SNGL form, and a caller can choose one for a type
// it does not own.
func TestExtensionHooks(t *testing.T) {
	doc, stderr := run(t, `
class Money { constructor(c) { this.cents = c; }
  [Symbol.for("sngl.marshal")]() { return (this.cents / 100).toFixed(2); } }
class Tag { constructor(s) { this.s = s; } }
globalThis.__SNGL_CONSTEVAL__.register(Tag, (t) => JSON.stringify(t.s));
consteval.emit("a", new Money(250));
consteval.emit("b", new Tag("x"));
`)
	for _, want := range []string{"a\t2.50", "b\t\"x\""} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in:\n%s\n%s", want, doc, stderr)
		}
	}
}

// Keys are sorted so a rebuild of the same batch is byte-identical.
func TestDeterministicOrder(t *testing.T) {
	doc, _ := run(t, `consteval.emit("z", 1); consteval.emit("a", 2);`)
	if strings.Index(doc, "a\t") > strings.Index(doc, "z\t") {
		t.Errorf("keys are not sorted:\n%s", doc)
	}
}
