package interp

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/opeval"
	"git.duckfam.us/jonathan/sngl/ir"
	goi18n "git.duckfam.us/jonathan/sngl/pkg/go/i18n"
)

// maxCallDepth is the maximum allowed function call depth.
const maxCallDepth = 100

// LambdaValue is a closure captured by a lambda expression.
type LambdaValue struct {
	fn  *ir.Func
	env *Env
}

// call invokes the lambda with the given values.
func (lv *LambdaValue) Call(args []any) (any, error) {
	child := lv.env.Snapshot()
	for i, p := range lv.fn.Params {
		if i < len(args) {
			child.Set(p, args[i])
		}
	}
	return child.execBlockForResult(lv.fn.Block)
}

// callWithEnv is like call but uses the provided env directly (no snapshot).
func (lv *LambdaValue) CallWithEnv(env *Env, args []any) (any, error) {
	for i, p := range lv.fn.Params {
		if i < len(args) {
			env.Set(p, args[i])
		}
	}
	return env.execBlockForResult(lv.fn.Block)
}

// unitValue is the runtime representation of a unit value.
type unitValue struct {
	BaseAmount float64
	Suffix     string
	Table      *unitTable
}

func (u unitValue) Equal(other unitValue) bool {
	if u.Table != other.Table {
		return false
	}
	return u.BaseAmount == other.BaseAmount
}

func (u unitValue) Add(other unitValue) unitValue {
	if u.Table != other.Table {
		return u
	}
	return unitValue{BaseAmount: u.BaseAmount + other.BaseAmount, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) Sub(other unitValue) unitValue {
	if u.Table != other.Table {
		return u
	}
	return unitValue{BaseAmount: u.BaseAmount - other.BaseAmount, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) Scale(factor float64) unitValue {
	return unitValue{BaseAmount: u.BaseAmount * factor, Suffix: u.Suffix, Table: u.Table}
}

func (u unitValue) displayAmount() float64 {
	if u.Table != nil {
		if factor, ok := u.Table.Conversions[u.Suffix]; ok && factor != 0 {
			return u.BaseAmount / factor
		}
	}
	return u.BaseAmount
}

func (u unitValue) String() string {
	amt := u.displayAmount()
	if amt == math.Trunc(amt) && !math.IsInf(amt, 0) && !math.IsNaN(amt) {
		return fmt.Sprintf("%d%s", int(amt), u.Suffix)
	}
	return fmt.Sprintf("%g%s", amt, u.Suffix)
}

func (u unitValue) sameFamily(other unitValue) bool {
	return u.Table != nil && u.Table == other.Table
}

// unitTable maps suffixes to conversion factors.
type unitTable struct {
	Base        string
	Conversions map[string]float64
}

// Env holds the mutable state for test execution.
type Env struct {
	// Keyed by the declaration rather than by name, so two declarations that
	// share a name are two bindings and a library constant needs no copy into
	// a name table.
	vals map[ir.Symbol]any
	// assigned is what a statement in this scope wrote, as opposed to what was
	// bound into it. RebindFrom carries only these back. A loop iteration
	// renders in a snapshot of the enclosing scope, so carrying every binding
	// back writes that scope's state as it stood when the snapshot was taken:
	// an effect's teardown, running in the scope of the iteration that is going
	// away, wrote the old list back and the removed element reappeared.
	assigned map[ir.Symbol]bool
	// recv is the implicit component receiver (`this`). Not a binding in vals
	// because no symbol can key it: every method declares its own `this`
	// param, but the caller supplies the value, so the site that binds it and
	// the site that reads it belong to different declarations. hasRecv
	// separates "bound to nil" from "not bound".
	recv        any
	hasRecv     bool
	Units       map[string]*unitTable
	Pkg         *ir.Package
	Comp        *ir.Component
	BodyStmts   []ir.Stmt
	depth       int
	RenderDepth int
	// maxIterations bounds a condition or forever loop; zero means
	// maxLoopIterations. A test sets it to something small, since asserting
	// the bound by reaching the real one would run ten million iterations.
	maxIterations int
	Log           []string
	// Locale is the active BCP-47 locale for i18n calls (default "en").
	Locale string
	// ContextVals holds runtime overrides for context values keyed by *ir.Context.
	// Set by t.setContext(); read by Eval(*ir.ContextRead).
	ContextVals map[*ir.Context]any
	// childEnvs caches per-NodeInst child component envs so state
	// persists across ResolveElementRef calls. Keyed by the
	// instantiation site's *ir.NodeInst pointer.
	childEnvs map[*ir.NodeInst]*Env
	// callChildEnvs is the analogous cache for user-component instantiations
	// expressed as ir.CallStmt (children-less call form, e.g. `main()`).
	callChildEnvs map[*ir.CallStmt]*Env
	// parent points at the surrounding env when this env is a child component
	// scope. lookup() falls through to the parent chain so child components
	// can read (and assignments can mutate) package-level vars defined in the
	// root env.
	parent *Env
}

func NewEnv() *Env {
	return &Env{
		vals:      map[ir.Symbol]any{},
		childEnvs: map[*ir.NodeInst]*Env{},
	}
}

func (env *Env) Set(sym ir.Symbol, val any) {
	if sym == nil {
		return
	}
	env.vals[sym] = val
}

// SetReceiver binds the implicit component receiver (`this`) for calls made
// against this env. See Env.recv.
func (env *Env) SetReceiver(val any) {
	env.recv, env.hasRecv = val, true
}

func (env *Env) Value(sym ir.Symbol) (any, bool) {
	if owner := env.findVarOwner(sym); owner != nil {
		return owner.vals[sym], true
	}
	return nil, false
}

// RebindFrom copies src's value for every symbol env already binds. A call
// runs against a Snapshot, whose bindings are copies; this writes the
// mutations back into the env the snapshot was taken from.
func (env *Env) RebindFrom(src *Env) {
	if src == nil {
		return
	}
	for e := env; e != nil; e = e.parent {
		for sym := range e.vals {
			if !src.wasAssigned(sym) {
				continue
			}
			if v, ok := src.Value(sym); ok {
				e.vals[sym] = v
			}
		}
	}
}

// wasAssigned reports whether src or an enclosing scope wrote sym. A write that
// landed in a shared parent is already visible and is reported here anyway,
// because copying a value onto itself needs no second rule.
func (env *Env) wasAssigned(sym ir.Symbol) bool {
	for e := env; e != nil; e = e.parent {
		if e.assigned[sym] {
			return true
		}
	}
	return false
}

// refreshFrom brings every binding this scope and its parents hold up to the
// value src knows, and leaves alone the ones src does not -- which is the loop
// variable an iteration bound, and the reason the scope is worth keeping at
// all.
//
// The counterpart to RebindFrom, for a scope that outlives the render that
// built it. An effect's bracket holds the scope of the iteration that placed
// it, and its state was copied in when that mount ran; a handler running later
// reads what the program said then, and RebindFrom carries the whole of it
// back. Two brackets ending in one settle is where that shows: the second
// teardown wrote its own mount's state back over the first teardown's.
func (env *Env) refreshFrom(src *Env) {
	if src == nil {
		return
	}
	for e := env; e != nil; e = e.parent {
		if e == src {
			return
		}
		for sym := range e.vals {
			if v, ok := src.Value(sym); ok {
				e.vals[sym] = v
			}
		}
	}
}

// noteAssigned records that a statement wrote sym in this scope.
func (env *Env) noteAssigned(sym ir.Symbol) {
	if env.assigned == nil {
		env.assigned = map[ir.Symbol]bool{}
	}
	env.assigned[sym] = true
}

// Values ranges over the values bound in this env, stopping when f returns
// false. Used to find a binding by what it holds rather than what it is
// called — the test harness locates the component under test this way.
func (env *Env) Values(f func(val any) bool) {
	for _, v := range env.vals {
		if !f(v) {
			return
		}
	}
}

// Snapshot returns a shallow copy of the env.
func (env *Env) Snapshot() *Env {
	childEnvs := env.childEnvs
	if childEnvs == nil {
		childEnvs = map[*ir.NodeInst]*Env{}
	}
	cp := &Env{
		vals: make(map[ir.Symbol]any, len(env.vals)),
		// Its own set, not the original's: what this scope wrote is what
		// RebindFrom carries back, and two snapshots of one scope must not be
		// credited with each other's writes.
		assigned:      map[ir.Symbol]bool{},
		recv:          env.recv,
		hasRecv:       env.hasRecv,
		Units:         env.Units,
		Pkg:           env.Pkg,
		Comp:          env.Comp,
		BodyStmts:     env.BodyStmts,
		depth:         env.depth,
		RenderDepth:   env.RenderDepth,
		Locale:        env.Locale,
		ContextVals:   env.ContextVals, // shared reference — overrides visible in child envs
		childEnvs:     childEnvs,       // shared reference — cached child envs persist through scope changes
		callChildEnvs: env.callChildEnvs,
		parent:        env.parent,
	}
	maps.Copy(cp.vals, env.vals)
	return cp
}

// SetContext stores a runtime override for ctx, replacing any default value.
// If ctx is named "locale" its value is also applied to env.Locale for i18n
// compat, until the i18n stack reads context throughout.
func (env *Env) SetContext(ctx *ir.Context, val any) {
	if env.ContextVals == nil {
		env.ContextVals = make(map[*ir.Context]any)
	}
	env.ContextVals[ctx] = val
	if ctx.Name == "locale" {
		if s, ok := val.(string); ok {
			env.Locale = s
		}
	}
}

// ContextVal returns the current value of ctx: the override if one has been
// stored via SetContext, otherwise ctx's default evaluated against env.
func (env *Env) ContextVal(ctx *ir.Context) any {
	if env.ContextVals != nil {
		if v, ok := env.ContextVals[ctx]; ok {
			return v
		}
	}
	if ctx.Default != nil {
		v, err := env.Eval(ctx.Default)
		if err == nil {
			return v
		}
	}
	return nil
}

// translatorFor returns a Translator for the env's current locale.
func (env *Env) translatorFor() *goi18n.Translator {
	loc := env.Locale
	if loc == "" {
		loc = "en"
	}
	return goi18n.NewTranslator(goi18n.Manifest{}, loc)
}

// translatorForLocale returns a translator for the given BCP-47 locale,
// falling back to env.Locale or "en" when empty.
func (env *Env) translatorForLocale(loc string) *goi18n.Translator {
	if loc == "" {
		loc = env.Locale
	}
	if loc == "" {
		loc = "en"
	}
	return goi18n.NewTranslator(goi18n.Manifest{}, loc)
}

// evalI18nPrimitiveCall dispatches a call to one of the unexported i18n._*
// primitives by its intrinsic id. Every one but _defaultLocale takes the
// active locale as its first argument, threaded there by NoContext from the
// `locale` context read in the wrapper that calls it. Returns (result,
// handled, error); handled is false when the id is unknown.
func (env *Env) evalI18nPrimitiveCall(method string, args []ir.CallArg) (any, bool, error) {
	switch method {
	case "i18n._defaultLocale":
		return goi18n.DefaultLocale(), true, nil
	}

	// Every other primitive carries the locale as args[0].
	if len(args) < 1 {
		return nil, true, fmt.Errorf("%s requires at least a locale argument", method)
	}
	locVal, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, true, err
	}
	loc := fmt.Sprintf("%v", locVal)
	tr := env.translatorForLocale(loc)

	eval := func(i int) (any, error) {
		return env.Eval(args[i].Value)
	}

	switch method {
	case "i18n._translate":
		if len(args) < 4 {
			return nil, true, fmt.Errorf("%s requires 4 arguments", method)
		}
		keyV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		tmplV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		argsV, err := eval(3)
		if err != nil {
			return nil, true, err
		}
		return tr.Tr(fmt.Sprintf("%v", keyV), fmt.Sprintf("%v", tmplV), toStringAnyMap(argsV)), true, nil
	case "i18n._format":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		tmplV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		argsV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.Format(fmt.Sprintf("%v", tmplV), toStringAnyMap(argsV)), true, nil
	case "i18n._numberInt":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		nV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberInt(ToInt(nV), fmt.Sprintf("%v", sV)), true, nil
	case "i18n._numberFloat":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		nV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberFloat(toFloat(nV), fmt.Sprintf("%v", sV)), true, nil
	case "i18n._date":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Date(tt, fmt.Sprintf("%v", sV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._time":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Time(tt, fmt.Sprintf("%v", sV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._dateTime":
		if len(args) < 4 {
			return nil, true, fmt.Errorf("%s requires 4 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		dsV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		tsV, err := eval(3)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Datetime(tt, fmt.Sprintf("%v", dsV), fmt.Sprintf("%v", tsV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._select":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		vV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		cV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.Select(fmt.Sprintf("%v", vV), toStringStringMap(cV)), true, nil
	case "i18n._plural":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		cV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[2].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Plural(ToInt(cV), forms), true, nil
	case "i18n._selectOrdinal":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		cV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[2].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Selectordinal(ToInt(cV), forms), true, nil
	}
	return nil, false, nil
}

// evalI18nCall dispatches a call to an i18n.* function using the env's locale.
// Returns (result, handled, error). handled is false when the function name is
// not a recognised i18n intrinsic, allowing the caller to fall through.
func (env *Env) evalI18nCall(funcName string, args []ir.CallArg) (any, bool, error) {
	tr := env.translatorFor()
	switch funcName {
	case "tr":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.tr requires 2 arguments")
		}
		keyVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		argsVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		key := fmt.Sprintf("%v", keyVal)
		argsMap := toStringAnyMap(argsVal)
		return tr.Tr(key, key, argsMap), true, nil

	case "format":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.format requires 2 arguments")
		}
		tmplVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		argsVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		tmpl := fmt.Sprintf("%v", tmplVal)
		argsMap := toStringAnyMap(argsVal)
		return tr.Format(tmpl, argsMap), true, nil

	case "numberInt":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.numberInt requires 2 arguments")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		styleVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberInt(ToInt(nVal), fmt.Sprintf("%v", styleVal)), true, nil

	case "numberFloat":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.numberFloat requires 2 arguments")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		styleVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberFloat(toFloat(nVal), fmt.Sprintf("%v", styleVal)), true, nil

	case "select":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.select requires 2 arguments")
		}
		valArg, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		casesArg, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		value := fmt.Sprintf("%v", valArg)
		cases := toStringStringMap(casesArg)
		return tr.Select(value, cases), true, nil

	case "exactly":
		if len(args) < 1 {
			return nil, true, fmt.Errorf("i18n.exactly requires 1 argument")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		return goi18n.Exactly(ToInt(nVal)), true, nil

	case "plural":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.plural requires 2 arguments")
		}
		countVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Plural(ToInt(countVal), forms), true, nil

	case "selectordinal":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.selectordinal requires 2 arguments")
		}
		countVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Selectordinal(ToInt(countVal), forms), true, nil

	case "defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return goi18n.DefaultLocale(), true, nil
	}
	return nil, false, nil
}

// toStringAnyMap coerces a runtime value to map[string]any. An i18n argument
// bag is written `{n = 1}`, which is a map literal or a struct literal
// depending on what the call's declared parameter type made of it.
func toStringAnyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(*Struct); ok {
		m := make(map[string]any, len(s.Fields))
		for _, f := range s.Fields {
			m[f.Name] = f.Value
		}
		return m
	}
	return map[string]any{}
}

// toStringStringMap coerces a runtime value to map[string]string.
func toStringStringMap(v any) map[string]string {
	m := map[string]string{}
	for k, val := range toStringAnyMap(v) {
		m[k] = fmt.Sprintf("%v", val)
	}
	return m
}

// evalPluralKeyMap evaluates a map<PluralKey, string> literal expr, converting
// keys to goi18n.PluralKey values. It handles ir.MapLitIR (the normal form after
// checking) and falls back to a generic eval for other expr kinds.
func (env *Env) evalPluralKeyMap(expr ir.Expr) (map[goi18n.PluralKey]string, error) {
	m := make(map[goi18n.PluralKey]string)
	if ml, ok := expr.(*ir.MapLitIR); ok {
		for _, entry := range ml.Entries {
			kv, err := env.Eval(entry.Key)
			if err != nil {
				return nil, err
			}
			vv, err := env.Eval(entry.Value)
			if err != nil {
				return nil, err
			}
			// Key is either a goi18n.PluralKey (from i18n.zero/one/etc. or
			// i18n.exactly(n)) or something else we can't handle.
			pk, ok := kv.(goi18n.PluralKey)
			if !ok {
				continue
			}
			m[pk] = fmt.Sprintf("%v", vv)
		}
		return m, nil
	}
	// Generic fallback: evaluate the entire map literal.
	v, err := env.Eval(expr)
	if err != nil {
		return nil, err
	}
	return toPluralKeyStringMap(v), nil
}

// toPluralKeyStringMap coerces a runtime value to map[goi18n.PluralKey]string.
// Keys that are goi18n.PluralKey values pass through; nil keys (from unresolved
// i18n.zero/one/etc. constants that were registered with nil Init) are mapped
// to PluralKey sentinel values based on their position in the i18n var list.
func toPluralKeyStringMap(v any) map[goi18n.PluralKey]string {
	m := map[goi18n.PluralKey]string{}
	if src, ok := v.(map[string]any); ok {
		for k, val := range src {
			var pk goi18n.PluralKey
			switch k {
			case "0", "zero":
				pk = goi18n.PluralZero
			case "1", "one":
				pk = goi18n.PluralOne
			case "2", "two":
				pk = goi18n.PluralTwo
			case "3", "few":
				pk = goi18n.PluralFew
			case "4", "many":
				pk = goi18n.PluralMany
			default:
				pk = goi18n.PluralOther
			}
			m[pk] = fmt.Sprintf("%v", val)
		}
	}
	return m
}

// Eval evaluates an IR expression and returns its value.
func (env *Env) Eval(e ir.Expr) (any, error) {
	switch n := e.(type) {
	case *ir.Literal:
		return env.evalLiteral(n)
	case *ir.Ident:
		return env.evalIdent(n)
	case *ir.Binary:
		return env.evalBinary(n)
	case *ir.Unary:
		return env.evalUnary(n)
	case *ir.Ternary:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.Eval(n.Then)
		}
		return env.Eval(n.Else)
	case *ir.Select:
		return env.evalSelect(n)
	case *ir.Index:
		return env.evalIndex(n)
	case *ir.Call:
		return env.evalCall(n)
	case *ir.Conversion:
		return env.evalConversion(n)
	case *ir.ListLit:
		return env.evalListLit(n)
	case *ir.StructLit:
		return env.evalStructLit(n)
	case *ir.MapLitIR:
		return env.evalMapLitIR(n)
	case *ir.Spread:
		return env.Eval(n.Operand)
	case *ir.Lambda:
		return &LambdaValue{fn: n.Func, env: env}, nil
	case *ir.ContextRead:
		return env.ContextVal(n.Ref), nil
	case *ir.Closure:
		return &LambdaValue{fn: n.Func, env: env}, nil
	}
	if e == nil {
		return nil, fmt.Errorf("cannot evaluate <nil> expression")
	}
	panic(fmt.Sprintf("testrunner.Eval: unhandled ir.Expr %T", e))
}

func (env *Env) evalLiteral(e *ir.Literal) (any, error) {
	if e.Suffix != "" {
		return env.makeUnitValue(e)
	}
	if e.Type == nil {
		return e.Value, nil
	}
	switch e.Type.Kind {
	case ir.TypeBool:
		return e.Value == "true", nil
	case ir.TypeNull:
		return nil, nil
	case ir.TypeInt:
		raw := strings.ReplaceAll(e.Value, "_", "")
		if e.Type.Unsigned && e.Type.Bits == 64 {
			// uint64 carries as Go uint64 to keep the range above 2^63 exact.
			u, err := strconv.ParseUint(raw, 0, 64)
			if err != nil {
				return uint64(0), nil
			}
			return u, nil
		}
		n, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			return 0, nil
		}
		return int(n), nil
	case ir.TypeFloat:
		raw := strings.ReplaceAll(e.Value, "_", "")
		f, _ := strconv.ParseFloat(raw, 64)
		if e.Type.Bits == 32 {
			// Pre-round to single precision so float32 arithmetic matches
			// native float32 targets.
			return float64(float32(f)), nil
		}
		return f, nil
	case ir.TypeString:
		return e.Value, nil
	}
	return e.Value, nil
}

func (env *Env) evalIdent(e *ir.Ident) (any, error) {
	if e.Member != "" {
		return e.Member, nil
	}
	if e.Sym != nil {
		if v, ok := env.Value(e.Sym); ok {
			return v, nil
		}
	}
	if env.hasRecv && readsReceiver(e) {
		return env.recv, nil
	}
	if e.Sym != nil {
		return env.lookup(e.Sym)
	}
	return nil, fmt.Errorf("undefined variable %q", e.Name)
}

// readsReceiver reports whether e reads the implicit component receiver: a
// resolved `this` param, or the bare name for a reference the test harness
// synthesized outside the checker and so without a symbol.
func readsReceiver(e *ir.Ident) bool {
	if p, ok := e.Sym.(*ir.Param); ok && p.Receiver {
		return true
	}
	return e.Sym == nil && e.Name == ir.ReceiverParam
}

// resolveCallableFunc resolves name to a function without invoking it, walking
// the parent chain. Needed alongside the symbol on the callee: where a bare
// name is both a #id node handle and a component function (`button #bump(…)`
// next to `func bump()`), the checker resolves it to the handle's Var, so the
// call has to find the function by name.
func (env *Env) resolveCallableFunc(name string) *ir.Func {
	for e := env; e != nil; e = e.parent {
		if e.Comp != nil {
			for _, fn := range e.Comp.Funcs {
				if fn.Name == name && fn.Receiver == "" {
					return fn
				}
			}
			if fn, ok := e.Comp.Methods[name]; ok {
				return fn
			}
		}
		if e.Pkg != nil {
			for _, fn := range e.Pkg.Funcs {
				if fn.Name == name && fn.Receiver == "" {
					return fn
				}
			}
		}
	}
	return nil
}

// methodOn resolves the method named method on the receiver named recv. The
// declaration named recv owns its members, so this is a scope lookup followed
// by a member lookup rather than a table of qualified names.
func (env *Env) methodOn(recv, method string) (*ir.Func, bool) {
	if env.Comp != nil && env.Comp.Name == recv {
		if fn, ok := env.Comp.Methods[method]; ok {
			return fn, true
		}
	}
	for e := env; e != nil; e = e.parent {
		if e.Pkg != nil && e.Pkg.Symbols != nil {
			if fn, ok := e.Pkg.Symbols.LookupMethod(recv, method); ok {
				return fn, true
			}
		}
	}
	return nil, false
}

// A write to a package-level var declared in a parent env mutates the
// binding there, so assignment and toggle resolve the owner first.
func (env *Env) findVarOwner(sym ir.Symbol) *Env {
	for e := env; e != nil; e = e.parent {
		if _, ok := e.vals[sym]; ok {
			return e
		}
	}
	return nil
}

// varInScope reports whether sym is bound in this env or an enclosing one.
// Unlike lookup it never auto-invokes a zero-arg function, so callers can
// distinguish a func-typed variable from a named function.
func (env *Env) varInScope(sym ir.Symbol) bool {
	return sym != nil && env.findVarOwner(sym) != nil
}

// lookup returns the value bound to sym. A zero-arg *ir.Func auto-invokes:
// a computed field is read by naming it, and the symbol says it is a function
// without a separate table having to.
func (env *Env) lookup(sym ir.Symbol) (any, error) {
	if owner := env.findVarOwner(sym); owner != nil {
		return owner.vals[sym], nil
	}
	if fn, ok := sym.(*ir.Func); ok {
		if effective := len(fn.Params); effective == 0 ||
			(effective == 1 && fn.Params[0].Receiver) {
			return env.EvalUserFunc(fn, nil)
		}
		return nil, fmt.Errorf("function %q requires arguments", fn.Name)
	}
	// A declaration the environment never bound but that carries its own
	// value: a constant from a library package, which is in no list this
	// program walks. The symbol has the initializer, so evaluate it.
	if v, ok := sym.(*ir.Var); ok && v.IsConst && v.Init != nil {
		return env.Eval(v.Init)
	}
	return nil, fmt.Errorf("undefined variable %q", sym.SymName())
}

func (env *Env) evalSelect(e *ir.Select) (any, error) {
	// i18n namespace field access: i18n.zero, i18n.one, etc.
	// These are predeclared PluralKey constants; resolve them before
	// attempting a general object lookup.
	if ident, ok := e.Operand.(*ir.Ident); ok && ident.Name == "i18n" {
		if pk, ok := i18nPluralConst(e.Field); ok {
			return pk, nil
		}
	}

	// `pkg.NAME` where pkg is an import's namespace. A namespace is not a
	// value, so evaluating the operand would report the alias as an undefined
	// variable -- which is what a const named through one used to do wherever
	// the optimizer had not already folded it away.
	if ident, ok := e.Operand.(*ir.Ident); ok {
		if ns, ok := ident.Sym.(*ir.Namespace); ok {
			return env.namespaceMember(ns, e.Field)
		}
	}

	obj, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	if cv, ok := obj.(ComponentValue); ok {
		return cv.GetField(e.Field)
	}
	if s, ok := obj.(*Struct); ok {
		v, _ := s.Get(e.Field)
		return v, nil
	}
	if m, ok := obj.(map[string]any); ok {
		return m[e.Field], nil
	}
	return nil, fmt.Errorf("cannot select field %q on %T", e.Field, obj)
}

// namespaceMember evaluates `alias.member` where alias names an imported
// package. A const is the only member a select can produce a value for: a
// type, a component or a function reached this way is a call or a literal, and
// arrives as one of those rather than here.
func (env *Env) namespaceMember(ns *ir.Namespace, field string) (any, error) {
	if ns.Pkg == nil || ns.Pkg.Symbols == nil {
		return nil, fmt.Errorf("package %q has no declarations", ns.Name)
	}
	sym, ok := ns.Pkg.Symbols.LookupMember(field)
	if !ok {
		return nil, fmt.Errorf("undefined: %s.%s", ns.Name, field)
	}
	v, isVar := sym.(*ir.Var)
	if !isVar {
		return nil, fmt.Errorf("cannot read %s.%s as a value", ns.Name, field)
	}
	if val, bound := env.Value(v); bound {
		return val, nil
	}
	if v.Init == nil {
		return nil, nil
	}
	return env.Eval(v.Init)
}

// i18nPluralConst maps the predeclared i18n PluralKey field names to their
// Go runtime values. Returns (PluralKey, true) if found; (nil, false) otherwise.
func i18nPluralConst(field string) (goi18n.PluralKey, bool) {
	switch field {
	case "zero":
		return goi18n.PluralZero, true
	case "one":
		return goi18n.PluralOne, true
	case "two":
		return goi18n.PluralTwo, true
	case "few":
		return goi18n.PluralFew, true
	case "many":
		return goi18n.PluralMany, true
	case "other":
		return goi18n.PluralOther, true
	}
	return goi18n.PluralKey{}, false
}

func (env *Env) evalIndex(e *ir.Index) (any, error) {
	obj, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	idx, err := env.Eval(e.Idx)
	if err != nil {
		return nil, err
	}
	if list, ok := obj.([]any); ok {
		i := ToInt(idx)
		if i < 0 || i >= len(list) {
			return nil, fmt.Errorf("index %d out of range (len %d)", i, len(list))
		}
		return list[i], nil
	}
	if m, ok := obj.(map[string]any); ok {
		key := fmt.Sprintf("%v", idx)
		if v, ok := m[key]; ok {
			return v, nil
		}
		// Miss → zero value. Use the Index node's result type when it is
		// concrete; fall back to sampling an existing map value when the type
		// was inferred as Dyn (e.g. c.scores["x"] through a component Select).
		if e.Type != nil && e.Type.Kind != ir.TypeDyn {
			return zeroValueFor(e.Type), nil
		}
		// Sample deterministically (smallest key): Go map iteration is
		// randomised, so ranging picked a run-to-run-varying value, making the
		// zero-value template flaky for heterogeneous dyn maps (bugs.md #24).
		if len(m) > 0 {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return zeroValueForValue(m[keys[0]]), nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("cannot index %T", obj)
}

func (env *Env) evalConversion(e *ir.Conversion) (any, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	if e.Type == nil {
		return v, nil
	}
	switch e.Type.Kind {
	case ir.TypeInt:
		if r, ok := opeval.ConvertInt(v, e.Type.Bits, e.Type.Unsigned); ok {
			return r, nil
		}
		return ToInt(v), nil
	case ir.TypeFloat:
		if r, ok := opeval.ConvertFloat(v, e.Type.Bits); ok {
			return r, nil
		}
		return toFloat(v), nil
	case ir.TypeString:
		return fmt.Sprintf("%v", v), nil
	case ir.TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return false, nil
	}
	return v, nil
}

func (env *Env) evalListLit(e *ir.ListLit) (any, error) {
	var list []any
	for _, el := range e.Elems {
		if sp, ok := el.(*ir.Spread); ok {
			v, err := env.Eval(sp.Operand)
			if err != nil {
				return nil, err
			}
			if items, ok := v.([]any); ok {
				list = append(list, items...)
			} else {
				list = append(list, v)
			}
			continue
		}
		v, err := env.Eval(el)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	if list == nil {
		list = []any{}
	}
	return list, nil
}

func (env *Env) evalStructLit(e *ir.StructLit) (any, error) {
	s := NewStruct(e.Def, e.Type)
	for _, f := range e.Fields {
		v, err := env.Eval(f.Value)
		if err != nil {
			return nil, err
		}
		if f.Spread {
			if src, ok := v.(*Struct); ok {
				s.Merge(src)
			}
			continue
		}
		s.Set(f.Name, v)
	}
	return s, nil
}

func (env *Env) evalMapLitIR(e *ir.MapLitIR) (any, error) {
	m := make(map[string]any, len(e.Entries))
	for _, entry := range e.Entries {
		k, err := env.Eval(entry.Key)
		if err != nil {
			return nil, err
		}
		v, err := env.Eval(entry.Value)
		if err != nil {
			return nil, err
		}
		// Keys may be PluralKey values (from i18n.exactly/i18n.one etc.) or
		// plain strings. Store them as formatted strings so the map is
		// map[string]any; toPluralKeyStringMap will re-interpret them.
		m[fmt.Sprintf("%v", k)] = v
	}
	return m, nil
}

func (env *Env) evalBinary(e *ir.Binary) (any, error) {
	left, err := env.Eval(e.Left)
	if err != nil {
		return nil, err
	}
	right, err := env.Eval(e.Right)
	if err != nil {
		return nil, err
	}

	lu, leftIsUnit := left.(unitValue)
	ru, rightIsUnit := right.(unitValue)

	if leftIsUnit || rightIsUnit {
		switch e.Op {
		case ast.BinEq:
			if leftIsUnit && rightIsUnit {
				return lu.Equal(ru), nil
			}
			return false, nil
		case ast.BinNeq:
			if leftIsUnit && rightIsUnit {
				return !lu.Equal(ru), nil
			}
			return true, nil
		case ast.BinAdd:
			if leftIsUnit && rightIsUnit {
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot add %s and %s units", lu.Suffix, ru.Suffix)
				}
				return lu.Add(ru), nil
			}
		case ast.BinSub:
			if leftIsUnit && rightIsUnit {
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot subtract %s and %s units", lu.Suffix, ru.Suffix)
				}
				return lu.Sub(ru), nil
			}
		case ast.BinMul:
			if leftIsUnit && !rightIsUnit {
				return lu.Scale(toFloat(right)), nil
			}
			if !leftIsUnit && rightIsUnit {
				return ru.Scale(toFloat(left)), nil
			}
			return nil, fmt.Errorf("cannot multiply two unit values")
		case ast.BinDiv:
			if leftIsUnit && !rightIsUnit {
				r := toFloat(right)
				if r == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return lu.Scale(1 / r), nil
			}
			return nil, fmt.Errorf("cannot divide by a unit value")
		}
	}

	switch e.Op {
	case ast.BinEq:
		return equals(left, right), nil
	case ast.BinNeq:
		return !equals(left, right), nil
	case ast.BinAnd:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("&& requires bool operands, got %T and %T", left, right)
		}
		return lb && rb, nil
	case ast.BinOr:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("|| requires bool operands, got %T and %T", left, right)
		}
		return lb || rb, nil
	case ast.BinLt:
		return compareNum(left, right) < 0, nil
	case ast.BinLte:
		return compareNum(left, right) <= 0, nil
	case ast.BinGt:
		return compareNum(left, right) > 0, nil
	case ast.BinGte:
		return compareNum(left, right) >= 0, nil
	case ast.BinAdd:
		if ls, ok := left.(string); ok {
			return ls + fmt.Sprintf("%v", right), nil
		}
		if rs, ok := right.(string); ok {
			return fmt.Sprintf("%v", left) + rs, nil
		}
		return opeval.Arith(ast.BinAdd, left, right, numKindOf(e.Type))
	case ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
		return opeval.Arith(e.Op, left, right, numKindOf(e.Type))
	}
	return nil, fmt.Errorf("unknown binary op %d", e.Op)
}

// numKindOf maps an IR result type to the opeval width descriptor so arithmetic
// wraps to the right width. A nil or non-numeric type (e.g. a dyn operand in a
// test context) yields the zero NumKind, which is default int / float-fallback.
func numKindOf(t *ir.Type) opeval.NumKind {
	if t == nil || !t.IsNumeric() {
		return opeval.NumKind{}
	}
	return opeval.NumKind{Bits: t.Bits, Unsigned: t.Unsigned, Float: t.Kind == ir.TypeFloat}
}

func (env *Env) evalUnary(e *ir.Unary) (any, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case ast.UnaryNot:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("! requires bool operand, got %T", v)
		}
		return !b, nil
	case ast.UnaryNeg:
		// Negation is 0 - v at the result width, so sized integers wrap
		// correctly (e.g. -(int8 -128) is -128) and floats stay floats.
		return opeval.Arith(ast.BinSub, 0, v, numKindOf(e.Type))
	case ast.UnaryDeref:
		// `*t` for an &-bound loop element (`for var &t = list`). The operand is a
		// listRef into the live list; reading derefs to the current element.
		if ref, ok := v.(*listRef); ok {
			return ref.get(), nil
		}
		// A non-ref operand (e.g. the interpreter already binds structs by
		// reference) derefs to itself.
		return v, nil
	}
	return nil, fmt.Errorf("unknown unary op %d", e.Op)
}

// listRef is an interpreter lvalue into a list element, produced when a loop
// binds its element variable with `&` (`for var &t = list`). Dereferencing reads
// the live element; assigning through the deref writes it back by index.
type listRef struct {
	list []any
	idx  int
}

func (r *listRef) get() any  { return r.list[r.idx] }
func (r *listRef) set(v any) { r.list[r.idx] = v }

func (env *Env) evalCall(call *ir.Call) (any, error) {
	// i18n by the id, before the call shape is examined: an entry point may
	// arrive qualified or not, and the `i18n._*` primitives arrive plain once
	// the wrapper is inlined, so neither is reliably a namespace call by the
	// time it gets here. The interpreter is the only implementation of these
	// — the primitives' bodies were dropped as placeholders after checking.
	if call.Func != nil {
		if id := call.Func.Intrinsic; strings.HasPrefix(id, "i18n.") {
			if strings.HasPrefix(id, "i18n._") {
				if result, handled, err := env.evalI18nPrimitiveCall(id, call.Args); handled {
					return result, err
				}
			} else if result, handled, err := env.evalI18nCall(strings.TrimPrefix(id, "i18n."), call.Args); handled {
				return result, err
			}
		}
	}

	// Namespace call (ns.foo / html.div) — receiver preserved.
	if call.Receiver != nil {
		return env.evalNamespaceCall(call)
	}

	// Type-attached method call (checker-normalized): Args[0] is the receiver.
	if call.Func != nil && call.Func.Receiver != "" {
		return env.evalTypeMethodCall(call)
	}

	// Plain function call.
	if call.Func != nil {
		return env.evalPlainFunc(call)
	}

	// Callee expression (func-typed var).
	if call.Callee != nil {
		// Bare named-function / component-method call, e.g. `bump()`. The
		// checker leaves Func nil for component methods (they aren't in plain
		// scope) and sets Callee to the bare ident. Resolve and invoke directly,
		// honouring the call's args. This must precede Eval(callee): lookup()
		// auto-invokes a zero-arg function when its name is *read*, which would
		// fire the body as a side effect here and then discard the result.
		if id, ok := call.Callee.(*ir.Ident); ok && !env.varInScope(id.Sym) {
			if fn, ok := id.Sym.(*ir.Func); ok {
				return env.EvalUserFuncCallArgs(fn, call.Args)
			}
			if fn := env.resolveCallableFunc(id.Name); fn != nil {
				return env.EvalUserFuncCallArgs(fn, call.Args)
			}
		}
		v, err := env.Eval(call.Callee)
		if err != nil {
			return nil, err
		}
		if lv, ok := v.(*LambdaValue); ok {
			args, err := env.evalCallArgs(call.Args)
			if err != nil {
				return nil, err
			}
			return lv.Call(args)
		}
	}
	return nil, fmt.Errorf("cannot call unresolved expression")
}

func (env *Env) evalPlainFunc(call *ir.Call) (any, error) {
	name := call.Func.Name
	switch name {
	case "string":
		if len(call.Args) == 1 {
			v, err := env.Eval(call.Args[0].Value)
			if err != nil {
				return nil, err
			}
			return fmt.Sprintf("%v", v), nil
		}
	case "int":
		if len(call.Args) == 1 {
			v, err := env.Eval(call.Args[0].Value)
			if err != nil {
				return nil, err
			}
			return ToInt(v), nil
		}
	case "float":
		if len(call.Args) == 1 {
			v, err := env.Eval(call.Args[0].Value)
			if err != nil {
				return nil, err
			}
			return toFloat(v), nil
		}
	case "regex":
		if len(call.Args) == 1 {
			v, err := env.Eval(call.Args[0].Value)
			if err != nil {
				return nil, err
			}
			re, err := regexp.Compile(fmt.Sprintf("%v", v))
			if err != nil {
				return nil, fmt.Errorf("invalid regex pattern: %v", err)
			}
			return re, nil
		}
	}
	return env.EvalUserFuncCallArgs(call.Func, call.Args)
}

func (env *Env) evalTypeMethodCall(call *ir.Call) (any, error) {
	method := call.Func.Name
	receiverName := call.Func.Receiver

	// ErrorRaise: construct an ErrorEvent payload and bubble a RaisedError
	// up the Go error chain. The originating CallStmt's ErrorMode then
	// routes it into the resolved handler (or propagates).
	if call.Func.Intrinsic == "error.raise" {
		evt := map[string]any{"message": "", "kind": ""}
		if len(call.Args) >= 1 {
			if v, err := env.Eval(call.Args[0].Value); err == nil {
				evt["message"] = v
			}
		}
		if len(call.Args) >= 2 {
			if v, err := env.Eval(call.Args[1].Value); err == nil {
				evt["kind"] = v
			}
		}
		return nil, &RaisedError{Event: evt}
	}

	// Alert and File have visible effects a headless run cannot perform, so
	// the interpreter records them and answers with a fixed value.
	switch call.Func.Intrinsic {
	case "Alert.toast":
		return env.logAlertToast(call.Args)
	case "Alert.info", "Alert.warn", "Alert.error":
		return env.logAlertSingle(method, call.Args)
	case "Alert.confirm":
		return env.logAlertConfirm(call.Args)
	case "File.pick":
		env.Log = append(env.Log, "[File.pick]")
		return "/mock/file.txt", nil
	case "File.pickFolder":
		env.Log = append(env.Log, "[File.pickFolder]")
		return "/mock/folder", nil
	}

	evalArgs, err := env.evalCallArgs(call.Args)
	if err != nil {
		return nil, err
	}

	// testingT dispatch: t.assert / t.tick / t.test.
	if len(evalArgs) > 0 {
		if tv, ok := evalArgs[0].(TestingT); ok {
			return tv.CallMethod(env, method, argExprs(call.Args[1:]))
		}
		if cv, ok := evalArgs[0].(ComponentValue); ok {
			if result, handled, err := cv.InvokeMethod(env, method, call.Args[1:]); handled {
				return result, err
			}
		}
	}

	if result, handled, err := runIntrinsic(call.Func.Intrinsic, evalArgs); handled {
		return result, err
	}

	// List mutation (push/remove) needs writeback; evaluate before user funcs
	// since stdlib push/remove delegate to untranslated intrinsics.
	if receiverName == "list" && (method == "push" || method == "remove") && len(call.Args) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}

	// Reached only when the id above found no implementation. Every
	// #[intrinsic] carries a body now, and most are placeholders standing in
	// for a backend's — running one answers with a plausible wrong value
	// rather than an error, which is why the id is tried first.
	// The checker already resolved which member this call names; re-deriving
	// it from the receiver's name would fail for a type reached through an
	// import alias, whose name here is not the name it was declared under.
	if len(call.Func.Block) > 0 {
		callEnv, args := env, call.Args
		// A method on a generic receiver declares no receiver parameter and
		// names the value `this`, so the leading argument the checker
		// normalized in has nothing to bind to. Supply it the way a component
		// method gets its receiver, in a scope of its own.
		if len(call.Args) == len(call.Func.Params)+1 && len(evalArgs) > 0 {
			callEnv = env.Snapshot()
			callEnv.SetReceiver(evalArgs[0])
			args = call.Args[1:]
		}
		return callEnv.EvalUserFuncCallArgs(call.Func, args)
	}

	// List/string higher-order and other built-in methods.
	if len(evalArgs) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}
	return nil, fmt.Errorf("unknown method %q", receiverName+"."+method)
}

func (env *Env) evalNamespaceCall(call *ir.Call) (any, error) {
	// Static-form receiver: Type.method(args) where Type is a type name. The
	// checker leaves Func nil when the method is a user-defined type method
	// (component-scoped) the symbol table doesn't see. Dispatch by qualified
	// name through the declaration it names.
	if ident, ok := call.Receiver.(*ir.Ident); ok {
		if _, valErr := env.evalIdent(ident); valErr != nil {
			method := methodNameFromCall(call)

			// Resolve the member once: its mark says whether this interpreter
			// implements it, and its body says whether there is anything to
			// run if it does not. A namespace's members include bodyless
			// intrinsic declarations, and running one of those returns null.
			if fn, ok := env.methodOn(ident.Name, method); ok {
				if evalArgs, err := env.evalCallArgs(call.Args); err == nil {
					if result, handled, err := runIntrinsic(fn.Intrinsic, evalArgs); handled {
						return result, err
					}
				}
				if len(fn.Block) > 0 {
					return env.EvalUserFuncCallArgs(fn, call.Args)
				}
			}
		}
	}

	// If the receiver is a value-bearing expression (variable ident, not a
	// type/namespace marker), treat this as an instance method call on that
	// value. This covers c.method(...) on component values and t.method(...)
	// on the Test value where the checker left Func nil (unresolved method).
	recv, err := env.Eval(call.Receiver)
	if err == nil {
		method := methodNameFromCall(call)
		if method != "" {
			// Element-ref event invocation: `c.btn.click()` (the handler is
			// stored at key "@click" on the rendered element map). The name
			// arrives "@"-prefixed when the checker statically tagged it, or
			// bare when the receiver's type was dynamic (e.g.
			// c.children[0].click()) and the checker couldn't resolve the
			// host component — so try the "@"-prefixed key regardless.
			explicitEvent := strings.HasPrefix(method, "@")
			event := strings.TrimPrefix(method, "@")
			if m, ok := recv.(map[string]any); ok {
				if h, ok := m["@"+event].(*ir.Func); ok {
					handlerEnv := env
					if oe, ok := m["__ownerEnv"].(*Env); ok && oe != nil {
						handlerEnv = oe
					}
					if owner, ok := m["__ownerComponent"]; ok && owner != nil {
						handlerEnv.SetReceiver(owner)
					}
					return handlerEnv.runEventHandler(h, call.Args, event)
				}
			}
			if explicitEvent {
				// Checker tagged this as an event but no handler is installed:
				// the caller never bound it, so the invocation is a no-op.
				return nil, nil
			}
			if tv, ok := recv.(TestingT); ok {
				return tv.CallMethod(env, method, argExprs(call.Args))
			}
			if cv, ok := recv.(ComponentValue); ok {
				if result, handled, err := cv.InvokeMethod(env, method, call.Args); handled {
					return result, err
				}
			}
			// Instance method on a primitive value: dispatch by runtime type.
			evalArgs := make([]any, 0, len(call.Args)+1)
			evalArgs = append(evalArgs, recv)
			for _, a := range call.Args {
				v, err := env.Eval(a.Value)
				if err != nil {
					return nil, err
				}
				evalArgs = append(evalArgs, v)
			}
			// The receiver's runtime type names the declaration whose member
			// this is; the member's mark says how to run it.
			recvFn, recvOK := env.methodOn(runtimeTypeName(recv), method)
			if recvOK {
				if result, handled, err := runIntrinsic(recvFn.Intrinsic, evalArgs); handled {
					return result, err
				}
			}
			// Mutation methods on lists need a writeback; dispatch before
			// user-defined stdlib bodies that delegate to intrinsics.
			if method == "push" || method == "remove" || method == "filter" || method == "map" {
				return env.evalBuiltinMethodFromRecv(call.Receiver, method, recv, evalArgs[1:])
			}
			if fn := recvFn; recvOK && len(fn.Block) > 0 {
				// A method on a generic receiver (list<T>, map<K,V>) declares
				// no receiver parameter and names the value `this`, so there
				// is nothing in the argument list to bind it to. Supply it the
				// way a component method gets its receiver, in a scope of its
				// own so the binding does not outlive the call.
				callEnv, synth := env, make([]ir.Expr, 0, len(call.Args)+1)
				if len(fn.Params) == len(call.Args) {
					callEnv = env.Snapshot()
					callEnv.SetReceiver(recv)
				} else {
					synth = append(synth, call.Receiver)
				}
				for _, a := range call.Args {
					synth = append(synth, a.Value)
				}
				return callEnv.EvalUserFunc(fn, synth)
			}
			// Enum value method fallback: when recv is a bare string and no
			// dispatch succeeded, scan user-defined enums for a matching
			// member and try the method against that enum's namespace. Covers
			// `c.s.isOk()` where `c.s` is typed dyn externally but holds an
			// enum member at runtime.
			if s, ok := recv.(string); ok && env.Pkg != nil {
				for _, ed := range env.Pkg.Enums {
					for _, m := range ed.Members {
						if m.Name == s {
							if fn, ok := ed.Methods[method]; ok {
								synth := make([]ir.Expr, 0, len(call.Args)+1)
								synth = append(synth, call.Receiver)
								for _, a := range call.Args {
									synth = append(synth, a.Value)
								}
								return env.EvalUserFunc(fn, synth)
							}
						}
					}
				}
			}
			return env.evalBuiltinMethodFromRecv(call.Receiver, method, recv, evalArgs[1:])
		}
	}

	// Resolved namespace call delegates to the type-method path.
	if call.Func != nil {
		return env.evalTypeMethodCall(&ir.Call{
			AST:  call.AST,
			Type: call.Type,
			Func: call.Func,
			Args: call.Args,
		})
	}
	return nil, fmt.Errorf("unresolved namespace call")
}

// runEventHandler invokes an event handler function's body using the current
// env. Args are evaluated and bound positionally to params; the caller is
// expected to pass the event payload as a literal struct (e.g.
// `c.entry.input(InputEvent{value="hello"})`) so the body's `e.value`
// resolves through the regular struct-field path.
// runEventHandlerValues runs a handler against values rather than expressions,
// which is the shape an event arrives in from a host: the widget already
// evaluated them.
func (env *Env) runEventHandlerValues(fn *ir.Func, vals []any) (any, error) {
	for i, p := range fn.Params {
		if i >= len(vals) {
			break
		}
		env.Set(p, coerceEventArg(p.Type, vals[i]))
	}
	return env.execBlockForResult(fn.Block)
}

// coerceEventArg shapes what a host reported into what the handler declared.
//
// A toolkit reports positional values -- OnChanged hands over a string -- while
// an event's payload is a declared struct, InputEvent{value string}. The
// declaration is the only thing that knows which is which, so the adaptation
// happens here rather than in a host that would have to know the field names.
func coerceEventArg(want *ir.Type, v any) any {
	if want == nil || want.Kind != ir.TypeStruct {
		return v
	}
	if _, already := v.(*Struct); already {
		return v
	}
	def, _ := want.Decl.(*ir.StructDef)
	if def == nil || len(def.Fields) == 0 {
		return v
	}
	// One value, one payload: it fills the first field. An event carrying more
	// than one would need the host to report them in declaration order, which
	// is what a Spec's param naming is for when that arrives.
	out := NewStruct(def, want)
	for i, f := range def.Fields {
		if i == 0 {
			out.Set(f.Name, v)
			continue
		}
		out.Set(f.Name, zeroOf(f.Type))
	}
	return out
}

// zeroOf is the empty value of a type, for a payload field nothing reported.
func zeroOf(t *ir.Type) any {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case ir.TypeString:
		return ""
	case ir.TypeBool:
		return false
	case ir.TypeInt:
		return 0
	case ir.TypeFloat:
		return 0.0
	}
	return nil
}

func (env *Env) runEventHandler(fn *ir.Func, args []ir.CallArg, eventName string) (any, error) {
	_ = eventName // reserved for future per-event semantics
	for i, p := range fn.Params {
		if i < len(args) {
			v, err := env.Eval(args[i].Value)
			if err != nil {
				return nil, err
			}
			env.Set(p, v)
		}
	}
	return env.execBlockForResult(fn.Block)
}

// evalBuiltinMethodFromRecv dispatches list/string built-in methods when the
// receiver expression is known separately from the rest of the args.
func (env *Env) evalBuiltinMethodFromRecv(recvExpr ir.Expr, method string, recv any, rest []any) (any, error) {
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				if b, ok := v.(bool); ok && b {
					out = append(out, item)
				}
			}
			if out == nil {
				out = []any{}
			}
			return out, nil
		}
	case "map":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			newList := append(list, rest[0])
			return env.writeBackList(recvExpr, newList)
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			idx := ToInt(rest[0])
			if idx < 0 || idx >= len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			newList := append(list[:idx], list[idx+1:]...)
			return env.writeBackList(recvExpr, newList)
		}
	case "contains":
		if s, ok := recv.(string); ok && len(rest) == 1 {
			return strings.Contains(s, fmt.Sprintf("%v", rest[0])), nil
		}
	}
	// --- Map methods ---
	if m, ok := recv.(map[string]any); ok {
		return mapMethodResult(method, m, rest)
	}
	return nil, fmt.Errorf("unsupported method %q on %T", method, recv)
}

// methodNameFromCall recovers the method name for a Receiver-bearing call from
// the AST back-reference (set by the checker when Func couldn't be resolved).
// For an element-ref event trigger (`c.btn.click()`) the name is prefixed
// with "@", which is the interpreter's own key and not source syntax.
func methodNameFromCall(call *ir.Call) string {
	// Element-ref event triggers (`c.btn.click()`) are tagged by the
	// checker; the interpreter keys handlers under "@<event>" internally.
	if call.Event != "" {
		return "@" + call.Event
	}
	if call.Func != nil {
		return call.Func.Name
	}
	if call.AST == nil {
		return ""
	}
	if sel, ok := call.AST.Func.(*ast.SelectExpr); ok {
		return sel.Field
	}
	return ""
}

func (env *Env) evalBuiltinMethod(call *ir.Call, method string, evalArgs []any) (any, error) {
	recv := evalArgs[0]
	rest := evalArgs[1:]
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				if b, ok := v.(bool); ok && b {
					out = append(out, item)
				}
			}
			if out == nil {
				out = []any{}
			}
			return out, nil
		}
	case "map":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			newList := append(list, rest[0])
			return env.writeBackList(call.Args[0].Value, newList)
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			idx := ToInt(rest[0])
			if idx < 0 || idx >= len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			newList := append(list[:idx], list[idx+1:]...)
			return env.writeBackList(call.Args[0].Value, newList)
		}
	case "contains":
		if s, ok := recv.(string); ok && len(rest) == 1 {
			return strings.Contains(s, fmt.Sprintf("%v", rest[0])), nil
		}
	}
	// --- Map methods ---
	if m, ok := recv.(map[string]any); ok {
		return mapMethodResult(method, m, rest)
	}
	return nil, fmt.Errorf("unsupported method %q on %T", method, recv)
}

// writeBackList applies a mutated list back to its originating variable.
func (env *Env) writeBackList(target ir.Expr, newList []any) (any, error) {
	switch t := target.(type) {
	case *ir.Ident:
		if owner := env.findVarOwner(t.Sym); owner != nil {
			owner.vals[t.Sym] = newList
			// Noted like an assignment, because that is what it is: a mutating
			// method writes its receiver, and RebindFrom carries back only what
			// a scope wrote. Without this a bare `xs.push(v)` inside a loop
			// iteration mutated that iteration's snapshot and nothing else --
			// which the effect fixtures caught the moment they stopped spelling
			// it `xs = xs.push(v)`, an ir.Assign that noted itself.
			owner.noteAssigned(t.Sym)
		} else {
			env.Set(t.Sym, newList)
			env.noteAssigned(t.Sym)
		}
	case *ir.Select:
		obj, err := env.Eval(t.Operand)
		if err != nil {
			return nil, err
		}
		if cv, ok := obj.(ComponentValue); ok {
			if err := cv.WriteBackList(t.Field, newList); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("testrunner.writeBackList: unhandled target ir.Expr %T", target)
	}
	return newList, nil
}

// mapMethodResult dispatches map built-in methods. All runtime maps use
// map[string]any regardless of the declared K type (keys are stringified in
// evalMapLitIR), so a single helper suffices.
func mapMethodResult(method string, m map[string]any, rest []any) (any, error) {
	switch method {
	case "length":
		return len(m), nil
	case "keys":
		out := make([]any, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out, nil
	case "values":
		out := make([]any, 0, len(m))
		for _, v := range m {
			out = append(out, v)
		}
		return out, nil
	case "contains":
		if len(rest) != 1 {
			return nil, fmt.Errorf("contains requires 1 argument")
		}
		k := fmt.Sprintf("%v", rest[0])
		_, ok := m[k]
		return ok, nil
	case "get":
		if len(rest) != 2 {
			return nil, fmt.Errorf("get requires 2 arguments")
		}
		k := fmt.Sprintf("%v", rest[0])
		if v, ok := m[k]; ok {
			return v, nil
		}
		return rest[1], nil // default
	}
	return nil, fmt.Errorf("unsupported method %q on map", method)
}

func (env *Env) evalCallArgs(args []ir.CallArg) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		v, err := env.Eval(a.Value)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func argExprs(args []ir.CallArg) []ir.Expr {
	out := make([]ir.Expr, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

// CallUserFuncValues invokes fn with pre-evaluated Go-side argument values
// (no expression evaluation needed). Used by the optimizer's interpretFunc
// adapter, which already has folded constant values in hand.
func (env *Env) CallUserFuncValues(fn *ir.Func, args []any) (any, error) {
	child := env.Snapshot()
	for i, p := range fn.Params {
		if i < len(args) {
			child.Set(p, args[i])
		}
	}
	return child.execBlockForResult(fn.Block)
}

// EvalUserFuncWithValues is like EvalUserFunc but takes pre-evaluated arg
// values. Used by callers that evaluate args in a different env from the
// one used to execute the function body (e.g. component methods).
func (env *Env) EvalUserFuncWithValues(fn *ir.Func, args []any) (any, error) {
	return env.evalUserFuncCore(fn, args)
}

// CopyDepth transfers the call-depth counter from src to dst. Used by
// out-of-tree dispatchers (e.g. testrunner component-method dispatch) to
// keep depth tracking accurate across env boundaries.
func CopyDepth(src, dst *Env) {
	if src == nil || dst == nil {
		return
	}
	dst.depth = src.depth
}

func (env *Env) EvalUserFunc(fn *ir.Func, argExprs []ir.Expr) (any, error) {
	args := make([]any, len(argExprs))
	for i, a := range argExprs {
		v, err := env.Eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return env.evalUserFuncCore(fn, args)
}

// EvalUserFuncCallArgs evaluates fn using named CallArg binding. Args with a
// Name field are bound to the matching parameter by name; args without a Name
// are bound positionally. Parameters with no supplied arg use their default.
func (env *Env) EvalUserFuncCallArgs(fn *ir.Func, callArgs []ir.CallArg) (any, error) {
	// Check whether any arg carries a Name — if not, fall back to positional.
	hasNamed := false
	for _, a := range callArgs {
		if a.Name != "" {
			hasNamed = true
			break
		}
	}
	if !hasNamed {
		return env.EvalUserFunc(fn, argExprs(callArgs))
	}

	// Build a param→value map honouring defaults.
	vals := make(map[string]any, len(fn.Params))
	for _, p := range fn.Params {
		if p.Default != nil {
			v, err := env.Eval(p.Default)
			if err != nil {
				return nil, err
			}
			vals[p.Name] = v
		}
	}
	// Positional index counter (for args without a name).
	positional := 0
	for _, a := range callArgs {
		v, err := env.Eval(a.Value)
		if err != nil {
			return nil, err
		}
		if a.Name != "" {
			vals[a.Name] = v
		} else {
			if positional < len(fn.Params) {
				vals[fn.Params[positional].Name] = v
			}
			positional++
		}
	}
	// Assemble positional slice in param order for evalUserFuncCore.
	args := make([]any, len(fn.Params))
	for i, p := range fn.Params {
		args[i] = vals[p.Name]
	}
	return env.evalUserFuncCore(fn, args)
}

func (env *Env) evalUserFuncCore(fn *ir.Func, args []any) (any, error) {

	env.depth++
	if env.depth > maxCallDepth {
		env.depth--
		return nil, fmt.Errorf("stack overflow: call depth exceeded %d", maxCallDepth)
	}
	defer func() { env.depth-- }()

	isPure := fn.Return != nil && fn.Return.Kind != ir.TypeDyn
	for {
		var execEnv *Env
		if !isPure {
			execEnv = env
		} else {
			execEnv = env.Snapshot()
		}

		// If the function declares a leading `this` receiver but the caller
		// supplied one-fewer arguments (intra-component method calls compile
		// as plain `foo(args)`), shift bindings so user args land in n,
		// not in this. `this` is expected to be bound in execEnv already.
		argOffset := 0
		if len(fn.Params) > 0 && fn.Params[0].Receiver && len(args) == len(fn.Params)-1 {
			argOffset = 1
		}
		// A recursive call reuses the same Param symbols in the same env, so
		// the previous frame's bindings still need saving and restoring.
		savedVars := make(map[ir.Symbol]any, len(fn.Params))
		for i, p := range fn.Params {
			if v, ok := execEnv.vals[p]; ok {
				savedVars[p] = v
			}
			argIdx := i - argOffset
			if argIdx >= 0 && argIdx < len(args) {
				execEnv.Set(p, args[argIdx])
			}
		}
		restoreVoid := func() {
			if !isPure {
				for _, p := range fn.Params {
					if orig, ok := savedVars[p]; ok {
						execEnv.vals[p] = orig
					} else {
						delete(execEnv.vals, p)
					}
				}
			}
		}

		// Walk block looking for a Return at the top of the body: that one is
		// the tail call candidate, so it is evaluated below rather than here.
		// A Return anywhere deeper arrives as a returnSignal from Exec and
		// ends the call with the value it carries, without trampolining.
		var tailExpr ir.Expr
		var localVars []ir.Symbol
		nested := false
		var nestedResult any
		for _, stmt := range fn.Block {
			if ret, ok := stmt.(*ir.Return); ok {
				tailExpr = ret.Value
				break
			}
			if lv, ok := stmt.(*ir.LocalVar); ok {
				if lv.Init != nil {
					v, err := execEnv.Eval(lv.Init)
					if err != nil {
						restoreVoid()
						return nil, err
					}
					execEnv.Set(lv.Sym, CopyValue(v))
					localVars = append(localVars, lv.Sym)
				}
				continue
			}
			if err := execEnv.Exec(stmt); err != nil {
				if ret, ok := err.(*returnSignal); ok {
					nested, nestedResult = true, ret.value
					break
				}
				restoreVoid()
				return nil, err
			}
		}

		if nested {
			restoreVoid()
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			return nestedResult, nil
		}

		if tailExpr == nil {
			restoreVoid()
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			return nil, nil
		}

		if isPure {
			result, newArgs, isTail, err := execEnv.evalTailAware(tailExpr, fn)
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			if err != nil {
				return nil, err
			}
			if isTail {
				args = newArgs
				continue
			}
			return result, nil
		}

		result, err := execEnv.Eval(tailExpr)
		restoreVoid()
		for _, sym := range localVars {
			delete(execEnv.vals, sym)
		}
		return result, err
	}
}

// execBlockForResult runs a function body, returning the value of whichever
// Return it reaches — including one nested inside an if or a for.
func (env *Env) execBlockForResult(block []ir.Stmt) (any, error) {
	for _, stmt := range block {
		err := env.Exec(stmt)
		if ret, ok := err.(*returnSignal); ok {
			return ret.value, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// evalTailAware detects self-tail calls for trampolining.
func (env *Env) evalTailAware(e ir.Expr, fn *ir.Func) (any, []any, bool, error) {
	switch n := e.(type) {
	case *ir.Call:
		if n.Func == fn {
			newArgs, err := env.evalCallArgs(n.Args)
			if err != nil {
				return nil, nil, false, err
			}
			return nil, newArgs, true, nil
		}
		result, err := env.Eval(e)
		return result, nil, false, err
	case *ir.Ternary:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, nil, false, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, nil, false, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.evalTailAware(n.Then, fn)
		}
		return env.evalTailAware(n.Else, fn)
	default:
		result, err := env.Eval(e)
		return result, nil, false, err
	}
}

// makeUnitValue converts a unit literal to a unitValue.
func (env *Env) makeUnitValue(lit *ir.Literal) (unitValue, error) {
	suffix := lit.Suffix
	raw := lit.Value
	if before, ok := strings.CutSuffix(raw, suffix); ok {
		raw = before
	}
	raw = strings.ReplaceAll(raw, "_", "")
	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return unitValue{}, fmt.Errorf("invalid unit literal %q: %w", lit.Value, err)
	}
	table := env.Units[suffix]
	baseAmount := num
	if table != nil {
		if factor, ok := table.Conversions[suffix]; ok {
			baseAmount = num * factor
		}
	}
	return unitValue{BaseAmount: baseAmount, Suffix: suffix, Table: table}, nil
}

func (env *Env) logAlertToast(args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	msg, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	variant := any("info")
	if len(args) > 1 {
		v, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, err
		}
		variant = v
	}
	env.Log = append(env.Log, fmt.Sprintf("[toast:%v] %v", variant, msg))
	return nil, nil
}

func (env *Env) logAlertSingle(method string, args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	v, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	env.Log = append(env.Log, fmt.Sprintf("[%s] %v", method, v))
	return nil, nil
}

func (env *Env) logAlertConfirm(args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return true, nil
	}
	v, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	env.Log = append(env.Log, fmt.Sprintf("[confirm] %v", v))
	return true, nil
}

// --- helpers ---

func equals(a, b any) bool {
	if au, ok := a.(unitValue); ok {
		if bu, ok := b.(unitValue); ok {
			return au.Equal(bu)
		}
		return false
	}
	// Two struct values hold the same fields whatever order they were written
	// in, so they are compared by name rather than by their rendered form.
	if as, ok := a.(*Struct); ok {
		bs, ok := b.(*Struct)
		if !ok || len(as.Fields) != len(bs.Fields) {
			return false
		}
		for _, f := range as.Fields {
			other, found := bs.Get(f.Name)
			if !found || !equals(f.Value, other) {
				return false
			}
		}
		return true
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v any) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case uint64:
		return float64(val)
	case float64:
		return val
	case bool:
		if val {
			return 1
		}
		return 0
	case unitValue:
		return val.BaseAmount
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	return 0
}

func ToInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case uint64:
		return int(val)
	case float64:
		return int(val)
	case bool:
		if val {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

func compareNum(a, b any) int {
	fa, fb := toFloat(a), toFloat(b)
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

func runtimeTypeName(v any) string {
	switch x := v.(type) {
	case int:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	case *Struct:
		// The declared type name, so method dispatch reaches user-defined
		// methods (`v.dot()` → Vec2.dot).
		if name := x.Name(); name != "" {
			return name
		}
		return "struct"
	case map[string]any:
		// Not "map": the name is looked up as a declared type, and the
		// built-in map declares methods with no body to run.
		return "struct"
	case *regexp.Regexp:
		return "regex"
	default:
		return "dyn"
	}
}

// zeroValueFor returns the runtime zero value for an IR type.
func zeroValueFor(t *ir.Type) any {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case ir.TypeInt:
		return 0
	case ir.TypeFloat:
		return 0.0
	case ir.TypeBool:
		return false
	case ir.TypeString:
		return ""
	}
	return nil
}

// zeroValueForValue returns the zero value of the same runtime type as v.
// Used when the static type is unknown (TypeDyn) but we can sample a value.
func zeroValueForValue(v any) any {
	switch v.(type) {
	case int:
		return 0
	case float64:
		return 0.0
	case bool:
		return false
	case string:
		return ""
	}
	return nil
}
