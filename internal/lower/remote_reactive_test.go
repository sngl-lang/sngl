package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// makeQueryPkg builds a package with:
//
//	#[query]
//	func users(page int) remote.Value<list<int>> { return fetchUsers(page) }
//
// The body answers the fetched type, not the box — the mark is what licenses
// that, and the lowering is what turns the body into the thunk.
func makeQueryPkg() (*ir.Package, *ir.Func) {
	fetched := ir.ListOf(ir.TypInt)
	fetchUsers := &ir.Func{
		Name:    "fetchUsers",
		IsAsync: true,
		Params:  []*ir.Param{{Name: "p", Type: ir.TypInt}},
		Return:  fetched,
	}
	page := &ir.Param{Name: "page", Type: ir.TypInt}
	users := &ir.Func{
		Name:    "users",
		Query:   true,
		Params:  []*ir.Param{page},
		Return:  ir.RemoteOf(fetched),
		IsAsync: true,
		Block: []ir.Stmt{&ir.Return{Value: &ir.Call{
			Type: fetched,
			Func: fetchUsers,
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "page", Sym: page, Type: ir.TypInt}}},
		}}},
	}
	pkg := &ir.Package{
		Funcs:          []*ir.Func{fetchUsers, users},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return pkg, users
}

// queryCall returns the RemoteQuery call a lowered query's body is.
func queryCall(t *testing.T, fn *ir.Func) *ir.Call {
	t.Helper()
	if len(fn.Block) != 1 {
		t.Fatalf("%s.Block len = %d; want 1", fn.Name, len(fn.Block))
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("%s body is %T; want a Return", fn.Name, fn.Block[0])
	}
	call, ok := ret.Value.(*ir.Call)
	if !ok {
		t.Fatalf("%s returns %T; want a call", fn.Name, ret.Value)
	}
	if got := call.Func.Intrinsic; got != "RemoteQuery" {
		t.Fatalf("%s returns intrinsic %q; want RemoteQuery", fn.Name, got)
	}
	return call
}

// The body becomes a lookup: the query is identified, the arguments are the key,
// and the original body is the thunk. No state var and no kicker — the pieces the
// first design synthesized are gone, because a key that is a loop variable has no
// setter for a kicker to watch.
func TestQueryLowersToALookup(t *testing.T) {
	pkg, users := makeQueryPkg()
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	if len(pkg.AsyncKickers) != 0 {
		t.Errorf("a query registered %d kicker(s); reading the box is what starts the fetch", len(pkg.AsyncKickers))
	}
	if findVar(pkg, "__async_users") != nil {
		t.Error("a query synthesized a settle state var; the box lives in the store")
	}

	call := queryCall(t, users)
	if len(call.Args) != 3 {
		t.Fatalf("RemoteQuery got %d args; want (queryID, args, fetch)", len(call.Args))
	}
	id, ok := call.Args[0].Value.(*ir.Literal)
	if !ok || id.Value != "users" {
		t.Errorf("first arg = %v; want the query id \"users\"", call.Args[0].Value)
	}
}

// The key is the arguments, so two calls with different arguments name different
// boxes. That is the whole point of per-call-site keying, and it is why the
// argument list rather than the declaration name carries it.
func TestQueryKeyIsItsArguments(t *testing.T) {
	pkg, users := makeQueryPkg()
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	key, ok := queryCall(t, users).Args[1].Value.(*ir.ListLit)
	if !ok {
		t.Fatalf("second arg = %T; want the argument tuple", queryCall(t, users).Args[1].Value)
	}
	if len(key.Elems) != 1 {
		t.Fatalf("key holds %d element(s); want just `page`", len(key.Elems))
	}
	id, ok := key.Elems[0].(*ir.Ident)
	if !ok || id.Name != "page" {
		t.Errorf("key element = %v; want the `page` parameter", key.Elems[0])
	}
}

// A component's implicit `this` is not part of the key: it identifies the caller,
// not the question, so two components asking the same thing must find one box.
func TestQueryKeyExcludesTheComponentReceiver(t *testing.T) {
	pkg, users := makeQueryPkg()
	recv := &ir.Param{Name: "this", Type: &ir.Type{Kind: ir.TypeComponent}}
	users.Params = append([]*ir.Param{recv}, users.Params...)
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	key := queryCall(t, users).Args[1].Value.(*ir.ListLit)
	for _, e := range key.Elems {
		if id, ok := e.(*ir.Ident); ok && id.Name == "this" {
			t.Fatal("the component receiver is in the key; two components would not share a box")
		}
	}
	if len(key.Elems) != 1 {
		t.Errorf("key holds %d element(s); want just `page`", len(key.Elems))
	}
}

// The thunk is the original body and answers the fetched type, never the box —
// the runtime is what boxes it. It stays async because the fetch is what gets
// awaited; a sync thunk would let a backend emit the call as already answered.
func TestQueryThunkAnswersTheFetchedType(t *testing.T) {
	pkg, users := makeQueryPkg()
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}

	thunk, ok := queryCall(t, users).Args[2].Value.(*ir.Lambda)
	if !ok {
		t.Fatalf("third arg = %T; want the fetch thunk", queryCall(t, users).Args[2].Value)
	}
	if !thunk.Func.IsAsync {
		t.Error("thunk.IsAsync = false; the fetch it wraps is awaited")
	}
	if thunk.Func.Return == nil || thunk.Func.Return.Kind != ir.TypeList {
		t.Errorf("thunk answers %v; want the fetched list<int>, not the box", thunk.Func.Return)
	}
	ret, ok := thunk.Func.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("thunk body is %T; want the original return", thunk.Func.Block[0])
	}
	if inner, ok := ret.Value.(*ir.Call); !ok || inner.Func.Name != "fetchUsers" {
		t.Errorf("thunk returns %v; want the original fetchUsers call", ret.Value)
	}
}

// Reading a box is synchronous, which is what lets a query appear in a prop
// expression at all. The await moved into the thunk.
func TestQueryBecomesSynchronous(t *testing.T) {
	pkg, users := makeQueryPkg()
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}
	if users.IsAsync {
		t.Error("users.IsAsync = true; the await belongs to the thunk now")
	}
	if users.Return == nil || users.Return.Kind != ir.TypeRemote {
		t.Errorf("users answers %v; the declaration keeps returning the box", users.Return)
	}
}

// A query declared inside a component is lowered too. The pass walks component
// funcs as well as package funcs, and a remote is most often component-local.
func TestQueryInsideAComponentIsLowered(t *testing.T) {
	pkg, users := makeQueryPkg()
	pkg.Funcs = pkg.Funcs[:1] // leave only fetchUsers at package level
	pkg.Components = []*ir.Component{{Name: "main", Funcs: []*ir.Func{users}}}
	if err := lowerAsyncReactive(pkg, Caps{NoAsyncReactive: true}, Options{}); err != nil {
		t.Fatalf("lowerAsyncReactive: %v", err)
	}
	queryCall(t, users)
}

// findVar returns the named var out of pkg.Vars, or nil.
func findVar(pkg *ir.Package, name string) *ir.Var {
	for _, v := range pkg.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}
