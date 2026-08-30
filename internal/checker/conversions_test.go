package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestConvIntFromFloat(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var x = int(3.14)
}
`)
}

func TestConvFloatFromInt(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var x = float(42)
}
`)
}

func TestConvStringFromPrimitives(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var a = string(42)
    var b = string(3.14)
    var c = string(true)
}
`)
}

func TestConvIntFromStructRejected(t *testing.T) {
	expectError(t, `
struct Vec {
    x int = 0
}
func test() {
    var v = Vec{x=1}
    var n = int(v)
}
`, "cannot convert")
}

func TestConvStringFromStructRejected(t *testing.T) {
	expectError(t, `
struct Vec {
    x int = 0
}
func test() {
    var v = Vec{x=1}
    var s = string(v)
}
`, "cannot convert")
}

func TestConvBoolFromNonBoolRejected(t *testing.T) {
	expectError(t, `
func test() {
    var b = bool(42)
}
`, "cannot convert")
}

func TestUnitZeroAccepted(t *testing.T) {
	expectNoErrors(t, `
unit kg { g, kg = 1000g }
var w kg = 0
`)
}

func TestUnitNonZeroRejected(t *testing.T) {
	expectError(t, `
unit kg { g, kg = 1000g }
var w kg = 5
`, "cannot initialize")
}

func TestNullToFuncCompiles(t *testing.T) {
	expectNoErrors(t, `
component main {
    var h func() int = null
    var n = h()
    text(value="{n}")
}
`)
}

func TestInterpolatePrimitives(t *testing.T) {
	expectNoErrors(t, `
component main {
    var n = 42
    var f = 3.14
    var b = true
    text(value="n={n} f={f} b={b}")
}
`)
}

func TestInterpolateStructRejectedWithoutMethod(t *testing.T) {
	expectError(t, `
struct Vec {
    x int = 0
    y int = 0
}
component main {
    var v = Vec{x=1, y=2}
    text(value="v={v}")
}
`, "no string() method")
}

func TestInterpolateStructWithStringMethod(t *testing.T) {
	expectNoErrors(t, `
struct Vec {
    x int = 0
    y int = 0
}
func Vec.string(v Vec) => "{v.x},{v.y}"
component main {
    var v = Vec{x=1, y=2}
    text(value="v={v}")
}
`)
}

func TestIRWrapsImplicitIntToFloat(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte(`
component main {
    var x int = 3
    var y float = x
    text(value="hi")
}
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if pkg == nil || len(pkg.Components) == 0 {
		t.Fatal("no components")
	}
	var yVar *ir.Var
	for _, v := range pkg.Components[0].Vars {
		if v.Name == "y" {
			yVar = v
			break
		}
	}
	if yVar == nil {
		t.Fatal("var y not found")
	}
	if _, ok := yVar.Init.(*ir.Conversion); !ok {
		t.Errorf("expected ir.Conversion wrapping y's init, got %T", yVar.Init)
	}
}

func TestMixedTypedNumericArithmeticRejected(t *testing.T) {
	// Adding a typed int value and a typed float value is no longer an implicit
	// promotion; it requires an explicit conversion. (Untyped constants still
	// unify — that is covered elsewhere.)
	expectError(t, `
component main {
    var a int = 3
    var b float = 1.5
    var c = a + b
    text(value="hi")
}
`, "not defined for int and float")
}

func TestConvIntFromBoolAccepted(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var x = int(true)
}
`)
}

func TestConvFloatFromStringAccepted(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var x = float("3.14")
}
`)
}

func TestConvStringFromEnumAccepted(t *testing.T) {
	expectNoErrors(t, `
enum Status { active, inactive }
func test() {
    var s = string(Status.active)
}
`)
}

func TestConvStringFromColorAccepted(t *testing.T) {
	expectNoErrors(t, `
func test() {
    var c = #ff0000
    var s = string(c)
}
`)
}

func TestConvBoolFromIntRejected(t *testing.T) {
	expectError(t, `
func test() {
    var b = bool(42)
}
`, "cannot convert")
}

func TestConvIntFromListRejected(t *testing.T) {
	expectError(t, `
func test() {
    var xs list<int> = [1, 2, 3]
    var n = int(xs)
}
`, "cannot convert")
}

func TestConvStringFromListRejected(t *testing.T) {
	expectError(t, `
func test() {
    var xs list<int> = [1, 2, 3]
    var s = string(xs)
}
`, "cannot convert")
}

func TestConvExpectsOneArgument(t *testing.T) {
	expectError(t, `
func test() {
    var s = string(1, 2)
}
`, "expected 1 argument")
}

func TestConvFuncImplicitCall(t *testing.T) {
	// string(fn) where fn is zero-arg implicitly calls fn().
	expectNoErrors(t, `
component main {
    var n = 42
    func total() => n * 2
    text(value=string(total))
}
`)
}

func TestUnitZeroInArg(t *testing.T) {
	expectNoErrors(t, `
unit ms { ms, s = 1000ms }
func delay(d ms) => d
func test() {
    var x = delay(0)
}
`)
}

func TestUnitZeroInReturn(t *testing.T) {
	expectNoErrors(t, `
unit ms { ms, s = 1000ms }
func zero() ms {
    return 0
}
`)
}

func TestUnitZeroInAssignment(t *testing.T) {
	expectNoErrors(t, `
unit ms { ms, s = 1000ms }
component main {
    var t ms = 5ms
    func reset() {
        t = 0
    }
    text(value="hi")
}
`)
}

func TestUnitNonZeroInArgRejected(t *testing.T) {
	expectError(t, `
unit ms { ms, s = 1000ms }
func delay(d ms) => d
func test() {
    var x = delay(3)
}
`, "cannot pass")
}

func TestNullToOption(t *testing.T) {
	expectNoErrors(t, `
component main {
    var maybe option<int> = null
    text(value="hi")
}
`)
}

func TestValueWrapsAsOption(t *testing.T) {
	// T → option<T>: bare value auto-wraps when expected is option.
	expectNoErrors(t, `
func takeMaybe(x option<int>) => x
func test() {
    var r = takeMaybe(42)
}
`)
}

func TestNullFuncZeroReturn(t *testing.T) {
	// Null flowing into a func-typed slot must still compile; codegen
	// produces a zero-value callable so calling through it does not panic.
	expectNoErrors(t, `
component main {
    var h func() int = null
    var g func(int) string = null
    var n = h()
    var s = g(3)
    text(value="hi")
}
`)
}

func TestStringToColorAssignment(t *testing.T) {
	expectNoErrors(t, `
component main {
    var c color = "#ff0000"
    text(value="hi")
}
`)
}

func TestStringDomainToStringInterp(t *testing.T) {
	expectNoErrors(t, `
component main {
    var c color = "#ff0000"
    var d date = "2026-03-12"
    text(value="c={c} d={d}")
}
`)
}

func TestInterpolateEnum(t *testing.T) {
	expectNoErrors(t, `
enum Status { active, inactive }
component main {
    var s = Status.active
    text(value="s={s}")
}
`)
}

func TestInterpolateUnit(t *testing.T) {
	expectNoErrors(t, `
unit ms { ms, s = 1000ms }
component main {
    var d ms = 500ms
    text(value="d={d}")
}
`)
}

func TestInterpolateList(t *testing.T) {
	// Lists currently fall back to a language-native stringifier when no
	// `list.string()` method exists — no checker error.
	expectNoErrors(t, `
component main {
    var xs list<int> = [1, 2, 3]
    text(value="xs={xs}")
}
`)
}

func TestInterpolateOption(t *testing.T) {
	expectNoErrors(t, `
component main {
    var maybe option<int> = null
    text(value="maybe={maybe}")
}
`)
}

func TestInterpolateLambdaCall(t *testing.T) {
	// Zero-arg lambda in interpolation implicitly calls.
	expectNoErrors(t, `
component main {
    var n = 3
    var double = func() => n * 2
    text(value="double={double}")
}
`)
}

func TestInterpolateComponentRejected(t *testing.T) {
	expectError(t, `
component Inner {
    var x = 1
    text(value="hi")
}

component main {
    var inst = Inner()
    text(value="inst={inst}")
}
`, "no string() method")
}

func TestMixedTypedMulRejected(t *testing.T) {
	expectError(t, `
component main {
    var a int = 3
    var b float = 1.5
    var c = a * b
    text(value="hi")
}
`, "not defined for int and float")
}

func TestMixedTypedDivRejected(t *testing.T) {
	expectError(t, `
component main {
    var a int = 3
    var b float = 1.5
    var c = a / b
    text(value="hi")
}
`, "not defined for int and float")
}

func TestNumericComparisonMixedRejected(t *testing.T) {
	// Comparing a typed int value with a typed float value requires an explicit
	// conversion — the same rule as arithmetic.
	expectError(t, `
func test() {
    var a int = 3
    var b float = 1.5
    var r = a < b
}
`, "not defined for int and float")
}

func TestIRBuiltinCastEmitsConversion(t *testing.T) {
	doc, _ := parser.Parse("test.sngl", []byte(`
component main {
    var x float = 3.14
    var n = int(x)
    text(value="hi")
}
`))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var nVar *ir.Var
	for _, v := range pkg.Components[0].Vars {
		if v.Name == "n" {
			nVar = v
			break
		}
	}
	if _, ok := nVar.Init.(*ir.Conversion); !ok {
		t.Errorf("int(x): expected ir.Conversion, got %T", nVar.Init)
	}
}

func TestIRInterpolationEmitsConversion(t *testing.T) {
	doc, _ := parser.Parse("test.sngl", []byte(`
import . "sngl:ui"

component main {
    var n = 42
    text(value="n={n}")
}
`))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	// Walk the text() node to find the interpolated value and check that
	// its int part is wrapped in ir.Conversion to string.
	found := false
	var walk func(e ir.Expr)
	walk = func(e ir.Expr) {
		switch x := e.(type) {
		case *ir.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ir.Conversion:
			if x.Type != nil && x.Type.Kind == ir.TypeString {
				if lit, ok := x.Operand.(*ir.Ident); ok && lit.Name == "n" {
					found = true
				}
			}
		}
	}
	for _, stmt := range pkg.Components[0].Body {
		if node, ok := stmt.(*ir.NodeInst); ok {
			for _, p := range node.Props {
				if p.Name == "value" {
					walk(p.Value)
				}
			}
		}
	}
	if !found {
		t.Error("expected Conversion{string, n} in interpolation IR")
	}
}

func TestLiteralZeroNotAppliedToString(t *testing.T) {
	expectError(t, `var s string = 0`, "cannot initialize string with int")
}

func TestDynToConcreteFlows(t *testing.T) {
	expectNoErrors(t, `
func makeDyn() dyn {
    var d dyn = 42
    return d
}
func test() {
    var n int = makeDyn()
}
`)
}
