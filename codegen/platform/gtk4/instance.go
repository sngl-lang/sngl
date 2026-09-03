package gtk4

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
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
// It replaces `func (m *Model) render<Comp>(props...) *C.GtkWidget`, whose
// widget fields and whose component `var`s were fields of the one Model. That
// is a singleton and an instance is not: two rows of a list shared one state
// cell, and a recursion frame overwrote its parent's widget field.
func emitComponentInstance(
	b *strings.Builder,
	cc *codegen.ComponentCtx,
	gc *golang.GoIRContext,
	pkg *ir.Package,
	reg *gir.TypeRegistry,
	shared *emitShared,
	wrapped bool,
) {
	comp := cc.Component
	typeName := golang.ComponentInstanceType(comp.Name)
	handleType := "*C.GtkWidget"
	if wrapped {
		handleType = gtk4rtHandleType
	}

	// Props are parameters of the ctor, so a read of one is a bare local.
	// passComponentProps has already rewritten every read inside the body to
	// the `__prop_<name>` cell; what still names the parameter is that cell's
	// own initializer.
	igc := gc.ForComponent(comp)
	igc.Ctx.StateReceiver = instanceReceiver
	for _, p := range cc.Props {
		igc = igc.WithLocal(p.Name)
	}
	igc.MethodRecvType = typeName

	var fields []widgetField
	sink := func(name, cType string) {
		fields = append(fields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}
	newTr := func(localRefs map[string]bool) *gtk4Translator {
		tr := newGtk4Translator(igc, sink).withPkg(pkg).withRegistry(reg).
			withShared(shared).withLocalRefs(localRefs).withWrapped(wrapped)
		maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
		return tr
	}

	tr := newTr(comp.LocalRefs)
	tr.collectTagComponents(comp.Body)
	bodyStmts := codegen.WalkLowered(context.Background(), comp.Body, tr)

	var ctorBody strings.Builder
	for _, v := range comp.Vars {
		fmt.Fprintf(&ctorBody, "\t%s.%s = %s\n", instanceReceiver, v.Name, instanceVarInit(v, igc, wrapped))
	}
	for _, stmt := range bodyStmts {
		for _, line := range igc.EvalStmt(stmt) {
			fmt.Fprintf(&ctorBody, "\t%s\n", line)
		}
	}
	ctorBody.WriteString(instanceRootBinding(tr, igc, wrapped))

	// Methods after the body, because the walk is what discovers the widget
	// fields the struct declares -- and a setter's body carries the same
	// injected widget updates a handler's does, so it goes through a
	// translator too.
	var methods strings.Builder
	for _, fn := range comp.Funcs {
		if fn == nil || len(fn.Block) == 0 {
			continue
		}
		mtr := newTr(fn.LocalRefs)
		params := fn.Params
		if len(params) > 0 && params[0].Receiver {
			params = params[1:]
		}
		if fn.Synthesized {
			// A reactive slot's render func, whose body was written against
			// the reactivity pass's `parent` name and is typed the way the
			// Model's own slot funcs type it (see emitIRSlotFunc).
			params = []*ir.Param{{Name: "parent", Type: slotParentType(wrapped)}}
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

	fmt.Fprintf(b, "// %s is one live instance of the %s component.\ntype %s struct {\n", typeName, comp.Name, typeName)
	fmt.Fprintf(b, "\t%s %s\n", golang.ComponentRootField, handleType)
	for _, v := range comp.Vars {
		fmt.Fprintf(b, "\t%s %s\n", v.Name, instanceVarGoType(v, wrapped))
	}
	for _, f := range fields {
		fmt.Fprintf(b, "\t%s %s\n", f.name, f.goType)
	}
	b.WriteString("}\n\n")

	params := make([]string, 0, len(cc.Props))
	for _, p := range cc.Props {
		params = append(params, p.Name+" "+golang.IRTypeToGo(p.Type))
	}
	fmt.Fprintf(b, "func %s(%s) *%s {\n", golang.ComponentInstanceCtor(comp.Name), strings.Join(params, ", "), typeName)
	fmt.Fprintf(b, "\t%s := &%s{}\n", instanceReceiver, typeName)
	b.WriteString(ctorBody.String())
	fmt.Fprintf(b, "\treturn %s\n}\n\n", instanceReceiver)

	b.WriteString(methods.String())

	// Always present, and empty when the component holds nothing: the destroy
	// is emitted at every site a render stops describing a position, so the
	// shape of a record cannot vary with what it happens to hold.
	if !componentDeclaresFunc(comp, lower.TeardownFunc) {
		fmt.Fprintf(b, "func (%s *%s) %s() {}\n\n", instanceReceiver, typeName, golang.ComponentDestroyMethod)
	}
}

// instanceRootBinding assigns the widget the instance renders as: whatever the
// body left unattached, boxed when it left several, because a parent holds one
// child per instance.
func instanceRootBinding(tr *gtk4Translator, igc *golang.GoIRContext, wrapped bool) string {
	root := instanceReceiver + "." + golang.ComponentRootField
	nodeRef := func(id string) ir.Expr {
		return tr.qualifyNodeExpr(&ir.Ident{Name: id, IsElementRef: true, Synthesized: true})
	}
	tops := tr.topLevel
	switch {
	case len(tops) == 0 && wrapped:
		return fmt.Sprintf("\t%s = gtk4rt.LabelNew(\"\")\n", root)
	case len(tops) == 0:
		return fmt.Sprintf("\t%s = C.gtk_label_new(nil)\n", root)
	case len(tops) == 1 && wrapped:
		return fmt.Sprintf("\t%s = %s\n", root, igc.EvalExpr(nodeRef(tops[0])))
	case len(tops) == 1:
		widget := &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: nodeRef(tops[0])}
		return fmt.Sprintf("\t%s = %s\n", root, igc.EvalExpr(widget))
	case wrapped:
		var b strings.Builder
		b.WriteString("\t__box := gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)\n")
		for _, id := range tops {
			fmt.Fprintf(&b, "\tgtk4rt.BoxAppend(__box, %s)\n", igc.EvalExpr(nodeRef(id)))
		}
		fmt.Fprintf(&b, "\t%s = __box\n", root)
		return b.String()
	}
	var b strings.Builder
	boxInit := &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_box_new"),
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: "6"}},
		},
	}}
	fmt.Fprintf(&b, "\t__box := %s\n", igc.EvalExpr(boxInit))
	boxRef := &ir.Ident{Name: "__box"}
	for _, id := range tops {
		appendCall := &ir.Call{
			Type:     ir.TypVoid,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("gtk_box_append"),
			Args: []ir.CallArg{
				{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: boxRef}},
				{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: nodeRef(id)}},
			},
		}
		for _, line := range igc.EvalStmt(&ir.CallStmt{Call: appendCall}) {
			fmt.Fprintf(&b, "\t%s\n", line)
		}
	}
	fmt.Fprintf(&b, "\t%s = %s\n", root, igc.EvalExpr(&ir.Conversion{
		Type: ir.NativePointerOf("GtkWidget"), Operand: boxRef,
	}))
	return b.String()
}

// instanceMethodName is the name one of a component's funcs carries as a
// method on its record. Two of them the lowering dispatches to by name from
// outside -- the prop setters and the teardown -- so those are spelled the way
// the intrinsic translator spells them; every other keeps the name it has.
func instanceMethodName(fn *ir.Func) string {
	if fn.Name == lower.TeardownFunc {
		return golang.ComponentDestroyMethod
	}
	if after, ok := strings.CutPrefix(fn.Name, setterPrefix); ok {
		return golang.ComponentSetterMethod(after)
	}
	return fn.Name
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
func instanceVarGoType(v *ir.Var, wrapped bool) string {
	switch {
	case v.Synthesized && ir.IsSlotVarName(v.Name):
		if wrapped {
			return "[]" + gtk4rtHandleType
		}
		return "[]*C.GtkWidget"
	case v.Synthesized && v.Name == instanceRootVar:
		if wrapped {
			return gtk4rtHandleType
		}
		return "*C.GtkBox"
	}
	return golang.VarGoType(v)
}

func instanceVarInit(v *ir.Var, igc *golang.GoIRContext, _ bool) string {
	// A widget handle and a list of them are both nil to start with, in either
	// mode: the ctor body is what fills them, and a cgo call is not valid in a
	// struct initializer anyway -- the same reason the Model's own __root is
	// bound inside buildWidgetTree.
	if v.Synthesized && (ir.IsSlotVarName(v.Name) || v.Name == instanceRootVar) {
		return "nil"
	}
	return irVarInit(v, igc)
}

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

// instanceRootVar is the container a reactive slot in a component body renders
// into.
const instanceRootVar = "__root"
