package gtk4

import (
	"strings"

	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

var (
	copiesField  = golang.CopiesField
	copyMapType  = golang.CopyMapType
	presentField = golang.PresentField
)

const (
	unlistInstance = golang.UnlistInstance
	rootedField    = golang.RootedField
)

func modelFields(fields []widgetField) []golang.ModelField {
	out := make([]golang.ModelField, len(fields))
	for i, f := range fields {
		out[i] = golang.ModelField{Name: f.name, GoType: f.goType}
	}
	return out
}

// listCopy is the write of a node's copy key: the node goes in its id's map
// under it. A write is how a key reaches a widget, and how a new one does when
// a render slot keeps an instance and hands it a new position.
func (t *gtk4Translator) listCopy(id string, node, key ir.Expr) ir.Stmt {
	return golang.CopyCall(&ir.Ident{Name: t.gc.ModelRef()}, id, key, t.qualifyNodeExpr(node))
}

func emitCopyRegistries(b *strings.Builder, fields []widgetField) {
	golang.EmitCopyRegistries(b, modelFields(fields))
}

func emitKeyedPresenceChecks(b *strings.Builder, ids map[string]bool, fields []widgetField) {
	golang.EmitKeyedPresenceChecks(b, ids, modelFields(fields))
}

func keyedInstances(pkg *ir.Package) bool { return golang.KeyedInstances(pkg) }

func emitUnlistInstance(b *strings.Builder, fields []widgetField, wrapped, roots bool) {
	golang.EmitUnlistInstance(b, modelFields(fields), widgetFieldGoType("GtkWidget", wrapped), roots)
}

// rootListing lists this scope's spliced instance roots on the Model, and in a
// record also in its rootedField, which the record's Destroy unlists.
func (t *gtk4Translator) rootListing() golang.RootListing {
	declare := t.modelSink
	if declare == nil {
		declare = t.sinkField
	}
	l := golang.RootListing{
		Model:      &ir.Ident{Name: t.gc.ModelRef()},
		Keyed:      func(id string) bool { return t.indexed[id] || t.instanceCopies[id] },
		Declare:    declare,
		Widget:     "GtkWidget",
		WidgetType: ir.NativeGoNamed(widgetFieldGoType("GtkWidget", t.wrapped)),
	}
	if t.record {
		l.Rooted = func() ir.Expr {
			t.sinkField(rootedField, "[]GtkWidget")
			return t.fieldRef(rootedField)
		}
	}
	return l
}

func (t *gtk4Translator) listRoot(id string, node, key ir.Expr) []ir.Stmt {
	return t.rootListing().ListRoot(id, t.asWidget(node), key)
}

// unlistRemoved takes a widget a render removes out of the instance lists, in
// a build listing a spliced instance's roots.
func (t *gtk4Translator) unlistRemoved(child ir.Expr) []ir.Stmt {
	if len(t.gc.Ctx.InstanceRootIDs()) == 0 {
		return nil
	}
	return t.rootListing().UnlistRemoved(t.asWidget(child))
}

// asWidget is node as the GtkWidget the instance lists hold: a cgo cast to
// one inline, and the handle every widget already is wrapped.
func (t *gtk4Translator) asWidget(node ir.Expr) ir.Expr {
	if t.wrapped {
		return t.qualifyNodeExpr(node)
	}
	return cgoCast("GtkWidget", t.qualifyNodeExpr(node))
}
