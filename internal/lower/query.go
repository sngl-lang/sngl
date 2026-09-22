package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passQuery settles what a query's key depends on, and erases the refs that
// said so.
//
// There is no query construct left to lower. `remote.query(id, key, fetch)` is
// an ordinary intrinsic an adapter calls, and the ordinary inliner is what
// carries the adapter's body to the call site -- which is where the box has to
// be keyed anyway. What remains is the one thing a backend cannot be handed: a
// `ref` in the key.
//
// A key element written `*url` arrives here as `*(&m.url)` when the caller
// passed the cell and as `m.url` when it passed the value, because the ref
// coercion leaves a value alone (see internal/checker/refcoerce.go). So the
// shape says which the author asked for:
//
//	http.fetch(&url)   →  key [*(&m.url)]  →  m.url, and m.url is a dependency
//	http.fetch(url)    →  key [m.url]      →  m.url, and nothing follows it
//
// The dependency is what a var initialized from a query needs to stop being a
// snapshot: reading the box is what starts a fetch, so a box bound once in the
// constructor never re-keys however often its url changes. Such a var is not
// stored at all -- see followQueries.
//
// Always on: a ref that reached a backend would be a pointer into a model the
// platform copies, and neither branch of this is gated on a capability.
var passQuery = pass{
	name:    "Query",
	enabled: func(Features) bool { return true },
	apply:   lowerQueries,
}

// remoteQueryIntrinsic is the id sngl:remote's `query` declares and every
// language backend implements against its own pkg/<lang>/remote runtime.
const remoteQueryIntrinsic = "RemoteQuery"

func lowerQueries(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	// Which vars each query call follows, collected before the refs naming them
	// are erased.
	followed := map[*ir.Call][]*ir.Var{}
	if err := ir.WalkExprs(pkg, func(e ir.Expr) error {
		call, isCall := e.(*ir.Call)
		if !isCall || !isRemoteQuery(call) || len(call.Args) < 2 {
			return nil
		}
		// The key arrives as list<dyn>(...) around the literal: `key` is
		// declared as list<dyn> and the argument is a list of whatever the
		// adapter keys on, so the checker converts.
		key, isList := unwrapConversions(call.Args[1].Value).(*ir.ListLit)
		if !isList {
			return nil
		}
		id := queryIDOf(call)
		for _, elem := range key.Elems {
			// The key has to be comparable for one box to be found again, and
			// the runtime can only assert that once the program is running. The
			// old #[query] asked it of the declaration's parameters; asking it
			// of the key is both the narrower question and the true one, since
			// a key is whatever the adapter chose to key on.
			if t := elem.ExprType(); !keyEncodable(t) {
				return fmt.Errorf("Query: %s is keyed by a %s, which cannot be part of a key", id, t)
			}
			if v := followedVar(elem); v != nil {
				followed[call] = append(followed[call], v)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// `*(&x)` is x wherever it appears -- in a key, in a thunk, or in a body
	// that never went near a query. Erasing it here rather than in the
	// optimizer is what kept the two spellings apart long enough to read them.
	if err := ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		if inner := derefOfAddr(e); inner != nil {
			return inner, nil
		}
		return e, nil
	}); err != nil {
		return err
	}

	return followQueries(pkg, followed)
}

// queryIDOf names the query for a diagnostic: the id the adapter passed, or the
// intrinsic itself when the argument is not a literal.
func queryIDOf(call *ir.Call) string {
	if lit, isLit := call.Args[0].Value.(*ir.Literal); isLit {
		return lit.Value
	}
	return remoteQueryIntrinsic
}

func isRemoteQuery(call *ir.Call) bool {
	return call != nil && call.Func != nil && call.Func.Intrinsic == remoteQueryIntrinsic
}

// derefOfAddr answers the expression `*(&e)` reduces to, or nil.
func derefOfAddr(e ir.Expr) ir.Expr {
	deref, isUnary := e.(*ir.Unary)
	if !isUnary || deref.Op != ast.UnaryDeref {
		return nil
	}
	addr, isUnary := deref.Operand.(*ir.Unary)
	if !isUnary || addr.Op != ast.UnaryAddr {
		return nil
	}
	return addr.Operand
}

// unwrapConversions strips the conversions wrapping e.
func unwrapConversions(e ir.Expr) ir.Expr {
	for {
		conv, isConv := e.(*ir.Conversion)
		if !isConv {
			return e
		}
		e = conv.Operand
	}
}

// followedVar names the state a key element follows, or nil when the element is
// a value rather than a cell. The address may be taken of a path -- `&m.url`
// reaches here as an Addr over a Select -- so the root is what identifies it.
func followedVar(elem ir.Expr) *ir.Var {
	inner := derefOfAddr(elem)
	if inner == nil {
		return nil
	}
	for {
		switch x := inner.(type) {
		case *ir.Ident:
			v, isVar := x.Sym.(*ir.Var)
			if !isVar {
				return nil
			}
			return v
		case *ir.Select:
			inner = x.Operand
		case *ir.Index:
			inner = x.Operand
		case *ir.Conversion:
			inner = x.Operand
		default:
			return nil
		}
	}
}

// followQueries makes a var initialized from a followed query stop being a
// snapshot.
//
// Such a var is not state -- it is a question, and the answer follows what the
// question is asked about. So it is not stored: every read of it becomes the
// lookup itself, which is what a computed is and what already worked when the
// program wrote one. Nothing about reactivity has to be arranged after that.
// The lookup mentions the cells the key follows, so the ordinary dependency
// walk re-evaluates it when one is written, the re-evaluation looks up a
// different key, and a key with no fetch behind it starts one.
//
// Copying the lookup to each read rather than synthesizing a function to hold
// it: a lookup is a map read that answers the same box for the same key, so the
// copies are one box, and this needs none of the receiver and dependency
// bookkeeping a synthesized computed would.
//
// A var something assigns to is left alone. Overwriting a derived box is a
// different program -- one that wanted the snapshot -- and re-deriving it would
// throw the write away.
func followQueries(pkg *ir.Package, followed map[*ir.Call][]*ir.Var) error {
	if len(followed) == 0 {
		return nil
	}
	derived := map[*ir.Var]ir.Expr{}
	for _, o := range ir.Owners(pkg) {
		for _, v := range o.Vars {
			if v == nil || v.Init == nil || v.IsConst {
				continue
			}
			if holdsFollowedQuery(v.Init, followed) && !isAssigned(pkg, v) {
				derived[v] = v.Init
			}
		}
	}
	if len(derived) == 0 {
		return nil
	}

	if err := ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		id, isIdent := e.(*ir.Ident)
		if !isIdent {
			return e, nil
		}
		v, isVar := id.Sym.(*ir.Var)
		if !isVar || derived[v] == nil {
			return e, nil
		}
		return deepCloneExpr(derived[v]), nil
	}); err != nil {
		return err
	}

	keep := func(vars []*ir.Var) []*ir.Var {
		kept := vars[:0]
		for _, v := range vars {
			if derived[v] == nil {
				kept = append(kept, v)
			}
		}
		return kept
	}
	pkg.Vars = keep(pkg.Vars)
	for _, c := range pkg.Components {
		c.Vars = keep(c.Vars)
	}
	return nil
}

// holdsFollowedQuery reports whether e is, or contains, a query whose key
// follows a cell.
func holdsFollowedQuery(e ir.Expr, followed map[*ir.Call][]*ir.Var) bool {
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if call, isCall := x.(*ir.Call); isCall && len(followed[call]) > 0 {
			found = true
		}
		return nil
	})
	return found
}

// isAssigned reports whether anything writes to v.
func isAssigned(pkg *ir.Package, v *ir.Var) bool {
	written := false
	_ = ir.WalkStmts(pkg, func(s ir.Stmt) error {
		switch n := s.(type) {
		case *ir.Assign:
			if id, isIdent := n.Target.(*ir.Ident); isIdent && id.Sym == ir.Symbol(v) {
				written = true
			}
		case *ir.Toggle:
			if id, isIdent := n.Target.(*ir.Ident); isIdent && id.Sym == ir.Symbol(v) {
				written = true
			}
		}
		return nil
	})
	return written
}

// keyEncodable reports whether a value of t can form part of a query's key.
//
// A key has to be comparable for one box to be found again, and — once the html
// route mode carries one in a URL — writable as text and readable back into the
// declared parameter types. Both rule out the same things: a function, a
// component, an iterator, and another Value, none of which has an identity a
// second process could reconstruct.
//
// Deliberately laxer than isComparable, which map keys use: a list is not a
// usable map key but is perfectly good in a key tuple, because the encoding
// walks it rather than hashing an address.
func keyEncodable(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString,
		ir.TypeEnum, ir.TypeUnit, ir.TypeDyn:
		return true
	case ir.TypeList, ir.TypeOption:
		return len(t.Elems) == 1 && keyEncodable(t.Elems[0])
	case ir.TypeMap:
		return len(t.Elems) == 2 && keyEncodable(t.Elems[0]) && keyEncodable(t.Elems[1])
	case ir.TypeStruct:
		// A named struct is walked field by field. An anonymous one has no
		// declaration to walk, so it is not one a key can hold.
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok {
			return false
		}
		for _, f := range sd.Fields {
			if !keyEncodable(f.Type) {
				return false
			}
		}
		return true
	}
	return false
}
