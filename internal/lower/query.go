package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passQuery rewrites every #[query] function's body into a RemoteQuery lookup.
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

func lowerQueries(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	for _, fn := range pkg.Funcs {
		if !fn.Query {
			continue
		}
		if err := lowerQuery(fn); err != nil {
			return err
		}
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			if !fn.Query {
				continue
			}
			if err := lowerQuery(fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// lowerQuery rewrites a #[query] function's body into a RemoteQuery lookup.
//
// The body was the fetch; it becomes the thunk RemoteQuery calls, and the
// function now answers whatever box the store holds for this query and these
// arguments. Everything the first design synthesized — a state var named for the
// declaration, a kicker, an AsyncKickerEntry per reactive dependency — is gone,
// because a key that is a loop variable or any other derived value has no setter
// for a kicker to hang on. Reading the box is what starts the fetch.
//
// So there is nothing here about reactivity. A prop reading `users(page)`
// mentions `page`, the ordinary dependency walk re-evaluates it, and the
// re-evaluation looks up a different key.
func lowerQuery(fn *ir.Func) error {
	retType := fn.Return
	if retType == nil || retType.Kind != ir.TypeRemote {
		// markQuery rejects this at the declaration, so reaching it means the
		// mark and this pass disagree about what a query is.
		return fmt.Errorf("NoAsyncReactive: %s carries #[query] but answers %s", fn.Name, retType)
	}
	if len(retType.Elems) != 1 || retType.Elems[0] == nil {
		return fmt.Errorf("NoAsyncReactive: cannot lower %s: %s has no type argument", fn.Name, retType)
	}
	fetched := retType.Elems[0]
	if len(fn.Block) == 0 {
		return fmt.Errorf("NoAsyncReactive: cannot lower %s: a query needs a body to fetch with", fn.Name)
	}

	// The thunk is the body verbatim, so a query whose fetch is several
	// statements lowers the same as a one-liner. It stays async when the body is:
	// the fetch is what the runtime awaits, and dropping the flag would let a
	// backend emit the call as though it had already answered.
	thunk := &ir.Lambda{
		Type: &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: fetched}},
		Func: &ir.Func{
			IsAsync: fn.IsAsync || ir.BlockHasAsyncCall(fn.Block),
			Return:  fetched,
			Block:   fn.Block,
		},
	}

	// The key is the arguments, in declaration order. A component's implicit
	// `this` is not one of them: it identifies the caller, not the question, and
	// two components asking the same question have to find the same box.
	args := make([]ir.Expr, 0, len(fn.Params))
	for _, p := range fn.Params {
		if p == nil || isComponentReceiver(p) {
			continue
		}
		args = append(args, &ir.Ident{Name: p.Name, Sym: p, Type: p.Type})
	}

	call, err := remoteQueryCall(fn, retType, fetched, args, thunk)
	if err != nil {
		return err
	}
	fn.Block = []ir.Stmt{&ir.Return{Value: call}}
	// The await moved into the thunk: reading a box is synchronous, which is what
	// lets a prop expression contain one.
	fn.IsAsync = false
	return nil
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
