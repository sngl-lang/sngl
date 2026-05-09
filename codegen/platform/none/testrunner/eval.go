package testrunner

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
	goi18n "git.duckfam.us/jonathan/sngl/pkg/go/i18n"
)

// maxCallDepth is the maximum allowed function call depth.
const maxCallDepth = 100

// lambdaValue is a closure captured by a lambda expression.
type lambdaValue struct {
	fn  *ir.Func
	env *Env
}

// call invokes the lambda with the given values.
func (lv *lambdaValue) call(args []any) (any, error) {
	child := lv.env.Snapshot()
	for i, p := range lv.fn.Params {
		if i < len(args) {
			child.vars[p.Name] = args[i]
		}
	}
	return child.execBlockForResult(lv.fn.Block)
}

// callWithEnv is like call but uses the provided env directly (no snapshot).
func (lv *lambdaValue) callWithEnv(env *Env, args []any) (any, error) {
	for i, p := range lv.fn.Params {
		if i < len(args) {
			env.vars[p.Name] = args[i]
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
	vars        map[string]any
	consts      map[string]any
	funcs       map[string]*ir.Func
	units       map[string]*unitTable
	pkg         *ir.Package
	comp        *ir.Component
	bodyStmts   []ir.Stmt
	depth       int
	renderDepth int
	Log         []string
	// locale is the active BCP-47 locale for i18n calls (default "en").
	locale string
}

func NewEnv() *Env {
	return &Env{
		vars:   map[string]any{},
		consts: map[string]any{},
		funcs:  map[string]*ir.Func{},
	}
}

// SetFunc registers a user-defined function.
func (env *Env) SetFunc(fn *ir.Func) {
	if fn.Receiver != "" {
		env.funcs[fn.Receiver+"."+fn.Name] = fn
		return
	}
	env.funcs[fn.Name] = fn
}

// SetVar sets a variable in the environment.
func (env *Env) SetVar(name string, val any) {
	env.vars[name] = val
}

// Snapshot returns a shallow copy of the env.
func (env *Env) Snapshot() *Env {
	cp := &Env{
		vars:        make(map[string]any, len(env.vars)),
		consts:      env.consts,
		funcs:       env.funcs,
		units:       env.units,
		pkg:         env.pkg,
		comp:        env.comp,
		bodyStmts:   env.bodyStmts,
		depth:       env.depth,
		renderDepth: env.renderDepth,
		locale:      env.locale,
	}
	maps.Copy(cp.vars, env.vars)
	return cp
}

// translatorFor returns a Translator for the env's current locale.
func (env *Env) translatorFor() *goi18n.Translator {
	loc := env.locale
	if loc == "" {
		loc = "en"
	}
	return goi18n.NewTranslator(goi18n.Manifest{}, loc)
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
		return tr.NumberInt(toInt(nVal), fmt.Sprintf("%v", styleVal)), true, nil

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
		return goi18n.Exactly(toInt(nVal)), true, nil

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
		return tr.Plural(toInt(countVal), forms), true, nil

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
		return tr.Selectordinal(toInt(countVal), forms), true, nil
	}
	return nil, false, nil
}

// toStringAnyMap coerces a runtime value to map[string]any.
func toStringAnyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// toStringStringMap coerces a runtime value to map[string]string.
func toStringStringMap(v any) map[string]string {
	m := map[string]string{}
	if src, ok := v.(map[string]any); ok {
		for k, val := range src {
			m[k] = fmt.Sprintf("%v", val)
		}
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
		return &lambdaValue{fn: n.Func, env: env}, nil
	}
	if e == nil {
		return nil, fmt.Errorf("cannot evaluate <nil> expression")
	}
	return nil, fmt.Errorf("cannot evaluate %T", e)
}

func (env *Env) evalLiteral(e *ir.Literal) (any, error) {
	if e.Suffix != "" {
		return env.makeUnitValue(e)
	}
	if e.Type == nil {
		return e.Raw, nil
	}
	switch e.Type.Kind {
	case ir.TypeBool:
		return e.Raw == "true", nil
	case ir.TypeNull:
		return nil, nil
	case ir.TypeInt:
		raw := strings.ReplaceAll(e.Raw, "_", "")
		n, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			return 0, nil
		}
		return int(n), nil
	case ir.TypeFloat:
		raw := strings.ReplaceAll(e.Raw, "_", "")
		f, _ := strconv.ParseFloat(raw, 64)
		return f, nil
	case ir.TypeString:
		if e.AST != nil {
			if s, ok := literalString(e.AST); ok {
				return s, nil
			}
		}
		return unquoteString(e.Raw), nil
	case ir.TypeColor:
		return colorHexToStruct(e.Raw), nil
	}
	return e.Raw, nil
}

func (env *Env) evalIdent(e *ir.Ident) (any, error) {
	if e.Member != "" {
		return e.Member, nil
	}
	return env.lookup(e.Name)
}

func (env *Env) lookup(name string) (any, error) {
	if v, ok := env.vars[name]; ok {
		return v, nil
	}
	if v, ok := env.consts[name]; ok {
		return v, nil
	}
	// Zero-arg functions auto-invoke (computed fields)
	if fn, ok := env.funcs[name]; ok && len(fn.Params) == 0 && fn.Receiver == "" {
		return env.evalUserFunc(fn, nil)
	}
	return nil, fmt.Errorf("undefined variable %q", name)
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

	obj, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	if cv, ok := obj.(*componentValue); ok {
		return cv.getField(e.Field)
	}
	if m, ok := obj.(map[string]any); ok {
		return m[e.Field], nil
	}
	return nil, fmt.Errorf("cannot select field %q on %T", e.Field, obj)
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
		i := toInt(idx)
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
		for _, sample := range m {
			return zeroValueForValue(sample), nil
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
		return toInt(v), nil
	case ir.TypeFloat:
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
	m := make(map[string]any, len(e.Fields))
	for _, f := range e.Fields {
		if f.Spread {
			v, err := env.Eval(f.Value)
			if err != nil {
				return nil, err
			}
			if src, ok := v.(map[string]any); ok {
				maps.Copy(m, src)
			}
			continue
		}
		v, err := env.Eval(f.Value)
		if err != nil {
			return nil, err
		}
		m[f.Name] = v
	}
	return m, nil
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
		return numericResult(toFloat(left) + toFloat(right)), nil
	case ast.BinSub:
		return numericResult(toFloat(left) - toFloat(right)), nil
	case ast.BinMul:
		return numericResult(toFloat(left) * toFloat(right)), nil
	case ast.BinDiv:
		r := toFloat(right)
		if r == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return numericResult(toFloat(left) / r), nil
	case ast.BinMod:
		return numericResult(math.Mod(toFloat(left), toFloat(right))), nil
	}
	return nil, fmt.Errorf("unknown binary op %d", e.Op)
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
		return numericResult(-toFloat(v)), nil
	}
	return nil, fmt.Errorf("unknown unary op %d", e.Op)
}

func (env *Env) evalCall(call *ir.Call) (any, error) {
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
		v, err := env.Eval(call.Callee)
		if err != nil {
			return nil, err
		}
		if lv, ok := v.(*lambdaValue); ok {
			args, err := env.evalCallArgs(call.Args)
			if err != nil {
				return nil, err
			}
			return lv.call(args)
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
			return toInt(v), nil
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
	return env.evalUserFunc(call.Func, argExprs(call.Args))
}

func (env *Env) evalTypeMethodCall(call *ir.Call) (any, error) {
	method := call.Func.Name
	receiverName := call.Func.Receiver
	qualName := receiverName + "." + method

	// error.raise: construct an ErrorEvent payload and bubble a RaisedError
	// up the Go error chain. The originating CallStmt's ErrorMode then
	// routes it into the resolved handler (or propagates).
	if qualName == "error.raise" {
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

	// Alert/File namespaces: static-only, receiver-less logging.
	switch qualName {
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
		if tv, ok := evalArgs[0].(*testingT); ok {
			return tv.callMethod(env, method, argExprs(call.Args[1:]))
		}
		if cv, ok := evalArgs[0].(*componentValue); ok {
			if method[0] == '@' {
				return nil, nil
			}
			if fn, ok := cv.funcs[method]; ok {
				compEnv := cv.compEnv()
				result, err := compEnv.evalUserFunc(fn, argExprs(call.Args[1:]))
				for k := range cv.vars {
					if v, ok := compEnv.vars[k]; ok {
						cv.vars[k] = v
						if !cv.testParams[k] {
							cv.env.vars[k] = v
						}
					}
				}
				return result, err
			}
		}
	}

	if result, handled, err := nativeMethod(qualName, evalArgs); handled {
		return result, err
	}
	if result, handled, err := nativeMethod("*."+method, evalArgs); handled {
		return result, err
	}

	// i18n namespace: dispatch via locale-aware translator.
	if receiverName == "i18n" {
		if result, handled, err := env.evalI18nCall(method, call.Args); handled {
			return result, err
		}
	}

	// List mutation (push/remove) needs writeback; evaluate before user funcs
	// since stdlib push/remove delegate to untranslated intrinsics.
	if receiverName == "list" && (method == "push" || method == "remove") && len(call.Args) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}

	// User-defined type-method.
	if fn, ok := env.funcs[qualName]; ok {
		return env.evalUserFunc(fn, argExprs(call.Args))
	}

	// List/string higher-order and other built-in methods.
	if len(evalArgs) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}
	return nil, fmt.Errorf("unknown method %q", qualName)
}

func (env *Env) evalNamespaceCall(call *ir.Call) (any, error) {
	// Static-form receiver: Type.method(args) where Type is a type name. The
	// checker leaves Func nil when the method is a user-defined type method
	// (component-scoped) the symbol table doesn't see. Dispatch by qualified
	// name using env.funcs.
	if ident, ok := call.Receiver.(*ir.Ident); ok {
		if _, lookupErr := env.lookup(ident.Name); lookupErr != nil {
			method := methodNameFromCall(call)

			// i18n namespace: dispatch via locale-aware translator before
			// falling through to the empty-body user func.
			if ident.Name == "i18n" {
				if result, handled, err := env.evalI18nCall(method, call.Args); handled {
					return result, err
				}
			}

			qualName := ident.Name + "." + method
			evalArgs, err := env.evalCallArgs(call.Args)
			if err == nil {
				if result, handled, err := nativeMethod(qualName, evalArgs); handled {
					return result, err
				}
			}
			if fn, ok := env.funcs[qualName]; ok {
				return env.evalUserFunc(fn, argExprs(call.Args))
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
			// Element-ref event invocation: c.btn.@click() → look up the
			// handler stored at key "@click" on the rendered element map.
			if strings.HasPrefix(method, "@") {
				if m, ok := recv.(map[string]any); ok {
					if h, ok := m[method].(*ir.Func); ok {
						return env.runEventHandler(h, call.Args)
					}
				}
				return nil, nil
			}
			switch rv := recv.(type) {
			case *testingT:
				return rv.callMethod(env, method, argExprs(call.Args))
			case *componentValue:
				if len(method) > 0 && method[0] == '@' {
					return nil, nil // event emission no-op
				}
				if fn, ok := rv.funcs[method]; ok {
					compEnv := rv.compEnv()
					result, err := compEnv.evalUserFunc(fn, argExprs(call.Args))
					for k := range rv.vars {
						if v, ok := compEnv.vars[k]; ok {
							rv.vars[k] = v
							if !rv.testParams[k] {
								rv.env.vars[k] = v
							}
						}
					}
					return result, err
				}
			}
			// Instance method on a primitive value: dispatch by runtime type.
			qualName := runtimeTypeName(recv) + "." + method
			evalArgs := make([]any, 0, len(call.Args)+1)
			evalArgs = append(evalArgs, recv)
			for _, a := range call.Args {
				v, err := env.Eval(a.Value)
				if err != nil {
					return nil, err
				}
				evalArgs = append(evalArgs, v)
			}
			if result, handled, err := nativeMethod(qualName, evalArgs); handled {
				return result, err
			}
			if result, handled, err := nativeMethod("*."+method, evalArgs); handled {
				return result, err
			}
			// Mutation methods on lists need a writeback; dispatch before
			// user-defined stdlib bodies that delegate to intrinsics.
			if method == "push" || method == "remove" || method == "filter" || method == "map" {
				return env.evalBuiltinMethodFromRecv(call.Receiver, method, recv, evalArgs[1:])
			}
			if fn, ok := env.funcs[qualName]; ok {
				// Prepend receiver expr so evalUserFunc sees normalized form.
				synth := make([]ir.Expr, 0, len(call.Args)+1)
				synth = append(synth, call.Receiver)
				for _, a := range call.Args {
					synth = append(synth, a.Value)
				}
				return env.evalUserFunc(fn, synth)
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

// runEventHandler invokes an event handler function's body using the current env.
func (env *Env) runEventHandler(fn *ir.Func, args []ir.CallArg) (any, error) {
	for i, p := range fn.Params {
		if i < len(args) {
			v, err := env.Eval(args[i].Value)
			if err != nil {
				return nil, err
			}
			env.vars[p.Name] = v
		}
	}
	for _, s := range fn.Block {
		if ret, ok := s.(*ir.Return); ok {
			if ret.Value == nil {
				return nil, nil
			}
			return env.Eval(ret.Value)
		}
		if err := env.Exec(s); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// evalBuiltinMethodFromRecv dispatches list/string built-in methods when the
// receiver expression is known separately from the rest of the args.
func (env *Env) evalBuiltinMethodFromRecv(recvExpr ir.Expr, method string, recv any, rest []any) (any, error) {
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.call([]any{item})
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
			lv, ok := rest[0].(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.call([]any{item})
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
			idx := toInt(rest[0])
			if idx >= 0 && idx < len(list) {
				newList := append(list[:idx], list[idx+1:]...)
				return env.writeBackList(recvExpr, newList)
			}
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
// For @event access ("c.btn.@click"), the name is prefixed with "@".
func methodNameFromCall(call *ir.Call) string {
	if call.Func != nil {
		return call.Func.Name
	}
	if call.AST == nil {
		return ""
	}
	if sel, ok := call.AST.Func.(*ast.SelectExpr); ok {
		if sel.Kind == ast.SelectEvent {
			return "@" + sel.Field
		}
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
			lv, ok := rest[0].(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.call([]any{item})
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
			lv, ok := rest[0].(*lambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.call([]any{item})
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
			idx := toInt(rest[0])
			if idx >= 0 && idx < len(list) {
				newList := append(list[:idx], list[idx+1:]...)
				return env.writeBackList(call.Args[0].Value, newList)
			}
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
		env.vars[t.Name] = newList
	case *ir.Select:
		obj, err := env.Eval(t.Operand)
		if err != nil {
			return nil, err
		}
		if cv, ok := obj.(*componentValue); ok {
			cv.vars[t.Field] = newList
			if !cv.testParams[t.Field] {
				cv.env.vars[t.Field] = newList
			}
		}
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

func (env *Env) evalUserFunc(fn *ir.Func, argExprs []ir.Expr) (any, error) {
	args := make([]any, len(argExprs))
	for i, a := range argExprs {
		v, err := env.Eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}

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

		savedVars := make(map[string]any)
		paramNames := make([]string, len(fn.Params))
		for i, p := range fn.Params {
			paramNames[i] = p.Name
			if v, ok := execEnv.vars[p.Name]; ok {
				savedVars[p.Name] = v
			}
			if i < len(args) {
				execEnv.vars[p.Name] = args[i]
			}
		}

		restoreVoid := func() {
			if !isPure {
				for _, name := range paramNames {
					if orig, ok := savedVars[name]; ok {
						execEnv.vars[name] = orig
					} else {
						delete(execEnv.vars, name)
					}
				}
			}
		}

		// Walk block looking for trailing Return. Execute preceding stmts.
		var tailExpr ir.Expr
		var localVars []string
		for i, stmt := range fn.Block {
			if ret, ok := stmt.(*ir.Return); ok {
				if i == len(fn.Block)-1 {
					tailExpr = ret.Value
					break
				}
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
					execEnv.vars[lv.Name] = v
					localVars = append(localVars, lv.Name)
				}
				continue
			}
			if err := execEnv.Exec(stmt); err != nil {
				restoreVoid()
				return nil, err
			}
		}

		if tailExpr == nil {
			restoreVoid()
			for _, name := range localVars {
				delete(execEnv.vars, name)
			}
			return nil, nil
		}

		if isPure {
			result, newArgs, isTail, err := execEnv.evalTailAware(tailExpr, fn)
			for _, name := range localVars {
				delete(execEnv.vars, name)
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
		for _, name := range localVars {
			delete(execEnv.vars, name)
		}
		return result, err
	}
}

// execBlockForResult runs a block, returning the value of the trailing Return (if any).
func (env *Env) execBlockForResult(block []ir.Stmt) (any, error) {
	for i, stmt := range block {
		if ret, ok := stmt.(*ir.Return); ok {
			if ret.Value == nil {
				return nil, nil
			}
			return env.Eval(ret.Value)
		}
		if err := env.Exec(stmt); err != nil {
			return nil, err
		}
		_ = i
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
	raw := lit.Raw
	if before, ok := strings.CutSuffix(raw, suffix); ok {
		raw = before
	}
	raw = strings.ReplaceAll(raw, "_", "")
	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return unitValue{}, fmt.Errorf("invalid unit literal %q: %w", lit.Raw, err)
	}
	table := env.units[suffix]
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
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v any) float64 {
	switch val := v.(type) {
	case int:
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

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
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
	switch v.(type) {
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
	case map[string]any:
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

func numericResult(f float64) any {
	if f == math.Trunc(f) && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return int(f)
	}
	return f
}

// literalString pulls the cooked string value from an ast literal.
func literalString(e *ast.LiteralExpr) (string, bool) {
	if e == nil {
		return "", false
	}
	raw := e.Raw
	return unquoteString(raw), true
}

func unquoteString(raw string) string {
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') ||
			(raw[0] == '`' && raw[len(raw)-1] == '`') {
			return raw[1 : len(raw)-1]
		}
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) && len(raw) >= 6 {
			return raw[3 : len(raw)-3]
		}
	}
	return raw
}
