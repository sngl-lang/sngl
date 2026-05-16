package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passNoInlineComponents inlines every non-recursive, non-native, non-main
// user-defined component into main. After the pass, codegen only sees one
// real ir.Component (main) plus any recursive cycles. See
// docs/superpowers/specs/2026-05-16-component-inlining-design.md.
var passNoInlineComponents = pass{
	name:    "NoInlineComponents",
	enabled: func(c Caps) bool { return c.NoInlineComponents },
	apply:   lowerInlineComponents,
}

func lowerInlineComponents(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	main := mainComponent(pkg)
	if main == nil {
		return nil
	}
	cycles := findRecursiveCycles(pkg)
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles}
	if err := st.run(); err != nil {
		return err
	}
	pkg.Components = retainComponents(pkg.Components, st.keep)
	return nil
}

type inlineCompState struct {
	pkg         *ir.Package
	main        *ir.Component
	cycles      map[*ir.Component]bool
	keep        map[*ir.Component]bool
	instCounter int
}

func (st *inlineCompState) run() error {
	// Real inlining is implemented in subsequent tasks. For now mark
	// main + any cycle members as the only components that survive,
	// leaving non-cycle non-main components in place untouched.
	st.keep = map[*ir.Component]bool{st.main: true}
	for c := range st.cycles {
		st.keep[c] = true
	}
	// Until inlining is implemented, also retain every other component
	// so the pass is observably a no-op end-to-end. Once expandCall
	// lands in Task B3, this loop goes away.
	for _, c := range st.pkg.Components {
		st.keep[c] = true
	}
	return nil
}

// mainComponent returns the package's main component, or nil if absent.
func mainComponent(pkg *ir.Package) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// findRecursiveCycles returns the set of components participating in any
// call cycle (including self-recursion). Edges follow NodeInst.Component
// from each component's body, funcs, and nested control-flow.
func findRecursiveCycles(pkg *ir.Package) map[*ir.Component]bool {
	edges := map[*ir.Component]map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		edges[c] = map[*ir.Component]bool{}
		collectCalleeEdges(c.Body, edges[c])
		for _, f := range c.Funcs {
			collectCalleeEdges(f.Block, edges[c])
		}
	}
	return tarjanCycles(edges)
}

func collectCalleeEdges(stmts []ir.Stmt, out map[*ir.Component]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil {
				out[n.Component] = true
			}
			collectCalleeEdges(n.Children, out)
			for _, h := range n.Handlers {
				if h.Func != nil {
					collectCalleeEdges(h.Func.Block, out)
				}
			}
		case *ir.If:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.For:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.PlatformFilter:
			collectCalleeEdges(n.Body, out)
		case *ir.SlotInst:
			collectCalleeEdges(n.Children, out)
		case *ir.ErrorBoundary:
			collectCalleeEdges(n.Children, out)
			if n.Handler != nil && n.Handler.Func != nil {
				collectCalleeEdges(n.Handler.Func.Block, out)
			}
		}
	}
}

// tarjanCycles runs Tarjan's SCC algorithm and returns the set of nodes
// in any non-trivial SCC (size > 1, or size 1 with a self-edge).
func tarjanCycles(edges map[*ir.Component]map[*ir.Component]bool) map[*ir.Component]bool {
	cycles := map[*ir.Component]bool{}
	idx := 0
	indices := map[*ir.Component]int{}
	lowlinks := map[*ir.Component]int{}
	onStack := map[*ir.Component]bool{}
	var stack []*ir.Component

	var strongconnect func(v *ir.Component)
	strongconnect = func(v *ir.Component) {
		indices[v] = idx
		lowlinks[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true
		for w := range edges[v] {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}
		if lowlinks[v] == indices[v] {
			var scc []*ir.Component
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				for _, n := range scc {
					cycles[n] = true
				}
			} else if edges[scc[0]][scc[0]] {
				cycles[scc[0]] = true
			}
		}
	}
	for v := range edges {
		if _, seen := indices[v]; !seen {
			strongconnect(v)
		}
	}
	return cycles
}

// retainComponents returns a new slice containing only components in keep,
// preserving relative order.
func retainComponents(in []*ir.Component, keep map[*ir.Component]bool) []*ir.Component {
	out := make([]*ir.Component, 0, len(in))
	for _, c := range in {
		if keep[c] {
			out = append(out, c)
		}
	}
	return out
}
