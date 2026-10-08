package fyne

import (
	"fmt"
	"sort"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// WidgetProbeSource pulls this platform in and does nothing else, which is all
// it takes to reach what the platform declares: an override's body is a
// declaration, so nothing has to be instantiated to find it.
const WidgetProbeSource = `
import . "sngl:ui"
import "sngl:platform/fyne"
output { go { fyne() } }
component main node {}
`

// OverriddenComponents lists the stdlib components this platform gives a body
// to, read off the checked symbol table.
func OverriddenComponents(pkg *ir.Package) []*ir.Component {
	if pkg == nil || pkg.Symbols == nil {
		return nil
	}
	var out []*ir.Component
	pkg.Symbols.EachSymbol(func(sym ir.Symbol) bool {
		c, ok := sym.(*ir.Component)
		if !ok {
			return true
		}
		if b, has := c.PlatformOverrides["fyne"]; has && len(b.Stmts) > 0 {
			out = append(out, c)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BareStdlibComponents lists the stdlib components this platform gives no body
// to, and should be empty: a component reaching this list is a widget nothing
// here builds.
//
// A #[builtin] kind is not one of them. `context`, `effect` and `output` are declared
// as components because that is the form their call site takes, but a visual
// tree dispatches them to a compiler construct rather than to a widget, so
// there is no body for a platform to write. They are recognised by their kind
// and not by their names, for the reason every built-in is: the name is
// shadowable and carries no meaning, and a list of names can only fail when
// someone edits one copy of it.
func BareStdlibComponents(pkg *ir.Package) []string {
	if pkg == nil || pkg.Symbols == nil {
		return nil
	}
	var out []string
	pkg.Symbols.EachSymbol(func(sym ir.Symbol) bool {
		c, ok := sym.(*ir.Component)
		if !ok || !c.Stdlib || c.Builtin != ir.BuiltinNone {
			return true
		}
		if b, has := c.PlatformOverrides["fyne"]; !has || len(b.Stmts) == 0 {
			out = append(out, c.Name)
		}
		return true
	})
	sort.Strings(out)
	return out
}

// WidgetSpec is what one stdlib component needs to be built at runtime.
//
// Less than the internal spec: no Go type and no callback signature, since a
// host holds the value and reflects both off it. The constructor is the one
// thing reflection cannot recover.
type WidgetSpec struct {
	// Element is the SNGL name, which is what reaches a host: an interpreted
	// program is checked without overrides inlined, so a window is asked for
	// `text`, never `Label`.
	Element string
	// CtorPath and CtorName locate the constructor, e.g.
	// "fyne.io/fyne/v2/widget" and "NewLabel".
	CtorPath, CtorName string
	// Setters maps a prop to the method that assigns it.
	Setters map[string]string
	// Handlers maps a declared event to its callback field.
	Handlers map[string]string
	// Add is a container's attach method; Content a wrapper's child field.
	Add, Content string
}

// overrideNode is the widget instantiation a stdlib component's fyne body
// writes. The rename lives there rather than on the primitive: by the time the
// walk reaches a primitive it is inside the widget declaration, where the props
// are that declaration's own.
func overrideNode(comp *ir.Component) *ir.NodeInst {
	for _, s := range comp.PlatformOverrides["fyne"].Stmts {
		if n, ok := s.(*ir.NodeInst); ok {
			return n
		}
	}
	return nil
}

// propRenames maps a stdlib component's own prop names to the widget prop each
// one is passed into, read off the override's instantiation.
//
// Only a bare reference to one of the component's props counts, matched by
// symbol rather than by name: an override may pass a literal or a local, and
// neither is a prop a caller can set.
func propRenames(comp *ir.Component, node *ir.NodeInst) map[string]string {
	if node == nil {
		return nil
	}
	own := map[ir.Symbol]string{}
	for _, p := range comp.Props {
		if p.Sym != nil {
			own[p.Sym] = p.Name
		}
	}
	out := map[string]string{}
	for _, arg := range node.Props {
		id, ok := arg.Value.(*ir.Ident)
		if !ok || id.Sym == nil {
			continue
		}
		if name, isProp := own[id.Sym]; isProp {
			out[name] = arg.Name
		}
	}
	return out
}

// WidgetSpecsFrom decodes the registry from a package that has been checked
// with this platform registered and instantiates AllComponentsSource.
//
// The checking is the caller's, deliberately: this package must not depend on
// the checker, and a generator is where parsing belongs anyway.
func WidgetSpecsFrom(pkg *ir.Package) ([]WidgetSpec, error) {
	comps := OverriddenComponents(pkg)
	if len(comps) == 0 {
		return nil, fmt.Errorf("no component carries a fyne override; was the platform registered when checking?")
	}

	var out []WidgetSpec
	for _, comp := range comps {
		name := comp.Name
		prims := fynePrimitiveNodes(comp.PlatformOverrides["fyne"].Stmts)
		if len(prims) == 0 {
			return nil, fmt.Errorf("%s: its fyne body reaches no primitive", name)
		}
		sp, err := specFromProps(prims[0].Name, nodeProps(prims[0]))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		w := WidgetSpec{
			Element:  name,
			CtorPath: sp.New.Path,
			CtorName: sp.New.Name,
			Add:      sp.Add,
			Content:  sp.Content,
		}
		// A Spec's setters are keyed by the *primitive's* prop vocabulary --
		// `text`, `placeholder`, `flag` -- while a host is handed the stdlib
		// component's own prop names. The override is what bridges them:
		// `component ui.text[platform] { Label(text=value) }` says this
		// component's `value` is that widget's `text`. Without following it a
		// registry looks right and sets nothing.
		rename := propRenames(comp, overrideNode(comp))
		if len(sp.Setters) > 0 {
			w.Setters = map[string]string{}
			for own, widgetProp := range rename {
				if method, ok := sp.Setters[widgetProp]; ok {
					w.Setters[own] = method
				}
			}
			if len(w.Setters) == 0 {
				// Every setter the widget offers is fed by nothing the
				// component declares, so none of them can ever fire.
				w.Setters = nil
			}
		}
		if len(sp.Handlers) > 0 {
			w.Handlers = map[string]string{}
			for k, h := range sp.Handlers {
				w.Handlers[k] = h.Field
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// fynePrimitiveNodes collects every fyne primitive instantiation reachable
// from stmts, following a non-primitive component into its own body — which is
// how a widget declaration (`component Label { Widget(spec=...) }`) is reached
// from an override that names it.
func fynePrimitiveNodes(stmts []ir.Stmt) []*ir.NodeInst {
	var out []*ir.NodeInst
	seen := map[*ir.Component]bool{}
	var walk func([]ir.Stmt)
	walk = func(s []ir.Stmt) {
		codegen.WalkVisualTree(s, func(n *ir.NodeInst, _ int) bool {
			if n.Component == nil {
				return false
			}
			if fynePrimitive(n.Component) != "" {
				out = append(out, n)
				return false
			}
			if !seen[n.Component] {
				seen[n.Component] = true
				walk(n.Component.Body)
			}
			return false
		})
	}
	walk(stmts)
	return out
}

// nodeProps is the node's props by name, the shape specFromProps decodes. In a
// real build these arrive as the assignments lowering emits after CreateNode;
// here they are read straight off the checked node.
func nodeProps(n *ir.NodeInst) map[string]ir.Expr {
	out := map[string]ir.Expr{}
	for _, p := range n.Props {
		out[p.Name] = p.Value
	}
	return out
}
