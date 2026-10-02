package checker

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// reportDeclarativeCycles refuses a declaration that reads itself, however
// many hops round: what it is depends on what it is, so it has no value to be
// read. The reads are the declarative ones -- a const's initializer, a node's
// prop read through another node's `#id` (`second.value`), and the functions
// a const calls, which a build evaluates where the const is folded.
//
// A cycle of functions alone is recursion and is left alone: a call runs when
// it is made, and its value is what the call returns. A node reading its own
// `#id` is reportSelfReferentialProps', which refuses any such read and not
// only a cycle, so the graph has no self-edge for one.
//
// Reported once per cycle, at the read that leaves the declaration written
// first, naming every read on the way round. The fold's re-entry guards
// (evalCtx.foldingProp, and passNodePropReads in the lowering) stay as the
// survivable answer for IR the checker did not see.
func (c *checker) reportDeclarativeCycles() {
	g := &cycleGraph{edges: map[cycleVertex][]cycleEdge{}, decl: map[cycleVertex]ast.Pos{}}
	g.collect(c.pkg)
	g.report(c)
}

// cycleVertex is a declaration whose value a read depends on: a const or a
// function (sym alone), or a node's prop (the node's handle and the prop).
type cycleVertex struct {
	sym  ir.Symbol
	prop string
}

type cycleEdge struct {
	to cycleVertex
	at ast.Pos
}

type cycleGraph struct {
	edges map[cycleVertex][]cycleEdge
	decl  map[cycleVertex]ast.Pos
	order []cycleVertex
	// props is each handle's node's props, so a read of a handle as a whole
	// depends on every one of them.
	props map[*ir.Var][]string
	funcs map[*ir.Func]bool
}

func (g *cycleGraph) vertex(v cycleVertex, at ast.Pos) {
	if _, ok := g.decl[v]; ok {
		return
	}
	g.decl[v] = at
	g.order = append(g.order, v)
}

func (g *cycleGraph) collect(pkg *ir.Package) {
	g.props = map[*ir.Var][]string{}
	g.funcs = map[*ir.Func]bool{}
	var nodes []*ir.NodeInst
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.Handle != nil {
			nodes = append(nodes, ni)
			for _, p := range ni.Props {
				if p.Name != "" {
					g.props[ni.Handle] = append(g.props[ni.Handle], p.Name)
				}
			}
		}
		return nil
	})
	var consts []*ir.Var
	for _, o := range ir.Owners(pkg) {
		consts = append(consts, o.Consts...)
	}
	var funcs []*ir.Func
	funcs = append(funcs, pkg.Funcs...)
	for _, comp := range pkg.Components {
		funcs = append(funcs, comp.Funcs...)
	}
	for _, f := range funcs {
		if f != nil && f.AST != nil {
			g.funcs[f] = true
		}
	}
	for _, k := range consts {
		if k == nil || k.Init == nil {
			continue
		}
		v := cycleVertex{sym: k}
		g.vertex(v, declPos(k))
		g.reads(v, k.Init)
	}
	for _, f := range funcs {
		if !g.funcs[f] {
			continue
		}
		v := cycleVertex{sym: f}
		g.vertex(v, declPos(f))
		for _, s := range f.Block {
			g.reads(v, s)
		}
	}
	for _, n := range nodes {
		for _, p := range n.Props {
			if p.Name == "" || p.Value == nil {
				continue
			}
			v := cycleVertex{sym: n.Handle, prop: p.Name}
			at := p.NamePos
			if at.Line == 0 {
				at = ir.NodePos(n)
			}
			g.vertex(v, at)
			g.reads(v, p.Value, n.Handle)
		}
	}
}

// reads adds an edge from v to each declaration root reads: a const, a
// function it calls, a node prop through a handle, and every prop of a handle
// read whole. A read of self, the handle whose prop v is, is left out.
func (g *cycleGraph) reads(v cycleVertex, root any, self ...*ir.Var) {
	isSelf := func(h *ir.Var) bool { return len(self) > 0 && h == self[0] }
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Select:
			id, ok := x.Operand.(*ir.Ident)
			if !ok {
				return nil
			}
			h, ok := id.Sym.(*ir.Var)
			if !ok || g.props[h] == nil {
				return nil
			}
			if !isSelf(h) && slices.Contains(g.props[h], x.Field) {
				g.edge(v, cycleVertex{sym: h, prop: x.Field}, identPos(id))
			}
			return ir.SkipDir
		case *ir.Ident:
			switch s := x.Sym.(type) {
			case *ir.Var:
				switch {
				case s.IsConst && s.Init != nil:
					g.edge(v, cycleVertex{sym: s}, identPos(x))
				case g.props[s] != nil && !isSelf(s):
					for _, p := range g.props[s] {
						g.edge(v, cycleVertex{sym: s, prop: p}, identPos(x))
					}
				}
			}
		case *ir.Call:
			if x.Func != nil && g.funcs[x.Func] {
				g.edge(v, cycleVertex{sym: x.Func}, callExprPos(x))
			}
		}
		return nil
	})
}

func (g *cycleGraph) edge(from, to cycleVertex, at ast.Pos) {
	g.edges[from] = append(g.edges[from], cycleEdge{to: to, at: at})
}

// report finds each strongly connected set of declarations that holds
// something other than a function, and reports one cycle through the one of
// them written first.
func (g *cycleGraph) report(c *checker) {
	for _, scc := range g.sccs() {
		in := map[cycleVertex]bool{}
		for _, v := range scc {
			in[v] = true
		}
		var start *cycleVertex
		for _, v := range scc {
			if _, isFunc := v.sym.(*ir.Func); isFunc {
				continue
			}
			if start == nil || posBefore(g.decl[v], g.decl[*start]) {
				vv := v
				start = &vv
			}
		}
		if start == nil {
			continue
		}
		if len(scc) == 1 && !slices.ContainsFunc(g.edges[*start], func(e cycleEdge) bool { return e.to == *start }) {
			continue
		}
		path := g.cycleFrom(*start, in)
		if len(path) == 0 {
			continue
		}
		var reads []string
		for i, e := range path {
			from := *start
			if i > 0 {
				from = path[i-1].to
			}
			reads = append(reads, g.name(from)+" reads "+g.name(e.to))
		}
		at := path[0].at
		if at.Line == 0 {
			at = g.decl[*start]
		}
		why := "each needs the next one's value first, so none has one"
		if len(path) == 1 {
			why = "it needs its own value first, so it has none"
		}
		c.error(at, "a cycle: %s; %s", strings.Join(reads, ", "), why)
	}
}

// cycleFrom is the shortest way from start back to itself inside the set.
func (g *cycleGraph) cycleFrom(start cycleVertex, in map[cycleVertex]bool) []cycleEdge {
	type step struct {
		v    cycleVertex
		path []cycleEdge
	}
	seen := map[cycleVertex]bool{}
	queue := []step{{v: start}}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, e := range g.edges[s.v] {
			if !in[e.to] {
				continue
			}
			path := append(slices.Clip(s.path), e)
			if e.to == start {
				return path
			}
			if !seen[e.to] {
				seen[e.to] = true
				queue = append(queue, step{v: e.to, path: path})
			}
		}
	}
	return nil
}

// sccs is Tarjan's strongly connected components, in declaration order.
func (g *cycleGraph) sccs() [][]cycleVertex {
	index := map[cycleVertex]int{}
	low := map[cycleVertex]int{}
	onStack := map[cycleVertex]bool{}
	var stack []cycleVertex
	var out [][]cycleVertex
	next := 0
	var visit func(v cycleVertex)
	visit = func(v cycleVertex) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, e := range g.edges[v] {
			if _, ok := index[e.to]; !ok {
				visit(e.to)
				low[v] = min(low[v], low[e.to])
			} else if onStack[e.to] {
				low[v] = min(low[v], index[e.to])
			}
		}
		if low[v] == index[v] {
			var scc []cycleVertex
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			out = append(out, scc)
		}
	}
	order := slices.Clone(g.order)
	slices.SortStableFunc(order, func(a, b cycleVertex) int {
		pa, pb := g.decl[a], g.decl[b]
		return cmp.Or(cmp.Compare(pa.Line, pb.Line), cmp.Compare(pa.Column, pb.Column))
	})
	for _, v := range order {
		if _, ok := index[v]; !ok {
			visit(v)
		}
	}
	return out
}

func (g *cycleGraph) name(v cycleVertex) string {
	if v.prop != "" {
		return fmt.Sprintf("%s.%s", v.sym.SymName(), v.prop)
	}
	return v.sym.SymName()
}

func posBefore(a, b ast.Pos) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}
