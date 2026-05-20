package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// synthRecvTypeExpr returns an ast.TypeExpr referring to `name`, with type
// parameters instantiated as themselves. Used to produce the synthetic `this`
// param's declared type when desugaring nested methods.
func synthRecvTypeExpr(pos ast.Pos, name string, typeParams []string) ast.TypeExpr {
	nt := &ast.NamedType{Pos: pos, Name: name}
	if len(typeParams) > 0 {
		args := make([]ast.TypeExpr, len(typeParams))
		for i, tp := range typeParams {
			args[i] = &ast.NamedType{Pos: pos, Name: tp}
		}
		nt.TypeArgs = args
	}
	return nt
}

// registerNestedMethods desugars each nested *ast.FuncDef into a top-level
// method form (prepends a synthetic `this` param, sets the receiver type name)
// and registers via the existing symtab.RegisterMethod path.
//
// recvName is the type name (struct/enum/component).
// typeParams is the receiver type's type parameters (empty for enum/component).
func (c *checker) registerNestedMethods(recvName string, typeParams []string, nested []*ast.FuncDef) []*ir.Func {
	out := make([]*ir.Func, 0, len(nested))
	for _, n := range nested {
		thisType := synthRecvTypeExpr(n.Pos, recvName, typeParams)
		thisParam := ast.Param{
			Pos:  n.Pos,
			Name: "this",
			Type: thisType,
		}
		newParams := ast.ParamList{
			Pos:         n.Params.Pos,
			IsMultiline: n.Params.IsMultiline,
			Params:      append([]ast.Param{thisParam}, n.Params.Params...),
		}
		synthetic := &ast.FuncDef{
			Pos:            n.Pos,
			Name:           recvName + "." + n.Name,
			TypeParams:     n.TypeParams,
			RecvTypeParams: append([]string(nil), typeParams...),
			Params:         newParams,
			ReturnType:     n.ReturnType,
			Body:           n.Body,
			Block:          n.Block,
		}
		fn := c.buildFunc(synthetic)
		c.pkg.Funcs = append(c.pkg.Funcs, fn)
		c.symtab.RegisterMethod(fn.Receiver, fn)
		out = append(out, fn)
	}
	return out
}
