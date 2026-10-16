package fyne

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// copiesField is the Model field a test-indexed id's copies are kept in, keyed
// by the copy each was stamped with (ir.CopyProp), for its `At` invoker. It is
// beside namedWidgetField and not in its place: an id one test indexes may be
// written once elsewhere, and reached there by the plain invoker.
func copiesField(id string) string { return "__copies_" + id }

// copyMapType is the Go type of copiesField.
func copyMapType(widgetType string) string { return "map[string]" + widgetType }

// copyRegistry is the Model method putting a copy of id under its key.
func copyRegistry(id string) string { return "__list_" + id }

// listCopy is the write of a node's copy key: the node goes in its id's map
// under it. A write is how a key reaches a widget, and how a new one does when
// a render slot keeps an instance and hands it a new position.
func (t *fyneTranslator) listCopy(id string, key ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: t.gc.ModelRef()},
		Func:     &ir.Func{Name: copyRegistry(id)},
		Args:     []ir.CallArg{{Value: key}, {Value: t.nodeRefFor(id)}},
	}}
}

// emitCopyRegistries writes copyRegistry for each map of copies the Model
// declares. A widget given a new key leaves the old one, so a key names the
// copy holding it now.
func emitCopyRegistries(b *strings.Builder, fields []irWidgetField) {
	seen := map[string]bool{}
	for _, f := range fields {
		id, ok := strings.CutPrefix(f.name, copiesField(""))
		widget, isMap := strings.CutPrefix(f.goType, copyMapType(""))
		if !ok || !isMap || seen[id] {
			continue
		}
		seen[id] = true
		fmt.Fprintf(b, "func (m *Model) %s(key string, w %s) {\n", copyRegistry(id), widget)
		fmt.Fprintf(b, "\tfor k, c := range m.%s {\n\t\tif c == w {\n\t\t\tdelete(m.%s, k)\n\t\t}\n\t}\n", f.name, f.name)
		fmt.Fprintf(b, "\tif m.%s == nil {\n\t\tm.%s = %s{}\n\t}\n", f.name, f.name, f.goType)
		fmt.Fprintf(b, "\tm.%s[key] = w\n}\n\n", f.name)
	}
}

// emitKeyedPresenceChecks writes, for each id a test null-tests one copy of,
// whether a copy with the key is rendered now: whether its map holds one.
func emitKeyedPresenceChecks(b *strings.Builder, ids map[string]bool, fields []irWidgetField) {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		name := codegen.PresenceAtMethod(id)
		fmt.Fprintf(b, "// %s reports whether the copy of #%s with the key is rendered now; for tests.\n", name, id)
		if !slices.ContainsFunc(fields, func(f irWidgetField) bool { return f.name == copiesField(id) }) {
			fmt.Fprintf(b, "func (m *Model) %s(key string) bool {\n\treturn false\n}\n\n", name)
			continue
		}
		fmt.Fprintf(b, "func (m *Model) %s(key string) bool {\n\t_, ok := m.%s[key]\n\treturn ok\n}\n\n", name, copiesField(id))
	}
}

// presentField is the Model field an instance built at run time, which a test
// null-tests by the id its call site gave it, hands its root to while it is
// rendered: it has no widget of its own.
func presentField(id string) string { return "__present_" + id }

// listInstanceCopy puts the root of an instance built at run time in its id's
// map of copies under key: it has no widget of its own to be listed as.
func (t *fyneTranslator) listInstanceCopy(id string, key, root ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: t.gc.ModelRef()},
		Func:     &ir.Func{Name: copyRegistry(id)},
		Args:     []ir.CallArg{{Value: key}, {Value: root}},
	}}
}

// unlistInstance is the Model method taking a destroyed instance's root out
// of every map of instance copies. The instance cannot know the id its call
// site gave it, and a destroy reaches it from the live list of a slot or from
// the instance that built it, neither of which names it either.
const unlistInstance = "__unlistInstance"

// keyedInstances reports whether some component is handed the key of the copy
// it stands in, whose record then unlists itself as it is destroyed, or some
// spliced instance's roots are listed (listRoot).
func keyedInstances(pkg *ir.Package) bool {
	return pkg != nil && (slices.ContainsFunc(pkg.Components, func(c *ir.Component) bool { return c.KeyedCopy }) ||
		len(codegen.InstanceRootIDs(pkg)) > 0)
}

// emitUnlistInstance writes unlistInstance over the maps of the instance
// copies and the instance widgets the Model declares, and keepRoot where a
// record lists a spliced instance's roots.
func emitUnlistInstance(b *strings.Builder, fields []irWidgetField, roots bool) {
	fmt.Fprintf(b, "func (m *Model) %s(w %s) {\n", unlistInstance, fyneWidgetHandleType)
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.name] {
			continue
		}
		seen[f.name] = true
		if strings.HasPrefix(f.name, copiesField("")) && f.goType == copyMapType(fyneWidgetHandleType) {
			fmt.Fprintf(b, "\tfor k, c := range m.%s {\n\t\tif c == w {\n\t\t\tdelete(m.%s, k)\n\t\t}\n\t}\n", f.name, f.name)
		}
		if strings.HasPrefix(f.name, presentField("")) && f.goType == fyneWidgetHandleType {
			fmt.Fprintf(b, "\tif m.%s == w {\n\t\tm.%s = nil\n\t}\n", f.name, f.name)
		}
	}
	b.WriteString("}\n\n")
	if !roots {
		return
	}
	fmt.Fprintf(b, "func (m *Model) %s(roots []%s, w %s) []%s {\n", keepRoot, fyneWidgetHandleType, fyneWidgetHandleType, fyneWidgetHandleType)
	b.WriteString("\tfor _, r := range roots {\n\t\tif r == w {\n\t\t\treturn roots\n\t\t}\n\t}\n\treturn append(roots, w)\n}\n\n")
}

// keepRoot is the Model method returning a record's rootedField with a root
// added once.
const keepRoot = "__keepRoot"

// rootedField is the field of a component built at run time holding the roots
// of the spliced instances it rendered and listed, which its Destroy unlists.
const rootedField = "__rooted"

// listRoot lists w, a root of the spliced instance id, as the instance: in its
// map of copies under key where a test reaches one copy, as its presentField
// otherwise. It is the instance's while it is on screen: a removal unlists it
// (unlistRemoved), and so does the Destroy of a record that rendered it.
func (t *fyneTranslator) listRoot(id string, w, key ir.Expr) []ir.Stmt {
	model := &ir.Ident{Name: t.gc.ModelRef()}
	declare := t.modelSink
	if declare == nil {
		declare = t.sinkField
	}
	handle := ir.NativeGoNamed(fyneWidgetHandleType)
	var out []ir.Stmt
	if t.indexed[id] || t.instanceCopies[id] {
		declare(copiesField(id), copyMapType(fyneWidgetHandleType))
		out = append(out, t.listInstanceCopy(id, key, w))
	} else {
		declare(presentField(id), fyneWidgetHandleType)
		out = append(out, &ir.Assign{Target: codegen.RecvFieldRef(model, presentField(id), handle), Op: ast.AssignSet, Value: w})
	}
	if t.record {
		t.sinkField(rootedField, "[]"+fyneWidgetHandleType)
		rooted := t.fieldRef(rootedField)
		out = append(out, &ir.Assign{Target: rooted, Op: ast.AssignSet, Value: &ir.Call{
			Type:     rooted.ExprType(),
			Receiver: model,
			Func:     &ir.Func{Name: keepRoot},
			Args:     []ir.CallArg{{Value: t.fieldRef(rootedField)}, {Value: w}},
		}})
	}
	return out
}

// unlistRemoved takes a widget a render removed out of the instance lists, in
// a build listing a spliced instance's roots: an instance built at run time is
// unlisted as it is destroyed.
func (t *fyneTranslator) unlistRemoved(child ir.Expr) []ir.Stmt {
	if len(t.gc.Ctx.InstanceRootIDs()) == 0 {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: t.gc.ModelRef()},
		Func:     &ir.Func{Name: unlistInstance},
		Args:     []ir.CallArg{{Value: t.qualifyChildExpr(child)}},
	}}}
}
