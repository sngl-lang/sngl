package golang

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

const sqlRuntime = "duckfam.us/sngl/pkg/go/snglsql"

// sqlEngines are the engines snglsql registers a driver for, each in its own
// package, so a program links only the drivers it queries.
var sqlEngines = []string{"sqlite", "postgresql", "mysql"}

func init() {
	codegen.DeclareLangImplements(langGo, ir.SQLQueryIntrinsic, ir.SQLExecIntrinsic)
}

// sqlCall renders a call of sql.query or sql.exec: a call into snglsql, and
// the async.Result built from what it handed back.
func (gc *GoIRContext) sqlCall(n *ir.Call) (string, bool) {
	if n == nil || !ir.IsSQL(n.Func) {
		return "", false
	}
	gc.RequireImport(sqlRuntime)
	engine := ir.SQLEngine(n)
	arg := func(name, none string) string {
		if a := n.ArgFor(name); a != nil {
			return gc.EvalExpr(a)
		}
		return none
	}
	// The engine's own package is named, rather than imported blank, so the
	// import is a use wherever the call's imports are gathered.
	eng := "sngl" + engine + ".Engine"
	var keep strings.Builder
	if engine != "" {
		gc.RequireImportAs(sqlRuntime+"/"+engine, "sngl"+engine)
	} else {
		eng = "string(" + arg("engine", `""`) + ")"
		for _, e := range sqlEngines {
			gc.RequireImportAs(sqlRuntime+"/"+e, "sngl"+e)
			keep.WriteString("_ = sngl" + e + ".Engine; ")
		}
	}
	ctx := "context.Background()"
	if life := n.ArgFor("life"); life != nil {
		gc.RequireImport(asyncRuntime)
		ctx = "snglasync.Context(" + gc.EvalExpr(&ir.Select{Operand: life, Field: "_id", Type: ir.TypInt}) + ")"
	} else {
		gc.RequireImport("context")
	}
	str := ir.TypString
	msg := codegen.HostValue("__snglF.Message", str)
	failed := fmt.Sprintf("if __snglF != nil { if __snglF.Cancelled { return %s }; if __snglF.Code != \"\" { return %s }; return %s }; ",
		gc.EvalExpr(codegen.ResultFailed(n.Type, "cancelled", msg)),
		gc.EvalExpr(codegen.ResultFailedCode(n.Type, "server", msg, codegen.HostValue("__snglF.Code", str))),
		gc.EvalExpr(codegen.ResultFailed(n.Type, "transport", msg)))
	var b strings.Builder
	fmt.Fprintf(&b, "func() %s { %s", typeToGo(n.Type, gc.site()), keep.String())
	src, text, args := arg("source", `""`), arg("text", `""`), arg("args", `"[]"`)
	if n.Func.Intrinsic == ir.SQLQueryIntrinsic {
		fmt.Fprintf(&b, "__snglR, __snglF := snglsql.Query(%s, %s, %s, %s, %s, %s, %s, %s); ", ctx, eng, src, text, args, arg("argKinds", "nil"), arg("columns", "nil"), arg("kinds", "nil"))
		b.WriteString(failed)
		fmt.Fprintf(&b, "return %s }()", gc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__snglR", str))))
		return b.String(), true
	}
	fmt.Fprintf(&b, "__snglN, __snglL, __snglF := snglsql.Exec(%s, %s, %s, %s, %s, %s); ", ctx, eng, src, text, args, arg("argKinds", "nil"))
	b.WriteString(failed)
	done := codegen.StructOf(ir.MemberPayload(n.Type, "ok"), map[string]ir.Expr{
		"rows":   codegen.HostValue("__snglN", ir.TypInt64),
		"lastId": codegen.HostValue("__snglL", ir.TypInt64),
	})
	fmt.Fprintf(&b, "return %s }()", gc.EvalExpr(codegen.ResultOK(n.Type, done)))
	return b.String(), true
}
