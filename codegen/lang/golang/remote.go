package golang

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// remoteImportPath is the Go runtime behind sngl:remote, and remoteGoType the
// box a query answers with. A remote.Value<T> is a *remote.Value[T]: the box has
// identity, since two readers of one key hold the same one and a settle has to
// be visible to both.
const (
	remoteImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/remote"
	remoteGoType     = "remote.Value"
	remoteFailureGo  = "remote.Failure"

	// The transport behind sngl:remote/http, and the value it answers with.
	remoteHTTPImportPath = remoteImportPath + "/http"
	remoteHTTPResultGo   = "http.Response"
)

// init registers the Go emitters for sngl:remote.
//
// The accessors are methods on the box, so each is a one-line rendering. Only
// RemoteQuery has anything to decide, and what it decides is the type argument.
func init() {
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterIntrinsic(langGo, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a), []string{remoteImportPath}
		})
	}

	// The three facts. Get rather than Value because the Go type is already
	// called Value; an option<T> is a *T in Go, which is what these answer.
	reg("remote.value", func(a []string) string { return a[0] + ".Get()" })
	reg("remote.error", func(a []string) string { return a[0] + ".Err()" })
	reg("remote.inFlight", func(a []string) string { return a[0] + ".InFlight()" })
	reg("remote.or", func(a []string) string { return a[0] + ".Or(" + a[1] + ")" })

	// Refresh is a statement in SNGL and an expression here, so it is wrapped
	// to yield nothing: the call site is a CallStmt either way.
	reg("remote.refresh", func(a []string) string {
		return "func() { " + a[0] + ".Refresh() }()"
	})

	// The three constructors need the type argument written out, since Go
	// cannot infer T for Pending from anything at the call site and inferring
	// it for the other two would leave the three spelled inconsistently.
	codegen.RegisterIntrinsic(langGo, "remote.of", remoteCtor("Of", true))
	codegen.RegisterIntrinsic(langGo, "remote.failedWith", remoteCtor("FailedWith", true))
	codegen.RegisterIntrinsic(langGo, "remote.pending", remoteCtor("Pending", false))

	codegen.RegisterIntrinsic(langGo, "RemoteQuery", emitRemoteQuery)
}

// remoteCtor emits one of the three direct constructors. takesArg says whether
// the SNGL declaration has one; Pending does not.
func remoteCtor(name string, takesArg bool) codegen.IntrinsicEmitter {
	return func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		var arg string
		if takesArg && len(args) > 0 {
			arg = tr(args[0])
		}
		return "remote." + name + "[" + remoteFetchedGo(args) + "](" + arg + ")", []string{remoteImportPath}
	}
}

// remoteFetchedGo is the Go spelling of the T a constructor is building a box
// of. Read off the argument for Of, and off nothing for Pending — where `any`
// is the honest answer, since a pending box holds no value to have a type.
func remoteFetchedGo(args []ir.Expr) string {
	if len(args) == 0 || args[0] == nil {
		return "any"
	}
	t := args[0].ExprType()
	if t == nil {
		return "any"
	}
	if t.Kind == ir.TypeStruct && ir.IsRemoteFailureStruct(t) {
		// FailedWith's argument is the failure, not the fetched type; a box of
		// failures is not what was asked for.
		return "any"
	}
	return IRTypeToGo(t)
}

// emitRemoteQuery renders the read-triggered lookup `remote.query` is.
//
// The thunk the lowering built answers T, while the runtime wants (T, error):
// a fetch that fails does so by returning a Failure, and SNGL has no second
// return to carry one. So the thunk is wrapped, and an error reaches the box
// through the runtime's own classification.
func emitRemoteQuery(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) != 3 {
		return "", nil
	}
	id, key, fetch := tr(args[0]), tr(args[1]), tr(args[2])
	fetched := "any"
	if t := args[2].ExprType(); t != nil && t.Kind == ir.TypeFunc && t.Sig != nil && t.Sig.Return != nil {
		fetched = IRTypeToGo(t.Sig.Return)
	}
	return "remote.Query[" + fetched + "](remote.Default, " + id + ", " + key +
			", func() (" + fetched + ", error) { return (" + fetch + ")(), nil })",
		[]string{remoteImportPath}
}
