package fyne

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

func modelFields(fields []irWidgetField) []golang.ModelField {
	out := make([]golang.ModelField, len(fields))
	for i, f := range fields {
		out[i] = golang.ModelField{Name: f.name, GoType: f.goType}
	}
	return out
}

// listCopy is the write of a node's copy key: the node goes in its id's map
// under it. A write is how a key reaches a widget, and how a new one does when
// a render slot keeps an instance and hands it a new position.
func (t *fyneTranslator) listCopy(id string, key ir.Expr) ir.Stmt {
	return golang.CopyCall(&ir.Ident{Name: t.gc.ModelRef()}, id, key, t.nodeRefFor(id))
}

func emitCopyRegistries(b *strings.Builder, fields []irWidgetField) {
	golang.EmitCopyRegistries(b, modelFields(fields))
}

func emitKeyedPresenceChecks(b *strings.Builder, ids map[string]bool, fields []irWidgetField) {
	golang.EmitKeyedPresenceChecks(b, ids, modelFields(fields))
}

// listInstanceCopy puts the root of an instance built at run time in its id's
// map of copies under key: it has no widget of its own to be listed as.
func (t *fyneTranslator) listInstanceCopy(id string, key, root ir.Expr) ir.Stmt {
	return golang.CopyCall(&ir.Ident{Name: t.gc.ModelRef()}, id, key, root)
}

func keyedInstances(pkg *ir.Package) bool { return golang.KeyedInstances(pkg) }

func emitUnlistInstance(b *strings.Builder, fields []irWidgetField, roots bool) {
	golang.EmitUnlistInstance(b, modelFields(fields), fyneWidgetHandleType, roots)
}

// rootListing lists this scope's spliced instance roots on the Model, and in a
// record also in its rootedField, which the record's Destroy unlists.
func (t *fyneTranslator) rootListing() golang.RootListing {
	declare := t.modelSink
	if declare == nil {
		declare = t.sinkField
	}
	l := golang.RootListing{
		Model:      &ir.Ident{Name: t.gc.ModelRef()},
		Keyed:      func(id string) bool { return t.indexed[id] || t.instanceCopies[id] },
		Declare:    declare,
		Widget:     fyneWidgetHandleType,
		WidgetType: ir.NativeGoNamed(fyneWidgetHandleType),
	}
	if t.record {
		l.Rooted = func() ir.Expr {
			t.sinkField(rootedField, "[]"+fyneWidgetHandleType)
			return t.fieldRef(rootedField)
		}
	}
	return l
}

func (t *fyneTranslator) listRoot(id string, w, key ir.Expr) []ir.Stmt {
	return t.rootListing().ListRoot(id, w, key)
}

// unlistRemoved takes a widget a render removed out of the instance lists, in
// a build listing a spliced instance's roots.
func (t *fyneTranslator) unlistRemoved(child ir.Expr) []ir.Stmt {
	if len(t.gc.Ctx.InstanceRootIDs()) == 0 {
		return nil
	}
	return t.rootListing().UnlistRemoved(t.qualifyChildExpr(child))
}
