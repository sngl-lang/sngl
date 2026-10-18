package fyne

import (
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// lookPropName is the primitive prop carrying the fynelayout.Look a node is
// shown in, which fyne.sngl's style.sngl builds.
const lookPropName = "look"

// fyneLayoutImportPath is the SNGL runtime package holding the box, the
// weighted layout and the window this platform builds.
const fyneLayoutImportPath = "duckfam.us/sngl/pkg/go/fynelayout"

// boxedLook reports whether a node's look asks for anything. A look the build
// folded to its zero leaves the widget unwrapped; any other is shown in a
// fynelayout.Box, created with the widget so a write of the look has
// something to set.
func boxedLook(props map[string]ir.Expr) bool {
	v, ok := props[lookPropName]
	return ok && !zeroLiteral(v)
}

func zeroLiteral(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Value != nil && !zeroLiteral(f.Value) {
				return false
			}
		}
		return true
	case *ir.Literal:
		if x.Value == "false" || x.Value == "" {
			return true
		}
		n, err := strconv.ParseFloat(x.Value, 64)
		return err == nil && n == 0
	}
	return false
}

func boxField(id string) string { return id + "__box" }

func fyneBox() *ir.Type { return ir.NativeGoPointerOf("fynelayout.Box") }

func (t *fyneTranslator) boxRef(id string) ir.Expr {
	name := boxField(id)
	if t.isLocalRef(id) {
		return &ir.Ident{Name: name, Type: fyneBox()}
	}
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(id), name, fyneBox())
}

func (t *fyneTranslator) createBox(id string, node ir.Expr) ir.Stmt {
	ctor := nativeCallAt((fyneNative{Path: fyneLayoutImportPath, Name: "NewBox"}).qualify(t.gc), fyneLayoutImportPath,
		[]ir.Expr{node}, fyneBox())
	if t.isLocalRef(id) {
		return &ir.LocalVar{Name: boxField(id), Init: ctor}
	}
	t.sinkField(boxField(id), (fyneNative{Path: fyneLayoutImportPath, Name: "*Box"}).qualify(t.gc))
	return &ir.Assign{Target: t.boxRef(id), Op: ast.AssignSet, Value: ctor}
}

// shown is what a container holds for a node: its box, or the widget itself.
// The Model field still holds the widget, so a setter reaches the widget and
// not its box.
func (t *fyneTranslator) shown(child ir.Expr, sp *fyneSpec, id string) ir.Expr {
	if sp != nil && sp.Boxed {
		return t.boxRef(id)
	}
	return child
}

// written is a folded look with only the fields it sets, which is what the
// literal says once the build has filled the rest with their zeros.
func written(v ir.Expr) ir.Expr {
	lit, ok := v.(*ir.StructLit)
	if !ok {
		return v
	}
	out := *lit
	out.Fields = nil
	for _, f := range lit.Fields {
		if f.Value != nil && !zeroLiteral(f.Value) {
			out.Fields = append(out.Fields, f)
		}
	}
	return &out
}
