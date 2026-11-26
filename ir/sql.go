package ir

// SQLQueryIntrinsic is sngl:sql's query: a statement answering rows, run by
// each language's runtime and answered as JSON.
const SQLQueryIntrinsic = "sql.query"

// SQLExecIntrinsic is sngl:sql's exec: a statement answering what it did.
const SQLExecIntrinsic = "sql.exec"

// IsSQL reports whether f is one of sngl:sql's statements.
func IsSQL(f *Func) bool {
	return f != nil && (f.Intrinsic == SQLQueryIntrinsic || f.Intrinsic == SQLExecIntrinsic)
}

// SQLEngine is the engine a call of sngl:sql names where it is written as a
// member, `sql.Engine.sqlite`, or "" where it is computed.
func SQLEngine(c *Call) string {
	if c == nil || !IsSQL(c.Func) {
		return ""
	}
	if id, ok := c.ArgFor("engine").(*Ident); ok && id.Member != "" {
		return id.Member
	}
	return ""
}
