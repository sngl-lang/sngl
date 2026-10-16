package gtk4

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// copiesField is the Model field a test-indexed id's copies are kept in, keyed
// by the copy each was stamped with (ir.CopyProp), for its `At` invoker. It is
// beside namedWidgetField and not in its place: an id one test indexes may be
// written once elsewhere, and reached there by the plain invoker.
func copiesField(id string) string { return "__copies_" + id }

// copyMapType is the type of copiesField.
func copyMapType(widgetType string) string { return "map[string]" + widgetType }

// copyRegistry is the Model method putting a copy of id under its key.
func copyRegistry(id string) string { return "__list_" + id }

// listCopy is the write of a node's copy key: the node goes in its id's map
// under it. A write is how a key reaches a widget, and how a new one does when
// a render slot keeps an instance and hands it a new position.
func (t *gtk4Translator) listCopy(id string, node, key ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: t.gc.ModelRef()},
		Func:     &ir.Func{Name: copyRegistry(id)},
		Args:     []ir.CallArg{{Value: key}, {Value: t.qualifyNodeExpr(node)}},
	}}
}

// emitCopyRegistries writes copyRegistry for each map of copies the Model
// declares. A widget given a new key leaves the old one, so a key names the
// copy holding it now.
func emitCopyRegistries(b *strings.Builder, fields []widgetField) {
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
