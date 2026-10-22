package gtk4

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
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
	canvases []codegen.Canvas,
	invokerSink func(gtkEventInvoker),
	modelFieldSink func(widgetField),
	indexed map[string]bool,
	presentIDs map[string]bool,
	presenceSink func(id, field string),
) {
	comp := cc.Component
	typeName := golang.ComponentInstanceType(comp.Name)
	records := golang.InstanceRecordTypes(gc.Ctx.Pkg)
	handleType := "*C.GtkWidget"
	if wrapped {
		handleType = gtk4rtHandleType
	}

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

	var fields []widgetField
	sink := func(name, cType string) {
		fields = append(fields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}
	modelSink := func(name, cType string) {
		modelFieldSink(widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}
	newTr := func(localRefs map[string]bool) *gtk4Translator {
		tr := newGtk4Translator(igc, sink).withPkg(pkg).withRegistry(reg).
			withShared(shared).withLocalRefs(localRefs).withWrapped(wrapped)
		maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
		// The whole component, not just its body: a reactive slot's renderer
		// is a func of this component and creates nodes of its own, and a tag
		// this map does not carry resolves to no widget and stops the build.
		// collectNodeCTypes answers over every component's funcs already, for
		// the same reason.
		tr.collectTagComponents(comp.Stmts)
		for _, fn := range comp.Funcs {
			if fn != nil {
				tr.collectTagComponents(fn.Block)
			}
		}
		return tr
	}

	// A widget the program named is one a test drives, and a record is built
	// and destroyed while the program runs, so the record hands its widget to
	// the Model as it is built: the invoker reaches whichever instance built
	// it last, which is the one on screen.
	var invokers []gtkEventInvoker
	tr := newTr(comp.LocalRefs).withInstanceCopies(indexed, modelSink).inRecord().withSlotRoot(instanceRootVar, comp.Stmts).
		withInvokerSink(func(inv gtkEventInvoker) { invokers = append(invokers, inv) })
	bodyStmts := codegen.WalkLowered(context.Background(), comp.Stmts, tr)

	var ctorBody strings.Builder
	for _, v := range comp.Vars {
		fmt.Fprintf(&ctorBody, "\t%s.%s = %s\n", instanceReceiver, v.Name, instanceVarInit(v, cgc, wrapped))
	}
	for _, stmt := range bodyStmts {
		for _, line := range cgc.EvalStmt(stmt) {
			fmt.Fprintf(&ctorBody, "\t%s\n", line)
		}
	}
	ctorBody.WriteString(instanceRootBinding(tr, cgc, wrapped))
	exposed := map[string]bool{}
	var listed []string
	for _, inv := range invokers {
		var field *widgetField
		for i := range fields {
			if fields[i].name == inv.FieldName {
				field = &fields[i]
				break
			}
		}
		if field == nil || invokerSink == nil {
			continue
		}
		named := namedWidgetField(inv.FieldName)
		// An id a test indexes is also every instance's copy, each under the
		// key its __copy write gave it (listCopy), and it leaves the map when
		// its instance is destroyed.
		list := indexed[inv.IDLabel]
		if !exposed[named] {
			exposed[named] = true
			model := instanceReceiver + "." + golang.InstanceModelField + "." + named
			fmt.Fprintf(&ctorBody, "\t%s = %s.%s\n", model, instanceReceiver, inv.FieldName)
			modelFieldSink(widgetField{name: named, goType: field.goType})
			if list {
				modelFieldSink(widgetField{name: copiesField(inv.FieldName), goType: copyMapType(field.goType)})
				listed = append(listed, inv.FieldName)
			}
		}
		inv.FieldName, inv.List = named, list
		invokerSink(inv)
	}
	// An id a test only null-tests one copy of is kept by key too, with no
	// invoker to have exposed it.
	// keepCopies keeps by key, in the Model's map, the copies of every
	// widget a test reaches one copy of that nothing above listed.
	keepCopies := func() {
		for _, f := range fields {
			if indexed[f.name] && !slices.Contains(listed, f.name) && !records[f.goType] {
				modelFieldSink(widgetField{name: copiesField(f.name), goType: copyMapType(f.goType)})
				listed = append(listed, f.name)
			}
		}
	}
	keepCopies()
	// A widget a test null-tests is handed over too, and taken back when the
	// instance goes, so the Model holds it exactly while it is rendered. One a
	// slot of the record builds is handed over by that render
	// (withRecordPresent).
	slotBuilt := codegen.SlotCreatedIDs(comp, presentIDs)
	var release strings.Builder
	for _, id := range slices.Sorted(maps.Keys(presentIDs)) {
		if slotBuilt[id] {
			named := namedWidgetField(id)
			held := instanceReceiver + "." + golang.InstanceModelField + "." + named
			fmt.Fprintf(&release, "\tif %s == %s.%s {\n\t\t%s = nil\n\t}\n", held, instanceReceiver, id, held)
			presenceSink(id, named)
			continue
		}
		widget := id
		for _, inv := range invokers {
			if inv.IDLabel == id {
				widget = inv.FieldName
			}
		}
		i := slices.IndexFunc(fields, func(f widgetField) bool { return f.name == widget })
		if i < 0 || presenceSink == nil {
			continue
		}
		named := "__named_" + widget
		if !exposed[named] {
			exposed[named] = true
			fmt.Fprintf(&ctorBody, "\t%s.%s.%s = %s.%s\n", instanceReceiver, golang.InstanceModelField, named, instanceReceiver, widget)
			modelFieldSink(widgetField{name: named, goType: fields[i].goType})
		}
		held := instanceReceiver + "." + golang.InstanceModelField + "." + named
		fmt.Fprintf(&release, "\tif %s == %s.%s {\n\t\t%s = nil\n\t}\n", held, instanceReceiver, widget, held)
		presenceSink(id, named)
	}
	fmt.Fprintf(&ctorBody, "\t%s\n", rootRefLine("g_object_ref_sink", "Retain", wrapped))

	// Methods after the body, because the walk is what discovers the widget
	// fields the struct declares -- and a setter's body carries the same
	// injected widget updates a handler's does, so it goes through a
	// translator too.
	var methods strings.Builder
	for _, fn := range comp.Funcs {
		if fn == nil || len(fn.Block) == 0 {
			continue
		}
		mtr := newTr(fn.Lowered.LocalRefs).withInstanceCopies(indexed, modelSink).inRecord()
		params := fn.Params
		if len(params) > 0 && params[0].Receiver {
			params = params[1:]
		}
		if fn.Lowered.SlotRender {
			mtr.withRecordPresent(presentIDs, fn.Block)
			// A reactive slot's render func, whose body was written against
			// the reactivity pass's `parent` name and is typed the way the
			// Model's own slot funcs type it (see emitIRSlotFunc).
			params = []*ir.Param{{Name: "parent", Type: slotParentType(wrapped)}}
		}
		block := fn.Block
		if fn.Lowered.Tag != "" {
			// A handler a widget's signal calls, promoted onto the record: the
			// trampoline passes it nothing, so the signal's instance is the
			// node it is attached to, as emitIRPromotedHandler reads it for
			// one on the Model.
			nodeID := strings.TrimSuffix(fn.Name, "_"+fn.Lowered.Event+"_handler")
			if mtr.signalFor(nodeID, fn.Lowered.Event) != "" {
				block = selfIsNode(block, fn.Params, &ir.Ident{Name: nodeID, IsElementRef: true})
				params = nil
			}
		}
		block = readWidgets(block, mtr.widgetReader())
		emitted := &ir.Func{
			Name:   instanceMethodName(fn),
			Params: params,
			Return: fn.Return,
			Block:  codegen.WalkLowered(context.Background(), block, mtr),
		}
		for _, line := range igc.EmitFuncDef(emitted) {
			methods.WriteString(line)
			methods.WriteByte('\n')
		}
		methods.WriteByte('\n')
	}

	// A drawing written in this component's body paints into fields of this
	// record -- the drawing area and the cairo context it is handed -- so its
	// routine is a method here rather than on the Model. There is one per
	// instance, and a Model method would address whichever was built last.
	for i := range canvases {
		cv := &canvases[i]
		if cv.Owner != comp {
			continue
		}
		dtr := newTr(nil)
		drawn := &ir.Func{
			Name:   cv.Name,
			Params: []*ir.Param{{Name: "ctx", Type: ir.NativePointerOf("cairo_t")}},
			Return: ir.TypVoid,
			Block:  codegen.WalkLowered(context.Background(), cv.Draw, dtr),
		}
		for _, line := range igc.EmitFuncDef(drawn) {
			methods.WriteString(line)
			methods.WriteByte('\n')
		}
		methods.WriteByte('\n')
	}

	// A widget a render slot of the record builds -- a copy of a loop in it --
	// is found only by walking that slot's method, above: its copies are kept
	// in the Model's map as the body's are, under the key the slot gives each.
	keepCopies()

	fmt.Fprintf(b, "// %s is one live instance of the %s component.\ntype %s struct {\n", typeName, comp.Name, typeName)
	fmt.Fprintf(b, "\t%s %s\n", golang.ComponentRootField, handleType)
	fmt.Fprintf(b, "\t%s *%s\n", golang.InstanceModelField, codegen.ModelTypeName)
	for _, v := range comp.Vars {
		fmt.Fprintf(b, "\t%s %s\n", v.Name, instanceVarGoType(v, wrapped))
		if isSlotVar(v) {
			fmt.Fprintf(b, "\t%s %s\n", codegen.SlotAnchorField(v.Name), handleType)
		}
	}
	seenField := map[string]bool{}
	for _, f := range fields {
		if seenField[f.name] {
			continue
		}
		seenField[f.name] = true
		fmt.Fprintf(b, "\t%s %s\n", f.name, f.goType)
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

	// A render slot removes every entry before appending what it keeps, and a
	// removed widget whose only reference was its parent is freed -- so a
	// reused instance's root would be appended after it was gone.
	root := instanceReceiver + "." + golang.ComponentRootField
	fmt.Fprintf(b, "func (%s *%s) %s() {\n", instanceReceiver, typeName, golang.ComponentDestroyMethod)
	if comp.KeyedCopy {
		fmt.Fprintf(b, "\t%s.%s.%s(%s)\n", instanceReceiver, golang.InstanceModelField, unlistInstance, root)
	}
	b.WriteString(golang.InstanceRelease(igc.InstanceReads, comp, instanceReceiver))
	if len(codegen.InstanceRootIDs(comp)) > 0 {
		fmt.Fprintf(b, "\tfor _, w := range %s.%s {\n\t\t%s.%s.%s(w)\n\t}\n", instanceReceiver, rootedField, instanceReceiver, golang.InstanceModelField, unlistInstance)
	}
	// A destroyed instance's widgets leave the Model's maps of them, so a
	// map holds the copies on screen.
	for _, w := range listed {
		list := instanceReceiver + "." + golang.InstanceModelField + "." + copiesField(w)
		fmt.Fprintf(b, "\tfor k, w := range %s {\n\t\tif w == %s.%s {\n\t\t\tdelete(%s, k)\n\t\t}\n\t}\n",
			list, instanceReceiver, w, list)
	}
	if componentDeclaresFunc(comp, lower.TeardownFunc) {
		fmt.Fprintf(b, "\t%s.%s()\n", instanceReceiver, instanceTeardownMethod)
	}
	b.WriteString(release.String())
	fmt.Fprintf(b, "\tif %s != nil {\n\t\t%s\n\t\t%s = nil\n\t}\n", root, rootRefLine("g_object_unref", "Release", wrapped), root)
	for _, v := range comp.Vars {
		if !isSlotVar(v) {
			continue
		}
		anchor := instanceReceiver + "." + codegen.SlotAnchorField(v.Name)
		release := "gtk4rt.Release(" + anchor + ")"
		if !wrapped {
			release = "C.g_object_unref(C.gpointer(unsafe.Pointer(" + anchor + ")))"
		}
		fmt.Fprintf(b, "\tif %s != nil {\n\t\t%s\n\t}\n", anchor, release)
	}
	b.WriteString("}\n\n")
}

func isSlotVar(v *ir.Var) bool { return v.Synthesized && ir.IsSlotVarName(v.Name) }

// instanceTeardownMethod is what the lowered teardown is emitted as, so that
// Destroy can run it and then release the root.
const instanceTeardownMethod = "teardown"

func rootRefLine(cFunc, rtFunc string, wrapped bool) string {
	root := instanceReceiver + "." + golang.ComponentRootField
	if wrapped {
		return fmt.Sprintf("gtk4rt.%s(%s)", rtFunc, root)
	}
	return fmt.Sprintf("C.%s(C.gpointer(unsafe.Pointer(%s)))", cFunc, root)
}

// instanceRootBinding assigns the widget the instance renders as: the box its
// reactive slots render into when the body has one, holding the body's own
// widgets in written order, and otherwise whatever the body left unattached,
// boxed when it left several, because a parent holds one child per instance.
func instanceRootBinding(tr *gtk4Translator, igc *golang.GoIRContext, wrapped bool) string {
	root := instanceReceiver + "." + golang.ComponentRootField
	nodeRef := func(id string) ir.Expr {
		return tr.qualifyNodeExpr(&ir.Ident{Name: id, IsElementRef: true, Synthesized: true})
	}
	if tr.renderedRoot != nil {
		var b strings.Builder
		for _, stmt := range tr.addTopsTo(context.Background(), tr.renderedRoot) {
			for _, line := range igc.EvalStmt(stmt) {
				fmt.Fprintf(&b, "\t%s\n", line)
			}
		}
		box := tr.qualifyNodeExpr(tr.renderedRoot)
		if !wrapped {
			box = &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: box}
		}
		fmt.Fprintf(&b, "\t%s = %s\n", root, igc.EvalExpr(box))
		return b.String()
	}
	tops := tr.topLevel
	switch {
	case len(tops) == 0 && wrapped:
		return fmt.Sprintf("\t%s = gtk4rt.Nothing()\n", root)
	case len(tops) == 0:
		// Hidden, so the box holding it gives it no room (gtk4rt.Nothing).
		return fmt.Sprintf("\t%s = C.gtk_label_new(nil)\n\tC.gtk_widget_set_visible(%s, 0)\n", root, root)
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
	boxInit := &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: nativeCall("gtk_box_new", ir.NativePointerOf("GtkWidget"), cOrientationVertical(), intLit("6"))}
	fmt.Fprintf(&b, "\t__box := %s\n", igc.EvalExpr(boxInit))
	boxRef := &ir.Ident{Name: "__box"}
	for _, id := range tops {
		b.WriteString(boxAppendLine(igc, boxRef, nodeRef(id)))
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
		return instanceTeardownMethod
	}
	if after, ok := strings.CutPrefix(fn.Name, setterPrefix); ok {
		return golang.ComponentSetterMethod(after)
	}
	return fn.Name
}

// setterPrefix is ir.ComponentSetter's, read back rather than spelled again.
var setterPrefix = ir.ComponentSetter("")

// boxAppendLine is one `gtk_box_append(box, child)` in cgo mode, cast on both
// sides the way the Model's own root wrapper casts them.
func boxAppendLine(igc *golang.GoIRContext, box, child ir.Expr) string {
	call := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_box_append"),
		Args: []ir.CallArg{
			{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: box}},
			{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: child}},
		},
	}
	var b strings.Builder
	for _, line := range igc.EvalStmt(&ir.CallStmt{Call: call}) {
		fmt.Fprintf(&b, "\t%s\n", line)
	}
	return b.String()
}

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

func instanceVarInit(v *ir.Var, igc *golang.GoIRContext, wrapped bool) string {
	// A slot accumulator starts empty: the render is what fills it.
	if v.Synthesized && ir.IsSlotVarName(v.Name) {
		return "nil"
	}
	// __root is not one of those. It is the box a reactive slot in the body
	// renders into, and the body runs in this same ctor -- so nil here meant
	// the first render appended into nothing. These assignments are statements
	// of the ctor rather than fields of a struct literal, which is what made
	// the Model's reason for binding its own __root later not apply.
	if v.Synthesized && v.Name == instanceRootVar {
		if wrapped {
			return "gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)"
		}
		return igc.EvalExpr(&ir.Conversion{
			Type:    ir.NativePointerOf("GtkBox"),
			Operand: nativeCall("gtk_box_new", ir.NativePointerOf("GtkWidget"), cOrientationVertical(), intLit("6")),
		})
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

// emitPresenceChecks writes, for each `#id` a test null-tests, whether the
// widget is rendered now: a widget an instance holds is the Model's while the
// instance lives, and one the Model builds itself is there from BuildUI on.
func emitPresenceChecks(b *strings.Builder, pkg *ir.Package, ids map[string]bool, held map[string]string, modelFields []widgetField) {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		field := held[id]
		if field == "" && slices.ContainsFunc(modelFields, func(f widgetField) bool { return f.name == id }) {
			field = id
		}
		if field == "" && slices.ContainsFunc(modelFields, func(f widgetField) bool { return f.name == presentField(id) }) {
			field = presentField(id)
		}
		name := codegen.PresenceMethod(id)
		if field == "" {
			if codegen.RendersNowhere(pkg, id) {
				fmt.Fprintf(b, "// %s reports that the #%s widget is never rendered; for tests.\n", name, id)
				fmt.Fprintf(b, "func (m *Model) %s() bool {\n\treturn false\n}\n\n", name)
			}
			continue
		}
		fmt.Fprintf(b, "// %s reports whether the #%s widget is rendered now; for tests.\n", name, id)
		fmt.Fprintf(b, "func (m *Model) %s() bool {\n\treturn m.%s != nil\n}\n\n", name, field)
	}
}
