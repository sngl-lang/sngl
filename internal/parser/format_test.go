package parser

import (
	"strings"
	"testing"
)

// roundTrip parses src, formats it, parses again, formats again,
// and asserts the two formatted outputs are identical.
func roundTrip(t *testing.T, src string) string {
	t.Helper()
	doc, err := Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := Format(doc)

	// Round-trip: parse formatted output and format again.
	doc2, err := Parse("test.sngl", []byte(got))
	if err != nil {
		t.Fatalf("parse formatted: %v\n---\n%s", err, got)
	}
	got2 := Format(doc2)
	if got != got2 {
		t.Errorf("round-trip mismatch:\n--- first ---\n%s\n--- second ---\n%s", got, got2)
	}
	return got
}

func assertFormat(t *testing.T, src, want string) {
	t.Helper()
	got := roundTrip(t, src)
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("format mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatImport(t *testing.T) {
	assertFormat(t,
		`import "widgets/counter"`,
		`import "widgets/counter"`)

	assertFormat(t,
		`import c => "widgets/counter"`,
		`import c => "widgets/counter"`)
}

func TestFormatVar(t *testing.T) {
	assertFormat(t,
		`var count int`,
		`var count int`)

	assertFormat(t,
		`var name string = "hello"`,
		`var name string = "hello"`)
}

func TestFormatConst(t *testing.T) {
	assertFormat(t,
		`const pi float = 3.14`,
		`const pi float = 3.14`)
}

func TestFormatConstGrouped(t *testing.T) {
	assertFormat(t,
		"const (\n    a = 1\n    b = 2\n)",
		"const (\n    a = 1\n    b = 2\n)")
}

func TestFormatStruct(t *testing.T) {
	assertFormat(t,
		"struct Todo {\n    title string\n    done bool\n}",
		"struct Todo {\n    title string\n    done bool\n}")
}

func TestFormatEmptyStruct(t *testing.T) {
	assertFormat(t,
		`struct Point {}`,
		`struct Point {}`)
}

func TestFormatEnum(t *testing.T) {
	assertFormat(t,
		`enum Color { red, green, blue }`,
		`enum Color { red, green, blue }`)
}

func TestFormatEnumMultiline(t *testing.T) {
	assertFormat(t,
		"enum Status {\n    active\n    inactive\n}",
		"enum Status {\n    active\n    inactive\n}")
}

func TestFormatUnit(t *testing.T) {
	assertFormat(t,
		`unit length { px, em, rem }`,
		`unit length { px, em, rem }`)
}

func TestFormatFunc(t *testing.T) {
	assertFormat(t,
		`func add(a int, b int) => a + b`,
		`func add(a int, b int) => a + b`)
}

func TestFormatFuncBlock(t *testing.T) {
	assertFormat(t,
		"func greet(name string) string {\n    return name\n}",
		"func greet(name string) string {\n    return name\n}")
}

func TestFormatMethod(t *testing.T) {
	assertFormat(t,
		`func int.double(x int) => x + x`,
		`func int.double(x int) => x + x`)
}

func TestFormatComponent(t *testing.T) {
	assertFormat(t,
		"component Counter(label string, :count int, @click) {\n}",
		"component Counter(label string, :count int, @click) {\n}")
}

func TestFormatVisualNode(t *testing.T) {
	assertFormat(t,
		"text(value=\"hello\")",
		"text(value=\"hello\")")
}

func TestFormatVisualNodeBlock(t *testing.T) {
	assertFormat(t,
		"hbox {\n    text(value=\"a\")\n    text(value=\"b\")\n}",
		"hbox {\n    text(value=\"a\")\n    text(value=\"b\")\n}")
}

func TestFormatIf(t *testing.T) {
	assertFormat(t,
		"if x > 0 {\n    text(value=\"pos\")\n}",
		"if x > 0 {\n    text(value=\"pos\")\n}")
}

func TestFormatIfElse(t *testing.T) {
	assertFormat(t,
		"if x > 0 {\n    text(value=\"pos\")\n} else {\n    text(value=\"neg\")\n}",
		"if x > 0 {\n    text(value=\"pos\")\n} else {\n    text(value=\"neg\")\n}")
}

func TestFormatFor(t *testing.T) {
	assertFormat(t,
		"for item = items {\n    text(value=item)\n}",
		"for item = items {\n    text(value=item)\n}")
}

func TestFormatForKeyValue(t *testing.T) {
	assertFormat(t,
		"for i, item = items {\n    text(value=item)\n}",
		"for i, item = items {\n    text(value=item)\n}")
}

func TestFormatExpressions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"binary", `const x = 1 + 2`, `const x = 1 + 2`},
		{"unary", `const x = !true`, `const x = !true`},
		{"ternary", `const x = a ? b : c`, `const x = a ? b : c`},
		{"call", `const x = add(1, 2)`, `const x = add(1, 2)`},
		{"index", `const x = items[0]`, `const x = items[0]`},
		{"select", `const x = foo.bar`, `const x = foo.bar`},
		{"list", `const x = [1, 2, 3]`, `const x = [1, 2, 3]`},
		{"paren", `const x = (1 + 2)`, `const x = (1 + 2)`},
		{"spread", `const x = [...items]`, `const x = [...items]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertFormat(t, tt.src, tt.want)
		})
	}
}

func TestFormatAssign(t *testing.T) {
	assertFormat(t,
		"func foo() {\n    x = 1\n}",
		"func foo() {\n    x = 1\n}")
}

func TestFormatToggle(t *testing.T) {
	assertFormat(t,
		"func foo() {\n    active!!\n}",
		"func foo() {\n    active!!\n}")
}

func TestFormatEmit(t *testing.T) {
	assertFormat(t,
		"func foo() {\n    @click\n}",
		"func foo() {\n    @click\n}")
}

func TestFormatEventHandler(t *testing.T) {
	assertFormat(t,
		"button(text=\"ok\", @click { count = count + 1 })",
		"button(text=\"ok\", @click { count = count + 1 })")
}

func TestFormatDisabled(t *testing.T) {
	assertFormat(t,
		`/- var x int`,
		`/- var x int`)
}

func TestFormatComment(t *testing.T) {
	assertFormat(t,
		`// hello`,
		`// hello`)
}

func TestFormatBlankLines(t *testing.T) {
	got := roundTrip(t, "var x int\n\nvar y int\n")
	if !strings.Contains(got, "\n\n") {
		t.Error("expected blank line preserved between vars")
	}
}

func TestFormatLambda(t *testing.T) {
	assertFormat(t,
		`const f = func(x int) => x + 1`,
		`const f = func(x int) => x + 1`)
}

func TestFormatTypeAnnotations(t *testing.T) {
	assertFormat(t,
		`var items list<int>`,
		`var items list<int>`)
}

func TestFormatFuncType(t *testing.T) {
	assertFormat(t,
		`var f func(int, string) bool`,
		`var f func(int, string) bool`)
}

func TestFormatGenericFunc(t *testing.T) {
	assertFormat(t,
		`func identity<T>(x T) => x`,
		`func identity<T>(x T) => x`)
}

func TestFormatReturn(t *testing.T) {
	assertFormat(t,
		"func foo() int {\n    return 42\n}",
		"func foo() int {\n    return 42\n}")
}

func TestFormatCompoundAssign(t *testing.T) {
	assertFormat(t,
		"func foo() {\n    x += 1\n}",
		"func foo() {\n    x += 1\n}")
}
