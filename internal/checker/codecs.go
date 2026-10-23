package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkCodecCall refuses a json.decode, json.encode or request whose type has
// no form on the wire: a unit, a function, a struct field of one, a map keyed
// by anything but a string -- or a type parameter, whose instantiation the
// call cannot know. Every target's encoder reads the type at the call, so this
// is where a type it cannot write is reported, with the part that has no
// form named.
func (c *checker) checkCodecCall(x *ast.CallExpr, out ir.Expr) {
	call, ok := out.(*ir.Call)
	if !ok {
		return
	}
	t, format, ok := ir.CodecCall(call)
	if !ok || t == nil {
		return
	}
	problem := ir.CodecProblem(t, format)
	if problem == "" {
		return
	}
	what := "json.encode cannot write"
	switch {
	case call.Func.Intrinsic == ir.JSONDecodeIntrinsic:
		what = "json.decode cannot read"
	case format == ir.CodecQuery:
		what = "http." + call.Func.Name + " cannot send a query of"
	}
	if c.codecReported == nil {
		c.codecReported = map[ast.Pos]bool{}
	}
	if c.codecReported[x.Pos] {
		return
	}
	c.codecReported[x.Pos] = true
	c.error(x.Pos, "%s %s", what, problem)
}
