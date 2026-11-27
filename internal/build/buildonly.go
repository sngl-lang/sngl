package build

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// refuseBuildOnlyCalls reports a call only the build can answer -- an
// intrinsic marked `build`, or a function that reaches one -- that survived
// the optimizer: written outside a const func, or with arguments not known
// until the program runs. No target can make it.
//
// A call inside a function that itself reaches the host is that function's
// definition, not a call anything makes, so what is refused is a call *of*
// such a function left standing -- reported where the program wrote the node
// around it, since a library component spliced in carries its own positions.
//
// foldsViews says the target writes its view out as markup, folding each
// document as it does (codegen.Fold): a call in a view is answered there, per
// document, with the loop variables around it bound -- what a `for` over the
// pages of a site hands `md.document`. The target refuses what its fold could
// not answer (codegen.RefuseUnfoldedBuildCalls).
//
// A call in a view a target does not fold is the target declining the
// program, where it is the program's arguments that no build knows: a static
// site answers `md.document(source=d.body)` in a `for` over a const list per
// page, and a target running the loop has no build-time value to hand it.
func refuseBuildOnlyCalls(pkg *ir.Package, target Target, foldsViews bool) error {
	memo := map[*ir.Func]bool{}
	skip := map[*ir.Call]bool{}
	for _, o := range ir.Owners(pkg) {
		for _, fn := range o.Funcs {
			if ir.IsBuildCall(fn, memo) {
				ir.Walk(fn.Block, func(n ir.Node) error {
					if c, ok := n.(*ir.Call); ok {
						skip[c] = true
					}
					return nil
				})
			}
		}
	}
	sites := map[*ir.Call]*ir.NodeInst{}
	for _, o := range ir.Owners(pkg) {
		ir.ViewCalls(*o.Body, func(c *ir.Call, site *ir.NodeInst) {
			sites[c] = site
			if foldsViews {
				skip[c] = true
			}
		})
	}
	var found *ir.Call
	ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && !skip[c] && ir.IsBuildCall(c.Func, memo) {
			found = c
			return ir.SkipAll
		}
		return nil
	})
	if found == nil {
		return nil
	}
	d := codegen.BuildOnlyDiagnostic(found, sites[found], memo)
	if sites[found] != nil {
		return &ir.TargetDeclined{Target: target.Platform, Msg: d.Error() + "; " + describe(target) + " makes a view's calls as the program runs, where a static site makes them per document"}
	}
	return &buildOnlyDeclined{d, target.Platform}
}

// buildOnlyDeclined is a call only the build can answer left where no view
// holds it, which the program checked clean with: bubbletea's loop over a
// site's const pages hands the parse a value the build does not have
// (cmd/sngl/testdata/markdown_document_refused.txt). The build declines it,
// at the position the diagnostic names, as it does one a view holds.
type buildOnlyDeclined struct {
	ir.Diagnostic
	target string
}

func (e *buildOnlyDeclined) DeclinedBy() string { return e.target }
func (e *buildOnlyDeclined) Unwrap() error      { return e.Diagnostic }

// refuseStateIntoBuildCalls reports, where the program wrote it, an argument
// that reads state and lands in a prop its component hands to a call only the
// build can answer -- `md.document(source=draft)`. That call can never be
// answered, and refused after lowering it may sit in a function the lowering
// synthesized, with nothing left to say where the program wrote it.
func refuseStateIntoBuildCalls(pkg *ir.Package) error {
	memo := map[*ir.Func]bool{}
	props := map[*ir.Component]map[string]bool{}
	buildProps := func(c *ir.Component) map[string]bool {
		if m, ok := props[c]; ok {
			return m
		}
		m := map[string]bool{}
		props[c] = m
		syms := map[*ir.Param]string{}
		for _, p := range c.Props {
			if p.Sym != nil {
				syms[p.Sym] = p.Name
			}
		}
		ir.Walk(c.Stmts, func(n ir.Node) error {
			call, ok := n.(*ir.Call)
			if !ok || !ir.IsBuildCall(call.Func, memo) {
				return nil
			}
			for _, a := range call.Args {
				ir.Walk(a.Value, func(n ir.Node) error {
					if id, ok := n.(*ir.Ident); ok {
						if p, ok := id.Sym.(*ir.Param); ok && syms[p] != "" {
							m[syms[p]] = true
						}
					}
					return nil
				})
			}
			return nil
		})
		return m
	}
	var err error
	ir.Walk(pkg, func(n ir.Node) error {
		node, ok := n.(*ir.NodeInst)
		if !ok || node.Component == nil {
			return nil
		}
		want := buildProps(node.Component)
		for _, a := range node.Props {
			if !want[a.Name] {
				continue
			}
			if v := stateRead(a.Value); v != nil {
				pos := a.NamePos
				if !pos.IsValid() {
					pos = ir.NodePos(node)
				}
				err = ir.Diagnostic{Severity: ir.Error, Pos: pos, Msg: fmt.Sprintf("%s's %s is read while the program builds, and %s is state, which it does not have until the program runs", node.Component.DisplayName(), a.Name, v.Name)}
				return ir.SkipAll
			}
		}
		return nil
	})
	return err
}

// stateRead is the first var e reads that holds state, nil where it reads
// none.
func stateRead(e ir.Expr) *ir.Var {
	var found *ir.Var
	ir.Walk(e, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && !v.IsConst && !v.NodeHandle {
				found = v
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}
