// Package sngltest checks that a Go type crosses into SNGL intact.
//
// The SNGL compiler evaluates pure go:// functions while it builds, and the
// value crosses in two halves that must agree. One half encodes: the child
// program writes the Go value as SNGL source with pkg/go/consteval, which
// honors a MarshalSNGL method and any registered encoder. The other half maps:
// the compiler asks the go:// importer which SNGL type the Go type is, and
// checks that source against it. Nothing in the compiler forces the halves to
// agree, and a type that writes its own MarshalSNGL can put them out of step —
// which surfaces only as a build that fails on a type the reader did not
// write.
//
// A test is three lines:
//
//	func TestItemCrossesIntoSNGL(t *testing.T) {
//		sngltest.CheckMarshal(t, Item{Name: "alpha", Value: 1})
//	}
//
// Only tests import this package. It pulls in the compiler, so nothing it
// touches reaches a generated program.
//
// # A struct now names its own type
//
// The encoder used to write a struct as `Item{...}` and now writes
// `import("go://example.com/pkg").Item{...}`, so a golden test over the text
// Encode returns changes with the compiler. The name alone was not enough to
// resolve a declaration for a value the declared type says nothing about — a
// func returning []any — and the encoder is the only end that knows which type
// it held. Encode reports what the encoder writes, which is the whole of what
// it is for, so the expected text is what moves.

package sngltest

import (
	"fmt"
	"reflect"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// TB is the part of *testing.T these checks use. A caller passes its *testing.T;
// the interface is here so a test of this package can capture what it reports.
type TB interface {
	Errorf(format string, args ...any)
	Helper()
}

// CheckMarshal runs each value through the compiler's own path for a folded
// go:// result: encode it with pkg/go/consteval, parse the source that
// produced, and check that against the SNGL type the go:// importer gives T.
//
// Each value is reported on its own — one that fails says nothing about the
// next. With no values it checks only that T has a SNGL type, which is what
// CheckType says more plainly.
func CheckMarshal[T any](t TB, values ...T) {
	t.Helper()
	m, err := resolve(reflect.TypeFor[T]())
	if err != nil {
		t.Errorf("%s", err)
		return
	}
	for _, v := range values {
		src, err := consteval.Encode(v)
		if err != nil {
			t.Errorf("%s could not be encoded as SNGL source at all: %v\n\n%s",
				m.goName(), err, encoderNote(m))
			continue
		}
		if err := checkValue(string(src), m.want); err != nil {
			t.Errorf("%s", m.disagree(v, string(src), err))
		}
	}
}

// CheckType checks the mapping half alone: that the go:// importer gives T a
// SNGL type a program can use. It is CheckMarshal for a type whose values are
// awkward to construct.
func CheckType[T any](t TB) {
	t.Helper()
	if _, err := resolve(reflect.TypeFor[T]()); err != nil {
		t.Errorf("%s", err)
	}
}

// Encode returns the SNGL source pkg/go/consteval writes for v, for a golden
// test over the exact text. It reports an encoding failure and returns "";
// whether the text agrees with T's SNGL type is CheckMarshal's question.
func Encode[T any](t TB, v T) string {
	t.Helper()
	src, err := consteval.Encode(v)
	if err != nil {
		t.Errorf("encoding %s as SNGL source: %v", reflect.TypeFor[T](), err)
		return ""
	}
	return string(src)
}

// checkValue checks encoded source against want the way the constant folder
// does, so a value that passes here is one that folds.
//
// No index of imported declarations is passed: a value here is checked against
// the type the importer gave it, which is the disagreement this helper exists
// to find. Naming its own type would let the value pick the declaration and
// the two halves could no longer differ.
func checkValue(src string, want *ir.Type) error {
	e, err := parser.ParseNativeValue("sngltest.sngl", []byte(src))
	if err != nil {
		return fmt.Errorf("the encoded source does not parse: %w", err)
	}
	_, err = checker.CheckNativeValue(e, want, nil)
	return err
}

// disagree is the message for a value that does not fit the type the importer
// gave it: which half produced what, and which of the two to change.
func (m *mapping) disagree(v any, src string, err error) string {
	return fmt.Sprintf(`%s does not survive the crossing into SNGL: its two halves disagree.

    Go type       %s
    value         %#v
    encodes as    %s
                  ^ %s
    imports as    %s
                  ^ codegen/scheme/golang, which the compiler checks against
    checker says  %v

The compiler encodes a value with the first and checks it against the second,
so a go:// function returning this type can never fold. Change one half to
match the other:

%s`,
		m.goName(), m.goTypeLine(), v, src, encoderName(m), m.want, err, m.advice())
}

// advice names the two edits that would make the halves agree, in the order
// the reader is likelier to want them. Both are about the declared type, not
// about the slice or pointer wrapping it: that is the type each half read.
func (m *mapping) advice() string {
	write := fmt.Sprintf("  - make %s.MarshalSNGL write %s, or", m.base, m.baseWant)
	if !m.marshaler() {
		write = fmt.Sprintf("  - give %s a MarshalSNGL method that writes %s, or", m.base, m.baseWant)
	}
	return fmt.Sprintf(`%s
  - map %s to the SNGL type it does write, with
    golang.RegisterType(%q, ...) in the compiler.`, write, m.base, m.qualified())
}

// encoderName says which encoder wrote the source, because that is the half
// the reader has to go and look at.
func encoderName(m *mapping) string {
	if m.marshaler() {
		return fmt.Sprintf("pkg/go/consteval, through %s.MarshalSNGL", m.base)
	}
	return "pkg/go/consteval, by reflection or a registered encoder"
}

// encoderNote points at the encode half for a failure that never got as far as
// a type to compare against.
func encoderNote(m *mapping) string {
	return fmt.Sprintf("The encoder is %s; every value of %s has to have a SNGL form for a go:// call returning it to fold.",
		encoderName(m), m.goName())
}
