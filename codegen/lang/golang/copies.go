package golang

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// The Model fields and methods fyne and gtk4 keep the copies and instances a
// test reaches in: both hold a widget per node on the Model, and differ only in
// the Go type a widget is.

// ModelField is a field a Model declares: its name and Go type.
type ModelField struct{ Name, GoType string }

// CopiesField is the Model field a test-indexed id's copies are kept in, keyed
// by the copy each was stamped with (ir.CopyProp), for its `At` invoker. It is
// beside the id's own widget and not in its place: an id one test indexes may
// be written once elsewhere, and reached there by the plain invoker.
func CopiesField(id string) string { return "__copies_" + id }

// CopyMapType is the Go type of CopiesField.
func CopyMapType(widgetType string) string { return "map[string]" + widgetType }

// CopyRegistry is the Model method putting a copy of id under its key.
func CopyRegistry(id string) string { return "__list_" + id }

// PresentField is the Model field an instance built at run time, which a test
// null-tests by the id its call site gave it, hands its root to while it is
// rendered: it has no widget of its own.
func PresentField(id string) string { return "__present_" + id }

// UnlistInstance is the Model method taking a destroyed instance's root out
// of every map of instance copies. The instance cannot know the id its call
// site gave it, and a destroy reaches it from the live list of a slot or from
// the instance that built it, neither of which names it either.
const UnlistInstance = "__unlistInstance"

// KeepRoot and DropRoot are the Model methods returning a record's
// RootedField with a root added once, or taken out.
const (
	KeepRoot = "__keepRoot"
	DropRoot = "__dropRoot"
)

// RootedField is the field of a component built at run time holding the roots
// of the spliced instances it renders and lists, which its Destroy unlists.
const RootedField = "__rooted"

// CopyCall is a call of a Model method listing w under key for id.
func CopyCall(model *ir.Ident, id string, key, w ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: model,
		Func:     &ir.Func{Name: CopyRegistry(id)},
		Args:     []ir.CallArg{{Value: key}, {Value: w}},
	}}
}

// KeyedInstances reports whether some component is handed the key of the copy
// it stands in, whose record then unlists itself as it is destroyed, or some
// spliced instance's roots are listed (RootListing).
func KeyedInstances(pkg *ir.Package) bool {
	return pkg != nil && (slices.ContainsFunc(pkg.Components, func(c *ir.Component) bool { return c.KeyedCopy }) ||
		len(codegen.InstanceRootIDs(pkg)) > 0)
}

// EmitCopyRegistries writes CopyRegistry for each map of copies the Model
// declares. A widget given a new key leaves the old one, so a key names the
// copy holding it now.
func EmitCopyRegistries(b *strings.Builder, fields []ModelField) {
	seen := map[string]bool{}
	for _, f := range fields {
		id, ok := strings.CutPrefix(f.Name, CopiesField(""))
		widget, isMap := strings.CutPrefix(f.GoType, CopyMapType(""))
		if !ok || !isMap || seen[id] {
			continue
		}
		seen[id] = true
		fmt.Fprintf(b, "func (m *Model) %s(key string, w %s) {\n", CopyRegistry(id), widget)
		fmt.Fprintf(b, "\tfor k, c := range m.%s {\n\t\tif c == w {\n\t\t\tdelete(m.%s, k)\n\t\t}\n\t}\n", f.Name, f.Name)
		fmt.Fprintf(b, "\tif m.%s == nil {\n\t\tm.%s = %s{}\n\t}\n", f.Name, f.Name, f.GoType)
		fmt.Fprintf(b, "\tm.%s[key] = w\n}\n\n", f.Name)
	}
}

// EmitKeyedPresenceChecks writes, for each id a test null-tests one copy of,
// whether a copy with the key is rendered now: whether its map holds one.
func EmitKeyedPresenceChecks(b *strings.Builder, ids map[string]bool, fields []ModelField) {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		name := codegen.PresenceAtMethod(id)
		fmt.Fprintf(b, "// %s reports whether the copy of #%s with the key is rendered now; for tests.\n", name, id)
		if !slices.ContainsFunc(fields, func(f ModelField) bool { return f.Name == CopiesField(id) }) {
			fmt.Fprintf(b, "func (m *Model) %s(key string) bool {\n\treturn false\n}\n\n", name)
			continue
		}
		fmt.Fprintf(b, "func (m *Model) %s(key string) bool {\n\t_, ok := m.%s[key]\n\treturn ok\n}\n\n", name, CopiesField(id))
	}
}

// EmitUnlistInstance writes UnlistInstance over the maps of the instance
// copies and the instance widgets the Model declares, and KeepRoot and
// DropRoot where a record lists a spliced instance's roots.
func EmitUnlistInstance(b *strings.Builder, fields []ModelField, widget string, roots bool) {
	fmt.Fprintf(b, "func (m *Model) %s(w %s) {\n", UnlistInstance, widget)
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		if strings.HasPrefix(f.Name, CopiesField("")) && f.GoType == CopyMapType(widget) {
			fmt.Fprintf(b, "\tfor k, c := range m.%s {\n\t\tif c == w {\n\t\t\tdelete(m.%s, k)\n\t\t}\n\t}\n", f.Name, f.Name)
		}
		if strings.HasPrefix(f.Name, PresentField("")) && f.GoType == widget {
			fmt.Fprintf(b, "\tif m.%s == w {\n\t\tm.%s = nil\n\t}\n", f.Name, f.Name)
		}
	}
	b.WriteString("}\n\n")
	if !roots {
		return
	}
	fmt.Fprintf(b, "func (m *Model) %s(roots []%s, w %s) []%s {\n", KeepRoot, widget, widget, widget)
	b.WriteString("\tfor _, r := range roots {\n\t\tif r == w {\n\t\t\treturn roots\n\t\t}\n\t}\n\treturn append(roots, w)\n}\n\n")
	fmt.Fprintf(b, "func (m *Model) %s(roots []%s, w %s) []%s {\n", DropRoot, widget, widget, widget)
	b.WriteString("\tfor i, r := range roots {\n\t\tif r == w {\n\t\t\treturn append(roots[:i:i], roots[i+1:]...)\n\t\t}\n\t}\n\treturn roots\n}\n\n")
}

// RootListing is how a scope lists a spliced instance's roots as the instance
// (ListRoot) and takes one out when a render removes it (UnlistRemoved).
type RootListing struct {
	Model *ir.Ident
	// Keyed reports whether id is listed by key, in its CopiesField, rather
	// than as its PresentField.
	Keyed func(id string) bool
	// Declare declares a Model field, given the platform's spelling of its
	// widget type, which Widget is and WidgetType the Go type of.
	Declare    func(name, widget string)
	Widget     string
	WidgetType *ir.Type
	// Rooted is the record's RootedField, nil outside a record.
	Rooted func() ir.Expr
}

// ListRoot lists w, a root of the spliced instance id, as the instance: in its
// map of copies under key where a test reaches one copy, as its PresentField
// otherwise. It is the instance's while it is on screen: a removal unlists it
// (UnlistRemoved), and so does the Destroy of a record that rendered it.
func (l RootListing) ListRoot(id string, w, key ir.Expr) []ir.Stmt {
	var out []ir.Stmt
	if l.Keyed(id) {
		l.Declare(CopiesField(id), CopyMapType(l.Widget))
		out = append(out, CopyCall(l.Model, id, key, w))
	} else {
		l.Declare(PresentField(id), l.Widget)
		out = append(out, &ir.Assign{Target: codegen.RecvFieldRef(l.Model, PresentField(id), l.WidgetType), Op: ast.AssignSet, Value: w})
	}
	if l.Rooted != nil {
		out = append(out, l.rootedCall(KeepRoot, ir.CloneExpr(w)))
	}
	return out
}

// UnlistRemoved takes a widget a render removes out of the instance lists, and
// out of the record's roots: an instance built at run time is unlisted as it
// is destroyed.
func (l RootListing) UnlistRemoved(w ir.Expr) []ir.Stmt {
	out := []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: l.Model,
		Func:     &ir.Func{Name: UnlistInstance},
		Args:     []ir.CallArg{{Value: w}},
	}}}
	if l.Rooted != nil {
		out = append(out, l.rootedCall(DropRoot, ir.CloneExpr(w)))
	}
	return out
}

func (l RootListing) rootedCall(method string, w ir.Expr) ir.Stmt {
	rooted := l.Rooted()
	return &ir.Assign{Target: rooted, Op: ast.AssignSet, Value: &ir.Call{
		Type:     rooted.ExprType(),
		Receiver: l.Model,
		Func:     &ir.Func{Name: method},
		Args:     []ir.CallArg{{Value: l.Rooted()}, {Value: w}},
	}}
}
