package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passTimer = pass{
	name:    "NoTimer",
	enabled: func(c Caps) bool { return c.NoTimer },
	apply:   lowerTimer,
}

// lowerTimer walks every *ir.Timer decl, replaces it with a synthesized
// handler *ir.Func plus a lower.scheduleTimer call, and injects
// schedule/cancel pairs after Assigns that mutate the Enabled Var.
func lowerTimer(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := newTimerState()
	st.processOwner(&pkg.Funcs, &pkg.Timers, nil)
	for _, comp := range pkg.Components {
		st.processOwner(&comp.Funcs, &comp.Timers, &comp.Body)
	}
	st.injectEnabledUpdaters(pkg)
	return nil
}

type timerState struct {
	schedule           *ir.Func
	cancel             *ir.Func
	timersByEnabledVar map[*ir.Var][]gatedTimer
	nextID             int
}

type gatedTimer struct {
	id          int
	intervalMs  int
	handlerName string
	handlerFunc *ir.Func
}

func newTimerState() *timerState {
	return &timerState{
		timersByEnabledVar: make(map[*ir.Var][]gatedTimer),
	}
}

func (st *timerState) scheduleFunc() *ir.Func {
	if st.schedule == nil {
		st.schedule = &ir.Func{
			Name:      "lower.scheduleTimer",
			Intrinsic: "LowerScheduleTimer",
			Params: []*ir.Param{
				{Name: "id", Type: ir.TypInt},
				{Name: "intervalMs", Type: ir.TypInt},
				{Name: "handler", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.schedule
}

func (st *timerState) cancelFunc() *ir.Func {
	if st.cancel == nil {
		st.cancel = &ir.Func{
			Name:      "lower.cancelTimer",
			Intrinsic: "LowerCancelTimer",
			Params: []*ir.Param{
				{Name: "id", Type: ir.TypInt},
			},
			Return: ir.TypVoid,
		}
	}
	return st.cancel
}

// processOwner walks one owner (package, component) replacing its Timer
// decls with handler funcs and prepending scheduleTimer calls into the body.
func (st *timerState) processOwner(funcs *[]*ir.Func, timers *[]*ir.Timer, body *[]ir.Stmt) {
	if len(*timers) == 0 {
		return
	}
	var newCalls []ir.Stmt
	for _, t := range *timers {
		id := st.nextID
		st.nextID++

		handlerName := "__timer" + strconv.Itoa(id) + "_handler"
		var handlerBlock []ir.Stmt
		if t.Handler != nil {
			handlerBlock = t.Handler.Block
		}
		handlerFunc := &ir.Func{
			Name:   handlerName,
			Block:  handlerBlock,
			Return: ir.TypVoid,
		}
		*funcs = append(*funcs, handlerFunc)

		intervalMs := extractIntervalMs(t.Interval)

		scheduleCall := &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.scheduleFunc(),
				Args: []ir.CallArg{
					{Value: intLiteralLit(id)},
					{Value: intLiteralLit(intervalMs)},
					{Value: &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: handlerFunc}},
				},
			},
		}

		var stmt ir.Stmt = scheduleCall
		if t.Enabled != nil {
			stmt = &ir.If{
				Cond: t.Enabled,
				Body: []ir.Stmt{scheduleCall},
			}
			if id, ok := t.Enabled.(*ir.Ident); ok {
				if v, ok := id.Sym.(*ir.Var); ok {
					st.timersByEnabledVar[v] = append(st.timersByEnabledVar[v], gatedTimer{
						id:          st.nextID - 1,
						intervalMs:  intervalMs,
						handlerName: handlerName,
						handlerFunc: handlerFunc,
					})
				}
			}
		}
		newCalls = append(newCalls, stmt)
	}
	*timers = nil
	if body != nil {
		*body = append(newCalls, *body...)
	}
}

// extractIntervalMs reads the interval literal. After Phase 2 NoUnit,
// interval literals are plain int Literals.
func extractIntervalMs(e ir.Expr) int {
	if lit, ok := e.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeInt {
		if n, err := strconv.Atoi(lit.Raw); err == nil {
			return n
		}
	}
	return 0
}

func (st *timerState) injectEnabledUpdaters(pkg *ir.Package) {
	if len(st.timersByEnabledVar) == 0 {
		return
	}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			return st.injectIntoStmts(stmts)
		},
	})
}

func (st *timerState) injectIntoStmts(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, s)
		switch n := s.(type) {
		case *ir.If:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.For:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.PlatformFilter:
			n.Body = st.injectIntoStmts(n.Body)
		case *ir.NodeInst:
			n.Children = st.injectIntoStmts(n.Children)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = st.injectIntoStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = st.injectIntoStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = st.injectIntoStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = st.injectIntoStmts(n.Handler.Func.Block)
			}
		}
		out = append(out, st.gatedUpdatersFor(s)...)
	}
	return out
}

func (st *timerState) gatedUpdatersFor(s ir.Stmt) []ir.Stmt {
	a, ok := s.(*ir.Assign)
	if !ok {
		return nil
	}
	id, ok := a.Target.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return nil
	}
	gated, ok := st.timersByEnabledVar[v]
	if !ok {
		return nil
	}
	var out []ir.Stmt
	for _, gt := range gated {
		out = append(out, &ir.If{
			Cond: &ir.Ident{Name: id.Name, Type: ir.TypBool, Sym: v},
			Body: []ir.Stmt{
				&ir.CallStmt{
					Call: &ir.Call{
						Type: ir.TypVoid,
						Func: st.scheduleFunc(),
						Args: []ir.CallArg{
							{Value: intLiteralLit(gt.id)},
							{Value: intLiteralLit(gt.intervalMs)},
							{Value: &ir.Ident{Name: gt.handlerName, Type: ir.TypDyn, Sym: gt.handlerFunc}},
						},
					},
				},
			},
			Else: []ir.Stmt{
				&ir.CallStmt{
					Call: &ir.Call{
						Type: ir.TypVoid,
						Func: st.cancelFunc(),
						Args: []ir.CallArg{
							{Value: intLiteralLit(gt.id)},
						},
					},
				},
			},
		})
	}
	return out
}

func intLiteralLit(n int) *ir.Literal {
	return &ir.Literal{
		Type: ir.TypInt,
		Raw:  strconv.Itoa(n),
	}
}
