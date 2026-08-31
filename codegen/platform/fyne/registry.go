package fyne

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// StdlibComponents is every stdlib component this platform overrides.
//
// It is data rather than a test fixture because two things read it: the tests
// that hold each override to having a body, and the generator that emits the
// worker's widget registry from those bodies. Two copies would drift, and the
// drift would be a widget the tests cover and no window can build.
var StdlibComponents = []string{
	"vbox", "hbox", "stack", "scroll", "spacer", "card", "splitview",
	"modal", "drawer", "popover", "tooltip",
	"tabs", "toolbar", "menubar", "menu",
	"table", "tree", "radio",
	"text", "badge", "chip", "divider", "avatar", "spinner", "progress", "datepicker",
	"button", "link", "image", "checkbox", "toggle", "select",
	"input", "textarea",
}

// AllComponentsSource instantiates every name in StdlibComponents so the
// checked IR carries a NodeInst pointing at each stdlib *ir.Component -- which
// is the only way to reach the Spec each override lowers to, since a Spec is a
// prop on an instantiation rather than anything a declaration holds.
const AllComponentsSource = `
import . "sngl:ui"
import "sngl:platform/fyne"
output { go { fyne() } }
component main {
    vbox {
        hbox {}
        stack {}
        scroll { text(value="in scroll") }
        spacer()
        card {}
        splitview {}
        modal {}
        drawer {}
        popover {}
        tooltip { text(value="tipped") }
        tabs {}
        toolbar {}
        menubar()
        menu()
        table()
        tree()
        radio()
        text(value="t")
        badge(value="b")
        chip(label="c")
        divider()
        avatar(initials="AB")
        spinner(label="s")
        progress(value=0.5)
        datepicker(placeholder="pick")
        button(text="go")
        link(href="/x", text="l")
        image(src="/i.png")
        checkbox(label="c")
        toggle(label="t")
        select(options=["a"])
        input(placeholder="p")
        textarea(placeholder="p", rows=3)
    }
}
`

// WidgetSpec is what one stdlib component needs to be built at runtime, decoded
// from the Spec its fyne override lowers to.
//
// It carries less than the internal spec does: no Go type and no callback
// signature, because those exist so the emitter can *print* them and a host
// holds the value and reads both off it by reflection. What is left is the one
// thing reflection cannot recover -- the constructor -- and the names of the
// methods and fields to reach through.
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
	if pkg == nil || len(pkg.Components) == 0 {
		return nil, fmt.Errorf("no components in the checked package")
	}
	seen := map[string]*ir.Component{}
	codegen.WalkVisualTree(pkg.Components[0].Body, func(n *ir.NodeInst, _ int) bool {
		if n.Component != nil {
			seen[n.Name] = n.Component
		}
		return false
	})

	var out []WidgetSpec
	for _, name := range StdlibComponents {
		comp := seen[name]
		if comp == nil {
			return nil, fmt.Errorf("%s: not instantiated; AllComponentsSource is out of step with StdlibComponents", name)
		}
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
