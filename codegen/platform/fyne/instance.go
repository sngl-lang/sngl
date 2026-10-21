package fyne

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
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
	invokerSink func(fyneEventInvoker),
	modelFieldSink func(name, goType string),
	indexed map[string]bool,
	presentIDs map[string]bool,
	presenceSink func(id, field string),
) {
	comp := cc.Component
	typeName := golang.ComponentInstanceType(comp.Name)
	records := golang.InstanceRecordTypes(gc.Ctx.Pkg)

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

	// A widget the program named is one a test drives, and a record is built
	// and destroyed while the program runs, so the record hands its widget to
	// the Model as it is built: the invoker reaches whichever instance built
	// it last, which is the one on screen.
	var invokers []fyneEventInvoker
	tr := newFyneTranslator(igc, nodeSpecs, sink, importSink, failSink).
		withLocalRefs(comp.LocalRefs).withSlotRoot(instanceRootVar, comp.Stmts).
		withInstanceCopies(indexed, modelFieldSink).inRecord().
		withInvokerSink(func(inv fyneEventInvoker) { invokers = append(invokers, inv) })
	tr.canvasByID, tr.canvasByNode = canvasByID, canvasByNode
	bodyStmts := codegen.WalkLowered(context.Background(), comp.Stmts, tr)

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
	exposed := map[string]bool{}
	var listed []string
	for _, inv := range invokers {
		widget := inv.IDLabel
		if inv.Target != "" {
			widget = inv.Target
		}
		goType := ""
		for _, f := range fields {
			if f.name == widget {
				goType = f.goType
				break
			}
		}
		if goType == "" || invokerSink == nil {
			continue
		}
		named := namedWidgetField(widget)
		// An id a test indexes is also every instance's copy, each under the
		// key its __copy write gave it (listCopy), and it leaves the map when
		// its instance is destroyed (unlist).
		list := indexed[inv.IDLabel] && inv.Target == ""
		if !exposed[widget] {
			exposed[widget] = true
			model := instanceReceiver + "." + golang.InstanceModelField + "." + named
			fmt.Fprintf(&ctorBody, "\t%s = %s.%s\n", model, instanceReceiver, widget)
			modelFieldSink(named, goType)
			if list {
				modelFieldSink(copiesField(widget), copyMapType(goType))
				listed = append(listed, widget)
			}
		}
		inv.Target, inv.List = named, list
		invokerSink(inv)
	}
	// An id a test only null-tests one copy of is kept by key too, with no
	// invoker to have exposed it.
	for _, f := range fields {
		if indexed[f.name] && !slices.Contains(listed, f.name) && !records[f.goType] {
			modelFieldSink(copiesField(f.name), copyMapType(f.goType))
			listed = append(listed, f.name)
		}
	}

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
			if inv.IDLabel == id && inv.Target != "" {
				widget = inv.Target
			}
		}
		goType := ""
		for _, f := range fields {
			if f.name == widget {
				goType = f.goType
				break
			}
		}
		if goType == "" || presenceSink == nil {
			continue
		}
		named := namedWidgetField(widget)
		if !exposed[widget] {
			exposed[widget] = true
			fmt.Fprintf(&ctorBody, "\t%s.%s.%s = %s.%s\n", instanceReceiver, golang.InstanceModelField, named, instanceReceiver, widget)
			modelFieldSink(named, goType)
		}
		held := instanceReceiver + "." + golang.InstanceModelField + "." + named
		fmt.Fprintf(&release, "\tif %s == %s.%s {\n\t\t%s = nil\n\t}\n", held, instanceReceiver, widget, held)
		presenceSink(id, named)
	}
	hasTeardown := componentDeclaresFunc(comp, lower.TeardownFunc)
	var unlisting strings.Builder
	if len(listed) > 0 {
		fmt.Fprintf(&unlisting, "\t%s.unlist()\n", instanceReceiver)
	}
	if comp.KeyedCopy {
		fmt.Fprintf(&unlisting, "\t%s.%s.%s(%s.%s)\n", instanceReceiver, golang.InstanceModelField, unlistInstance, instanceReceiver, golang.ComponentRootField)
	}
	unlisting.WriteString(golang.InstanceRelease(gc.InstanceReads, comp, instanceReceiver))
	if len(codegen.InstanceRootIDs(comp)) > 0 {
		fmt.Fprintf(&unlisting, "\tfor _, w := range %s.%s {\n\t\t%s.%s.%s(w)\n\t}\n", instanceReceiver, rootedField, instanceReceiver, golang.InstanceModelField, unlistInstance)
	}

	// Methods after the body, because the walk is what discovers the widget
	// fields the struct declares -- and a setter's body carries the same
	// injected widget updates a handler's does, so it goes through a
	// translator too.
	var methods strings.Builder
	for _, fn := range comp.Funcs {
		if fn == nil || len(fn.Block) == 0 {
			continue
		}
		mtr := newFyneTranslator(igc, nodeSpecs, sink, importSink, failSink).withLocalRefs(fn.Lowered.LocalRefs).
			withInstanceCopies(indexed, modelFieldSink).inRecord()
		mtr.canvasByNode = canvasByNode
		params := fn.Params
		if len(params) > 0 && params[0].Receiver {
			params = params[1:]
		}
		if fn.Lowered.SlotRender {
			mtr.withRecordPresent(presentIDs, fn.Block)
			// A reactive slot's render func. Its body was written against the
			// reactivity pass's `parent` name, which the translator rewrites
			// to slotParentParam -- so the parameter has to be spelled the way
			// the Model's own slot funcs spell it (see emitIRSlotFunc), or the
			// body names a package instead of its argument.
			params = []*ir.Param{{Name: slotParentParam, Type: ir.NativeGoPointerOf("fyne.Container")}}
		}
		name := instanceMethodName(fn)
		if fn.Name == lower.TeardownFunc && release.Len() > 0 {
			name = instanceTeardownMethod
		}
		emitted := &ir.Func{
			Name:   name,
			Params: params,
			Return: fn.Return,
			Block:  codegen.WalkLowered(context.Background(), fn.Block, mtr),
		}
		for i, line := range igc.EmitFuncDef(emitted) {
			methods.WriteString(line)
			methods.WriteByte('\n')
			if i == 0 && fn.Name == lower.TeardownFunc {
				methods.WriteString(unlisting.String())
			}
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

	// A widget a render slot of the record builds -- a copy of a loop in it --
	// is found only by walking that slot's method, above: its copies are kept
	// in the Model's map as the body's are, under the key the slot gives each.
	for _, f := range fields {
		if indexed[f.name] && !slices.Contains(listed, f.name) && !records[f.goType] {
			modelFieldSink(copiesField(f.name), copyMapType(f.goType))
			listed = append(listed, f.name)
		}
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
	switch {
	case release.Len() > 0:
		fmt.Fprintf(b, "func (%s *%s) %s() {\n", instanceReceiver, typeName, golang.ComponentDestroyMethod)
		if hasTeardown {
			fmt.Fprintf(b, "\t%s.%s()\n", instanceReceiver, instanceTeardownMethod)
		} else {
			b.WriteString(unlisting.String())
		}
		fmt.Fprintf(b, "%s}\n\n", release.String())
	case !hasTeardown && unlisting.Len() > 0:
		fmt.Fprintf(b, "func (%s *%s) %s() {\n%s}\n\n", instanceReceiver, typeName, golang.ComponentDestroyMethod, unlisting.String())
	case !hasTeardown:
		fmt.Fprintf(b, "func (%s *%s) %s() {}\n\n", instanceReceiver, typeName, golang.ComponentDestroyMethod)
	}
	if len(listed) > 0 {
		emitUnlist(b, typeName, listed)
	}
}

// emitUnlist writes the method taking a destroyed instance's widgets out of
// the Model's maps of them (copiesField), so a map holds the copies on screen.
func emitUnlist(b *strings.Builder, typeName string, widgets []string) {
	fmt.Fprintf(b, "func (%s *%s) unlist() {\n", instanceReceiver, typeName)
	for _, w := range widgets {
		list := instanceReceiver + "." + golang.InstanceModelField + "." + copiesField(w)
		fmt.Fprintf(b, "\tfor k, w := range %s {\n\t\tif w == %s.%s {\n\t\t\tdelete(%s, k)\n\t\t}\n\t}\n",
			list, instanceReceiver, w, list)
	}
	b.WriteString("}\n\n")
}

// namedWidgetField is the Model field an instance's program-named widget is
// handed to, for the test invoker that drives it.
func namedWidgetField(widget string) string { return "__named_" + widget }

// instanceTeardownMethod is what the lowered teardown is emitted as when
// Destroy has more to do than run it.
const instanceTeardownMethod = "teardown"

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
	case v.Synthesized && v.Name == instanceRootVar:
		return "*fyne.Container"
	}
	return golang.VarGoType(v)
}

func instanceVarInit(v *ir.Var, igc *golang.GoIRContext) string {
	switch {
	case v.Synthesized && ir.IsSlotVarName(v.Name):
		return "nil"
	case v.Synthesized && v.Name == instanceRootVar:
		igc.RequireImport("fyne.io/fyne/v2/container")
		return "container.NewVBox()"
	}
	return golang.LowerVarInit(v, igc)
}

// instanceRootExpr is the widget the instance renders as: the container its
// reactive slots render into when the body has one, holding the body's own
// widgets in written order, and otherwise whatever the body left unattached.
// Wrapped when the body left several, because a parent holds one child per
// instance and an instance with two top-level nodes has no single widget to be.
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

// instanceRootVar is the container a reactive slot in a component body renders
// into.
const instanceRootVar = "__root"

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

// emitFynePresenceChecks writes, for each `#id` a test null-tests, whether the
// widget is rendered now: a widget an instance holds is the Model's while the
// instance lives, and one the Model builds itself is there from BuildUI on.
func emitFynePresenceChecks(b *strings.Builder, pkg *ir.Package, ids map[string]bool, held map[string]string, modelFields []irWidgetField) {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		field := held[id]
		if field == "" && slices.ContainsFunc(modelFields, func(f irWidgetField) bool { return f.name == id }) {
			field = id
		}
		if field == "" && slices.ContainsFunc(modelFields, func(f irWidgetField) bool { return f.name == presentField(id) }) {
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
