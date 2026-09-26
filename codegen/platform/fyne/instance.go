package fyne

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// instanceReceiver is the identifier a component instance's ctor and methods
// dispatch through: the record, not the model.
const instanceReceiver = "c"

// emitComponentInstance writes the record one component's live instances are
// allocated as -- the struct, the ctor, one setter per prop it can absorb, and
// the teardown -- for a component the build could not inline away.
//
// It replaces `func (m *Model) render<Comp>(props...) fyne.CanvasObject`, whose
// widget temps and whose component `var`s were fields of the one Model. That is
// a singleton, and an instance is not: two rows of a list shared one `clicks`
// cell, and a recursion frame overwrote its parent's widget temp. The record is
// what makes a per-instance cell per-instance, which is the same job html's
// factory closure does for JS.
func emitComponentInstance(
	b *strings.Builder,
	cc *codegen.ComponentCtx,
	gc *golang.GoIRContext,
	nodeSpecs map[string]*fyneSpec,
	importSink func(string),
	canvasByID map[string]*canvasMeta,
	canvasByNode map[*ir.NodeInst]*canvasMeta,
	canvases []codegen.Canvas,
	failSink func(error),
) {
	comp := cc.Component
	typeName := golang.ComponentInstanceType(comp.Name)

	// Props are parameters of the ctor, and only the ctor names them: every read
	// in the body is of the `__prop_<name>` cell, which the ctor initializes.
	igc := gc.ForComponent(comp)
	igc.Ctx.StateReceiver = instanceReceiver
	igc.Ctx.OuterReceiver = instanceReceiver + "." + golang.InstanceModelField
	igc.Ctx.OuterNodes = golang.PageNodes(gc.Ctx.Pkg, comp)
	igc.MethodRecvType = typeName
	cgc := igc
	for _, p := range cc.Props {
		cgc = cgc.WithRenamedLocal(p.Name, golang.InstanceCtorParam(p.Name))
	}
	cgc.MethodRecvType = typeName

	var fields []irWidgetField
	sink := func(name, goType string) {
		fields = append(fields, irWidgetField{name: name, goType: goType})
	}

	tr := newFyneTranslator(igc, nodeSpecs, sink, importSink, failSink).
		withLocalRefs(comp.LocalRefs).withSlotRoot(comp.Body)
	tr.canvasByID, tr.canvasByNode = canvasByID, canvasByNode
	bodyStmts := codegen.WalkLowered(context.Background(), comp.Body, tr)

	var ctorBody strings.Builder
	for _, v := range comp.Vars {
		fmt.Fprintf(&ctorBody, "\t%s.%s = %s\n", instanceReceiver, v.Name, instanceVarInit(v, cgc))
	}
	for _, stmt := range bodyStmts {
		for _, line := range cgc.EvalStmt(stmt) {
			fmt.Fprintf(&ctorBody, "\t%s\n", line)
		}
	}
	rootLines, root := instanceRootExpr(tr, cgc)
	for _, line := range rootLines {
		fmt.Fprintf(&ctorBody, "\t%s\n", line)
	}
	fmt.Fprintf(&ctorBody, "\t%s.%s = %s\n", instanceReceiver, golang.ComponentRootField, root)

	// Methods after the body, because the walk is what discovers the widget
	// fields the struct declares -- and a setter's body carries the same
	// injected widget updates a handler's does, so it goes through a
	// translator too.
	var methods strings.Builder
	for _, fn := range comp.Funcs {
		if fn == nil || len(fn.Block) == 0 {
			continue
		}
		mtr := newFyneTranslator(igc, nodeSpecs, sink, importSink, failSink).withLocalRefs(fn.LocalRefs)
		mtr.canvasByNode = canvasByNode
		params := fn.Params
		if len(params) > 0 && params[0].Receiver {
			params = params[1:]
		}
		if fn.SlotRender {
			// A reactive slot's render func. Its body was written against the
			// reactivity pass's `parent` name, which the translator rewrites
			// to slotParentParam -- so the parameter has to be spelled the way
			// the Model's own slot funcs spell it (see emitIRSlotFunc), or the
			// body names a package instead of its argument.
			params = []*ir.Param{{Name: slotParentParam, Type: ir.NativeGoPointerOf("fyne.Container")}}
		}
		emitted := &ir.Func{
			Name:   instanceMethodName(fn),
			Params: params,
			Return: fn.Return,
			Block:  codegen.WalkLowered(context.Background(), fn.Block, mtr),
		}
		for _, line := range igc.EmitFuncDef(emitted) {
			methods.WriteString(line)
			methods.WriteByte('\n')
		}
		methods.WriteByte('\n')
	}

	// A drawing written in this component's body paints into fields of this
	// record, so its routine is a method here rather than on the Model. The
	// receiver is what makes it one: there is one context per instance, and a
	// Model method would address whichever was built last.
	for i := range canvases {
		cv := &canvases[i]
		if cv.Owner != comp {
			continue
		}
		dtr := newFyneTranslator(igc, nodeSpecs, sink, importSink, failSink)
		dtr.canvasByID, dtr.canvasByNode = canvasByID, canvasByNode
		drawn := &ir.Func{
			Name:   cv.Name,
			Params: []*ir.Param{{Name: "ctx", Type: canvasCtxType()}},
			Return: ir.TypVoid,
			Block:  codegen.WalkLowered(context.Background(), cv.Draw, dtr),
		}
		for _, line := range igc.EmitFuncDef(drawn) {
			methods.WriteString(line)
			methods.WriteByte('\n')
		}
		methods.WriteByte('\n')
	}

	fmt.Fprintf(b, "// %s is one live instance of the %s component.\ntype %s struct {\n", typeName, comp.Name, typeName)
	fmt.Fprintf(b, "\t%s fyne.CanvasObject\n", golang.ComponentRootField)
	fmt.Fprintf(b, "\t%s *%s\n", golang.InstanceModelField, codegen.ModelTypeName)
	for _, v := range comp.Vars {
		goType := instanceVarGoType(v)
		fmt.Fprintf(b, "\t%s %s\n", v.Name, goType)
		requireTypeImports(igc, goType)
		if v.Synthesized && ir.IsSlotVarName(v.Name) {
			fmt.Fprintf(b, "\t%s fyne.CanvasObject\n", codegen.SlotAnchorField(v.Name))
		}
	}
	seenField := map[string]bool{}
	for _, f := range fields {
		if seenField[f.name] {
			continue
		}
		seenField[f.name] = true
		fmt.Fprintf(b, "\t%s %s\n", f.name, f.goType)
		requireTypeImports(igc, f.goType)
	}
	b.WriteString("}\n\n")

	params := []string{golang.InstanceModelField + " *" + codegen.ModelTypeName}
	for _, p := range cc.Props {
		params = append(params, golang.InstanceCtorParam(p.Name)+" "+golang.IRTypeToGo(p.Type))
	}
	fmt.Fprintf(b, "func %s(%s) *%s {\n", golang.ComponentInstanceCtor(comp.Name), strings.Join(params, ", "), typeName)
	fmt.Fprintf(b, "\t%s := &%s{}\n", instanceReceiver, typeName)
	fmt.Fprintf(b, "\t%s.%s = %s\n", instanceReceiver, golang.InstanceModelField, golang.InstanceModelField)
	b.WriteString(ctorBody.String())
	fmt.Fprintf(b, "\treturn %s\n}\n\n", instanceReceiver)

	b.WriteString(methods.String())

	// Always present, and empty when the component holds nothing: the
	// destroy is emitted at every site a render stops describing a position,
	// so the shape of a record cannot vary with what it happens to hold.
	if !componentDeclaresFunc(comp, lower.TeardownFunc) {
		fmt.Fprintf(b, "func (%s *%s) %s() {}\n\n", instanceReceiver, typeName, golang.ComponentDestroyMethod)
	}
}

// instanceMethodName is the name one of a component's funcs carries as a
// method on its record. Two of them the lowering dispatches to by name from
// outside -- the prop setters and the teardown -- so those are spelled the way
// the intrinsic translator spells them; every other keeps the name it has.
func instanceMethodName(fn *ir.Func) string {
	if fn.Name == lower.TeardownFunc {
		return golang.ComponentDestroyMethod
	}
	if prop, ok := setterProp(fn.Name); ok {
		return golang.ComponentSetterMethod(prop)
	}
	return fn.Name
}

// setterProp reports the prop a `__set_<prop>` func writes.
func setterProp(name string) (string, bool) {
	if !strings.HasPrefix(name, setterPrefix) {
		return "", false
	}
	return strings.TrimPrefix(name, setterPrefix), true
}

// setterPrefix is ir.ComponentSetter's, read back rather than spelled again.
var setterPrefix = ir.ComponentSetter("")

func componentDeclaresFunc(comp *ir.Component, name string) bool {
	for _, f := range comp.Funcs {
		if f != nil && f.Name == name && len(f.Block) > 0 {
			return true
		}
	}
	return false
}

// instanceVarGoType is the Go type a component var takes as a record field --
// the same three answers the Model makes for its own state.
func instanceVarGoType(v *ir.Var) string {
	switch {
	case v.Synthesized && ir.IsSlotVarName(v.Name):
		return "[]fyne.CanvasObject"
	case v.Synthesized && v.Name == slotRootVar:
		return "*fyne.Container"
	}
	return golang.VarGoType(v)
}

func instanceVarInit(v *ir.Var, igc *golang.GoIRContext) string {
	switch {
	case v.Synthesized && ir.IsSlotVarName(v.Name):
		return "nil"
	case v.Synthesized && v.Name == slotRootVar:
		igc.RequireImport("fyne.io/fyne/v2/container")
		return "container.NewVBox()"
	}
	return golang.LowerVarInit(v, igc)
}

// instanceRootExpr is the widget the instance renders as: the container a
// reactive slot at the top of its body renders into, when there is one, and
// otherwise whatever the body left unattached. Wrapped when the body left
// several, because a parent holds one child per instance and an instance with
// two top-level nodes has no single widget to be.
func instanceRootExpr(tr *fyneTranslator, igc *golang.GoIRContext) (lines []string, root string) {
	if tr.renderedRoot != nil {
		for _, stmt := range tr.addTopsTo(tr.renderedRoot) {
			lines = append(lines, igc.EvalStmt(stmt)...)
		}
		return lines, igc.EvalExpr(tr.renderedRoot)
	}
	tops := tr.topLevel
	switch len(tops) {
	case 0:
		igc.RequireImport("fyne.io/fyne/v2/widget")
		return nil, `widget.NewLabel("")`
	case 1:
		return nil, igc.EvalExpr(topRef(tr, tops[0]))
	}
	igc.RequireImport("fyne.io/fyne/v2/container")
	parts := make([]string, len(tops))
	for i, ref := range tops {
		parts[i] = igc.EvalExpr(topRef(tr, ref))
	}
	return nil, "container.NewVBox(" + strings.Join(parts, ", ") + ")"
}

// slotRootVar is the container a reactive slot at the top of a body renders
// into.
const slotRootVar = "__root"

// isInstanceComponent reports whether a component's body belongs to a record
// rather than to the Model. What survives inlining and was met at a site the
// build could not flatten is exactly that -- see ir.Component.RuntimeInstance.
func isInstanceComponent(comp *ir.Component) bool {
	return comp != nil && comp.RuntimeInstance
}

// instanceOwnsFunc reports whether fn is a method of some instance record, so
// that the Model's own emitters skip it. Emitting one in both places declared
// the same method twice, on two receivers, over one component's state.
func instanceOwnsFunc(pkg *ir.Package, fn *ir.Func) bool {
	if pkg == nil || fn == nil {
		return false
	}
	for _, comp := range pkg.Components {
		if !isInstanceComponent(comp) {
			continue
		}
		if slices.Contains(comp.Funcs, fn) {
			return true
		}
	}
	return false
}
