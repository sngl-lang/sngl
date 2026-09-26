package bubbletea

import (
	"fmt"
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
	// ${w}/${h} resolve to the inset, clamped terminal width/height a widget
	// should occupy. The tui helpers centralize the margin/clamp and the
	// zero-fallback (so a pre-WindowSizeMsg m.width==0 never sizes a widget to
	// 0). Used by resize templates (.SetWidth(${w}) / .SetSize(${w}, ${h})).
	if token == "w" {
		gc.RequireImport(tuiImportPath)
		return "tui.WidgetWidth(m.width)"
	}
	if token == "h" {
		gc.RequireImport(tuiImportPath)
		return "tui.WidgetHeight(m.height)"
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
	// bpFlow and bpSpan are the rich-text pair. Unlike the four above they
	// are decided by the #[intrinsic] id alone: a Span carries no prop that
	// is its own, since a run of words with nothing said about it is the
	// common case.
	bpFlow
	bpSpan
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
	// Resize is an optional method-chain template (e.g. ".SetWidth(${w})")
	// applied to the widget field on every terminal-size change. Emitted as
	// `m.<field><Resize>` from the generated resizeWidgets() helper. Empty for
	// widgets with no size (spinner).
	Resize string
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
// btIntrinsic returns the primitive a node resolved to — "Layout", "Styled",
// "Widget", "Overlay" — or "" for anything that is not one. Read off the
// #[intrinsic] id on the declaration rather than the node's name: a stdlib
// wrapper inlines to one of these, and matching the name would also match a
// user component that happened to be called Widget.
func btIntrinsic(n *ir.NodeInst) string {
	if n == nil || n.Component == nil {
		return ""
	}
	id, ok := strings.CutPrefix(n.Component.Intrinsic, "bubbletea:")
	if !ok {
		return ""
	}
	return id
}

func extractBlueprint(n *ir.NodeInst) blueprint {
	var bp blueprint

	modelProp := codegen.NodeProp(n, "model")
	joinProp := codegen.NodeProp(n, "join")
	placementProp := codegen.NodeProp(n, "placement")

	switch {
	case btIntrinsic(n) == "Flow":
		bp.Kind = bpFlow
	case btIntrinsic(n) == "Span":
		bp.Kind = bpSpan
	case btIntrinsic(n) == "Overlay" || placementProp != nil:
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

// extractJoinDir reads a JoinDir enum member off a prop value: an *ir.Ident
// with Member set, which is what the checker makes of a qualified
// `JoinDir.horizontal` too. "horizontal" selects the horizontal axis and
// "vertical" the vertical one.
//
// `join` is const, so the optimizer has folded it to a member; anything else
// is a compiler bug, not a program to lay out vertically.
func extractJoinDir(e ir.Expr) joinDir {
	v, ok := e.(*ir.Ident)
	if !ok || v.Member == "" {
		panic(fmt.Sprintf("internal: bubbletea Layout.join not folded to a JoinDir member: %T", e))
	}
	if v.Member == "horizontal" {
		return joinHorizontal
	}
	return joinVertical
}

// descriptorString reads one string field of a descriptor record. The record
// is a const prop, folded to literals by the optimizer, so a field that is not
// one is a compiler bug rather than a field to leave empty.
func descriptorString(what string, e ir.Expr) string {
	s, ok := codegen.IRLiteralString(e)
	if !ok {
		panic(fmt.Sprintf("internal: bubbletea %s not folded to a string literal: %T", what, e))
	}
	return s
}

// descriptorList reads a const list prop, folded to a list literal. nil for a
// prop the instantiation left out.
func descriptorList(what string, e ir.Expr) []ir.Expr {
	if e == nil {
		return nil
	}
	ll, ok := e.(*ir.ListLit)
	if !ok {
		panic(fmt.Sprintf("internal: bubbletea %s not folded to a list literal: %T", what, e))
	}
	return ll.Elems
}

// descriptorRecord reads one record of a const descriptor, folded to a struct
// literal.
func descriptorRecord(what string, e ir.Expr) *ir.StructLit {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		panic(fmt.Sprintf("internal: bubbletea %s not folded to a struct literal: %T", what, e))
	}
	return sl
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
	sl := descriptorRecord("Widget.model", e)
	var m modelMeta
	for _, f := range sl.Fields {
		s := descriptorString("Widget.model."+f.Name, f.Value)
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
		case "resize":
			m.Resize = s
		}
	}
	return m
}

// extractBinds reads a `[Bind{...}, ...]` list-of-structs prop. Lists appear in
// IR as an *ir.ListLit whose Elems are *ir.StructLit.
func extractBinds(e ir.Expr) []bindMeta {
	elems := descriptorList("Widget.binds", e)
	if elems == nil {
		return nil
	}
	out := make([]bindMeta, 0, len(elems))
	for _, el := range elems {
		sl := descriptorRecord("Widget.binds", el)
		var b bindMeta
		if v := structField(sl, "prop"); v != nil {
			b.Prop = descriptorString("Bind.prop", v)
		}
		if v := structField(sl, "get"); v != nil {
			b.Get = descriptorString("Bind.get", v)
		}
		if v := structField(sl, "set"); v != nil {
			b.Set = descriptorString("Bind.set", v)
		}
		out = append(out, b)
	}
	return out
}

// extractEvents reads a `[Event{...}, ...]` list-of-structs prop.
func extractEvents(e ir.Expr) []eventMeta {
	elems := descriptorList("Styled.events", e)
	if elems == nil {
		return nil
	}
	out := make([]eventMeta, 0, len(elems))
	for _, el := range elems {
		sl := descriptorRecord("Styled.events", el)
		var ev eventMeta
		if v := structField(sl, "on"); v != nil {
			ev.On = descriptorString("Event.on", v)
		}
		if v := structField(sl, "key"); v != nil {
			ev.Key = descriptorString("Event.key", v)
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
