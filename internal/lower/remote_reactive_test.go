package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// queryFixture is a query declaration and a component that calls it:
//
//	#[query]
//	func users(page int) remote.Value<list<int>> { return fetchUsers(page) }
//
//	component main { var page = 1; var rows = users(page) }
//
// The body answers the fetched type, not the box — the mark is what licenses
// that, and the lowering is what turns the body into the thunk. What gets
// rewritten is the call: a query is a box the store holds, found by the
// declaration and the arguments it was asked with, and a call is what asks.
type queryFixture struct {
	pkg   *ir.Package
	users *ir.Func
	page  *ir.Var
	rows  *ir.Var
}

func makeQueryFixture() *queryFixture {
	fetched := ir.ListOf(ir.TypInt)
	fetchUsers := &ir.Func{
		Name:    "fetchUsers",
		IsAsync: true,
		Params:  []*ir.Param{{Name: "p", Type: ir.TypInt}},
		Return:  fetched,
	}
	pageParam := &ir.Param{Name: "page", Type: ir.TypInt}
	users := &ir.Func{
		Name:    "users",
		Query:   true,
		Params:  []*ir.Param{pageParam},
		Return:  ir.RemoteOf(fetched),
		IsAsync: true,
		Block: []ir.Stmt{&ir.Return{Value: &ir.Call{
			Type: fetched,
			Func: fetchUsers,
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "page", Sym: pageParam, Type: ir.TypInt}}},
		}}},
	}

	page := &ir.Var{Name: "page", Type: ir.TypInt}
	rows := &ir.Var{Name: "rows", Type: ir.RemoteOf(fetched), Init: queryCallTo(users, page)}
	comp := &ir.Component{Name: "main", Vars: []*ir.Var{page, rows}}

	pkg := &ir.Package{
		Funcs:          []*ir.Func{fetchUsers, users},
		Components:     []*ir.Component{comp},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return &queryFixture{pkg: pkg, users: users, page: page, rows: rows}
}

// queryCallTo is one call site: `users(<arg>)`.
func queryCallTo(users *ir.Func, arg *ir.Var) *ir.Call {
	return &ir.Call{
		Type: users.Return,
		Func: users,
		Args: []ir.CallArg{{Value: &ir.Ident{Name: arg.Name, Sym: arg, Type: arg.Type}}},
	}
}

// lookup returns the RemoteQuery call an expression was rewritten into.
func lookup(t *testing.T, e ir.Expr) *ir.Call {
	t.Helper()
	call, ok := e.(*ir.Call)
	if !ok {
		t.Fatalf("call site is %T; want a call", e)
	}
	if got := call.Func.Intrinsic; got != "RemoteQuery" {
		t.Fatalf("call site is intrinsic %q; want RemoteQuery", got)
	}
	return call
}

// The call becomes a lookup: the query is identified, the arguments are the key,
// and the declaration's body is the thunk. No state var and no kicker — the
// pieces the first design synthesized are gone, because a key that is a loop
// variable has no setter for a kicker to watch.
func TestQueryCallLowersToALookup(t *testing.T) {
	f := makeQueryFixture()
	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	if len(f.pkg.AsyncKickers) != 0 {
		t.Errorf("a query registered %d kicker(s); reading the box is what starts the fetch", len(f.pkg.AsyncKickers))
	}
	if findVar(f.pkg, "__async_users") != nil {
		t.Error("a query synthesized a settle state var; the box lives in the store")
	}

	call := lookup(t, f.rows.Init)
	if len(call.Args) != 3 {
		t.Fatalf("RemoteQuery got %d args; want (queryID, args, fetch)", len(call.Args))
	}
	id, ok := call.Args[0].Value.(*ir.Literal)
	if !ok || id.Value != "users" {
		t.Errorf("first arg = %v; want the query id \"users\"", call.Args[0].Value)
	}
}

// The declaration is left alone. It is not what the output contains — the call
// sites are — and a library query is not emitted at all: nothing emits an
// imported package's functions, which is why rewriting the body left the call
// standing against a name the output did not have.
func TestQueryDeclarationIsUntouched(t *testing.T) {
	f := makeQueryFixture()
	before := f.users.Block[0]
	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}
	if f.users.Block[0] != before {
		t.Error("the query declaration's body was rewritten; the call site is what becomes the lookup")
	}
	if f.users.Return == nil || f.users.Return.Kind != ir.TypeRemote {
		t.Errorf("users answers %v; the declaration keeps returning the box", f.users.Return)
	}
}

// The key is what the call site passed, so two calls with different arguments
// name different boxes and two with the same one name the same. That is the
// whole point of keying per call site, and it is why the arguments rather than
// the declaration carry it.
func TestQueryKeyIsTheCallsArguments(t *testing.T) {
	f := makeQueryFixture()
	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	key, ok := lookup(t, f.rows.Init).Args[1].Value.(*ir.ListLit)
	if !ok {
		t.Fatalf("second arg = %T; want the argument tuple", lookup(t, f.rows.Init).Args[1].Value)
	}
	if len(key.Elems) != 1 {
		t.Fatalf("key holds %d element(s); want just the page argument", len(key.Elems))
	}
	id, ok := key.Elems[0].(*ir.Ident)
	if !ok || id.Sym != ir.Symbol(f.page) {
		t.Errorf("key element = %v; want the `page` the call site passed", key.Elems[0])
	}
}

// Two call sites are two lookups, each keyed by what it asked. A declaration
// serves them both and is spliced into neither.
func TestTwoCallSitesAreTwoLookups(t *testing.T) {
	f := makeQueryFixture()
	other := &ir.Var{Name: "next", Type: ir.TypInt}
	second := &ir.Var{Name: "more", Type: f.users.Return, Init: queryCallTo(f.users, other)}
	comp := f.pkg.Components[0]
	comp.Vars = append(comp.Vars, other, second)

	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	first := lookup(t, f.rows.Init)
	sec := lookup(t, second.Init)
	if first == sec {
		t.Fatal("both call sites became one expression")
	}
	keyOf := func(c *ir.Call) ir.Symbol {
		elems := c.Args[1].Value.(*ir.ListLit).Elems
		return elems[0].(*ir.Ident).Sym
	}
	if keyOf(first) == keyOf(sec) {
		t.Error("both lookups are keyed by the same value; each is keyed by what its own call site passed")
	}
}

// A component's implicit `this` is not part of the key: it identifies the
// caller, not the question, so two components asking the same thing must find
// one box.
func TestQueryKeyExcludesTheComponentReceiver(t *testing.T) {
	f := makeQueryFixture()
	recv := &ir.Param{Name: "this", Type: &ir.Type{Kind: ir.TypeComponent}}
	f.users.Params = append([]*ir.Param{recv}, f.users.Params...)
	call := f.rows.Init.(*ir.Call)
	call.Args = append([]ir.CallArg{{Name: "this", Value: &ir.Ident{Name: "this", Sym: recv, Type: recv.Type}}}, call.Args...)

	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	key := lookup(t, f.rows.Init).Args[1].Value.(*ir.ListLit)
	for _, e := range key.Elems {
		if id, ok := e.(*ir.Ident); ok && id.Name == "this" {
			t.Fatal("the component receiver is in the key; two components would not share a box")
		}
	}
	if len(key.Elems) != 1 {
		t.Errorf("key holds %d element(s); want just the page argument", len(key.Elems))
	}
}

// The thunk is the declaration's body with the call's arguments in it, and it
// answers the fetched type, never the box — the runtime is what boxes it. It
// stays async because the fetch is what gets awaited; a sync thunk would let a
// backend emit the call as already answered.
func TestQueryThunkAnswersTheFetchedType(t *testing.T) {
	f := makeQueryFixture()
	if err := lowerQueries(f.pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	thunk, ok := lookup(t, f.rows.Init).Args[2].Value.(*ir.Lambda)
	if !ok {
		t.Fatalf("third arg = %T; want the fetch thunk", lookup(t, f.rows.Init).Args[2].Value)
	}
	if !thunk.Func.IsAsync {
		t.Error("thunk.IsAsync = false; the fetch it wraps is awaited")
	}
	if len(thunk.Func.Params) != 0 {
		t.Errorf("thunk declares %d param(s); what the arguments said is in its body and its key", len(thunk.Func.Params))
	}
	if thunk.Func.Return == nil || thunk.Func.Return.Kind != ir.TypeList {
		t.Errorf("thunk answers %v; want the fetched list<int>, not the box", thunk.Func.Return)
	}
	ret, ok := thunk.Func.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("thunk body is %T; want the original return", thunk.Func.Block[0])
	}
	inner, ok := ret.Value.(*ir.Call)
	if !ok || inner.Func.Name != "fetchUsers" {
		t.Fatalf("thunk returns %v; want the original fetchUsers call", ret.Value)
	}
	// The parameter is gone from the body: what the call site passed stands
	// where it stood, so the thunk closes over nothing the declaration named.
	arg, ok := inner.Args[0].Value.(*ir.Ident)
	if !ok || arg.Sym != ir.Symbol(f.page) {
		t.Errorf("thunk fetches with %v; want the `page` the call site passed", inner.Args[0].Value)
	}
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
