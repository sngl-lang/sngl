package fyne

import (
	"fmt"
	astgo "go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// intrinsicPrefix is the namespace every fyne primitive's #[intrinsic] id
// carries. The prefix is the platform whose codegen answers to the id, so a
// fyne primitive can never collide with a stdlib intrinsic or with another
// platform's.
const intrinsicPrefix = "fyne:"

// specPropName is the primitive prop carrying the Spec record. Renaming it
// here means renaming it in fyne.sngl.
const specPropName = "spec"

// stylePropName is the primitive prop carrying the sngl.Style record.
const stylePropName = "style"

// fyneArg is one argument the Go constructor is called with, decoded from the
// SNGL `Arg` record. Prop names a value prop of the primitive; when the
// instantiation supplied it, its expression is the argument. Raw is Go source
// spliced verbatim, and is the fallback when Prop names a prop the caller left
// out — a Fyne constructor takes its content up front, so the argument has to
// be something.
type fyneArg struct {
	Raw  string
	Prop string
	// Type is the Go declaration a Raw argument continues from, when Raw is a
	// call or a literal belonging to a package this file has to name. The
	// alias is the emitting context's to assign, so the Native is qualified
	// where the argument is rendered and Raw is whatever follows it --
	// `fynetext.Style` plus `().WithBold()`. Empty for the arguments a Spec
	// writes in fyne.sngl, which name no package.
	Type fyneNative
}

// fyneHandler is the Fyne callback field one declared event is assigned to,
// decoded from the SNGL `Handler` record. See that declaration for why
// Signature is Fyne's shape rather than the SNGL event's, and what Param is
// for.
type fyneHandler struct {
	Field     string
	Signature string
	Param     string
}

// fyneSpec is one widget's construction, decoded from the SNGL `Spec` record
// the primitive was instantiated with. Everything this platform knows about
// Fyne arrives here: no Go code below names a Fyne type, a constructor, or a
// component.
// fyneNative is a Go declaration reference: the import path and the
// identifier in it. The alias it is qualified with is not here, because it is
// not the Spec's to choose -- see fyneSpec.qualify.
type fyneNative struct {
	Path string
	Name string
}

type fyneSpec struct {
	// toplevel is the platform's own Toplevel (toplevelSpec).
	toplevel bool
	New      fyneNative
	Args     []fyneArg
	GoType   fyneNative
	// Add is the method a multi-child container attaches each child with;
	// Content the field a single-child container assigns its child to. Which
	// applies is decided by Content being set, because that is the case with
	// no method to call.
	Add     string
	Content string
	// Setters maps a value prop to the Go method it becomes, without the
	// receiver: "SetText" emits `w.SetText(v)`. A prop absent here has no
	// Fyne surface to reach, so the assignment is dropped.
	Setters map[string]string
	// SetterArgs splits a prop's value into the setter's arguments, for a
	// setter that takes a record's fields rather than the record: a span's
	// style is a SNGL struct the runtime has no type for.
	SetterArgs map[string]func(ir.Expr) []ir.Expr
	// Currents maps a prop to the Go field holding the value its setter
	// writes, for a widget whose setter fires its own change callback even
	// when the value is the one it holds: the assignment is skipped then, or
	// a binding's write-back re-syncing the widget from inside that callback
	// calls it again until the stack runs out (Select.SetSelected).
	Currents map[string]string
	// Handlers maps a declared event to its callback field.
	Handlers map[string]fyneHandler
	// CtorProps holds the expression the instantiation gave each prop an Arg
	// names. Harvested from the prop assignments lowering emits immediately
	// after the node's CreateNode, which is the only place they are known to
	// be in scope.
	CtorProps map[string]ir.Expr
	// CtorOnly names the props the constructor reads and no setter can write
	// again. The write that seeded CtorProps is answered by the constructor;
	// any other write to one of these reaches the screen not at all.
	CtorOnly map[string]bool
	// Axis is "horizontal"/"vertical" on a container that lays children out
	// along one, empty otherwise.
	Axis string
	// Style is what the node's own `style` prop asked for, of the fields a
	// layout can answer. These reach Fyne without a Setter because none of
	// them is a method on this widget: flex and margin are read by the
	// *parent's* layout, gap and padding by this one's.
	Style fyneStyle
	// Children names this container's children in the order they are appended,
	// which is the order a layout is handed them in.
	Children []string
	// ThemeVar is the generated theme this node's paint styles resolved to,
	// empty when it asked for none. See theme.go.
	ThemeVar string
}

// fyneStyle is the part of sngl.Style this platform can answer, in the
// device-independent pixels Fyne measures in.
//
// The layout half is read by a layout -- flex and margin by the *parent's*,
// gap and padding by this box's own. The paint half is read by a theme, which
// is the only per-widget styling Fyne has: see theme.go.
type fyneStyle struct {
	Flex    float64
	Margin  float64
	Gap     float64
	Padding float64

	Background   *fyneColor
	Color        *fyneColor
	FontSize     float64
	BorderRadius float64
	Bold         bool
	Italic       bool
}

// fyneColor is an RGBA colour, comparable so a set of styles dedupes to a set
// of themes.
type fyneColor struct{ R, G, B, A uint8 }

// aliaser assigns the alias an import path's symbols are qualified with. The
// Go context implements it; see golang.GoIRContext.AliasFor.
type aliaser interface {
	AliasFor(path string) string
}

// qualify renders a Native as Go source: the identifier, qualified with
// whatever alias the emitting context assigns its path. A Native with no path
// is a bare identifier and is emitted as written.
//
// The prefix is the pointer and slice syntax a type carries, which sits
// outside the qualification: "*Container" in fyne.io/fyne/v2 is
// "*<alias>.Container".
func (n fyneNative) qualify(a aliaser) string {
	if n.Name == "" {
		return ""
	}
	if n.Path == "" || a == nil {
		return n.Name
	}
	alias := a.AliasFor(n.Path)
	if alias == "" {
		return n.Name
	}
	i := 0
	for i < len(n.Name) && (n.Name[i] == '*' || n.Name[i] == '[' || n.Name[i] == ']') {
		i++
	}
	return n.Name[:i] + alias + "." + n.Name[i:]
}

// isSingleChild reports whether this widget takes its child through a field
// rather than through a method.
func (s *fyneSpec) isSingleChild() bool { return s.Content != "" }

// addMethod is the method one child is attached with. Fyne's containers all
// spell it `Add`, so a Spec that says nothing gets that.
func (s *fyneSpec) addMethod() string {
	if s.Add != "" {
		return s.Add
	}
	return "Add"
}

// fynePrimitive returns the primitive a component resolved to — "Widget",
// "Container", "Wrapper" — or "" for anything that is not one. Read off the
// #[intrinsic] id rather than the declaration's name: every widget in
// fyne.sngl inlines down to one of these, and matching the name would also
// match a user component that happened to be called Widget.
func fynePrimitive(comp *ir.Component) string {
	if comp == nil {
		return ""
	}
	id, ok := strings.CutPrefix(comp.Intrinsic, intrinsicPrefix)
	if !ok {
		return ""
	}
	return id
}

// specFromProps decodes the Spec a primitive node was instantiated with.
// props is the node's prop expressions by name, harvested from the
// assignments lowering emits after CreateNode.
//
// A primitive with no decodable Spec is an authoring error rather than a node
// to skip: skipping it drops the widget while leaving the AppendChild that
// names it behind, which is issue #120's shape — output that does not compile,
// from a build that reported success.
func specFromProps(tag string, props map[string]ir.Expr) (*fyneSpec, error) {
	if markupTags(tag) {
		return markupSpec(tag, props)
	}
	if tag == toplevelTag {
		return toplevelSpec(props), nil
	}
	raw := props[specPropName]
	if raw == nil {
		return nil, fmt.Errorf("fyne primitive %s was instantiated without a %s record", tag, specPropName)
	}
	lit := specRecord(tag, specPropName, raw)
	sp := &fyneSpec{
		Setters:   map[string]string{},
		Currents:  map[string]string{},
		Handlers:  map[string]fyneHandler{},
		CtorProps: props,
	}
	for _, f := range lit.Fields {
		switch f.Name {
		case "new":
			sp.New = nativeFromExpr(tag, f.Value)
		case "goType":
			sp.GoType = nativeFromExpr(tag, f.Value)
		case "add":
			sp.Add = specString(tag, "spec.add", f.Value)
		case "content":
			sp.Content = specString(tag, "spec.content", f.Value)
		case "axis":
			sp.Axis = specString(tag, "spec.axis", f.Value)
		case "args":
			for _, e := range specList(tag, "spec.args", f.Value) {
				sl := specRecord(tag, "spec.args", e)
				var a fyneArg
				a.Raw = specString(tag, "Arg.raw", structField(sl, "raw"))
				a.Prop = specString(tag, "Arg.prop", structField(sl, "prop"))
				sp.Args = append(sp.Args, a)
			}
		case "setters":
			for _, e := range specList(tag, "spec.setters", f.Value) {
				sl := specRecord(tag, "spec.setters", e)
				prop := specString(tag, "Setter.prop", structField(sl, "prop"))
				call := specString(tag, "Setter.call", structField(sl, "call"))
				if prop != "" && call != "" {
					sp.Setters[prop] = call
				}
				if cur := specString(tag, "Setter.current", structField(sl, "current")); prop != "" && cur != "" {
					sp.Currents[prop] = cur
				}
			}
		case "handlers":
			for _, e := range specList(tag, "spec.handlers", f.Value) {
				sl := specRecord(tag, "spec.handlers", e)
				on := specString(tag, "Handler.on", structField(sl, "on"))
				var h fyneHandler
				h.Field = specString(tag, "Handler.field", structField(sl, "field"))
				h.Signature = specString(tag, "Handler.signature", structField(sl, "signature"))
				h.Param = specString(tag, "Handler.param", structField(sl, "param"))
				if _, err := signatureParams(tag, on, h.Signature); err != nil {
					return nil, err
				}
				if on != "" && h.Field != "" {
					sp.Handlers[on] = h
				}
			}
		}
	}
	if sp.New.Name == "" || sp.GoType.Name == "" {
		return nil, fmt.Errorf("fyne primitive %s: Spec needs both `new` and `goType`", tag)
	}
	sp.Style = styleFromProps(props)
	return sp, nil
}

// styleFromProps reads the layout fields off the node's `style` record. A
// field whose expression is not a literal is left at zero: the layout is built
// once with the widget tree, so a flex that varies with state is not something
// it can answer.
func styleFromProps(props map[string]ir.Expr) fyneStyle {
	var st fyneStyle
	lit, ok := props[stylePropName].(*ir.StructLit)
	if !ok {
		return st
	}
	for _, f := range lit.Fields {
		switch f.Name {
		case "background":
			st.Background = colorFromExpr(f.Value)
		case "color":
			st.Color = colorFromExpr(f.Value)
		case "fontWeight":
			// Written as a string and typed as an enum, so it is the member
			// after the optimizer folded it and the string before -- both
			// reach here, since a caller may generate from unoptimized IR.
			// Fyne has two faces, so everything but bold is the regular one.
			st.Bold = enumOrString(f.Value) == "bold"
			continue
		case "fontStyle":
			st.Italic = enumOrString(f.Value) == "italic"
			continue
		}
		var into *float64
		switch f.Name {
		case "flex":
			into = &st.Flex
		case "margin":
			into = &st.Margin
		case "gap":
			into = &st.Gap
		case "padding":
			into = &st.Padding
		case "fontSize":
			into = &st.FontSize
		case "borderRadius":
			into = &st.BorderRadius
		default:
			continue
		}
		if v, ok := literalNumber(f.Value); ok {
			*into = v
		}
	}
	return st
}

// enumOrString reads an enum member, or a plain string, as its name.
func enumOrString(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok && id.Member != "" {
		return id.Member
	}
	v, _ := codegen.IRLiteralString(e)
	return v
}

// colorFromExpr reads a `color{r=.., g=.., b=.., a=..}` literal. A colour that
// is not a literal is no colour: the theme is built once with the widget tree.
func colorFromExpr(e ir.Expr) *fyneColor {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return nil
	}
	var c fyneColor
	into := map[string]*uint8{"r": &c.R, "g": &c.G, "b": &c.B, "a": &c.A}
	seen := 0
	for _, f := range sl.Fields {
		p, ok := into[f.Name]
		if !ok {
			continue
		}
		v, ok := literalNumber(f.Value)
		if !ok {
			return nil
		}
		*p = uint8(v)
		seen++
	}
	if seen == 0 {
		return nil
	}
	return &c
}

// literalNumber reads a numeric literal, with or without a unit suffix: a
// Style measurement is written `6` or `6px` and both mean the same number of
// device-independent pixels to Fyne.
func literalNumber(e ir.Expr) (float64, bool) {
	lit, ok := e.(*ir.Literal)
	if !ok || lit == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ctorArgs renders this widget's constructor arguments. A prop-backed Arg
// resolves to the expression the instantiation gave that prop; when the prop
// was left out, the Arg's raw text stands in.
func (s *fyneSpec) ctorArgs(al aliaser) []ir.Expr {
	out := make([]ir.Expr, 0, len(s.Args))
	for _, a := range s.Args {
		if a.Prop != "" {
			if e, ok := s.CtorProps[a.Prop]; ok && e != nil {
				out = append(out, e)
				continue
			}
		}
		if a.Type.Name != "" {
			out = append(out, rawGoExpr(a.Type.qualify(al)+a.Raw))
			continue
		}
		out = append(out, rawGoExpr(a.Raw))
	}
	return out
}

// rawGoExpr wraps Go source so the language renderer emits it verbatim. An
// ir.Ident resolving to nothing renders as its name, which is what makes a
// Spec able to name a constructor argument — `nil`, `""`, a numeric default —
// without this package holding a table of the ones it knows.
func rawGoExpr(src string) ir.Expr {
	return &ir.Ident{Name: src, Type: ir.TypDyn}
}

// listElems returns the elements of an IR list literal, or nil.
func listElems(e ir.Expr) []ir.Expr {
	if ll, ok := e.(*ir.ListLit); ok {
		return ll.Elems
	}
	return nil
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

// signatureParams parses a callback signature into its parameters, one name
// and Go type each.
//
// Parsed as Go rather than split on spaces: the parameters become the promoted
// handler's, and the handler body refers to one of them by the name Spec's
// `param` gives -- so what counts as a parameter has to be what Go says, not
// what a delimiter guess says. Two string heuristics that disagreed is how an
// unnamed parameter got dropped from `func(string)` while `func(v, w int)`,
// which names every one of them, was rejected.
func signatureParams(tag, on, sig string) ([]*ir.Param, error) {
	if sig == "" {
		return nil, nil
	}
	expr, err := parser.ParseExpr(sig + "{}")
	if err != nil {
		return nil, fmt.Errorf("fyne primitive %s: handler %q signature %q is not a Go func type: %v", tag, on, sig, err)
	}
	fn, ok := expr.(*astgo.FuncLit)
	if !ok || fn.Type == nil {
		return nil, fmt.Errorf("fyne primitive %s: handler %q signature %q is not a func type", tag, on, sig)
	}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		return nil, fmt.Errorf("fyne primitive %s: handler %q signature %q returns a value; "+
			"the promoted handler returns nothing", tag, on, sig)
	}
	var out []*ir.Param
	if fn.Type.Params == nil {
		return out, nil
	}
	for _, f := range fn.Type.Params.List {
		var buf strings.Builder
		if err := printer.Fprint(&buf, token.NewFileSet(), f.Type); err != nil {
			return nil, fmt.Errorf("fyne primitive %s: handler %q signature %q: %v", tag, on, sig, err)
		}
		if len(f.Names) == 0 {
			return nil, fmt.Errorf("fyne primitive %s: handler %q signature %q needs a name for every "+
				"parameter (the %s has none); the handler body refers to one by name", tag, on, sig, buf.String())
		}
		for _, n := range f.Names {
			out = append(out, &ir.Param{
				Name: n.Name,
				Type: ir.NativeGoNamed(buf.String()),
			})
		}
	}
	return out, nil
}

// nativeFromExpr reads a Native record. A bare string is accepted as the
// identifier alone, which is what a Go builtin or a name already in scope
// needs.
func nativeFromExpr(tag string, e ir.Expr) fyneNative {
	if sl, ok := e.(*ir.StructLit); ok {
		var n fyneNative
		n.Path = specString(tag, "Native.path", structField(sl, "path"))
		n.Name = specString(tag, "Native.name", structField(sl, "name"))
		return n
	}
	return fyneNative{Name: specString(tag, "Native", e)}
}

// The Spec is a const prop, so the optimizer has folded it to literals all the
// way down (optimize.foldConstArg). A part of it that is not one is a compiler
// bug, and reading it as empty would build a widget out of nothing. A field
// the record leaves out is nil, which is the empty the declaration defaults to.

func specString(tag, what string, e ir.Expr) string {
	if e == nil {
		return ""
	}
	s, ok := codegen.IRLiteralString(e)
	if !ok {
		panic(fmt.Sprintf("internal: fyne primitive %s: %s not folded to a string literal: %T", tag, what, e))
	}
	return s
}

func specList(tag, what string, e ir.Expr) []ir.Expr {
	if e == nil {
		return nil
	}
	ll, ok := e.(*ir.ListLit)
	if !ok {
		panic(fmt.Sprintf("internal: fyne primitive %s: %s not folded to a list literal: %T", tag, what, e))
	}
	return ll.Elems
}

func specRecord(tag, what string, e ir.Expr) *ir.StructLit {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		panic(fmt.Sprintf("internal: fyne primitive %s: %s not folded to a struct literal: %T", tag, what, e))
	}
	return sl
}
