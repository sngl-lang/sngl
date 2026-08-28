package fyne

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// intrinsicPrefix is the namespace every fyne primitive's #[intrinsic] id
// carries. The prefix is the platform whose codegen answers to the id, so a
// fyne primitive can never collide with a stdlib intrinsic or with another
// platform's.
const intrinsicPrefix = "fyne:"

// specPropName is the primitive prop carrying the Spec record. Renaming it
// here means renaming it in fyne.sngl.
const specPropName = "spec"

// fyneArg is one argument the Go constructor is called with, decoded from the
// SNGL `Arg` record. Prop names a value prop of the primitive; when the
// instantiation supplied it, its expression is the argument. Raw is Go source
// spliced verbatim, and is the fallback when Prop names a prop the caller left
// out — a Fyne constructor takes its content up front, so the argument has to
// be something.
type fyneArg struct {
	Raw  string
	Prop string
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
type fyneSpec struct {
	New     string
	Args    []fyneArg
	GoType  string
	Imports []string
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
	// Handlers maps a declared event to its callback field.
	Handlers map[string]fyneHandler
	// CtorProps holds the expression the instantiation gave each prop an Arg
	// names. Harvested from the prop assignments lowering emits immediately
	// after the node's CreateNode, which is the only place they are known to
	// be in scope.
	CtorProps map[string]ir.Expr
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
	lit, ok := props[specPropName].(*ir.StructLit)
	if !ok {
		return nil, fmt.Errorf("fyne primitive %s was instantiated without a %s record", tag, specPropName)
	}
	sp := &fyneSpec{
		Setters:   map[string]string{},
		Handlers:  map[string]fyneHandler{},
		CtorProps: props,
	}
	for _, f := range lit.Fields {
		switch f.Name {
		case "new":
			sp.New, _ = codegen.IRLiteralString(f.Value)
		case "goType":
			sp.GoType, _ = codegen.IRLiteralString(f.Value)
		case "add":
			sp.Add, _ = codegen.IRLiteralString(f.Value)
		case "content":
			sp.Content, _ = codegen.IRLiteralString(f.Value)
		case "imports":
			for _, e := range listElems(f.Value) {
				if s, ok := codegen.IRLiteralString(e); ok && s != "" {
					sp.Imports = append(sp.Imports, s)
				}
			}
		case "args":
			for _, e := range listElems(f.Value) {
				sl, ok := e.(*ir.StructLit)
				if !ok {
					continue
				}
				var a fyneArg
				a.Raw, _ = codegen.IRLiteralString(structField(sl, "raw"))
				a.Prop, _ = codegen.IRLiteralString(structField(sl, "prop"))
				sp.Args = append(sp.Args, a)
			}
		case "setters":
			for _, e := range listElems(f.Value) {
				sl, ok := e.(*ir.StructLit)
				if !ok {
					continue
				}
				prop, _ := codegen.IRLiteralString(structField(sl, "prop"))
				call, _ := codegen.IRLiteralString(structField(sl, "call"))
				if prop != "" && call != "" {
					sp.Setters[prop] = call
				}
			}
		case "handlers":
			for _, e := range listElems(f.Value) {
				sl, ok := e.(*ir.StructLit)
				if !ok {
					continue
				}
				on, _ := codegen.IRLiteralString(structField(sl, "on"))
				var h fyneHandler
				h.Field, _ = codegen.IRLiteralString(structField(sl, "field"))
				h.Signature, _ = codegen.IRLiteralString(structField(sl, "signature"))
				h.Param, _ = codegen.IRLiteralString(structField(sl, "param"))
				if on != "" && h.Field != "" {
					sp.Handlers[on] = h
				}
			}
		}
	}
	if sp.New == "" || sp.GoType == "" {
		return nil, fmt.Errorf("fyne primitive %s: Spec needs both `new` and `goType`", tag)
	}
	return sp, nil
}

// ctorArgs renders this widget's constructor arguments. A prop-backed Arg
// resolves to the expression the instantiation gave that prop; when the prop
// was left out, the Arg's raw text stands in.
func (s *fyneSpec) ctorArgs() []ir.Expr {
	out := make([]ir.Expr, 0, len(s.Args))
	for _, a := range s.Args {
		if a.Prop != "" {
			if e, ok := s.CtorProps[a.Prop]; ok && e != nil {
				out = append(out, e)
				continue
			}
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
