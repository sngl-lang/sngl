package kotlin

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.DeclareLangImplements(langKt, ir.SQLQueryIntrinsic, ir.SQLExecIntrinsic)
}

// UsesSQL reports whether pkg runs a statement of sngl:sql anywhere, tests
// included: what makes android write SnglSql.
func UsesSQL(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && ir.IsSQL(c.Func) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// sqlCall renders a call of sql.query or sql.exec over SnglSql, a suspend
// function made where its caller waits for it.
func (kc *KtIRContext) sqlCall(n *ir.Call) (string, bool) {
	if n == nil || !ir.IsSQL(n.Func) {
		return "", false
	}
	arg := func(name, none string) string {
		if a := n.ArgFor(name); a != nil {
			return kc.EvalExpr(a)
		}
		return none
	}
	engine := strconv.Quote(ir.SQLEngine(n))
	if ir.SQLEngine(n) == "" {
		engine = arg("engine", `""`) + ".toString()"
	}
	life := "0"
	if l := n.ArgFor("life"); l != nil {
		life = kc.EvalExpr(&ir.Select{Operand: l, Field: "_id", Type: ir.TypInt})
	}
	str := ir.TypString
	msg := codegen.HostValue("__s.message", str)
	failed := fmt.Sprintf("if (__s.cancelled) %s else if (__s.code.isNotEmpty()) %s else %s",
		kc.EvalExpr(codegen.ResultFailed(n.Type, "cancelled", msg)),
		kc.EvalExpr(codegen.ResultFailedCode(n.Type, "server", msg, codegen.HostValue("__s.code", str))),
		kc.EvalExpr(codegen.ResultFailed(n.Type, "transport", msg)))
	src, text, args := arg("source", `""`), arg("text", `""`), arg("args", `"[]"`)
	if n.Func.Intrinsic == ir.SQLQueryIntrinsic {
		ok := kc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__s.rows", str)))
		return fmt.Sprintf("run { val __s = SnglSql.query(%s, %s, %s, %s, %s, %s, %s); if (__s.failed) (%s) else %s }",
			engine, src, text, args, arg("columns", "emptyList()"), arg("kinds", "emptyList()"), life, failed, ok), true
	}
	done := codegen.StructOf(ir.MemberPayload(n.Type, "ok"), map[string]ir.Expr{
		"rows":   codegen.HostValue("__s.changed", ir.TypInt64),
		"lastId": codegen.HostValue("__s.lastId", ir.TypInt64),
	})
	return fmt.Sprintf("run { val __s = SnglSql.exec(%s, %s, %s, %s, %s); if (__s.failed) (%s) else %s }",
		engine, src, text, args, life, failed, kc.EvalExpr(codegen.ResultOK(n.Type, done))), true
}
