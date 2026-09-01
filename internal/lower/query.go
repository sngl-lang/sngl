package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passQuery rewrites every call to a #[query] function into a RemoteQuery
// lookup.
//
// Always on, unlike the settle lowering it used to live inside. That one is
// gated on NoAsyncReactive — whether the target needs async lowered out — and a
// query is not an async construct. Sharing the gate meant a target that handles
// async natively never rewrote a query at all: bubbletea emitted the fetch as
// the function's whole body, with no box, no key and no store, and nothing said
// so.
//
// It runs before the settle lowering so a query's own async call is already
// inside the thunk by the time that pass looks for one to hoist.
var passQuery = pass{
	name:    "Query",
	enabled: func(Caps) bool { return true },
	apply:   lowerQueries,
}

// lowerQueries rewrites the call sites, not the declarations.
//
// A query has no existence as a function in the output: it is a box the store
// holds, found by the declaration it belongs to and the arguments it was asked
// with, and a call is what asks. Rewriting the body instead left the call
// standing — which worked only because a program's own declarations are emitted
// beside it. A query the library declares (sngl:remote/http's `fetch`) is not:
// nothing emits an imported package's functions, so the call was to a name the
// output did not contain.
//
// The arguments reach the key as the call site wrote them, which is the whole
// point of keying per call site: two components asking `fetch(profileURL)` and
// `fetch(feedURL)` hold different boxes, and two asking the same url hold one.
func lowerQueries(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		call, ok := e.(*ir.Call)
		if !ok || call.Func == nil || !call.Func.Query {
			return e, nil
		}
		return lowerQueryCall(call)
	})
}

// lowerQueryCall builds the RemoteQuery lookup one call to a query becomes.
//
// The declaration's body is the fetch; it becomes the thunk RemoteQuery calls,
// with the call's arguments substituted for the parameters, and the call now
// answers whatever box the store holds for this query and these arguments.
// Everything the first design synthesized — a state var named for the
// declaration, a kicker, an AsyncKickerEntry per reactive dependency — is gone,
// because a key that is a loop variable or any other derived value has no setter
// for a kicker to hang on. Reading the box is what starts the fetch.
//
// So there is nothing here about reactivity. A prop reading `users(page)`
// mentions `page`, the ordinary dependency walk re-evaluates it, and the
// re-evaluation looks up a different key.
func lowerQueryCall(call *ir.Call) (ir.Expr, error) {
	fn := call.Func
	retType := fn.Return
	if retType == nil || retType.Kind != ir.TypeRemote {
		// markQuery rejects this at the declaration, so reaching it means the
		// mark and this pass disagree about what a query is.
		return nil, fmt.Errorf("Query: %s carries #[query] but answers %s", fn.Name, retType)
	}
	if len(retType.Elems) != 1 || retType.Elems[0] == nil {
		return nil, fmt.Errorf("Query: cannot lower %s: %s has no type argument", fn.Name, retType)
	}
	if len(fn.Block) == 0 {
		return nil, fmt.Errorf("Query: cannot lower %s: a query needs a body to fetch with", fn.Name)
	}
	fetched := retType.Elems[0]

	bound, key, err := queryArgs(fn, call)
	if err != nil {
		return nil, err
	}

	// The body verbatim, so a query whose fetch is several statements lowers the
	// same as a one-liner. Cloned through a lambda carrying the declaration's
	// params, so the copy's own parameter references are the ones substitution
	// rewrites and the declaration is left as it was for the next call site.
	// It stays async when the body is: the fetch is what the runtime awaits, and
	// dropping the flag would let a backend emit the call as though it had
	// already answered.
	thunk, _ := ir.CloneExpr(&ir.Lambda{
		Type: &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: fetched}},
		Func: &ir.Func{
			IsAsync: fn.IsAsync || ir.BlockHasAsyncCall(fn.Block),
			Params:  fn.Params,
			Return:  fetched,
			Block:   fn.Block,
		},
	}).(*ir.Lambda)
	thunk.Func.Block = substituteParams(thunk.Func.Block, bound)
	// A thunk takes nothing: what the arguments said is in its body now, and in
	// the key beside it.
	thunk.Func.Params = nil

	return remoteQueryCall(fn, retType, fetched, key, thunk)
}

// queryArgs binds a call's arguments to the declaration's parameters, answering
// the bindings the thunk substitutes and the key the box is found by.
//
// The key is the arguments in declaration order. A component's implicit `this`
// is not one of them: it identifies the caller, not the question, and two
// components asking the same question have to find the same box.
func queryArgs(fn *ir.Func, call *ir.Call) (map[string]ir.Expr, []ir.Expr, error) {
	bound := make(map[string]ir.Expr, len(fn.Params))
	byName := map[string]ir.Expr{}
	var positional []ir.Expr
	for _, a := range call.Args {
		if a.Name != "" {
			byName[a.Name] = a.Value
			continue
		}
		positional = append(positional, a.Value)
	}
	var key []ir.Expr
	next := 0
	for _, p := range fn.Params {
		if p == nil {
			continue
		}
		var arg ir.Expr
		switch {
		case isComponentReceiver(p):
			continue
		case byName[p.Name] != nil:
			arg = byName[p.Name]
		case next < len(positional):
			arg = positional[next]
			next++
		case p.Default != nil:
			arg = p.Default
		default:
			return nil, nil, fmt.Errorf("Query: cannot lower a call to %s: no argument for %q", fn.Name, p.Name)
		}
		bound[p.Name] = arg
		key = append(key, deepCloneExpr(arg))
	}
	return bound, key, nil
}

// isComponentReceiver reports whether p is the implicit `this` a function
// declared inside a component carries.
func isComponentReceiver(p *ir.Param) bool {
	return p != nil && p.Name == "this" && p.Type != nil && p.Type.Kind == ir.TypeComponent
}

// remoteQueryCall builds the RemoteQuery call with T substituted for the type
// this query fetches.
//
// Substituting is the point of declaring the primitive generic. The registry
// entry is a shape hint over `dyn`, and a backend handed that would emit `any`
// plus a cast at every use; here the fetched type is known, so what a backend
// sees is `func() list<User>` answering a remote.Value<list<User>>.
func remoteQueryCall(fn *ir.Func, retType, fetched *ir.Type, args []ir.Expr, thunk ir.Expr) (*ir.Call, error) {
	// The signature is written here rather than looked up. The declaration in
	// sngl:internal/remote is generic in T, and this is the site that knows what
	// T is — so substituting into a looked-up shape would be a longer way of
	// saying what is already in hand, and it would make the pass depend on that
	// package having been loaded. A pass that synthesizes a call knows the call
	// it is synthesizing.
	params := []*ir.Param{
		{Name: "queryID", Type: ir.TypString},
		{Name: "args", Type: ir.ListOf(ir.TypDyn)},
		{Name: "fetch", Type: &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: fetched}}},
	}
	return &ir.Call{
		Type: retType,
		Func: &ir.Func{
			Name:      remoteQueryIntrinsic,
			Intrinsic: remoteQueryIntrinsic,
			Params:    params,
			Return:    retType,
			Purity:    ir.PurityReadonly,
		},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Value: queryID(fn)}},
			{Value: &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: args}},
			{Value: thunk},
		},
	}, nil
}

// remoteQueryIntrinsic is the id sngl:internal/remote declares and every
// language backend implements against its own pkg/<lang>/remote runtime.
const remoteQueryIntrinsic = "RemoteQuery"

// queryID names the declaration a box belongs to, so two queries never share a
// key space. The name alone suffices: the checker already refuses two
// declarations of one name at file scope.
func queryID(fn *ir.Func) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}
