package optimize

import (
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/opeval"
	"git.duckfam.us/jonathan/sngl/ir"
)

// isConstExpr reports whether the expression can be evaluated at compile time.
func isConstExpr(e ir.Expr, ctx *evalCtx) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		if x.Member != "" {
			return true // an enum member is written, not computed
		}
		if v, ok := x.Sym.(*ir.Var); ok && v.IsConst {
			return true
		}
		if _, _, ok := ir.TargetNode(x.Sym); ok {
			return true // a target's identity is its name
		}
		if _, ok := x.Sym.(*ir.Namespace); ok {
			return true // namespace refs are compile-time resolvable
		}
		// Any symbol bound by a parent context (loop vars during expansion,
		// component params during call-site inlining) becomes a const for
		// the duration of that scope.
		if x.Sym != nil {
			if _, found := ctx.values[x.Sym]; found {
				return true
			}
		}
		return false
	case *ir.Binary:
		return isConstExpr(x.Left, ctx) && isConstExpr(x.Right, ctx)
	case *ir.Unary:
		// Reference operations (&x, *p) are never const: the underlying
		// storage is mutable and may be aliased, so the value at any
		// given moment is not knowable at compile time.
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return false
		}
		return isConstExpr(x.Operand, ctx)
	case *ir.Ternary:
		return isConstExpr(x.Cond, ctx) && isConstExpr(x.Then, ctx) && isConstExpr(x.Else, ctx)
	case *ir.Call:
		if x.Func != nil && x.Func.Intrinsic != "" {
			if _, ok := ctx.answers[x.Func.Intrinsic]; ok {
				return true // answered for this fold: the page a document is for
			}
		}
		for _, a := range x.Args {
			if !isConstExpr(a.Value, ctx) {
				return false
			}
		}
		if x.Receiver != nil && !isConstExpr(x.Receiver, ctx) {
			return false
		}
		if x.Func != nil && x.Func.Purity == ir.PurityPure {
			return true
		}
		// Check native import pure functions.
		return isNativePureCall(x, ctx)
	case *ir.Conversion:
		return isConstExpr(x.Operand, ctx)
	case *ir.ListLit:
		for _, el := range x.Elems {
			if !isConstExpr(el, ctx) {
				return false
			}
		}
		return true
	case *ir.Spread:
		return isConstExpr(x.Operand, ctx)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Value == nil || !isConstExpr(f.Value, ctx) {
				return false
			}
		}
		return true
	case *ir.Select:
		if v, ok := nsConst(x); ok {
			return v.Init != nil || v.Builtin.IsConst()
		}
		if _, ok := nsTarget(x); ok {
			return true
		}
		return isConstExpr(x.Operand, ctx)
	case *ir.Index:
		return isConstExpr(x.Operand, ctx) && isConstExpr(x.Idx, ctx)
	default:
		return false
	}
}

// nativeCallTarget resolves a call written as `alias.name(...)` against the
// package's native imports. It reads the AST because the checker leaves such a
// call unresolved: there is no ir.Func to point at.
func nativeCallTarget(call *ir.Call, ctx *evalCtx) (name string, ns *ir.NativeImport, ok bool) {
	if call == nil || call.AST == nil {
		return "", nil, false
	}
	sel, ok := call.AST.Func.(*ast.SelectExpr)
	if !ok {
		return "", nil, false
	}
	ident, ok := sel.Operand.(*ast.IdentExpr)
	if !ok {
		return "", nil, false
	}
	ns, ok = ctx.getNativeImports()[ident.Name]
	if !ok {
		return "", nil, false
	}
	return sel.Field, ns, true
}

// isNativePureCall checks if an unresolved call is to a pure native function.
func isNativePureCall(call *ir.Call, ctx *evalCtx) bool {
	if call.Func != nil {
		return false // already resolved
	}
	name, ns, ok := nativeCallTarget(call, ctx)
	if !ok {
		return false
	}
	for _, f := range ns.Funcs {
		if f.Name == name && f.Purity == ir.PurityPure {
			return true
		}
	}
	return false
}

// evalExpr evaluates a constant expression and returns the Go value.
func evalExpr(e ir.Expr, ctx *evalCtx) (any, bool) {
	if e == nil {
		return nil, false
	}
	if !isConstExpr(e, ctx) {
		return nil, false
	}
	switch x := e.(type) {
	case *ir.Literal:
		val := parseLiteral(x)
		// parseLiteral returns nil for kinds it can't represent (units, etc.);
		// signal non-foldable so the original literal is preserved.
		if val == nil && x.Type != nil && x.Type.Kind != ir.TypeNull {
			return nil, false
		}
		return val, true
	case *ir.Ident:
		return evalIdent(x, ctx)
	case *ir.Binary:
		left, lok := evalExpr(x.Left, ctx)
		right, rok := evalExpr(x.Right, ctx)
		if !lok || !rok {
			return nil, false
		}
		return evalBinaryOp(x.Op, left, right, numKindOf(x.Type))
	case *ir.Unary:
		operand, ok := evalExpr(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		return evalUnaryOp(x.Op, operand, numKindOf(x.Type))
	case *ir.Ternary:
		cond, ok := evalExpr(x.Cond, ctx)
		if !ok {
			return nil, false
		}
		if b, ok := cond.(bool); ok {
			if b {
				return evalExpr(x.Then, ctx)
			}
			return evalExpr(x.Else, ctx)
		}
		return nil, false
	case *ir.Call:
		return evalCall(x, ctx)
	case *ir.Conversion:
		return evalConversion(x, ctx)
	case *ir.ListLit:
		result := make([]any, 0, len(x.Elems))
		for _, el := range x.Elems {
			if sp, isSpread := el.(*ir.Spread); isSpread {
				v, ok := evalExpr(sp.Operand, ctx)
				items, isList := v.([]any)
				if !ok || !isList {
					return nil, false
				}
				result = append(result, items...)
				continue
			}
			v, ok := evalExpr(el, ctx)
			if !ok {
				return nil, false
			}
			result = append(result, v)
		}
		return result, true
	case *ir.StructLit:
		result := interp.NewStruct(x.Def, x.Type)
		for _, f := range x.Fields {
			if f.Spread || f.Name == "" || f.Value == nil {
				return nil, false
			}
			v, ok := evalExpr(f.Value, ctx)
			if !ok {
				return nil, false
			}
			result.Set(f.Name, v)
		}
		return result, true
	case *ir.MapLitIR:
		result := make(map[string]any, len(x.Entries))
		for _, e := range x.Entries {
			k, ok := evalExpr(e.Key, ctx)
			if !ok {
				return nil, false
			}
			v, ok := evalExpr(e.Value, ctx)
			if !ok {
				return nil, false
			}
			result[fmt.Sprintf("%v", k)] = v
		}
		return result, true
	case *ir.Select:
		// A package member is a name, not a field of a value: the operand is a
		// namespace and evaluating it yields nothing. A const behind one folds
		// like any other — which is what makes `PLATFORM == html.platform`
		// fold, since a target identity is reached only through its package.
		if v, ok := nsConst(x); ok {
			return evalIdent(&ir.Ident{Name: x.Field, Sym: v}, ctx)
		}
		if name, ok := nsTarget(x); ok {
			return name, true
		}
		recv, ok := evalExpr(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		if x.Field == "length" {
			if n, ok := constLength(x.Operand.ExprType(), recv); ok {
				return n, true
			}
		}
		if s, ok := recv.(*interp.Struct); ok {
			if v, exists := s.Get(x.Field); exists {
				return v, true
			}
		}
		if m, ok := recv.(map[string]any); ok {
			v, exists := m[x.Field]
			if exists {
				return v, true
			}
		}
		return nil, false
	case *ir.Index:
		operand, ok := evalExpr(x.Operand, ctx)
		if !ok {
			return nil, false
		}
		idx, ok := evalExpr(x.Idx, ctx)
		if !ok {
			return nil, false
		}
		if list, ok := operand.([]any); ok {
			if i, ok := toInt(idx); ok && i >= 0 && i < len(list) {
				return list[i], true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

func evalIdent(x *ir.Ident, ctx *evalCtx) (any, bool) {
	// An enum member is its name, which is what the interpreter holds for one
	// too. Which enum that name belongs to is the position's type, and
	// irFromValue reads the member back off it.
	if x.Member != "" {
		return x.Member, true
	}
	// Const variable — evaluate its initializer, except where the compiler
	// supplies the value. The build target is keyed off the #[builtin] mark
	// rather than the name, so a declaration shadowing PLATFORM is an
	// ordinary const and folds to whatever it was declared as.
	// A build-target node read as a value is the target's identity, and that
	// is its registered name -- the value PLATFORM and LANGUAGE fold to.
	if _, name, ok := ir.TargetNode(x.Sym); ok {
		return name, true
	}
	if v, ok := x.Sym.(*ir.Var); ok && v.IsConst {
		switch v.Builtin {
		case ir.BuiltinTargetPlatform:
			return ctx.platform, true
		case ir.BuiltinTargetLanguage:
			return ctx.language, true
		}
		if val, found := ctx.values[v]; found {
			return val, true
		}
		if v.Init != nil {
			// A const whose initializer reads itself, directly or around a
			// cycle. Unguarded this is evalIdent <-> evalExpr with nothing to
			// stop it: `const a int = a` took the compiler down with a stack
			// overflow, no position and no message.
			//
			// Answered "not constant", which leaves the initializer standing
			// -- what a const the optimizer cannot evaluate already does. It
			// is not the right answer, only a survivable one: the rule that a
			// declaration may not read itself belongs in the checker, where it
			// reaches a program this pass never folds. See CLAUDE.md.
			//
			// Survivable rests on something in another package. The standing
			// expression reaches JavaScript as `var a = a`, which is benign
			// because `var` hoists; spelled `let` or `const` it is a TDZ
			// ReferenceError and the page breaks on load rather than rendering
			// one value short. cmd/sngl/testdata/const_reads_itself.txt denies
			// both spellings so that modernising the emitter is caught here.
			if ctx.evaluatingConst[v] {
				return nil, false
			}
			if ctx.evaluatingConst == nil {
				ctx.evaluatingConst = map[*ir.Var]bool{}
			}
			ctx.evaluatingConst[v] = true
			val, ok := evalExpr(v.Init, ctx)
			delete(ctx.evaluatingConst, v)
			if ok {
				ctx.values[v] = val
			}
			return val, ok
		}
	}
	// Loop variable during expansion.
	if val, found := ctx.values[x.Sym]; found {
		return val, true
	}
	return nil, false
}

func evalCall(call *ir.Call, ctx *evalCtx) (any, bool) {
	// What this fold answers the intrinsic with: in a document written for
	// one page of a stack, what the stack shows is that page, whatever handle
	// names the stack.
	if call.Func != nil && call.Func.Intrinsic != "" {
		if v, ok := ctx.answers[call.Func.Intrinsic]; ok {
			return v, true
		}
	}
	// Collect argument values. An argument is consumed as a value and never
	// rebuilt into an expression, so a T promoted to option<T> is its
	// operand's: the wrap evalConversion declines changes nothing here.
	args := make([]any, 0, len(call.Args))
	for _, a := range call.Args {
		e := a.Value
		if c, ok := e.(*ir.Conversion); ok && ir.IsOptionWrap(c) {
			e = c.Operand
		}
		v, ok := evalExpr(e, ctx)
		if !ok {
			return nil, false
		}
		args = append(args, v)
	}

	// Dispatch by intrinsic id before anything name-based: sngl:seq declares
	// `range` as a package function, and the by-name paths below would fold a
	// program's own function of that name too.
	if call.Func != nil && call.Func.Intrinsic != "" {
		if v, ok := evalIntrinsic(call.Func.Intrinsic, args, ctx.unroll); ok {
			return v, true
		}
	}

	// Try the generic SNGL-body interpreter for pure user/stdlib funcs. A
	// declaration marked #[intrinsic] is skipped here and retried below: the
	// folder has its own implementation of the id, and most of those bodies
	// are placeholders a backend is expected to replace.
	if canFoldBody(call.Func) && call.Func.Intrinsic == "" {
		ctx.interpDepth++
		v, ok := interpretFunc(call.Func, args, ctx, ctx.interpDepth)
		ctx.interpDepth--
		if ok {
			return v, true
		}
	}

	// Resolved function call. Type-attached methods (including both
	// "x.upper()" and "string.upper(x)" syntaxes) are normalized so that
	// Func.Receiver is set and the receiver value is Args[0].
	if call.Func != nil {
		if call.Func.Receiver != "" {
			qualName := call.Func.Receiver + "." + call.Func.Name
			if v, ok := evalQualifiedMethod(qualName, args); ok {
				return v, true
			}
		}
	}

	// An intrinsic the folder does not implement. A body it still has is one
	// the declaration said computes the right answer; the placeholders were
	// dropped after type checking.
	if canFoldBody(call.Func) && call.Func.Intrinsic != "" {
		ctx.interpDepth++
		v, ok := interpretFunc(call.Func, args, ctx, ctx.interpDepth)
		ctx.interpDepth--
		if ok {
			return v, true
		}
	}

	// Native import pure function.
	return evalNativeCall(call, args, ctx)
}

// maxFoldedSequence bounds what a sequence folds to on a target that does not
// unroll loops. A folded sequence is the numbers themselves -- a list literal
// in the IR -- and a target that emits the loop has no use for a literal of
// 200000 elements in a variable it is about to walk. Past this many the call
// stands, and the sequence is computed where it is read.
//
// A static target has no such choice and is not bounded here; see
// maxStaticUnroll for what bounds it.
const maxFoldedSequence = 1024

// evalIntrinsic folds a call by its #[intrinsic] id. An integer sequence has
// to fold here rather than through a SNGL body, because there is no way to
// write one: building a range needs a loop, and a loop needs a range.
// opeval.Sequence is the same implementation the interpreter runs.
func evalIntrinsic(id string, args []any, unbounded bool) (any, bool) {
	ints := func(want int) ([]int, bool) {
		if len(args) != want {
			return nil, false
		}
		out := make([]int, want)
		for i, a := range args {
			v, ok := toInt(a)
			if !ok {
				return nil, false
			}
			out[i] = v
		}
		return out, true
	}
	seq := func(start, end, step int) (any, bool) {
		if !unbounded && opeval.SequenceLen(start, end, step) > maxFoldedSequence {
			return nil, false
		}
		return opeval.Sequence(start, end, step), true
	}
	switch id {
	case ir.NavHrefID:
		return foldNavHref(args)
	case "seq.count":
		if a, ok := ints(1); ok {
			return seq(0, a[0], 1)
		}
	case "seq.range":
		if a, ok := ints(2); ok {
			return seq(a[0], a[1], 1)
		}
	case "seq.step":
		if a, ok := ints(3); ok {
			return seq(a[0], a[1], a[2])
		}
	}
	return nil, false
}

// isUnfoldableNative reports whether f is a declaration the host implements,
// leaving this folder nothing to run.
//
// Purity says a call has no effects. It does not say the folder can produce
// the call's value, and for a native it cannot: the declaration *is* the host
// identifier, and its SNGL signature describes it rather than implementing it.
// Treating one as constant and then failing to evaluate it yields the return
// type's zero -- which is how every constant-coloured Compose shape became
// `drawRect(color = null, ...)`, `null` being no `Color` at all and no Kotlin
// that compiles.
//
// A `#[foreign]` `const func` is the deliberate opposite and keeps
// folding: it is marked rather than native, and its body is what runs.
func isUnfoldableNative(f *ir.Func) bool {
	return f.Foreign.Name != "" && !f.Foreign.Marked && len(f.Block) == 0
}

// canFoldBody reports whether f has a SNGL body the folder may run.
func canFoldBody(f *ir.Func) bool {
	return f != nil && len(f.Block) > 0 && f.Purity == ir.PurityPure && hasWrittenBody(f)
}

// hasWrittenBody reports whether f's Block is a body someone wrote rather than
// the `return <zero>` ir.Normalize gives a signature. A bodyless declaration is
// a host function, an intrinsic or an import, and a `const func` one is pure
// by the declaration's word -- so without this the folder ran the placeholder
// and answered zero for a call it had no way to make. A func with no AST was
// built by a pass, and an override's body is swapped in over its base's
// signature; both are real.
func hasWrittenBody(f *ir.Func) bool {
	if f == nil || f.AST == nil || f.SpecializedFor != "" {
		return true
	}
	return f.AST.Body != nil || f.AST.Block.IsDefined()
}

func evalNativeCall(call *ir.Call, args []any, ctx *evalCtx) (any, bool) {
	name, ns, ok := nativeCallTarget(call, ctx)
	if !ok {
		return nil, false
	}

	// Try file: scheme functions.
	for _, f := range ns.Funcs {
		if f.Name == name && f.Foreign.Path == "file" {
			if len(args) == 1 {
				if filename, ok := args[0].(string); ok {
					return evalFileFunc(f.Foreign.Name, ns.ImportPath, filename, ctx)
				}
			}
		}
	}

	// The value model's view of a compile-time result. There is one result —
	// the checked IR — and evalExpr projects it back to a value for the
	// arithmetic, selection and argument rendering that operate on values.
	e, ok := evalPureGoCall(call, name, ns, args, ctx)
	if !ok {
		return nil, false
	}
	return evalExpr(e, ctx)
}

// evalPureGoCall requests the compile-time value of one pure non-file scheme
// function and returns its checked IR.
func evalPureGoCall(call *ir.Call, name string, ns *ir.NativeImport, args []any, ctx *evalCtx) (ir.Expr, bool) {
	if ctx.dir == "" {
		return nil, false
	}
	alias := call.AST.Func.(*ast.SelectExpr).Operand.(*ast.IdentExpr).Name
	qualName := alias + "." + name
	for _, f := range ns.Funcs {
		if f.Name != name || f.Purity != ir.PurityPure || f.Foreign.Path == "file" {
			continue
		}
		scheme := ctx.nativeSchemes[alias]
		result, state, err := requestPureNativeFunc(ctx, scheme, ns.ImportPath, f, args)
		switch state {
		case nativePending:
			// Recorded for the next round; this pass leaves the call
			// unfolded and the round loop starts over with the value
			// in hand.
			return nil, false
		case nativeFailed:
			// Refused rather than failed: tallied per package and reported
			// once, as a warning or as the error that stops the target.
			if ctx.noteRefused(call, scheme, err) {
				return nil, false
			}
			// A failed compile-time evaluation can only be tolerated when
			// the target can recompute the value at runtime instead. That
			// requires the target language to call this scheme natively
			// (go: from a go target, js: from a js target, …). When it
			// can't — html static/none, kotlin, js+go:, etc. — the const
			// is unrecoverable, and silently dropping it renders pages with
			// empty/broken content. Abort the build instead.
			if !schemeRunnableAtRuntime(scheme, ctx.language) && ctx.err == nil {
				ctx.err = fmt.Errorf("%s:// import %q failed to evaluate at build time and the %q target cannot call it at runtime: %w", scheme, qualName, ctx.language, err)
			} else {
				slog.Debug("pure func eval failed", "func", qualName, "err", err)
			}
			return nil, false
		}
		slog.Debug("pure func eval", "func", qualName, "result_type", fmt.Sprintf("%T", result))
		return result, true
	}
	return nil, false
}

func evalConversion(conv *ir.Conversion, ctx *evalCtx) (any, bool) {
	// A T promoted to option<T> is not a value change, so folding it produces
	// the operand's value and the rebuilt expression is typed T again -- the
	// wrap vanishes, and Go's *T field is assigned a T. There is nothing to
	// gain by folding it either: the operand folds on its own.
	if ir.IsOptionWrap(conv) {
		return nil, false
	}
	if conv.Type.Kind == ir.TypeString {
		if s, ok := multiBaseUnitString(conv.Operand); ok {
			return s, true
		}
	}
	operand, ok := evalExpr(conv.Operand, ctx)
	if !ok {
		return nil, false
	}
	switch conv.Type.Kind {
	case ir.TypeString:
		// A folded unit arrives as the bare float64 its magnitude is, so
		// without this the constant `"{500ms}"` was "500" on every target
		// while the same read off a var was the target's own spelling.
		if s, ok := unitConversionString(conv, operand); ok {
			return s, true
		}
		return fmt.Sprintf("%v", operand), true
	case ir.TypeInt:
		// Numeric operands go through the shared width-aware converter so a
		// folded int8(x)/uint64(x) matches the interpreter exactly. String
		// operands parse first, then apply the target width.
		if r, ok := opeval.ConvertInt(operand, conv.Type.Bits, conv.Type.Unsigned); ok {
			return r, true
		}
		if s, ok := operand.(string); ok {
			i, err := strconv.Atoi(s)
			if err != nil {
				return nil, false
			}
			r, _ := opeval.ConvertInt(i, conv.Type.Bits, conv.Type.Unsigned)
			return r, true
		}
	case ir.TypeFloat:
		if r, ok := opeval.ConvertFloat(operand, conv.Type.Bits); ok {
			return r, true
		}
		if s, ok := operand.(string); ok {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, false
			}
			r, _ := opeval.ConvertFloat(f, conv.Type.Bits)
			return r, true
		}
	case ir.TypeBool:
		if b, ok := operand.(bool); ok {
			return b, true
		}
	case ir.TypeList, ir.TypeStruct, ir.TypeDyn, ir.TypeOption:
		// Compound-type conversions are widening or narrowing within a
		// compatible Go shape ([]any / map[string]any). Pass through.
		return operand, true
	}
	return nil, false
}

// parseLiteral converts an IR literal to a Go value.
func parseLiteral(lit *ir.Literal) any {
	if lit.Type == nil {
		return nil
	}
	switch lit.Type.Kind {
	case ir.TypeBool:
		return lit.Value == "true"
	case ir.TypeNull:
		return nil
	case ir.TypeInt:
		n, err := strconv.Atoi(lit.Value)
		if err != nil {
			return nil
		}
		return n
	case ir.TypeFloat:
		f, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return 0.0
		}
		return f
	case ir.TypeString:
		return lit.Value
	case ir.TypeUnit:
		// A unit value is its magnitude in the unit's base, so it evaluates
		// like the number it is. Without this a unit literal was simply
		// unevaluable, and anything holding one went with it -- most visibly a
		// constant list, so `for var p = [20ms, 400ms]` did not unroll on a
		// target that writes every iteration into its output, and rendered one
		// element with its variable bound to nothing.
		// Only a single-base unit: a magnitude is spellable again in the one
		// base it reduces to, and a multi-base one -- `measurement`, whose
		// value is a record of px/em/vw/vh/pct -- is not. Reducing one of
		// those anyway dropped the unit on the way back and every rem and vw
		// in a style came out as a bare number.
		ud := ir.UnitDeclOf(lit.Type)
		if ud == nil || !ud.IsSingleBase() {
			return nil
		}
		mag, _, ok := ir.UnitMagnitude(lit)
		if !ok {
			return nil
		}
		return mag
	case ir.TypeStruct:
		// A target identity is opaque to the program but is a name to the
		// compiler, so `PLATFORM == html.platform` folds the way the string
		// comparison it replaced did.
		if ir.TargetIDStruct(lit.Type) {
			return lit.Value
		}
	}
	return nil
}

// irLiteral converts a Go value back to an IR Literal.
// For strings, Raw stores the unquoted content (the formatter adds %q quoting).
func irLiteral(val any, typ *ir.Type) *ir.Literal {
	// A unit target comes first: the magnitude arrives as a float64 like any
	// other number, and matching on the value alone would spell it back as a
	// bare float and lose the unit.
	if lit := unitLiteral(val, typ); lit != nil {
		return lit
	}
	switch v := val.(type) {
	case string:
		return &ir.Literal{Type: ir.TypString, Value: v}
	case int:
		return &ir.Literal{Type: intLitType(typ), Value: intToStr(v)}
	case uint64:
		return &ir.Literal{Type: intLitType(typ), Value: strconv.FormatUint(v, 10)}
	case float64:
		t := floatLitType(typ)
		// Spelled at the width it is stored at: a folded float32 has already
		// been rounded to one, and printing it back at float64 precision gives
		// `0.20000000298023224f` where the value is 0.2.
		if t.Bits == 32 {
			return &ir.Literal{Type: t, Value: strconv.FormatFloat(v, 'g', -1, 32)}
		}
		return &ir.Literal{Type: t, Value: floatToStr(v)}
	case bool:
		raw := "false"
		if v {
			raw = "true"
		}
		return &ir.Literal{Type: ir.TypBool, Value: raw}
	case nil:
		return &ir.Literal{Type: ir.TypNull, Value: "null"}
	}
	return nil
}

// unitLiteral spells a magnitude back as a literal of the unit type it came
// from, in that unit's base: 1s evaluates to 1000 and returns as `1000ms`.
// Normalising to the base rather than the written suffix is what a unit value
// already is -- its magnitude per base, not its own spelling.
func unitLiteral(val any, typ *ir.Type) *ir.Literal {
	ud := ir.UnitDeclOf(typ)
	if ud == nil || !ud.IsSingleBase() {
		return nil
	}
	var mag float64
	switch v := val.(type) {
	case float64:
		mag = v
	case int:
		mag = float64(v)
	default:
		return nil
	}
	bases := ud.Bases()
	if len(bases) != 1 {
		return nil
	}
	return &ir.Literal{Type: typ, Value: ir.FormatUnitMagnitude(mag), Suffix: bases[0].Name}
}

// intLitType returns typ when it is an integer type (preserving a sized
// width/signedness), else plain int. Keeps folded constants at the width the
// checker assigned so codegen still emits e.g. a BigInt literal for uint64.
func intLitType(typ *ir.Type) *ir.Type {
	if typ != nil && typ.Kind == ir.TypeInt {
		return typ
	}
	return ir.TypInt
}

// floatLitType mirrors intLitType for float widths.
func floatLitType(typ *ir.Type) *ir.Type {
	if typ != nil && typ.Kind == ir.TypeFloat {
		return typ
	}
	return ir.TypFloat
}

// numericOrNativeEq compares two folded constants. When both sides are
// numeric, comparison happens after promotion to float so that
// `1 == 1.0` folds to true (matching the language's mixed-numeric == rule).
// For non-numeric pairs, it falls back to Go's native ==.
func numericOrNativeEq(left, right any) bool {
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if lok && rok {
		return lf == rf
	}
	return left == right
}

// --- Arithmetic and comparison helpers (operate on Go values) ---

// numKindOf maps a result type to the opeval width descriptor. A nil or
// non-numeric type yields the zero NumKind (default int semantics).
func numKindOf(t *ir.Type) opeval.NumKind {
	if t == nil || !t.IsNumeric() {
		return opeval.NumKind{}
	}
	return opeval.NumKind{Bits: t.Bits, Unsigned: t.Unsigned, Float: t.Kind == ir.TypeFloat}
}

func evalBinaryOp(op ast.BinaryOp, left, right any, kind opeval.NumKind) (any, bool) {
	switch op {
	case ast.BinEq:
		return numericOrNativeEq(left, right), true
	case ast.BinNeq:
		return !numericOrNativeEq(left, right), true
	case ast.BinAnd:
		lb, lok := left.(bool)
		rb, rok := right.(bool)
		if lok && rok {
			return lb && rb, true
		}
	case ast.BinOr:
		lb, lok := left.(bool)
		rb, rok := right.(bool)
		if lok && rok {
			return lb || rb, true
		}
	case ast.BinAdd:
		if ls, ok := left.(string); ok {
			if rs, ok := right.(string); ok {
				return ls + rs, true
			}
		}
		return numericOp(op, left, right, kind)
	case ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
		return numericOp(op, left, right, kind)
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		return compareOp(op, left, right)
	}
	return nil, false
}

func numericOp(op ast.BinaryOp, left, right any, kind opeval.NumKind) (any, bool) {
	// Arithmetic semantics live in internal/opeval, shared with the
	// interpreter so folded and interpreted results can't diverge (#8/#10).
	// A non-nil error (div/mod by zero, non-numeric) means "not foldable".
	v, err := opeval.Arith(op, left, right, kind)
	if err != nil {
		return nil, false
	}
	return v, true
}
func compareOp(op ast.BinaryOp, left, right any) (any, bool) {
	li, lok := toInt(left)
	ri, rok := toInt(right)
	if lok && rok {
		switch op {
		case ast.BinLt:
			return li < ri, true
		case ast.BinLte:
			return li <= ri, true
		case ast.BinGt:
			return li > ri, true
		case ast.BinGte:
			return li >= ri, true
		}
	}
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if lok && rok {
		switch op {
		case ast.BinLt:
			return lf < rf, true
		case ast.BinLte:
			return lf <= rf, true
		case ast.BinGt:
			return lf > rf, true
		case ast.BinGte:
			return lf >= rf, true
		}
	}
	if ls, ok := left.(string); ok {
		if rs, ok := right.(string); ok {
			switch op {
			case ast.BinLt:
				return ls < rs, true
			case ast.BinLte:
				return ls <= rs, true
			case ast.BinGt:
				return ls > rs, true
			case ast.BinGte:
				return ls >= rs, true
			}
		}
	}
	return nil, false
}

func evalUnaryOp(op ast.UnaryOp, operand any, kind opeval.NumKind) (any, bool) {
	switch op {
	case ast.UnaryNot:
		if b, ok := operand.(bool); ok {
			return !b, true
		}
	case ast.UnaryNeg:
		// Negation is 0 - operand at the result width, shared with the
		// interpreter so sized-integer wrap folds identically.
		v, err := opeval.Arith(ast.BinSub, 0, operand, kind)
		if err != nil {
			return nil, false
		}
		return v, true
	}
	return nil, false
}

func evalQualifiedMethod(qualName string, args []any) (any, bool) {
	switch qualName {
	case "int.min":
		a, aok := toInt(args[0])
		b, bok := toInt(args[1])
		if aok && bok {
			if a < b {
				return a, true
			}
			return b, true
		}
	case "int.max":
		a, aok := toInt(args[0])
		b, bok := toInt(args[1])
		if aok && bok {
			if a > b {
				return a, true
			}
			return b, true
		}
	case "int.abs":
		x, ok := toInt(args[0])
		if ok {
			if x < 0 {
				return -x, true
			}
			return x, true
		}
	case "float.min":
		a, aok := toFloat(args[0])
		b, bok := toFloat(args[1])
		if aok && bok {
			return math.Min(a, b), true
		}
	case "float.max":
		a, aok := toFloat(args[0])
		b, bok := toFloat(args[1])
		if aok && bok {
			return math.Max(a, b), true
		}
	case "float.abs":
		x, ok := toFloat(args[0])
		if ok {
			return math.Abs(x), true
		}
	case "float.floor":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Floor(x)), true
		}
	case "float.ceil":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Ceil(x)), true
		}
	case "float.round":
		x, ok := toFloat(args[0])
		if ok {
			return int(math.Round(x)), true
		}
	case "float.sqrt":
		x, ok := toFloat(args[0])
		if ok {
			return math.Sqrt(x), true
		}
	case "float.sin":
		x, ok := toFloat(args[0])
		if ok {
			return math.Sin(x), true
		}
	case "float.cos":
		x, ok := toFloat(args[0])
		if ok {
			return math.Cos(x), true
		}
	case "float.tan":
		x, ok := toFloat(args[0])
		if ok {
			return math.Tan(x), true
		}
	case "float.asin":
		x, ok := toFloat(args[0])
		if ok {
			return math.Asin(x), true
		}
	case "float.acos":
		x, ok := toFloat(args[0])
		if ok {
			return math.Acos(x), true
		}
	case "float.atan":
		x, ok := toFloat(args[0])
		if ok {
			return math.Atan(x), true
		}
	case "float.atan2":
		y, yok := toFloat(args[0])
		x, xok := toFloat(args[1])
		if yok && xok {
			return math.Atan2(y, x), true
		}
	case "float.pow":
		x, xok := toFloat(args[0])
		y, yok := toFloat(args[1])
		if xok && yok {
			return math.Pow(x, y), true
		}
	case "string.length":
		if s, ok := args[0].(string); ok {
			// Rune count, not byte count — SNGL strings are runes (bugs.md #16).
			return utf8.RuneCountInString(s), true
		}
	case "list.length":
		if l, ok := args[0].([]any); ok {
			return len(l), true
		}
	case "string.upper":
		if s, ok := args[0].(string); ok {
			return strings.ToUpper(s), true
		}
	case "string.lower":
		if s, ok := args[0].(string); ok {
			return strings.ToLower(s), true
		}
	case "string.trim":
		if s, ok := args[0].(string); ok {
			return strings.TrimSpace(s), true
		}
	case "string.contains":
		s, sok := args[0].(string)
		sub, subok := args[1].(string)
		if sok && subok {
			return strings.Contains(s, sub), true
		}
	case "string.replace":
		s, sok := args[0].(string)
		old, ook := args[1].(string)
		new_, nok := args[2].(string)
		if sok && ook && nok {
			return strings.ReplaceAll(s, old, new_), true
		}
	case "string.indexOf":
		s, sok := args[0].(string)
		sub, subok := args[1].(string)
		if sok && subok {
			return strings.Index(s, sub), true
		}
	case "string.substring":
		s, sok := args[0].(string)
		start, stok := toInt(args[1])
		end, eok := toInt(args[2])
		if sok && stok && eok {
			// Rune indices, not byte indices (bugs.md #16).
			runes := []rune(s)
			if start >= 0 && end <= len(runes) && start <= end {
				return string(runes[start:end]), true
			}
		}
	case "string.startsWith":
		s, sok := args[0].(string)
		pre, pok := args[1].(string)
		if sok && pok {
			return strings.HasPrefix(s, pre), true
		}
	case "string.endsWith":
		s, sok := args[0].(string)
		suf, sufok := args[1].(string)
		if sok && sufok {
			return strings.HasSuffix(s, suf), true
		}
	}
	return nil, false
}

// evalFileFunc evaluates file: scheme functions (path, contents, names).
func evalFileFunc(funcName, dirPath, filename string, ctx *evalCtx) (any, bool) {
	if funcName == "names" {
		return fileNames(dirPath, filename)
	}
	f := ctx.evalCache().file(dirPath, filename)
	if f.err != nil {
		// A symlink out of the asset directory is not a file that is
		// missing: left unfolded, the call would render nothing in silence.
		if strings.Contains(f.err.Error(), "path escapes") {
			ctx.fail(fmt.Errorf("file: %s in %s is a link to something outside it", filename, dirPath))
		}
		return nil, false
	}

	switch funcName {
	case "path":
		outRel := filename
		if !ctx.noCacheBust {
			dir, _ := path.Split(filename)
			outRel = dir + f.hashed
		}
		outPath := "assets/" + outRel
		url := "/" + outPath
		ctx.fileAssets = append(ctx.fileAssets, FileAsset{
			SrcPath: filepath.Join(dirPath, filename),
			OutPath: outPath,
			Data:    f.data,
		})
		return url, true

	case "contents":
		return string(f.data), true
	}
	return nil, false
}

// fileNames is file:'s names(pattern): the names in dirPath that pattern
// matches, sorted, as the list<string> the declaration returns. A pattern
// reaching outside the directory matches nothing rather than listing a
// directory the import was not allowed to name.
func fileNames(dirPath, pattern string) (any, bool) {
	if _, err := filepath.Match(pattern, ""); err != nil || strings.ContainsAny(pattern, `/\\`) {
		return nil, false
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, false
	}
	out := []any{}
	for _, e := range entries {
		if ok, _ := filepath.Match(pattern, e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	return out, true
}

// --- Helper functions ---

// constLength folds the `.length` select the checker types as int on a list,
// a map or a string. Asked of the operand's type, since a map value may hold a
// "length" key.
func constLength(t *ir.Type, v any) (int, bool) {
	if t == nil {
		return 0, false
	}
	switch t.Kind {
	case ir.TypeList:
		if l, ok := v.([]any); ok {
			return len(l), true
		}
	case ir.TypeMap:
		if m, ok := v.(map[string]any); ok {
			return len(m), true
		}
	case ir.TypeString:
		if s, ok := v.(string); ok {
			return utf8.RuneCountInString(s), true
		}
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	if n, ok := v.(int); ok {
		return n, true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func intToStr(v int) string {
	return strconv.Itoa(v)
}

func floatToStr(v float64) string {
	return fmt.Sprintf("%v", v)
}

// nsConst resolves a `pkg.name` selector to the const it names, or reports
// false. The operand of such a selector is a namespace rather than a value, so
// the member has to be looked up in the package's own symbols; a var is not a
// const and does not fold.
func nsConst(x *ir.Select) (*ir.Var, bool) {
	id, ok := x.Operand.(*ir.Ident)
	if !ok {
		return nil, false
	}
	ns, ok := id.Sym.(*ir.Namespace)
	if !ok || ns.Pkg == nil || ns.Pkg.Symbols == nil {
		return nil, false
	}
	sym, ok := ns.Pkg.Symbols.LookupMember(x.Field)
	if !ok {
		return nil, false
	}
	v, ok := sym.(*ir.Var)
	if !ok || !v.IsConst {
		return nil, false
	}
	return v, true
}

// nsTarget is the registered name of the build-target node a package member
// select names, as in `html.platform`.
func nsTarget(x *ir.Select) (string, bool) {
	id, ok := x.Operand.(*ir.Ident)
	if !ok {
		return "", false
	}
	ns, ok := id.Sym.(*ir.Namespace)
	if !ok || ns.Pkg == nil || ns.Pkg.Symbols == nil {
		return "", false
	}
	sym, ok := ns.Pkg.Symbols.LookupMember(x.Field)
	if !ok {
		return "", false
	}
	_, name, ok := ir.TargetNode(sym)
	return name, ok
}

// unitConversionString spells a folded unit value the way every target
// displays one. Only a single-base unit reaches it: evalLiteral declines to
// reduce a multi-base value to a number at all, so one never folds.
func unitConversionString(conv *ir.Conversion, operand any) (string, bool) {
	if conv.Operand == nil {
		return "", false
	}
	ud := ir.UnitDeclOf(conv.Operand.ExprType())
	if ud == nil || !ud.IsSingleBase() {
		return "", false
	}
	var mag float64
	switch v := operand.(type) {
	case float64:
		mag = v
	case int:
		mag = float64(v)
	default:
		return "", false
	}
	return ir.FormatUnitTerm(mag, ud.Bases()[0].Name), true
}

// multiBaseUnitString displays a constant multi-base unit value, which
// evalLiteral declines to reduce to a number and so never reaches
// unitConversionString.
func multiBaseUnitString(e ir.Expr) (string, bool) {
	ud := ir.UnitDeclOf(e.ExprType())
	if ud == nil || ud.IsSingleBase() {
		return "", false
	}
	amounts := map[string]float64{}
	if !unitAmounts(e, 1, amounts, map[*ir.Var]bool{}) {
		return "", false
	}
	var terms []string
	for _, b := range ud.Bases() {
		if m := amounts[b.Name]; m != 0 {
			terms = append(terms, ir.FormatUnitTerm(m, b.Name))
		}
	}
	if len(terms) == 0 {
		return ir.FormatUnitZero(ud), true
	}
	return strings.Join(terms, ir.UnitTermSep), true
}

// unitAmounts adds sign times e's magnitude per base into out, for the
// literals, consts, sums and differences a constant unit value is written as.
func unitAmounts(e ir.Expr, sign float64, out map[string]float64, seen map[*ir.Var]bool) bool {
	switch x := e.(type) {
	case *ir.Literal:
		mag, base, ok := ir.UnitMagnitude(x)
		if ok {
			out[base] += sign * mag
		}
		return ok
	case *ir.Ident:
		v, ok := x.Sym.(*ir.Var)
		if !ok || !v.IsConst || v.Init == nil || seen[v] {
			return false
		}
		seen[v] = true
		defer delete(seen, v)
		return unitAmounts(v.Init, sign, out, seen)
	case *ir.Binary:
		switch x.Op {
		case ast.BinAdd:
			return unitAmounts(x.Left, sign, out, seen) && unitAmounts(x.Right, sign, out, seen)
		case ast.BinSub:
			return unitAmounts(x.Left, sign, out, seen) && unitAmounts(x.Right, -sign, out, seen)
		}
	}
	return false
}

// foldNavHref is `nav.href(to, params)` where both are known: the page's href
// with each `{name}` filled from the params' field of that name, escaped as a
// path segment -- what html's emitter writes for one it is handed at run time.
// A float field is left to run time, whose spelling of one is the target's.
func foldNavHref(args []any) (any, bool) {
	if len(args) < 1 {
		return nil, false
	}
	rec, ok := args[0].(*interp.Struct)
	if !ok {
		return nil, false
	}
	hv, _ := rec.Get("href")
	href, ok := hv.(string)
	if !ok {
		return nil, false
	}
	var params *interp.Struct
	if len(args) > 1 && args[1] != nil {
		if params, ok = args[1].(*interp.Struct); !ok {
			return nil, false
		}
	}
	filled, ok := ir.FillHref(href, func(name string) (string, bool) {
		if params == nil {
			return "", false
		}
		v, ok := params.Get(name)
		if !ok {
			return "", false
		}
		switch x := v.(type) {
		case string:
			return url.PathEscape(x), true
		case int64:
			return strconv.FormatInt(x, 10), true
		case int:
			return strconv.Itoa(x), true
		case bool:
			return strconv.FormatBool(x), true
		}
		return "", false
	})
	return filled, ok
}
