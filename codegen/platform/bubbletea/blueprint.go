package bubbletea

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// widgetTemplateConverters maps a `${prop|conv}` converter name to a pkg/go/tui
// helper function. The substituted prop expression is wrapped as
// `tui.<helper>(<expr>)`. Extensible: add a row to support a new conversion.
var widgetTemplateConverters = map[string]string{
	"listItems": "StringItems",
	"columns":   "Columns",
	"rows":      "Rows",
}

// expandWidgetTemplate substitutes `${...}` tokens in a Model.New/View/Update/
// Init string against the Widget node `n`'s props. Tokens:
//
//	${prop}       → the Go expression for n's prop named "prop"
//	${prop|conv}  → tui.<Helper>(<expr>), where conv maps via
//	                widgetTemplateConverters; the pkg/go/tui import is required
//	${self}       → the widget's field accessor `m.<field>` (used by Init
//	                cmds that are model methods, e.g. spinner `${self}.Tick`)
//	$${           → a literal "${" (escape)
//
// A string with no `${...}` token is returned unchanged (so existing widgets
// such as input/textarea, whose Model strings carry no tokens, emit
// byte-identically). An unknown prop or converter leaves the raw token in place
// — the resulting Go won't compile, surfacing the authoring error loudly rather
// than silently dropping it.
func expandWidgetTemplate(gc *golang.GoIRContext, tmpl string, n *ir.NodeInst, field string) string {
	if !strings.Contains(tmpl, "${") {
		return tmpl
	}
	var b strings.Builder
	i := 0
	for i < len(tmpl) {
		// Escape: "$${" → literal "${".
		if strings.HasPrefix(tmpl[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(tmpl[i:], "${") {
			end := strings.IndexByte(tmpl[i+2:], '}')
			if end < 0 {
				// Unterminated token: emit the rest verbatim.
				b.WriteString(tmpl[i:])
				break
			}
			token := tmpl[i+2 : i+2+end]
			b.WriteString(expandWidgetToken(gc, token, n, field))
			i += 2 + end + 1
			continue
		}
		b.WriteByte(tmpl[i])
		i++
	}
	return b.String()
}

// expandWidgetToken resolves a single token body (the text between "${" and
// "}") into a Go expression, applying a converter if "prop|conv" form is used.
func expandWidgetToken(gc *golang.GoIRContext, token string, n *ir.NodeInst, field string) string {
	// ${self} resolves to the widget's field accessor. It carries no prop and
	// no converter, so handle it before the prop/converter split.
	if token == "self" {
		return "m." + field
	}
	propName, conv := token, ""
	if before, after, ok := strings.Cut(token, "|"); ok {
		propName = before
		conv = after
	}
	prop := codegen.NodeProp(n, propName)
	if prop == nil {
		// Leave the token in place so the missing prop surfaces as a Go
		// compile error rather than vanishing.
		return "${" + token + "}"
	}
	expr := gc.EvalExpr(prop)
	if conv == "" {
		return expr
	}
	helper, ok := widgetTemplateConverters[conv]
	if !ok {
		return "${" + token + "}"
	}
	gc.RequireImport(tuiImportPath)
	return "tui." + helper + "(" + expr + ")"
}

// blueprintKind identifies which of the three inlined platform primitives a
// stdlib component renders through. It is decided structurally from the
// inlined node's props (see extractBlueprint): a `model` prop ⇒ Widget, a
// `join` prop ⇒ Layout, otherwise Styled.
type blueprintKind int

const (
	bpStyled blueprintKind = iota
	bpLayout
	bpWidget
	bpOverlay
)

// joinDir mirrors the JoinDir enum declared in bubbletea.sngl. It selects the
// lipgloss join axis for a Layout primitive.
type joinDir int

const (
	joinVertical joinDir = iota
	joinHorizontal
)

// modelMeta mirrors the SNGL `Model` record: the per-widget bubbles model
// metadata that drives Widget rendering (field type, constructor, view method,
// update/init hooks, and the Go import path).
type modelMeta struct {
	Type   string
	New    string
	View   string
	Update string
	Init   string
	Pkg    string
}

// bindMeta mirrors the SNGL `Bind` record: a two-way binding between a SNGL
// prop and the getter on the underlying bubbles model (e.g. prop "value"
// reads back via ".Value()").
// Set is the method appended to the model field to push the SNGL target value
// back into the widget (e.g. ".SetValue" → m.field.SetValue(m.target)). When
// empty the bind is read-only: the widget owns its value and SNGL only reads
// the selection/value back via Get. List-backed widgets (menu/tree/select) are
// read-only this way — their items come from a data prop, not the bind target.
type bindMeta struct {
	Prop string
	Get  string
	Set  string
}

// eventMeta mirrors the SNGL `Event` record: a key-triggered event handler
// (e.g. on "submit" when key "enter").
type eventMeta struct {
	On  string
	Key string
}

// blueprint is the platform-driven render directive extracted off one inlined
// primitive *ir.NodeInst. The renderer (Phase 2) consumes this instead of
// branching on stdlib component name.
type blueprint struct {
	Kind blueprintKind

	// Layout
	Join joinDir

	// Styled / Widget content expression (the `content` prop).
	Content ir.Expr

	// Focus reports whether the primitive allocates a focus index
	// (`focus=Focus{enabled=true}`).
	Focus bool

	// Widget
	Model  modelMeta
	Binds  []bindMeta
	Events []eventMeta

	// Placeholder carries the `placeholder` prop for input-style widgets.
	Placeholder ir.Expr

	// Overlay: Placement is the `placement` prop expression — the string
	// "center" for a modal, or the drawer's `side` prop ("left"/"right"/
	// "top"/"bottom"). Dim reports the `dim` prop (fade the background).
	Placement ir.Expr
	Dim       bool
}

// extractBlueprint reads a blueprint off an inlined primitive node. Kind is
// decided structurally: a `model` prop ⇒ Widget; else a `join` prop ⇒ Layout;
// else Styled.
func extractBlueprint(n *ir.NodeInst) blueprint {
	var bp blueprint

	modelProp := codegen.NodeProp(n, "model")
	joinProp := codegen.NodeProp(n, "join")
	placementProp := codegen.NodeProp(n, "placement")

	switch {
	case n.Name == "Overlay" || placementProp != nil:
		bp.Kind = bpOverlay
		bp.Placement = placementProp
		if d := codegen.NodeProp(n, "dim"); d != nil {
			bp.Dim, _ = codegen.IRLiteralBool(d)
		}
	case modelProp != nil:
		bp.Kind = bpWidget
	case joinProp != nil:
		bp.Kind = bpLayout
	default:
		bp.Kind = bpStyled
	}

	if joinProp != nil {
		bp.Join = extractJoinDir(joinProp)
	}
	bp.Content = codegen.NodeProp(n, "content")
	bp.Placeholder = codegen.NodeProp(n, "placeholder")
	bp.Focus = extractFocus(codegen.NodeProp(n, "focus"))

	if modelProp != nil {
		bp.Model = extractModel(modelProp)
	}
	bp.Binds = extractBinds(codegen.NodeProp(n, "binds"))
	bp.Events = extractEvents(codegen.NodeProp(n, "events"))

	return bp
}

// extractJoinDir reads a JoinDir enum member off a prop value. A bare enum
// member appears as an *ir.Ident with Member set; a qualified `JoinDir.horizontal`
// (the form the platform bodies use) survives as an *ir.Select whose Field names
// the member. Either shape with the "horizontal" member selects the horizontal
// axis; anything else (incl. "vertical") defaults to vertical.
func extractJoinDir(e ir.Expr) joinDir {
	switch v := e.(type) {
	case *ir.Ident:
		if v.Member == "horizontal" {
			return joinHorizontal
		}
	case *ir.Select:
		if v.Field == "horizontal" {
			return joinHorizontal
		}
	}
	return joinVertical
}

// extractFocus reads a `Focus{enabled=...}` struct literal.
func extractFocus(e ir.Expr) bool {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return false
	}
	if v := structField(sl, "enabled"); v != nil {
		b, _ := codegen.IRLiteralBool(v)
		return b
	}
	return false
}

// extractModel reads a `Model{...}` struct literal.
func extractModel(e ir.Expr) modelMeta {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return modelMeta{}
	}
	var m modelMeta
	for _, f := range sl.Fields {
		s, _ := codegen.IRLiteralString(f.Value)
		switch f.Name {
		case "type":
			m.Type = s
		case "new":
			m.New = s
		case "view":
			m.View = s
		case "update":
			m.Update = s
		case "init":
			m.Init = s
		case "pkg":
			m.Pkg = s
		}
	}
	return m
}

// extractBinds reads a `[Bind{...}, ...]` list-of-structs prop. Lists appear in
// IR as an *ir.ListLit whose Elems are *ir.StructLit.
func extractBinds(e ir.Expr) []bindMeta {
	ll, ok := e.(*ir.ListLit)
	if !ok {
		return nil
	}
	out := make([]bindMeta, 0, len(ll.Elems))
	for _, el := range ll.Elems {
		sl, ok := el.(*ir.StructLit)
		if !ok {
			continue
		}
		var b bindMeta
		if v := structField(sl, "prop"); v != nil {
			b.Prop, _ = codegen.IRLiteralString(v)
		}
		if v := structField(sl, "get"); v != nil {
			b.Get, _ = codegen.IRLiteralString(v)
		}
		if v := structField(sl, "set"); v != nil {
			b.Set, _ = codegen.IRLiteralString(v)
		}
		out = append(out, b)
	}
	return out
}

// extractEvents reads a `[Event{...}, ...]` list-of-structs prop.
func extractEvents(e ir.Expr) []eventMeta {
	ll, ok := e.(*ir.ListLit)
	if !ok {
		return nil
	}
	out := make([]eventMeta, 0, len(ll.Elems))
	for _, el := range ll.Elems {
		sl, ok := el.(*ir.StructLit)
		if !ok {
			continue
		}
		var ev eventMeta
		if v := structField(sl, "on"); v != nil {
			ev.On, _ = codegen.IRLiteralString(v)
		}
		if v := structField(sl, "key"); v != nil {
			ev.Key, _ = codegen.IRLiteralString(v)
		}
		out = append(out, ev)
	}
	return out
}

// structField returns the value of a named field in a struct literal, or nil.
func structField(sl *ir.StructLit, name string) ir.Expr {
	for _, f := range sl.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}
