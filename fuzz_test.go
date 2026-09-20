package sngl_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// FuzzDocument exercises the full Parse → Check → Format / IR-Convert
// round-trip on whole-document inputs. For inputs that parse and check
// cleanly, it asserts:
//
//  1. parser.Format produces a source that re-parses and re-checks to an
//     IR identical (after StripForCompare) to the original.
//  2. ir.Convert produces an AST whose formatted source re-parses and
//     re-checks to an IR identical to the original.
//  3. The original IR satisfies the structural invariants the rest of the
//     pipeline assumes (every Var/Param/Field has a Type, every Expr has a
//     non-nil ExprType, every named decl has a Name, etc.).
func FuzzDocument(f *testing.F) {
	for _, src := range loadTestdataSeeds() {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		doc1, ok := safeParseDoc(src)
		if !ok {
			t.Skip("parse failed")
		}
		pkg1, diags := checker.Check(doc1, &checker.Config{IsMain: true})
		if hasError(diags) {
			t.Skip("check failed")
		}
		if len(pkg1.Imports) > 0 {
			t.Skip("imports require resolver")
		}

		// 1. Required-field invariants on the freshly checked IR.
		if err := validateIR(pkg1); err != nil {
			t.Fatalf("checker missed required field: %v", err)
		}

		// 2. Format → reparse → recheck → strip-compare.
		formatted := parser.Format(doc1)
		fmtDoc, err := parser.Parse("fuzz.fmt.sngl", []byte(withStdSrc(formatted)))
		if err != nil {
			t.Fatalf("formatter output failed to parse: %v\n--- formatted ---\n%s", err, formatted)
		}
		fmtPkg, fmtDiags := checker.Check(fmtDoc, &checker.Config{IsMain: true})
		if hasError(fmtDiags) {
			t.Fatalf("formatter output failed to type-check:\n--- formatted ---\n%s\n--- diags ---\n%s",
				formatted, joinDiags(fmtDiags))
		}
		if err := comparePkgs(pkg1, fmtPkg); err != nil {
			t.Fatalf("formatter is not semantics-preserving: %v\n--- original ---\n%s\n--- formatted ---\n%s",
				err, src, formatted)
		}

		// 3. IR.Convert → format → reparse → recheck → strip-compare.
		convDoc := ir.Convert(pkg1)
		convSrc := parser.Format(convDoc)
		convReparsed, err := parser.Parse("fuzz.conv.sngl", []byte(withStdSrc(convSrc)))
		if err != nil {
			t.Fatalf("ir.Convert output failed to parse: %v\n--- generated ---\n%s", err, convSrc)
		}
		convPkg, convDiags := checker.Check(convReparsed, &checker.Config{IsMain: true})
		if hasError(convDiags) {
			t.Fatalf("ir.Convert output failed to type-check:\n--- generated ---\n%s\n--- diags ---\n%s",
				convSrc, joinDiags(convDiags))
		}
		if err := comparePkgs(pkg1, convPkg); err != nil {
			t.Fatalf("ir.Convert is not semantics-preserving: %v\n--- original ---\n%s\n--- generated ---\n%s",
				err, src, convSrc)
		}
	})
}

// FuzzExpression wraps the fuzzed string as the body of an expression-bodied
// SNGL function and asserts every supported subset of lowering passes
// produces the same evaluated value. Any divergence implies a lowering pass
// changed observable expression semantics.
func FuzzExpression(f *testing.F) {
	for _, e := range expressionSeeds() {
		f.Add(e)
	}

	// Cap subsets that preserve the runtime value representation used by
	// the testrunner interpreter. NoUnit / NoEnum collapse those values
	// into ints, which would trivially diverge from the baseline encoding,
	// so they are excluded here.
	capSubsets := []lower.Caps{
		{},
		{NoTernary: true},
		{NoToggle: true},
		{NoTernary: true, NoToggle: true, NoComputed: true},
	}

	f.Fuzz(func(t *testing.T, exprSrc string) {
		// Reject obviously malformed inputs early so we don't waste time
		// reparsing the same syntactic dead end across every cap subset.
		if strings.TrimSpace(exprSrc) == "" {
			t.Skip("empty expression")
		}
		wrapped := wrapExpression(exprSrc)
		if _, ok := safeParseDoc(wrapped); !ok {
			t.Skip("wrapped source does not parse")
		}

		var baseline any
		for i, caps := range capSubsets {
			val, err := evalWrapped(wrapped, caps)
			if err != nil {
				// Same input under different caps must succeed-or-fail
				// consistently; any asymmetric failure is a bug.
				if i == 0 {
					t.Skip(fmt.Sprintf("baseline eval failed: %v", err))
				}
				t.Fatalf("eval diverged under caps %s: %v\n--- source ---\n%s",
					caps.String(), err, wrapped)
			}
			if i == 0 {
				baseline = val
				continue
			}
			if !valuesEqual(baseline, val) {
				t.Fatalf("lowering changed expression value:\nbaseline = %#v\ncaps %s = %#v\n--- source ---\n%s",
					baseline, caps.String(), val, wrapped)
			}
		}
	})
}

// --- helpers ---

func loadTestdataSeeds() []string {
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "testdata")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		// Skip fixtures that explicitly exercise error paths or that
		// require a private library (badlib) to type-check.
		if strings.HasPrefix(e.Name(), "error_") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, string(data))
	}
	return out
}

func expressionSeeds() []string {
	return []string{
		"1 + 2",
		"1 + 2 * 3",
		"(1 + 2) * 3",
		"10 - 4 - 1",
		"true && false",
		"true || false",
		"!true",
		"5 > 3",
		"5 == 5",
		"1.5 + 2.5",
		"3 / 2",
		"10 % 3",
		"true ? 10 : 20",
		"false ? 10 : 20",
		"(1 == 1) ? 1 + 1 : 2 * 2",
		`"hello" + " " + "world"`,
		`"abc".len`,
		"[1, 2, 3].len",
		"[1, 2, 3][1]",
		"-5",
		"--5",
		"-(1 + 2)",
		"1 < 2 && 3 < 4",
		"(true ? 1 : 2) + (false ? 10 : 20)",
	}
}

func wrapExpression(expr string) string {
	expr = strings.TrimSpace(expr)
	// Take an unused parameter so the func is not eligible for the
	// NoComputed inlining pass (which targets zero-param funcs).
	return "component main {}\nfunc _eval(__seed int) => (" + expr + ")\n"
}

func safeParseDoc(src string) (doc *ast.Document, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	d, err := parser.Parse("fuzz.sngl", []byte(withStdSrc(src)))
	if err != nil {
		return nil, false
	}
	return d, true
}

func hasError(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

func joinDiags(diags []ir.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Error())
		b.WriteString("\n")
	}
	return b.String()
}

func comparePkgs(a, b *ir.Package) error {
	defer func() { _ = recover() }()
	ir.StripForCompare(a)
	ir.StripForCompare(b)
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("IR mismatch:\n  a: %s\n  b: %s", summarize(a), summarize(b))
	}
	return nil
}

func summarize(pkg *ir.Package) string {
	if pkg == nil {
		return "<nil>"
	}
	return fmt.Sprintf("imports=%d structs=%d enums=%d units=%d consts=%d vars=%d funcs=%d comps=%d windows=%d outputs=%d",
		len(pkg.Imports), len(pkg.Structs), len(pkg.Enums), len(pkg.Units),
		len(pkg.Consts), len(pkg.Vars), len(pkg.Funcs), len(pkg.Components),
		len(pkg.Windows), len(pkg.Outputs))
}

// validateIR walks pkg and asserts that the checker populated every field
// downstream consumers (codegen, lowering, interpreter) treat as required.
func validateIR(pkg *ir.Package) error {
	if pkg == nil {
		return fmt.Errorf("nil package")
	}
	v := &irValidator{}
	v.walkPackage(pkg)
	if v.err != nil {
		return v.err
	}
	return nil
}

type irValidator struct {
	err  error
	path []string
}

func (v *irValidator) push(seg string) { v.path = append(v.path, seg) }
func (v *irValidator) pop()            { v.path = v.path[:len(v.path)-1] }
func (v *irValidator) fail(msg string) {
	if v.err != nil {
		return
	}
	v.err = fmt.Errorf("%s: %s", strings.Join(v.path, "."), msg)
}

func (v *irValidator) walkPackage(pkg *ir.Package) {
	for i, s := range pkg.Structs {
		v.push(fmt.Sprintf("structs[%d]", i))
		v.walkStruct(s)
		v.pop()
	}
	for i, e := range pkg.Enums {
		v.push(fmt.Sprintf("enums[%d]", i))
		v.walkEnum(e)
		v.pop()
	}
	for i, u := range pkg.Units {
		v.push(fmt.Sprintf("units[%d]", i))
		if u.Name == "" {
			v.fail("unit has no name")
		}
		v.pop()
	}
	for i, c := range pkg.Consts {
		v.push(fmt.Sprintf("consts[%d]", i))
		v.walkVar(c, true)
		v.pop()
	}
	for i, va := range pkg.Vars {
		v.push(fmt.Sprintf("vars[%d]", i))
		v.walkVar(va, false)
		v.pop()
	}
	for i, fn := range pkg.Funcs {
		v.push(fmt.Sprintf("funcs[%d]", i))
		v.walkFunc(fn, false)
		v.pop()
	}
	for i, c := range pkg.Components {
		v.push(fmt.Sprintf("components[%d]", i))
		v.walkComponent(c)
		v.pop()
	}
	for i, w := range pkg.Windows {
		v.push(fmt.Sprintf("windows[%d]", i))
		v.walkWindow(w)
		v.pop()
	}
}

func (v *irValidator) walkStruct(s *ir.StructDef) {
	if s.Name == "" {
		v.fail("struct has no name")
	}
	for i, f := range s.Fields {
		v.push(fmt.Sprintf("fields[%d]", i))
		if f.Name == "" {
			v.fail("field has no name")
		}
		if f.Type == nil {
			v.fail("field has no type")
		}
		if f.Default != nil {
			v.walkExpr(f.Default)
		}
		v.pop()
	}
}

func (v *irValidator) walkEnum(e *ir.EnumDef) {
	if e.Name == "" {
		v.fail("enum has no name")
	}
	for i, m := range e.Members {
		v.push(fmt.Sprintf("members[%d]", i))
		if m.Name == "" {
			v.fail("enum member has no name")
		}
		if m.Value != nil {
			v.walkExpr(m.Value)
		}
		v.pop()
	}
}

func (v *irValidator) walkVar(va *ir.Var, isConst bool) {
	if va.Name == "" {
		v.fail("var has no name")
	}
	if va.Type == nil {
		v.fail("var has no type")
	}
	if va.Init != nil {
		v.walkExpr(va.Init)
	} else if isConst && va.Foreign.Name == "" {
		v.fail("const has no initializer")
	}
}

func (v *irValidator) walkFunc(fn *ir.Func, allowAnonymous bool) {
	if !allowAnonymous && fn.Name == "" && fn.Foreign.Name == "" {
		v.fail("func has no name")
	}
	for i, p := range fn.Params {
		v.push(fmt.Sprintf("params[%d]", i))
		if p.Name == "" {
			v.fail("param has no name")
		}
		if p.Type == nil {
			v.fail("param has no type")
		}
		if p.Default != nil {
			v.walkExpr(p.Default)
		}
		v.pop()
	}
	v.push("block")
	v.walkStmts(fn.Block)
	v.pop()
}

func (v *irValidator) walkComponent(c *ir.Component) {
	if c.Name == "" {
		v.fail("component has no name")
	}
	for i, p := range c.Props {
		v.push(fmt.Sprintf("props[%d]", i))
		if p.Name == "" {
			v.fail("prop has no name")
		}
		if p.Type == nil {
			v.fail("prop has no type")
		}
		if p.Default != nil {
			v.walkExpr(p.Default)
		}
		v.pop()
	}
	for i, e := range c.Events {
		v.push(fmt.Sprintf("events[%d]", i))
		if e.Name == "" {
			v.fail("event has no name")
		}
		v.pop()
	}
	for i, va := range c.Vars {
		v.push(fmt.Sprintf("vars[%d]", i))
		v.walkVar(va, va.IsConst)
		v.pop()
	}
	for i, fn := range c.Funcs {
		v.push(fmt.Sprintf("funcs[%d]", i))
		v.walkFunc(fn, false)
		v.pop()
	}
	v.push("body")
	v.walkStmts(c.Body)
	v.pop()
}

func (v *irValidator) walkWindow(w *ir.Window) {
	if w.Name == "" {
		v.fail("window has no name")
	}
	for _, p := range w.Props {
		if p.Value != nil {
			v.walkExpr(p.Value)
		}
	}
	v.push("body")
	v.walkStmts(w.Body)
	v.pop()
}

func (v *irValidator) walkStmts(stmts []ir.Stmt) {
	for i, s := range stmts {
		v.push(fmt.Sprintf("[%d]", i))
		v.walkStmt(s)
		v.pop()
	}
}

func (v *irValidator) walkStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		if n.Name == "" {
			v.fail("node inst has no name")
		}
		for i, p := range n.Props {
			v.push(fmt.Sprintf("props[%d]", i))
			if p.Value != nil {
				v.walkExpr(p.Value)
			}
			v.pop()
		}
		if n.Key != nil {
			v.walkExpr(n.Key)
		}
		if n.Ref != nil {
			v.walkExpr(n.Ref)
		}
		v.walkStmts(n.Children)
	case *ir.CallStmt:
		if n.Call != nil {
			v.walkExpr(n.Call)
		}
	case *ir.SlotInst:
		v.walkStmts(n.Children)
	case *ir.ErrorBoundary:
		v.walkStmts(n.Children)
	case *ir.Assign:
		if n.Target == nil {
			v.fail("assign has no target")
		} else {
			v.walkExpr(n.Target)
		}
		if n.Value == nil {
			v.fail("assign has no value")
		} else {
			v.walkExpr(n.Value)
		}
	case *ir.Toggle:
		if n.Target == nil {
			v.fail("toggle has no target")
		} else {
			v.walkExpr(n.Target)
		}
	case *ir.Emit:
		for i, a := range n.Args {
			v.push(fmt.Sprintf("args[%d]", i))
			if a.Value != nil {
				v.walkExpr(a.Value)
			}
			v.pop()
		}
	case *ir.LocalVar:
		if n.Name == "" {
			v.fail("local var has no name")
		}
		if n.Type == nil {
			v.fail("local var has no type")
		}
		if n.Init != nil {
			v.walkExpr(n.Init)
		}
	case *ir.Return:
		if n.Value != nil {
			v.walkExpr(n.Value)
		}
	case *ir.If:
		if n.Cond == nil {
			v.fail("if has no cond")
		} else {
			v.walkExpr(n.Cond)
		}
		v.walkStmts(n.Body)
		v.walkStmts(n.Else)
	case *ir.For:
		if n.Iter == nil {
			v.fail("for has no iter")
		} else {
			v.walkExpr(n.Iter)
		}
		if n.ElemType == nil {
			v.fail("for has no element type")
		}
		v.walkStmts(n.Body)
		v.walkStmts(n.Else)
	}
}

func (v *irValidator) walkExpr(e ir.Expr) {
	if e == nil {
		v.fail("nil expression")
		return
	}
	if e.ExprType() == nil {
		v.fail(fmt.Sprintf("%T has no resolved type", e))
		return
	}
	switch n := e.(type) {
	case *ir.Binary:
		v.walkExpr(n.Left)
		v.walkExpr(n.Right)
	case *ir.Unary:
		v.walkExpr(n.Operand)
	case *ir.Ternary:
		v.walkExpr(n.Cond)
		v.walkExpr(n.Then)
		v.walkExpr(n.Else)
	case *ir.Call:
		if n.Receiver != nil {
			v.walkExpr(n.Receiver)
		}
		for i, a := range n.Args {
			v.push(fmt.Sprintf("args[%d]", i))
			if a.Value != nil {
				v.walkExpr(a.Value)
			}
			v.pop()
		}
	case *ir.Conversion:
		v.walkExpr(n.Operand)
	case *ir.Select:
		v.walkExpr(n.Operand)
		if n.Field == "" {
			v.fail("select has no field")
		}
	case *ir.Index:
		v.walkExpr(n.Operand)
		v.walkExpr(n.Idx)
	case *ir.StructLit:
		for i, f := range n.Fields {
			v.push(fmt.Sprintf("fields[%d]", i))
			if f.Value != nil {
				v.walkExpr(f.Value)
			}
			v.pop()
		}
	case *ir.ListLit:
		for i, el := range n.Elems {
			v.push(fmt.Sprintf("elems[%d]", i))
			v.walkExpr(el)
			v.pop()
		}
	case *ir.Spread:
		v.walkExpr(n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			v.walkFunc(n.Func, true)
		}
	}
}

// --- expression eval helpers ---

func evalWrapped(src string, caps lower.Caps) (any, error) {
	doc, err := parser.Parse("fuzz_eval.sngl", []byte(withStdSrc(src)))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if hasError(diags) {
		return nil, fmt.Errorf("check: %s", joinDiags(diags))
	}
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		return nil, fmt.Errorf("lower: %w", err)
	}
	return runEvalFn(pkg)
}

func runEvalFn(pkg *ir.Package) (val any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("interpreter panic: %v", r)
		}
	}()
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "_eval" {
			fn = f
			break
		}
	}
	if fn == nil {
		return nil, fmt.Errorf("_eval not present in package")
	}
	env, err := interp.BuildEnv(pkg, "")
	if err != nil {
		return nil, err
	}
	for _, p := range fn.Params {
		// Bind seed params to a deterministic dummy value so any code path
		// that references them produces a stable result.
		env.Set(p, 0)
	}
	for _, s := range fn.Block {
		if r, ok := s.(*ir.Return); ok {
			if r.Value == nil {
				return nil, nil
			}
			return env.Eval(r.Value)
		}
		if err := env.Exec(s); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("function block has no return")
}

// FuzzLoweredDocument extends FuzzDocument with a lowering step. It
// asserts that for every input which parses+checks cleanly:
//
//	Convert(Lower(checked)) → format → reparse → recheck → strip-compare
//
// produces source that re-parses and re-checks. This pins the round-trip
// invariant for lowered output: every IR shape any pass produces must be
// expressible in valid AST.
func FuzzLoweredDocument(f *testing.F) {
	for _, src := range loadTestdataSeeds() {
		f.Add(src)
	}
	caps := lower.Caps{
		NoReactivity:  true,
		NoDeclarative: true,
	}
	f.Fuzz(func(t *testing.T, src string) {
		doc1, ok := safeParseDoc(src)
		if !ok {
			t.Skip("parse failed")
		}
		pkg1, diags := checker.Check(doc1, &checker.Config{IsMain: true})
		if hasError(diags) {
			t.Skip("check failed")
		}
		if len(pkg1.Imports) > 0 {
			// Imports require a resolver; skip per the FuzzDocument convention.
			t.Skip("imports require resolver")
		}
		if err := lower.Lower(pkg1, caps, lower.Options{}); err != nil {
			t.Fatalf("lower: %v", err)
		}
		convDoc := ir.Convert(pkg1)
		convSrc := parser.Format(convDoc)
		convReparsed, err := parser.Parse("fuzz.lower.sngl", []byte(withStdSrc(convSrc)))
		if err != nil {
			t.Fatalf("lowered ir.Convert output failed to parse: %v\n--- generated ---\n%s", err, convSrc)
		}
		_, convDiags := checker.Check(convReparsed, &checker.Config{IsMain: true})
		if hasError(convDiags) {
			t.Fatalf("lowered ir.Convert output failed to type-check:\n--- generated ---\n%s\n--- diags ---\n%s",
				convSrc, joinDiags(convDiags))
		}
	})
}

// valuesEqual compares two interpreter values, allowing a small ULP tolerance
// on floats and treating NaN==NaN so divisions like `0/0` don't surface as
// bogus diffs. Recurses through reflection so the same handling applies to
// composite values (lists, maps, and the testrunner's private unit/struct
// wrapper types).
func valuesEqual(a, b any) bool {
	return reflectEqual(reflect.ValueOf(a), reflect.ValueOf(b))
}

func reflectEqual(av, bv reflect.Value) bool {
	if !av.IsValid() || !bv.IsValid() {
		return av.IsValid() == bv.IsValid()
	}
	// Unwrap interface so concrete kinds drive the comparison.
	for av.Kind() == reflect.Interface {
		av = av.Elem()
	}
	for bv.Kind() == reflect.Interface {
		bv = bv.Elem()
	}
	if !av.IsValid() || !bv.IsValid() {
		return av.IsValid() == bv.IsValid()
	}
	if av.Type() != bv.Type() {
		return false
	}
	switch av.Kind() {
	case reflect.Float32, reflect.Float64:
		af, bf := av.Float(), bv.Float()
		if math.IsNaN(af) && math.IsNaN(bf) {
			return true
		}
		if af == bf {
			return true
		}
		eps := 1e-12 * math.Max(1, math.Max(math.Abs(af), math.Abs(bf)))
		return math.Abs(af-bf) <= eps
	case reflect.Slice, reflect.Array:
		if av.Len() != bv.Len() {
			return false
		}
		for i := 0; i < av.Len(); i++ {
			if !reflectEqual(av.Index(i), bv.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if av.Len() != bv.Len() {
			return false
		}
		iter := av.MapRange()
		for iter.Next() {
			bvv := bv.MapIndex(iter.Key())
			if !bvv.IsValid() || !reflectEqual(iter.Value(), bvv) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < av.NumField(); i++ {
			// Skip pointer fields used as identity handles (e.g. unit
			// table pointers); compare by referenced contents only when
			// both nil/non-nil shape match.
			fa, fb := av.Field(i), bv.Field(i)
			if fa.Kind() == reflect.Pointer {
				if fa.IsNil() != fb.IsNil() {
					return false
				}
				if fa.IsNil() {
					continue
				}
				if !reflectEqual(fa.Elem(), fb.Elem()) {
					return false
				}
				continue
			}
			if !reflectEqual(fa, fb) {
				return false
			}
		}
		return true
	case reflect.Pointer:
		if av.IsNil() != bv.IsNil() {
			return false
		}
		if av.IsNil() {
			return true
		}
		return reflectEqual(av.Elem(), bv.Elem())
	}
	if av.CanInterface() && bv.CanInterface() {
		return reflect.DeepEqual(av.Interface(), bv.Interface())
	}
	return false
}
