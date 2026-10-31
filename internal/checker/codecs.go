package checker

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkCodecCall refuses a json.decode, json.encode or request whose type has
// no form on the wire: a unit, a function, a struct field of one, a map keyed
// by anything but a string. Every target's encoder reads the type at the
// call, so this is where a type it cannot write is reported, with the part
// that has no form named. A type parameter is checked where a call binds it
// (checkGenericCodecs).
func (c *checker) checkCodecCall(x *ast.CallExpr, out ir.Expr) {
	call, ok := out.(*ir.Call)
	if !ok {
		return
	}
	t, format, ok := ir.CodecCall(call)
	if !ok || t == nil {
		return
	}
	problem := ir.CodecProblemOpen(t, format)
	if problem == "" {
		return
	}
	c.reportCodec(x.Pos, "%s %s", codecVerb(call.Func.Intrinsic, format), problem)
}

func codecVerb(intrinsic string, format ir.CodecFormat) string {
	switch {
	case intrinsic == ir.JSONDecodeIntrinsic:
		return "json.decode cannot read"
	case format == ir.CodecQuery:
		return intrinsic + " cannot send a query of"
	}
	return "json.encode cannot write"
}

func (c *checker) reportCodec(pos ast.Pos, format string, args ...any) {
	if c.codecReported == nil {
		c.codecReported = map[ast.Pos]bool{}
	}
	if c.codecReported[pos] {
		return
	}
	c.codecReported[pos] = true
	c.error(pos, format, args...)
}

// checkGenericCodecs refuses a call of a generic function, or a node of a
// generic component, binding a type parameter its body reads or writes on the
// wire to a type with no form there. The body was checked with its parameters
// open, so this is the one place the type is known.
func (c *checker) checkGenericCodecs() {
	var reach ir.CodecReach
	_ = ir.Walk(c.pkg, func(n ir.Node) error {
		callee, b := ir.GenericCallee(n)
		if callee == nil {
			return nil
		}
		var pos ast.Pos
		var name string
		switch x := n.(type) {
		case *ir.Call:
			if x.AST == nil {
				return nil
			}
			pos, name = x.AST.Pos, x.Func.Name
		case *ir.NodeInst:
			pos, name = ir.NodePos(x), x.Component.Name
		}
		for _, u := range reach.GenericCodecs(callee) {
			t := u.Type.Substitute(b)
			if t.HasTypeParam() {
				continue
			}
			if problem := ir.CodecProblem(t, u.Format); problem != "" {
				c.reportCodec(pos, "%s %s, in %s called with %s", codecVerb(u.Intrinsic, u.Format), problem, name, bindingText(callee, b))
				return nil
			}
		}
		return nil
	})
}

// bindingText spells what a call binds callee's type parameters to, in the
// order it declares them: `T = Handler`.
func bindingText(callee any, b map[string]*ir.Type) string {
	var params []ir.TypeParam
	switch d := callee.(type) {
	case *ir.Func:
		params = append(slices.Clone(d.RecvTypeParams), d.TypeParams...)
	case *ir.Component:
		params = d.TypeParams
	}
	var parts []string
	for _, tp := range params {
		if t, ok := b[tp.Name]; ok {
			parts = append(parts, tp.Name+" = "+t.String())
		}
	}
	return strings.Join(parts, ", ")
}
