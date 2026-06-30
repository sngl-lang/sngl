package golang

import (
	"fmt"
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
	NeedDate     bool
	NeedTime     bool
	NeedDateTime bool
	NeedDuration bool
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
		recordTypeHelpers(&h, v.Type)
		recordExprHelpers(&h, v.Init)
	}
	for _, c := range pkg.Consts {
		recordTypeHelpers(&h, c.Type)
		recordExprHelpers(&h, c.Init)
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			recordTypeHelpers(&h, v.Type)
			recordExprHelpers(&h, v.Init)
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
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		switch n := e.(type) {
		case *ir.Literal:
			recordTypeHelpers(&h, n.Type)
		case *ir.Conversion:
			recordTypeHelpers(&h, n.Type)
		}
		return false
	})
	return h
}

// Imports returns the stdlib import paths needed by the recorded
// helpers. Returns nil when no helpers are needed.
func (h HelperSet) Imports() []string {
	if h.NeedDate || h.NeedTime || h.NeedDateTime || h.NeedDuration {
		return []string{"time"}
	}
	return nil
}

// Emit returns the Go source for the helper functions recorded on h,
// concatenated in a stable order. Empty string when h is zero-valued.
func (h HelperSet) Emit() string {
	var b strings.Builder
	if h.NeedDuration {
		b.WriteString(`func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
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
	return b.String()
}

func recordTypeHelpers(h *HelperSet, t *ir.Type) {
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
	case t.Kind == ir.TypeDuration:
		h.NeedDuration = true
	case t.Kind == ir.TypeUnit:
		if ud, ok := t.Decl.(*ir.UnitDef); ok && ud.Name == "duration" {
			h.NeedDuration = true
		}
	}
	for _, el := range t.Elems {
		recordTypeHelpers(h, el)
	}
}

func recordExprHelpers(h *HelperSet, e ir.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Conversion:
		recordTypeHelpers(h, n.Type)
		recordExprHelpers(h, n.Operand)
	case *ir.Literal:
		recordTypeHelpers(h, n.Type)
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
			recordTypeHelpers(h, n.Type)
			recordExprHelpers(h, n.Init)
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
		case *ir.PlatformFilter:
			recordStmtHelpers(h, n.Body)
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
		default:
			panic(fmt.Sprintf("recordStmtHelpers: unhandled ir.Stmt %T", n))
		}
	}
}
