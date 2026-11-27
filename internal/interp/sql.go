package interp

import (
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/snglasync"
	"duckfam.us/sngl/pkg/go/snglsql"
)

// evalSQLCall answers sql.query and sql.exec through snglsql, which the
// interpreter links every engine's driver into (sql_drivers.go). handled is
// false for any other call.
func (env *Env) evalSQLCall(call *ir.Call) (any, bool, error) {
	if call == nil || !ir.IsSQL(call.Func) {
		return nil, false, nil
	}
	values, err := env.evalCallArgs(call.Args)
	if err != nil {
		return nil, true, err
	}
	args, err := env.boundArgValues(call.Func, call.Args, values)
	if err != nil {
		return nil, true, err
	}
	arg := func(name string) any {
		for i, p := range call.Func.Params {
			if p.Name == name && i < len(args) {
				return args[i]
			}
		}
		return nil
	}
	str := func(name string) string { s, _ := arg(name).(string); return s }
	strs := func(name string) []string {
		var out []string
		l, _ := arg(name).([]any)
		for _, v := range l {
			s, _ := v.(string)
			out = append(out, s)
		}
		return out
	}
	rt := env.boundType(call.Type)
	ctx := snglasync.Context(heldID(arg("life")))
	engine := str("engine")
	if call.Func.Intrinsic == ir.SQLQueryIntrinsic {
		rows, f := snglsql.Query(ctx, engine, str("source"), str("text"), str("args"), strs("argKinds"), strs("columns"), strs("kinds"))
		if f != nil {
			return sqlFailed(rt, f), true, nil
		}
		return EnumValue{Type: rt, Member: "ok", Payload: rows}, true, nil
	}
	n, last, f := snglsql.Exec(ctx, engine, str("source"), str("text"), str("args"), strs("argKinds"))
	if f != nil {
		return sqlFailed(rt, f), true, nil
	}
	done := newStructOf(ir.MemberPayload(rt, "ok"), map[string]any{"rows": int(n), "lastId": int(last)})
	return EnumValue{Type: rt, Member: "ok", Payload: done}, true, nil
}

// sqlFailed is the failure f is, as each language's runtime answers one:
// `cancelled`, `server` coded where the database said why, or `transport`.
func sqlFailed(rt *ir.Type, f *snglsql.Failure) EnumValue {
	switch {
	case f.Cancelled:
		return resultFailed(rt, "cancelled", f.Message, "")
	case f.Code != "":
		return resultFailed(rt, "server", f.Message, f.Code)
	}
	return resultFailed(rt, "transport", f.Message, "")
}
