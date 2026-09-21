package golang

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// HelperSet records which lang-emitted runtime helpers and stdlib
// imports the generated Go code needs. Platforms call HelpersNeeded
// once at emit time, merge .Imports() into their import set, and
// append .Emit() to the generated source.
//
// This centralizes the helper bookkeeping in the lang layer: when a
// new typed literal lowering needs runtime support, the helper text
// and its import are recorded here and every platform picks them up
// automatically.
type HelperSet struct {
	// The parse helpers, one per date/time family member. Each is recorded
	// where a value of that type is *built* -- a literal, or a conversion's
	// target -- because that is the only place Go needs something to build
	// one from a string. A type annotation needs the `time` package spelled
	// and nothing else, which is NeedTimeImport below.
	NeedDate     bool
	NeedTime     bool
	NeedDateTime bool
	// NeedTimeImport is any mention of the family at all, annotation
	// included. It was the same four flags doing both jobs, and fyne's timer
	// is what separated them: its `*time.Ticker` carries a
	// `go.chan<time.datetime>`, which is a type nothing ever parses, so six
	// timer fixtures emitted a `mustParseDateTime` no line called.
	NeedTimeImport bool
	// `int(s)` and `float(s)` are conversions the language allows and Go does
	// not: `float64("3.5")` builds no number, and `int("42")` is a rune. They
	// go through strconv, and a string that is not a number reads as zero —
	// which is what the interpreter and the JavaScript backend already do.
	NeedParseInt   bool
	NeedParseFloat bool
	// A sngl:seq sequence that is not a loop head's own iterable: the loop
	// head becomes a counting `for` and needs nothing, and everywhere else
	// the sequence is the pull func iter<int> is spelled as. One helper says
	// that in three lines rather than a closure at every call site.
	NeedSeq bool
	// A bare T promoted into an option<T>. Go's option is *T and `&` is only
	// legal on an addressable operand, so the box is a helper rather than an
	// operator -- see the IsOptionWrap arm of evalConversion.
	NeedSome bool
	// UnitStrings are the unit decls some string conversion renders, in the
	// order first seen. One helper each rather than one shared: a unit's bases
	// are per declaration, so the terms have to be written out.
	UnitStrings []*ir.UnitDef
}

// needUnitString records that ud is rendered as a string somewhere, ignoring
// a decl already seen.
func (h *HelperSet) needUnitString(ud *ir.UnitDef) {
	if ud == nil {
		return
	}
	if slices.Contains(h.UnitStrings, ud) {
		return
	}
	h.UnitStrings = append(h.UnitStrings, ud)
}

// UnitStringFn names the helper that renders a value of ud as a string.
func UnitStringFn(ud *ir.UnitDef) string { return "snglStr" + ExportName(ud.Name) }

// UnitStringConversion reports the unit a string conversion renders, if it
// renders one. Go's own spelling is unusable for either shape a unit takes
// here: fmt.Sprint of the generated struct prints `{3 2 0 0 0}`, and
// time.Duration's String normalises 1100ms to "1.1s" where the declared base
// is ms.
func UnitStringConversion(n *ir.Conversion) *ir.UnitDef {
	if n == nil || n.Type == nil || n.Type.Kind != ir.TypeString || n.Operand == nil {
		return nil
	}
	return ir.UnitDeclOf(n.Operand.ExprType())
}

// StringToNumberHelper names the helper a conversion needs, or "" when Go's
// own cast will do. `float(s)` and `int(s)` are conversions the language
// allows on a string and Go does not: `float64(s)` does not compile and
// `int(s)` would be a rune.
//
// The operand's declared type is what decides, so a `dyn` stays a plain cast —
// there is nothing to say it holds a string.
func StringToNumberHelper(n *ir.Conversion) string {
	if n == nil || n.Type == nil || n.Operand == nil {
		return ""
	}
	src := n.Operand.ExprType()
	if src == nil || src.Kind != ir.TypeString {
		return ""
	}
	switch n.Type.Kind {
	case ir.TypeInt:
		return "snglParseInt"
	case ir.TypeFloat:
		return "snglParseFloat"
	}
	return ""
}

// HelpersNeeded walks pkg and returns the helpers the generated code
// will use. It inspects every typed var (package-level + per-component)
// and every checker-inserted ir.Conversion target.
func HelpersNeeded(pkg *ir.Package) HelperSet {
	var h HelperSet
	if pkg == nil {
		return h
	}
	for _, v := range pkg.Vars {
		recordInitHelpers(&h, v.Type, v.Init)
	}
	for _, c := range pkg.Consts {
		recordInitHelpers(&h, c.Type, c.Init)
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			recordInitHelpers(&h, v.Type, v.Init)
		}
		for _, f := range comp.Funcs {
			recordFuncHelpers(&h, f)
		}
	}
	for _, f := range pkg.Funcs {
		recordFuncHelpers(&h, f)
	}
	// The loops above miss exprs that live only in the rendered visual tree
	// (component/window bodies, handlers, timers) — e.g. a date/time literal
	// synthesized as an inlined widget's zero-value default, like the bubbletea
	// datepicker's `mustParseDate("0001-01-01")`. Sweep every expression in the
	// package so such literals still flag their parse helper.
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		switch n := e.(type) {
		case *ir.Literal:
			recordValueHelpers(&h, n.Type)
		case *ir.Conversion:
			recordValueHelpers(&h, n.Type)
			if ir.IsOptionWrap(n) {
				h.NeedSome = true
			}
			h.needUnitString(UnitStringConversion(n))
		}
		return nil
	})
	h.NeedSeq = packageBuildsASequence(pkg)
	return h
}

// packageBuildsASequence reports whether any sngl:seq call has to produce a
// value. A call that is a counted loop's own iterable does not -- ForHead
// emits the bounds as a counting loop and never renders the call -- so a
// program that only writes sequences in loop heads needs no helper.
func packageBuildsASequence(pkg *ir.Package) bool {
	inLoopHead := map[ir.Expr]bool{}
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if f, ok := s.(*ir.For); ok && ir.CountedSeq(f) != nil {
			inLoopHead[f.Iter] = true
		}
		return nil
	})
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		call, ok := e.(*ir.Call)
		if !ok || call.Func == nil || inLoopHead[e] {
			return nil
		}
		switch call.Func.Intrinsic {
		case "seq.count", "seq.range", "seq.step":
			found = true
		}
		return nil
	})
	return found
}

// Imports returns the stdlib import paths needed by the recorded
// helpers. Returns nil when no helpers are needed.
func (h HelperSet) Imports() []string {
	var imps []string
	if h.NeedTimeImport {
		imps = append(imps, "time")
	}
	if h.NeedParseInt || h.NeedParseFloat {
		imps = append(imps, "strconv")
	}
	if len(h.UnitStrings) > 0 {
		imps = append(imps, "fmt")
		for _, ud := range h.UnitStrings {
			if ClassifyUnit(ud) == UnitDuration {
				imps = append(imps, "time")
				break
			}
		}
	}
	return imps
}

// EmitUnitStringFuncs renders the display helper for each recorded unit. The
// magnitude goes through fmt.Sprint rather than a spelling of its own, so a
// unit's number reads the way a bare float already does on this target.
func (h HelperSet) EmitUnitStringFuncs() string {
	var b strings.Builder
	for _, ud := range h.UnitStrings {
		fmt.Fprintf(&b, "func %s(v %s) string {\n", UnitStringFn(ud), IRTypeToGo(unitTypeOf(ud)))
		switch ClassifyUnit(ud) {
		case UnitDuration:
			// A time.Duration counts nanoseconds and the declared base is ms,
			// the same divide the cast makes.
			fmt.Fprintf(&b, "\treturn fmt.Sprint(float64(v)/float64(time.Millisecond)) + %q\n", ud.Bases()[0].Name)
		case UnitScalar:
			fmt.Fprintf(&b, "\treturn fmt.Sprint(float64(v)) + %q\n", ud.Bases()[0].Name)
		case UnitMultiBase:
			b.WriteString("\ts := \"\"\n")
			for i, base := range ud.Bases() {
				field := ExportName(base.Name)
				fmt.Fprintf(&b, "\tif v.%s != 0 {\n", field)
				if i > 0 {
					fmt.Fprintf(&b, "\t\tif s != \"\" {\n\t\t\ts += %q\n\t\t}\n", ir.UnitTermSep)
				}
				fmt.Fprintf(&b, "\t\ts += fmt.Sprint(v.%s) + %q\n\t}\n", field, base.Name)
			}
			fmt.Fprintf(&b, "\tif s == \"\" {\n\t\treturn %q\n\t}\n\treturn s\n", ir.FormatUnitZero(ud))
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

// unitTypeOf rebuilds the ir.Type for a UnitDef so IRTypeToGo can name it --
// duration included, which is time.Duration and not a type this package emits.
func unitTypeOf(ud *ir.UnitDef) *ir.Type {
	return &ir.Type{Kind: ir.TypeUnit, Decl: ud}
}

// Emit returns the Go source for the helper functions recorded on h,
// concatenated in a stable order. Empty string when h is zero-valued.
func (h HelperSet) Emit() string {
	var b strings.Builder
	if h.NeedSeq {
		// The sign test is on the step: a `by` of 0 yields nothing, which is
		// what sngl:seq documents, rather than spinning.
		b.WriteString(`func snglSeq(start, end, step int) func(func(int) bool) {
	return func(yield func(int) bool) {
		if step > 0 {
			for i := start; i < end; i += step {
				if !yield(i) {
					return
				}
			}
		} else if step < 0 {
			for i := start; i > end; i += step {
				if !yield(i) {
					return
				}
			}
		}
	}
}

`)
	}
	if h.NeedSome {
		// By value, so the pointer addresses a copy: a SNGL struct is a value,
		// and `&x` would let a later write to x reach whatever holds the
		// option. It is also what makes a non-addressable operand work.
		b.WriteString(`func snglSome[T any](v T) *T {
	return &v
}

`)
	}
	if h.NeedParseInt {
		b.WriteString(`func snglParseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

`)
	}
	if h.NeedParseFloat {
		b.WriteString(`func snglParseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

`)
	}
	if h.NeedDate {
		b.WriteString(`func mustParseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

`)
	}
	if h.NeedTime {
		b.WriteString(`func mustParseTime(s string) time.Time {
	t, err := time.Parse("15:04:05", s)
	if err != nil {
		t, err = time.Parse("15:04", s)
		if err != nil {
			panic(err)
		}
	}
	return t
}

`)
	}
	if h.NeedDateTime {
		b.WriteString(`func mustParseDateTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

`)
	}
	b.WriteString(h.EmitUnitStringFuncs())
	return b.String()
}

// recordTypeHelpers records what a type *annotation* needs, which is the
// `time` import and nothing else: a Go `time.Time` field needs the package
// spelled, and nothing there parses anything.
func recordTypeHelpers(h *HelperSet, t *ir.Type) {
	if t == nil {
		return
	}
	if namesTimePackage(t) {
		h.NeedTimeImport = true
	}
	for _, el := range t.Elems {
		recordTypeHelpers(h, el)
	}
}

// recordInitHelpers mirrors LowerVarInit: a binding whose declared type is a
// date, time or datetime and whose initializer is a literal is spelled with
// the parse helper, and the type it parses into is the *binding's* rather
// than the literal's -- the fold that settled `date("2026-05-09")` leaves a
// plain string behind, so neither half says on its own that a helper is
// wanted.
func recordInitHelpers(h *HelperSet, t *ir.Type, init ir.Expr) {
	recordTypeHelpers(h, t)
	if _, isLit := init.(*ir.Literal); isLit {
		recordValueHelpers(h, t)
	}
	recordExprHelpers(h, init)
}

// recordValueHelpers is the same question asked where a value of the type is
// *built* -- a literal, or a conversion's target -- which is what the parse
// helpers are for.
func recordValueHelpers(h *HelperSet, t *ir.Type) {
	if t == nil {
		return
	}
	switch {
	case ir.IsDateStruct(t):
		h.NeedDate = true
	case ir.IsTimeStruct(t):
		h.NeedTime = true
	case ir.IsDateTimeStruct(t):
		h.NeedDateTime = true
	}
	for _, el := range t.Elems {
		recordValueHelpers(h, el)
	}
	recordTypeHelpers(h, t)
}

// namesTimePackage reports whether t is spelled with Go's `time` package.
func namesTimePackage(t *ir.Type) bool {
	if ir.IsDateStruct(t) || ir.IsTimeStruct(t) || ir.IsDateTimeStruct(t) {
		return true
	}
	if t.Kind == ir.TypeUnit {
		ud, ok := t.Decl.(*ir.UnitDef)
		return ok && ud.Builtin == ir.BuiltinDuration
	}
	return false
}

func recordExprHelpers(h *HelperSet, e ir.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Conversion:
		recordValueHelpers(h, n.Type)
		switch StringToNumberHelper(n) {
		case "snglParseInt":
			h.NeedParseInt = true
		case "snglParseFloat":
			h.NeedParseFloat = true
		}
		recordExprHelpers(h, n.Operand)
	case *ir.Literal:
		recordValueHelpers(h, n.Type)
	case *ir.Binary:
		recordExprHelpers(h, n.Left)
		recordExprHelpers(h, n.Right)
	case *ir.Unary:
		recordExprHelpers(h, n.Operand)
	case *ir.Ternary:
		recordExprHelpers(h, n.Cond)
		recordExprHelpers(h, n.Then)
		recordExprHelpers(h, n.Else)
	case *ir.Call:
		for _, a := range n.Args {
			recordExprHelpers(h, a.Value)
		}
		if n.Receiver != nil {
			recordExprHelpers(h, n.Receiver)
		}
	case *ir.Select:
		recordExprHelpers(h, n.Operand)
	case *ir.Index:
		recordExprHelpers(h, n.Operand)
		recordExprHelpers(h, n.Idx)
	case *ir.StructLit:
		for _, f := range n.Fields {
			recordExprHelpers(h, f.Value)
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			recordExprHelpers(h, el)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			recordExprHelpers(h, en.Key)
			recordExprHelpers(h, en.Value)
		}
	case *ir.Spread:
		recordExprHelpers(h, n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			recordFuncHelpers(h, n.Func)
		}
	case *ir.Closure:
		if n.Func != nil {
			recordFuncHelpers(h, n.Func)
		}
		if n.State != nil {
			recordExprHelpers(h, n.State)
		}
	case *ir.Ident, *ir.ContextRead:
		// Terminal — no type or sub-expression to record.
	default:
		panic(fmt.Sprintf("recordExprHelpers: unhandled ir.Expr %T", n))
	}
}

func recordFuncHelpers(h *HelperSet, f *ir.Func) {
	if f == nil {
		return
	}
	for _, p := range f.Params {
		if p != nil {
			recordTypeHelpers(h, p.Type)
		}
	}
	recordTypeHelpers(h, f.Return)
	recordStmtHelpers(h, f.Block)
}

func recordStmtHelpers(h *HelperSet, stmts []ir.Stmt) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			recordExprHelpers(h, n.Target)
			recordExprHelpers(h, n.Value)
		case *ir.Toggle:
			recordExprHelpers(h, n.Target)
		case *ir.Emit:
			for _, a := range n.Args {
				recordExprHelpers(h, a.Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				recordExprHelpers(h, n.Call)
			}
		case *ir.LocalVar:
			recordInitHelpers(h, n.Type, n.Init)
		case *ir.Return:
			recordExprHelpers(h, n.Value)
		case *ir.If:
			recordExprHelpers(h, n.Cond)
			recordStmtHelpers(h, n.Body)
			recordStmtHelpers(h, n.Else)
		case *ir.For:
			recordExprHelpers(h, n.Iter)
			recordStmtHelpers(h, n.Body)
		case *ir.NodeInst:
			for _, p := range n.Props {
				recordExprHelpers(h, p.Value)
			}
			for _, hd := range n.Handlers {
				if hd.Func != nil {
					recordFuncHelpers(h, hd.Func)
				}
			}
			recordStmtHelpers(h, n.Children)
		case *ir.SlotInst:
			recordStmtHelpers(h, n.Children)
		case *ir.ErrorBoundary:
			recordStmtHelpers(h, n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				recordFuncHelpers(h, n.Handler.Func)
			}
		case *ir.Window:
			recordStmtHelpers(h, n.Body)
		case *ir.ContextProvider:
			recordExprHelpers(h, n.Value)
			recordStmtHelpers(h, n.Children)
		case *ir.CanvasRedrawStmt:
			// No helpers needed.
		case *ir.Break, *ir.Continue:
			// A loop escape needs no helper.
		default:
			panic(fmt.Sprintf("recordStmtHelpers: unhandled ir.Stmt %T", n))
		}
	}
}
