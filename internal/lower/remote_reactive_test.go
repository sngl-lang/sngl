package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// queryFixture is a component holding one `remote.query` lookup, the way an
// inlined adapter leaves one:
//
//	component main { var page = 1; var rows = remote.query("users", [*(&page)], fetch) }
//
// The key element is what the whole design turns on. `*(&page)` is what the
// caller wrote as `users(&page)` -- the cell -- and a bare `page` is what
// `users(page)` leaves -- its contents. Nothing else distinguishes them by the
// time lowering runs, because inlining has dissolved the adapter that declared
// the parameter.
type queryFixture struct {
	pkg  *ir.Package
	comp *ir.Component
	page *ir.Var
	rows *ir.Var
	call *ir.Call
}

func makeQueryFixture(key func(*ir.Var) ir.Expr) *queryFixture {
	fetched := ir.ListOf(ir.TypInt)
	page := &ir.Var{Name: "page", Type: ir.TypInt}
	call := remoteQueryFor(fetched, key(page))
	rows := &ir.Var{Name: "rows", Type: ir.RemoteOf(fetched), Init: call}
	// One read of the var, in a func the component owns -- what a prop
	// mentioning it amounts to, and the thing the lookup has to move into.
	show := &ir.Func{
		Name:   "show",
		Return: rows.Type,
		Block:  []ir.Stmt{&ir.Return{Value: &ir.Ident{Name: "rows", Sym: rows, Type: rows.Type}}},
	}
	comp := &ir.Component{Name: "main", Vars: []*ir.Var{page, rows}, Funcs: []*ir.Func{show}}
	pkg := &ir.Package{
		Components:     []*ir.Component{comp},
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	return &queryFixture{pkg: pkg, comp: comp, page: page, rows: rows, call: call}
}

// followedKey is the key `users(&page)` leaves: the cell, not its contents.
func followedKey(page *ir.Var) ir.Expr {
	read := &ir.Ident{Name: "page", Sym: page, Type: page.Type}
	return &ir.Unary{
		Type:    page.Type,
		Op:      ast.UnaryDeref,
		Operand: &ir.Unary{Type: ir.RefOf(page.Type), Op: ast.UnaryAddr, Operand: read},
	}
}

// snapshotKey is what `users(page)` leaves: the value the coercion passed
// through untouched.
func snapshotKey(page *ir.Var) ir.Expr {
	return &ir.Ident{Name: "page", Sym: page, Type: page.Type}
}

func remoteQueryFor(fetched *ir.Type, key ir.Expr) *ir.Call {
	boxed := ir.RemoteOf(fetched)
	thunk := &ir.Lambda{
		Type: &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: fetched}},
		Func: &ir.Func{IsAsync: true, Return: fetched, Block: []ir.Stmt{&ir.Return{Value: &ir.ListLit{Type: fetched}}}},
	}
	return &ir.Call{
		Type: boxed,
		Func: &ir.Func{
			Name:      remoteQueryIntrinsic,
			Intrinsic: remoteQueryIntrinsic,
			Params: []*ir.Param{
				{Name: "id", Type: ir.TypString},
				{Name: "key", Type: ir.ListOf(ir.TypDyn)},
				{Name: "fetch", Type: &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{Return: fetched}}},
			},
			Return: boxed,
			Purity: ir.PurityReadonly,
		},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Value: "users"}},
			{Value: &ir.Conversion{
				Type:    ir.ListOf(ir.TypDyn),
				Operand: &ir.ListLit{Type: ir.ListOf(ir.TypInt), Elems: []ir.Expr{key}},
			}},
			{Value: thunk},
		},
	}
}

func keyElems(t *testing.T, call *ir.Call) []ir.Expr {
	t.Helper()
	list, isList := unwrapConversions(call.Args[1].Value).(*ir.ListLit)
	if !isList {
		t.Fatalf("key is %T; want a list literal", call.Args[1].Value)
	}
	return list.Elems
}

// No ref reaches a backend. A key element written `*url` is the cell on one
// call and its contents on another, and by here it is the contents either way.
func TestARefInAKeyIsErased(t *testing.T) {
	f := makeQueryFixture(followedKey)
	if err := lowerQueries(f.pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}
	elem := keyElems(t, f.call)[0]
	id, isIdent := elem.(*ir.Ident)
	if !isIdent || id.Sym != ir.Symbol(f.page) {
		t.Fatalf("key element is %v; want the plain `page` read", elem)
	}
}

// The hole this exists to close. A box bound once in the constructor never
// re-keys however often its url changes, so a var whose key follows a cell is
// not stored at all: the lookup moves to every read, which is the shape that
// already re-evaluated.
func TestAFollowedQueryVarBecomesItsLookup(t *testing.T) {
	f := makeQueryFixture(followedKey)
	if err := lowerQueries(f.pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}

	for _, v := range f.comp.Vars {
		if v == f.rows {
			t.Fatal("the var is still stored; a box bound once never re-keys")
		}
	}
	read, isStmt := f.comp.Funcs[0].Block[0].(*ir.Return)
	if !isStmt {
		t.Fatalf("the read is %T; want a return", f.comp.Funcs[0].Block[0])
	}
	call, isCall := read.Value.(*ir.Call)
	if !isCall || call.Func == nil || call.Func.Intrinsic != remoteQueryIntrinsic {
		t.Fatalf("the read is %v; want the lookup itself", read.Value)
	}
	// Mentioning the cell it follows is the whole of the reactivity: the
	// ordinary dependency walk re-evaluates an expression that names `page`.
	if elem := keyElems(t, call)[0]; !namesVar(elem, f.page) {
		t.Errorf("the moved lookup keys on %v; want the `page` it follows", elem)
	}
}

// A key of plain values is a question asked once, and the var holding its
// answer stays a var. This is the other half of the caller's choice -- without
// it `&` would say nothing.
func TestASnapshotQueryVarStaysAVar(t *testing.T) {
	f := makeQueryFixture(snapshotKey)
	if err := lowerQueries(f.pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}
	found := false
	for _, v := range f.comp.Vars {
		if v == f.rows {
			found = true
		}
	}
	if !found {
		t.Error("a query keyed by values was re-derived; the caller asked for the answer to the url it named")
	}
}

// Reading the box is what starts the fetch, so nothing synthesizes a kicker or
// a settle state var to start one.
func TestAQueryRegistersNoKicker(t *testing.T) {
	f := makeQueryFixture(followedKey)
	if err := lowerQueries(f.pkg, Features{}, Options{}); err != nil {
		t.Fatalf("lowerQueries: %v", err)
	}
	if len(f.pkg.AsyncKickers) != 0 {
		t.Errorf("a query registered %d kicker(s); reading the box is what starts the fetch", len(f.pkg.AsyncKickers))
	}
}

func namesVar(e ir.Expr, v *ir.Var) bool {
	id, isIdent := e.(*ir.Ident)
	return isIdent && id.Sym == ir.Symbol(v)
}
