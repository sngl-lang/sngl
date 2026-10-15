package ir

// ReachesBuildHost reports whether fn's body can call an intrinsic only the
// build answers (sngl:x/gen's host API, `md.parse`): directly or through any
// function or lambda it names. It errs toward yes, since a call that reaches
// the host outside a producer is refused rather than recorded. fn itself
// being such an intrinsic is not asked; IsBuildCall asks both.
func ReachesBuildHost(fn *Func, memo map[*Func]bool) bool {
	if v, ok := memo[fn]; ok {
		return v
	}
	memo[fn] = false
	found := false
	visit := func(f *Func) {
		if found || f == nil {
			return
		}
		if f.BuildOnly || ReachesBuildHost(f, memo) {
			found = true
		}
	}
	Walk(fn.Block, func(n Node) error {
		if found {
			return SkipAll
		}
		switch x := n.(type) {
		case *Call:
			visit(x.Func)
		case *Ident:
			if f, ok := x.Sym.(*Func); ok {
				visit(f)
			}
		case *Lambda:
			visit(x.Func)
		}
		return nil
	})
	memo[fn] = found
	return found
}

// IsBuildCall reports whether a call of fn is one only the build can answer:
// fn is a build intrinsic, or reaches one.
func IsBuildCall(fn *Func, memo map[*Func]bool) bool {
	return fn != nil && (fn.BuildOnly || ReachesBuildHost(fn, memo))
}

// BuildIntrinsicOf names the build intrinsic fn is or reaches -- what a
// refusal is about, where fn is a library's wrapper around one.
func BuildIntrinsicOf(fn *Func, memo map[*Func]bool) string {
	return buildIntrinsicOf(fn, memo, map[*Func]bool{})
}

func buildIntrinsicOf(fn *Func, memo, seen map[*Func]bool) string {
	if fn.BuildOnly {
		return fn.Intrinsic
	}
	seen[fn] = true
	name := ""
	Walk(fn.Block, func(n Node) error {
		if c, ok := n.(*Call); ok && c.Func != nil && !seen[c.Func] && IsBuildCall(c.Func, memo) {
			name = buildIntrinsicOf(c.Func, memo, seen)
			return SkipAll
		}
		return nil
	})
	if name == "" {
		return fn.Name
	}
	return name
}

// ViewCalls hands visit every call a view's own expressions make -- a node's
// props, a slot insertion's arguments, an `if`'s condition, a `for`'s head, a
// provider's value -- with the nearest node around it the program wrote: one
// carrying a Site, or the outermost. Handlers and lambdas are not the view's:
// they run when the program does.
func ViewCalls(stmts []Stmt, visit func(call *Call, site *NodeInst)) {
	viewCalls(stmts, nil, visit)
}

func viewCalls(stmts []Stmt, site *NodeInst, visit func(*Call, *NodeInst)) {
	for _, s := range stmts {
		here := site
		var own []Expr
		switch n := s.(type) {
		case *NodeInst:
			if n.Site != nil || here == nil {
				here = n
			}
			for _, a := range n.Props {
				own = append(own, a.Value)
			}
		case *SlotInst:
			own = n.Args
		case *If:
			if n.Catch == nil {
				own = append(own, n.Cond)
			}
		case *For:
			own = append(own, n.Iter)
		case *ContextProvider:
			own = append(own, n.Value)
		}
		for _, e := range own {
			Walk(e, func(n Node) error {
				switch x := n.(type) {
				case *Lambda:
					return SkipDir
				case *Call:
					visit(x, here)
				}
				return nil
			})
		}
		for _, b := range ViewBlocks(s) {
			viewCalls(*b, here, visit)
		}
	}
}
