package interp

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/opeval"
	"duckfam.us/sngl/ir"
	goi18n "duckfam.us/sngl/pkg/go/i18n"
)

// maxCallDepth is the maximum allowed function call depth.
const maxCallDepth = 100

// LambdaValue is a closure captured by a lambda expression.
type LambdaValue struct {
	fn  *ir.Func
	env *Env
	// ctx is the provided values where the function value was made, which its
	// body reads wherever it is later called -- the answer passContext gives,
	// since a call through a value cannot know what to pass.
	ctx map[*ir.Context]any
	// site is the function whose code the body is, for the build host to ask
	// a host call in it of: the function a lambda was written in, or a named
	// function taken as a value. Nil is the root's code.
	site *ir.Func
}

// newLambda is a lambda written where env is running.
func newLambda(fn *ir.Func, env *Env) *LambdaValue {
	return &LambdaValue{fn: fn, env: env, ctx: capturedContext(env), site: env.runningFunc()}
}

// funcValue is the named function fn taken as a value.
func funcValue(fn *ir.Func, env *Env) *LambdaValue {
	return &LambdaValue{fn: fn, env: env, ctx: capturedContext(env), site: fn}
}

// call invokes the lambda with the given values.
//
// The snapshot is what gives the body its own scope, so the parameters do not
// leak into the scope the lambda closed over. What it must not do is swallow
// the body's writes: a lambda handed to a function that calls it is how a
// callback is written, and every assignment one made landed in the copy and was
// dropped -- `run(func() { n += 1 })` left n where it was, with nothing to say
// so. The list methods never noticed because a predicate only reads.
//
// Rebound even when the body failed, because the statements before the failure
// did run.
//
// A lambda made while another lambda ran closes over that call's snapshot,
// which nothing reads once the call has returned: its values were carried back
// to where it was taken from, and go stale there as the program runs on. So a
// lambda closing over an ended call reads the scopes that call was taken from
// first, and writes back to them after -- a handler a record holds is made
// inside the `map` that builds the list, and its writes reached nobody
// (testdata/family_data_events.sngl). A call still running is left alone: what
// it has written has not reached its origin yet.
func (lv *LambdaValue) Call(args []any) (any, error) {
	ended := lv.env.ended
	if ended {
		for o := lv.env.origin; o != nil; o = o.origin {
			lv.env.refreshFrom(o)
		}
	}
	child := lv.env.Snapshot()
	defer func() { child.ended = true }()
	for i, p := range lv.params(args) {
		if i < len(args) {
			child.Set(p, bindArg(args[i], p))
		}
	}
	defer child.enterFrame(lv.site)()
	res, err := child.underContext(lv.ctx, func() (any, error) { return child.execBlockForResult(lv.fn.Block) })
	lv.env.RebindFrom(child)
	if ended {
		lv.env.writeBack()
	}
	return res, err
}

// callWithEnv is like call but uses the provided env directly (no snapshot).
func (lv *LambdaValue) CallWithEnv(env *Env, args []any) (any, error) {
	for i, p := range lv.params(args) {
		if i < len(args) {
			env.Set(p, bindArg(args[i], p))
		}
	}
	defer env.enterFrame(lv.site)()
	return env.underContext(lv.ctx, func() (any, error) { return env.execBlockForResult(lv.fn.Block) })
}

// params is what args bind to: a method taken as a value is handed its
// arguments without the receiver, which the scope it was taken in holds.
func (lv *LambdaValue) params(args []any) []*ir.Param {
	ps := lv.fn.Params
	if len(ps) > 0 && ps[0].Receiver && len(args) == len(ps)-1 {
		return ps[1:]
	}
	return ps
}

// unitValue is the runtime representation of a unit value: a magnitude per
// base, keyed by base name, which is the record `ir.UnitFields` describes and
// every compiled backend emits. A base the value does not carry is absent
// rather than stored as zero.
type unitValue struct {
	Amounts map[string]float64
	// Suffix is the spelling the value was written with. It says which base a
	// multi-base value's cast reads and nothing else -- display normalizes to
	// the bases, since no compiled target carries a spelling to prefer.
	Suffix string
	Table  *unitTable
}

// UnitAmounts reports v's magnitude per base when v is a unit value. The map
// is the value's own and must not be written.
func UnitAmounts(v any) (map[string]float64, bool) {
	u, ok := v.(unitValue)
	if !ok {
		return nil, false
	}
	return u.Amounts, true
}

// UnitLiteralValue is a unit literal's value, for an evaluator outside the
// interpreter that holds one: the optimizer, folding a constant that holds a
// multi-base unit, which no number can stand for. It is the interpreter's own
// value, so UnitAmounts spells it back and UnitBinary combines it with one the
// interpreter made.
func UnitLiteralValue(lit *ir.Literal, tables *UnitTables) (any, bool) {
	ud := ir.UnitDeclOf(lit.Type)
	mag, base, ok := ir.UnitMagnitude(lit)
	if ud == nil || !ok {
		return nil, false
	}
	amounts := map[string]float64{}
	if mag != 0 {
		amounts[base] = mag
	}
	return newUnitValue(tables.of(ud), lit.Suffix, amounts), true
}

// UnitZero is the zero of a multi-base unit, what the literal 0 the checker
// lets stand for one means.
func UnitZero(ud *ir.UnitDef, tables *UnitTables) any {
	return newUnitValue(tables.of(ud), "", map[string]float64{})
}

// UnitTables holds the table of each unit declaration a caller has read a
// literal of, which UnitLiteralValue would otherwise build per literal.
type UnitTables struct {
	m sync.Map // *ir.UnitDef -> *unitTable
}

func (ts *UnitTables) of(ud *ir.UnitDef) *unitTable {
	if t, ok := ts.m.Load(ud); ok {
		return t.(*unitTable)
	}
	t, _ := ts.m.LoadOrStore(ud, buildUnitTableFromDef(ud))
	return t.(*unitTable)
}

// UnitBinary is op on a and b where either is a unit value (isUnit): the sum
// or difference of two values of one unit, a value scaled by a number, and
// equality base by base. Anything else is not evaluable (ok false) -- the
// checker refuses an ordering of a multi-base unit, and a raw comparison of two
// values would compare maps.
func UnitBinary(op ast.BinaryOp, a, b any) (val any, ok, isUnit bool) {
	ua, aUnit := a.(unitValue)
	ub, bUnit := b.(unitValue)
	if !aUnit && !bUnit {
		return nil, false, false
	}
	num := func(v any) (float64, bool) {
		switch n := v.(type) {
		case int:
			return float64(n), true
		case float64:
			return n, true
		}
		return 0, false
	}
	switch {
	case aUnit && bUnit && ua.sameUnit(ub):
		switch op {
		case ast.BinAdd:
			return ua.Add(ub), true, true
		case ast.BinSub:
			return ua.Sub(ub), true, true
		case ast.BinEq:
			return ua.Equal(ub), true, true
		case ast.BinNeq:
			return !ua.Equal(ub), true, true
		}
	case aUnit && !bUnit:
		if f, ok := num(b); ok {
			switch op {
			case ast.BinMul:
				return ua.Scale(f), true, true
			case ast.BinDiv:
				if f != 0 {
					return ua.Scale(1 / f), true, true
				}
			}
		}
	case bUnit && !aUnit:
		if f, ok := num(a); ok && op == ast.BinMul {
			return ub.Scale(f), true, true
		}
	}
	return nil, false, true
}

// newUnitValue takes ownership of amounts, so every caller passes a map it
// built.
func newUnitValue(table *unitTable, suffix string, amounts map[string]float64) unitValue {
	return unitValue{Amounts: amounts, Suffix: suffix, Table: table}
}

// amountIn is the magnitude this value carries in one base.
func (u unitValue) amountIn(base string) float64 { return u.Amounts[base] }

// eachBase calls f for every base in declaration order, so two values of one
// unit are combined and printed in one order whichever bases each carries. A
// value whose suffix resolved to no table has no declared bases, so it falls
// back to the union of the maps in hand -- sorted, because a map range is not
// an order and this one reaches String.
func (u unitValue) eachBase(peers []unitValue, f func(base string)) {
	if u.Table != nil && len(u.Table.Bases) > 0 {
		for _, b := range u.Table.Bases {
			f(b)
		}
		return
	}
	seen := map[string]bool{}
	for _, v := range append([]unitValue{u}, peers...) {
		for b := range v.Amounts {
			seen[b] = true
		}
	}
	for _, b := range slices.Sorted(maps.Keys(seen)) {
		f(b)
	}
}

// sameUnit reports whether u and other are values of one unit declaration.
// Each environment builds its own tables, so two tables of one declaration
// are one unit; and a build clones the package per target, so two
// declarations of one name and the same bases are one declaration copied --
// the optimizer's value of a literal and the interpreter's met as two.
func (u unitValue) sameUnit(other unitValue) bool {
	if u.Table == other.Table {
		return true
	}
	a, b := u.Table, other.Table
	if a == nil || b == nil || a.Decl == nil || b.Decl == nil {
		return false
	}
	return a.Decl == b.Decl || a.Decl.Name == b.Decl.Name && slices.Equal(a.Bases, b.Bases)
}

func (u unitValue) Equal(other unitValue) bool {
	if !u.sameUnit(other) {
		return false
	}
	equal := true
	u.eachBase([]unitValue{other}, func(b string) {
		if u.amountIn(b) != other.amountIn(b) {
			equal = false
		}
	})
	return equal
}

// combine builds a new value from u and other base by base.
func (u unitValue) combine(other unitValue, op func(a, b float64) float64) unitValue {
	amounts := make(map[string]float64, len(u.Amounts))
	u.eachBase([]unitValue{other}, func(b string) {
		if v := op(u.amountIn(b), other.amountIn(b)); v != 0 {
			amounts[b] = v
		}
	})
	return newUnitValue(u.Table, u.Suffix, amounts)
}

func (u unitValue) Add(other unitValue) unitValue {
	if !u.sameUnit(other) {
		return u
	}
	return u.combine(other, func(a, b float64) float64 { return a + b })
}

func (u unitValue) Sub(other unitValue) unitValue {
	if !u.sameUnit(other) {
		return u
	}
	return u.combine(other, func(a, b float64) float64 { return a - b })
}

func (u unitValue) Scale(factor float64) unitValue {
	amounts := make(map[string]float64, len(u.Amounts))
	for b, v := range u.Amounts {
		if v*factor != 0 {
			amounts[b] = v * factor
		}
	}
	return newUnitValue(u.Table, u.Suffix, amounts)
}

// baseAmount reads one of the unit's per-base members -- the ones
// ir.UnitFields registers and the checker types as a float.
func (u unitValue) baseAmount(base string) (any, error) {
	if u.Table == nil {
		return nil, fmt.Errorf("no unit table for %q", base)
	}
	if u.Table.BaseOf[base] != base {
		// The checker registers a field per base and refuses anything else, so
		// this is a value whose table was built without one rather than a
		// program that asked for it.
		return nil, fmt.Errorf("no base %q on this unit", base)
	}
	return u.amountIn(base), nil
}

// countedIn is the base this value's suffix reduces to.
func (u unitValue) countedIn() string {
	if u.Table == nil {
		return u.Suffix
	}
	if b, ok := u.Table.BaseOf[u.Suffix]; ok {
		return b
	}
	return u.Suffix
}

// magnitude is the one number a cast reads. A single-base unit has exactly one
// and that is the whole of its value; a multi-base one is answered in the base
// its own suffix names, which is all a single number can say about a record.
func (u unitValue) magnitude() float64 {
	if u.Table != nil && len(u.Table.Bases) == 1 {
		return u.amountIn(u.Table.Bases[0])
	}
	return u.amountIn(u.countedIn())
}

// String prints every base the value carries a magnitude for, summed the way
// the source would have written it: `3px + 2em`. A value carrying none prints
// a zero rather than nothing at all.
//
// The written suffix is not consulted, so `2rem` prints as 32em. A compiled
// target holds the record and nothing else, so preferring the spelling here
// is a memory only the interpreter has -- and this is the one renderer every
// target has to match.
func (u unitValue) String() string {
	var parts []string
	u.eachBase(nil, func(b string) {
		if amt := u.amountIn(b); amt != 0 {
			parts = append(parts, ir.FormatUnitTerm(amt, b))
		}
	})
	if len(parts) == 0 {
		return ir.FormatUnitTerm(0, u.firstBase())
	}
	return strings.Join(parts, ir.UnitTermSep)
}

// firstBase is the base a value carrying no magnitude at all prints in. It is
// the declared first rather than the one the suffix reduces to, because
// `0rem` and `0px` are the same value and must print the same.
func (u unitValue) firstBase() string {
	if u.Table != nil && len(u.Table.Bases) > 0 {
		return u.Table.Bases[0]
	}
	return u.countedIn()
}

// singleBase reports whether the unit declares one base, which is what makes
// magnitude() the whole of the value rather than one component of it.
func (u unitValue) singleBase() bool {
	return u.Table != nil && len(u.Table.Bases) == 1
}

func (u unitValue) sameFamily(other unitValue) bool {
	return u.Table != nil && u.sameUnit(other)
}

// unitTable maps suffixes to conversion factors.
type unitTable struct {
	// Decl is the declaration the table was built from, which is what makes
	// two values one unit.
	Decl        *ir.UnitDef
	Base        string
	Conversions map[string]float64
	// BaseOf is the base each suffix reduces to (a base reduces to itself),
	// from ir.UnitSuffix.BaseName. Needed to read a per-base member off a
	// value: `3rem` counts in em, not in whichever suffix was declared first.
	BaseOf map[string]string
	// Bases is ir.UnitDef.Bases in declaration order -- what a record's fields
	// are, and the order two values are combined and printed in.
	Bases []string
}

// Env holds the mutable state for test execution.
type Env struct {
	// Keyed by the declaration rather than by name, so two declarations that
	// share a name are two bindings and a library constant needs no copy into
	// a name table.
	vals map[ir.Symbol]any
	// assigned is what a statement in this scope wrote, as opposed to what was
	// bound into it. RebindFrom carries only these back. A loop iteration
	// renders in a snapshot of the enclosing scope, so carrying every binding
	// back writes that scope's state as it stood when the snapshot was taken:
	// an effect's teardown, running in the scope of the iteration that is going
	// away, wrote the old list back and the removed element reappeared.
	assigned map[ir.Symbol]bool
	// recv is the implicit component receiver (`this`). Not a binding in vals
	// because no symbol can key it: every method declares its own `this`
	// param, but the caller supplies the value, so the site that binds it and
	// the site that reads it belong to different declarations. hasRecv
	// separates "bound to nil" from "not bound".
	recv        any
	hasRecv     bool
	Units       map[string]*unitTable
	Pkg         *ir.Package
	Comp        *ir.Component
	BodyStmts   []ir.Stmt
	depth       int
	RenderDepth int
	// maxIterations bounds a condition or forever loop; zero means
	// maxLoopIterations. A test sets it to something small, since asserting
	// the bound by reaching the real one would run ten million iterations.
	maxIterations int
	Log           []string
	// Locale is the active BCP-47 locale for i18n calls (default "en").
	Locale string
	// Clock is what `time.now()` reads, set on the scope a run is rooted at
	// and found from the rest through the owner chain (Env.clock). A test
	// hands in the Virtual one its timers are scheduled against, so the two
	// cannot disagree; nil anywhere on the chain is the wall clock.
	Clock Clock
	// ContextVals holds runtime overrides for context values keyed by *ir.Context.
	// Set by t.setContext(); read by Eval(*ir.ContextRead).
	ContextVals map[*ir.Context]any
	// childEnvs caches child component envs so state persists across
	// ResolveElementRef calls and re-renders.
	childEnvs map[childKey]*Env
	// callChildEnvs is the analogous cache for user-component instantiations
	// expressed as ir.CallStmt (children-less call form, e.g. `main()`).
	callChildEnvs map[*ir.CallStmt]*Env
	// parent points at the surrounding env when this env is a child component
	// scope. lookup() falls through to the parent chain so child components
	// can read (and assignments can mutate) package-level vars defined in the
	// root env.
	parent *Env
	// caught records, for each boundary rendered in this scope that declared a
	// fallback, whether it has caught yet. A boundary's `failed` slot is what
	// it renders from then on, and nothing static can answer "has it": the
	// question is about the run.
	//
	// Keyed by the boundary's @error handler, because that is what a raise
	// reaches (Call.ResolvedHandler) and so what invokeHandler has in hand.
	// Held per scope rather than per program, so two instantiations of one
	// component catch separately; registered by the render, which is the only
	// thing that knows a boundary is in this scope at all.
	//
	// The compiled targets get this from passBoundaryFailed, which desugars
	// the pair into a flag and a conditional. `sngl test` on the `none`
	// platform runs the checked IR without lowering, the same reason the
	// interpreter honors For.Else itself, so it answers the question here.
	caught map[*ir.EventHandler]bool
	// inst is the instantiation this component scope was entered through, or
	// nil at the root and for the children-less call form. It is what an emit
	// needs: the handlers a call site supplied are written there, and the scope
	// one runs in is this env's parent.
	inst *ir.NodeInst
	// entries is the insertions whose populations were being mounted where
	// inst was, innermost last: what a forwarded component entry's `.empty`
	// is answered from (slotEmpty).
	entries []entryFrame
	// origin is the scope this one is a Snapshot of, or nil.
	origin *Env
	// ended says this is the snapshot a lambda call ran in, and the call has
	// returned (LambdaValue.Call).
	ended bool
	// mountCopy says this is the copy of a live scope a mount took for one
	// copy of a loop's body or one scoped population: the state it holds is
	// as old as that mount (Env.runHandler).
	mountCopy bool
	// spreadVals holds the operands of the spreads the running statement
	// evaluates once (ir.StatementSpreads), by site.
	spreadVals map[int]any
	// handling counts the event handlers running, shared by every env of one
	// program so that a nested dispatch can tell it is one.
	handling *int
	// build answers sngl:x/gen's host API, set only by the build's evaluator
	// (stream.go).
	build *buildFrame
	// nav is what a stack's instance keeps across mounts (nav.go), navStack
	// the stack a page's instance registered with, and navParams the params
	// that page is showing with, nil for its own.
	nav       *navState
	navStack  *Env
	navParams *navParams
	// wlog is the program's write log, held by the outermost scope and
	// reached by every other through writes().
	wlog *writeLog
	// handles is the program's owned values, shared the same way (owned()).
	handles *handles
}

func NewEnv() *Env {
	return &Env{
		vals:      map[ir.Symbol]any{},
		childEnvs: map[childKey]*Env{},
		handling:  new(int),
		wlog:      &writeLog{},
	}
}

func (env *Env) Set(sym ir.Symbol, val any) {
	if sym == nil {
		return
	}
	env.vals[sym] = val
}

// SetReceiver binds the implicit component receiver (`this`) for calls made
// against this env. See Env.recv.
func (env *Env) SetReceiver(val any) {
	env.recv, env.hasRecv = val, true
}

func (env *Env) Value(sym ir.Symbol) (any, bool) {
	if owner := env.findVarOwner(sym); owner != nil {
		return owner.vals[sym], true
	}
	return nil, false
}

// RebindFrom copies src's value for every symbol env already binds. A call
// runs against a Snapshot, whose bindings are copies; this writes the
// mutations back into the env the snapshot was taken from.
func (env *Env) RebindFrom(src *Env) {
	if src == nil {
		return
	}
	// A symbol is rebound in the nearest scope binding it only: a recursive
	// instance's scope encloses its parent's, which binds the same vars.
	seen := map[ir.Symbol]bool{}
	for e := env; e != nil; e = e.parent {
		for sym := range e.vals {
			if seen[sym] {
				continue
			}
			seen[sym] = true
			if !src.wasAssigned(sym) {
				continue
			}
			if v, ok := src.Value(sym); ok {
				e.vals[sym] = v
				// Credited as a write here too, or the value stops one scope
				// short of where it has to go: a rebind carries only what its
				// source was credited with, so a method whose body wrote
				// through a lambda took the value but was not recorded as
				// having written it, and the method's own rebind onto the
				// instance passed over it.
				if e.assigned != nil {
					e.assigned[sym] = true
				}
			}
		}
	}
}

// wasAssigned reports whether src or an enclosing scope wrote sym. A write that
// landed in a shared parent is already visible and is reported here anyway,
// because copying a value onto itself needs no second rule.
func (env *Env) wasAssigned(sym ir.Symbol) bool {
	for e := env; e != nil; e = e.parent {
		if e.assigned[sym] {
			return true
		}
	}
	return false
}

// refreshFrom brings every binding this scope and its parents hold up to the
// value src knows, and leaves alone the ones src does not -- which is the loop
// variable an iteration bound, and the reason the scope is worth keeping at
// all.
//
// The counterpart to RebindFrom, for a scope that outlives the render that
// built it. An effect's bracket holds the scope of the iteration that placed
// it, and its state was copied in when that mount ran; a handler running later
// reads what the program said then, and RebindFrom carries the whole of it
// back. Two brackets ending in one settle is where that shows: the second
// teardown wrote its own mount's state back over the first teardown's.
func (env *Env) refreshFrom(src *Env) {
	if src == nil {
		return
	}
	for e := env; e != nil; e = e.parent {
		if e == src {
			return
		}
		for sym := range e.vals {
			if v, ok := src.Value(sym); ok {
				e.vals[sym] = v
			}
		}
	}
}

// noteAssigned records that a statement wrote sym in this scope.
func (env *Env) noteAssigned(sym ir.Symbol) {
	if env.assigned == nil {
		env.assigned = map[ir.Symbol]bool{}
	}
	env.assigned[sym] = true
}

// Values ranges over the values bound in this env, stopping when f returns
// false. Used to find a binding by what it holds rather than what it is
// called — the test harness locates the component under test this way.
func (env *Env) Values(f func(val any) bool) {
	for _, v := range env.vals {
		if !f(v) {
			return
		}
	}
}

// Isolate is a Snapshot whose writes are its own: nothing run in it is carried
// back to env, as what a loop iteration's handlers write is.
func (env *Env) Isolate() *Env {
	cp := env.Snapshot()
	cp.origin = nil
	return cp
}

// Snapshot returns a shallow copy of the env.
func (env *Env) Snapshot() *Env {
	childEnvs := env.childEnvs
	if childEnvs == nil {
		childEnvs = map[childKey]*Env{}
	}
	cp := &Env{
		vals: make(map[ir.Symbol]any, len(env.vals)),
		// Its own set, not the original's: what this scope wrote is what
		// RebindFrom carries back, and two snapshots of one scope must not be
		// credited with each other's writes.
		assigned:      map[ir.Symbol]bool{},
		recv:          env.recv,
		hasRecv:       env.hasRecv,
		Units:         env.Units,
		Pkg:           env.Pkg,
		Comp:          env.Comp,
		BodyStmts:     env.BodyStmts,
		depth:         env.depth,
		RenderDepth:   env.RenderDepth,
		maxIterations: env.maxIterations,
		Locale:        env.Locale,
		Clock:         env.Clock,
		ContextVals:   env.ContextVals, // shared reference — overrides visible in child envs
		// Shared reference: a click handler runs against a snapshot, and the
		// boundary it trips is the one the original scope renders.
		caught:        env.caught,
		childEnvs:     childEnvs, // shared reference — cached child envs persist through scope changes
		callChildEnvs: env.callChildEnvs,
		parent:        env.parent,
		// The instantiation this scope belongs to. A snapshot of a frame is
		// still that frame, and dropping it broke the one thing that reads it:
		// an emit finds the nearest enclosing instantiation to know whose
		// handler to run, so an event emitted from inside a lambda found the
		// *caller's* instantiation instead, asked it for a subscriber to an
		// event it does not declare, and went nowhere.
		inst:       env.inst,
		entries:    env.entries,
		origin:     env,
		spreadVals: env.spreadVals,
		handling:   env.handling,
		wlog:       env.writes(),
		handles:    env.owned(),
		build:      env.build,
	}
	maps.Copy(cp.vals, env.vals)
	return cp
}

// writeBack carries what ran in a snapshot back to every scope it was taken
// from, the way LambdaValue.Call does for a callback. A node mounted in a loop
// iteration or a scoped slot's population holds a snapshot, so a handler run
// in it wrote only the copy.
//
// A scope above the snapshot may be a snapshot too, and a write the handler
// made to a binding that scope holds landed in it rather than in the handler's
// own: a block that declares a `var` mounts in a scope of its own (blockEnv)
// whose parent is the loop iteration's snapshot. So every snapshot on the way
// up carries what it was written back to where it was taken from.
func (env *Env) writeBack() {
	for e := env; e != nil; e = e.parent {
		for o := e.origin; o != nil; o = o.origin {
			o.RebindFrom(e)
		}
	}
}

// SetContext stores a runtime override for ctx, replacing any default value.
// If ctx is named "locale" its value is also applied to env.Locale for i18n
// compat, until the i18n stack reads context throughout.
func (env *Env) SetContext(ctx *ir.Context, val any) {
	if env.ContextVals == nil {
		env.ContextVals = make(map[*ir.Context]any)
	}
	env.ContextVals[ctx] = val
	if ctx.Name == "locale" {
		if s, ok := val.(string); ok {
			env.Locale = s
		}
	}
}

// ContextVal returns the current value of ctx: the override if one has been
// stored via SetContext, otherwise ctx's default evaluated against env.
func (env *Env) ContextVal(ctx *ir.Context) any {
	if env.ContextVals != nil {
		if v, ok := env.ContextVals[ctx]; ok {
			return v
		}
	}
	if ctx.Default != nil {
		v, err := env.Eval(ctx.Default)
		if err == nil {
			return v
		}
	}
	return nil
}

// translatorFor returns a Translator for the env's current locale.
func (env *Env) translatorFor() *goi18n.Translator {
	return env.translatorForLocale("")
}

// translatorForLocale returns a translator for the given BCP-47 locale,
// falling back to env.Locale or "en" when empty.
func (env *Env) translatorForLocale(loc string) *goi18n.Translator {
	if loc == "" {
		loc = env.Locale
	}
	if loc == "" {
		loc = "en"
	}
	// The package's i18n.manifest.json, what a `$"..."` is translated
	// against, as a generated program embeds it.
	var m goi18n.Manifest
	if env.Pkg != nil {
		m = manifestBeside(env.Pkg.SourcePath)
	}
	return goi18n.NewTranslator(m, loc)
}

// evalI18nPrimitiveCall dispatches a call to one of the unexported i18n._*
// primitives by its intrinsic id. Every one but _defaultLocale takes the
// active locale as its first argument, threaded there by NoContext from the
// `locale` context read in the wrapper that calls it. Returns (result,
// handled, error); handled is false when the id is unknown.
func (env *Env) evalI18nPrimitiveCall(method string, args []ir.CallArg) (any, bool, error) {
	switch method {
	case "i18n._defaultLocale":
		return goi18n.DefaultLocale(), true, nil
	}

	// Every other primitive carries the locale as args[0].
	if len(args) < 1 {
		return nil, true, fmt.Errorf("%s requires at least a locale argument", method)
	}
	locVal, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, true, err
	}
	loc := fmt.Sprintf("%v", locVal)
	tr := env.translatorForLocale(loc)

	eval := func(i int) (any, error) {
		return env.Eval(args[i].Value)
	}

	switch method {
	case "i18n._translate":
		if len(args) < 4 {
			return nil, true, fmt.Errorf("%s requires 4 arguments", method)
		}
		keyV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		tmplV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		argsV, err := eval(3)
		if err != nil {
			return nil, true, err
		}
		return tr.Tr(fmt.Sprintf("%v", keyV), fmt.Sprintf("%v", tmplV), toStringAnyMap(argsV)), true, nil
	case "i18n._format":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		tmplV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		argsV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.Format(fmt.Sprintf("%v", tmplV), toStringAnyMap(argsV)), true, nil
	case "i18n._numberInt":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		nV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberInt(ToInt(nV), fmt.Sprintf("%v", sV)), true, nil
	case "i18n._numberFloat":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		nV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberFloat(toFloat(nV), fmt.Sprintf("%v", sV)), true, nil
	case "i18n._date":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Date(tt, fmt.Sprintf("%v", sV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._time":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		sV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Time(tt, fmt.Sprintf("%v", sV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._dateTime":
		if len(args) < 4 {
			return nil, true, fmt.Errorf("%s requires 4 arguments", method)
		}
		dV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		dsV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		tsV, err := eval(3)
		if err != nil {
			return nil, true, err
		}
		if tt, ok := dV.(time.Time); ok {
			return tr.Datetime(tt, fmt.Sprintf("%v", dsV), fmt.Sprintf("%v", tsV)), true, nil
		}
		return fmt.Sprintf("%v", dV), true, nil
	case "i18n._select":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		vV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		cV, err := eval(2)
		if err != nil {
			return nil, true, err
		}
		return tr.Select(fmt.Sprintf("%v", vV), toStringStringMap(cV)), true, nil
	case "i18n._plural":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		cV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[2].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Plural(ToInt(cV), forms), true, nil
	case "i18n._selectOrdinal":
		if len(args) < 3 {
			return nil, true, fmt.Errorf("%s requires 3 arguments", method)
		}
		cV, err := eval(1)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[2].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Selectordinal(ToInt(cV), forms), true, nil
	}
	return nil, false, nil
}

// evalI18nCall dispatches a call to an i18n.* function using the env's locale.
// Returns (result, handled, error). handled is false when the function name is
// not a recognized i18n intrinsic, allowing the caller to fall through.
func (env *Env) evalI18nCall(funcName string, args []ir.CallArg) (any, bool, error) {
	tr := env.translatorFor()
	switch funcName {
	case "tr":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.tr requires 2 arguments")
		}
		keyVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		argsVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		key := fmt.Sprintf("%v", keyVal)
		argsMap := toStringAnyMap(argsVal)
		return tr.Tr(key, key, argsMap), true, nil

	case "format":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.format requires 2 arguments")
		}
		tmplVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		argsVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		tmpl := fmt.Sprintf("%v", tmplVal)
		argsMap := toStringAnyMap(argsVal)
		return tr.Format(tmpl, argsMap), true, nil

	case "numberInt":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.numberInt requires 2 arguments")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		styleVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberInt(ToInt(nVal), fmt.Sprintf("%v", styleVal)), true, nil

	case "numberFloat":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.numberFloat requires 2 arguments")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		styleVal, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.NumberFloat(toFloat(nVal), fmt.Sprintf("%v", styleVal)), true, nil

	case "select":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.select requires 2 arguments")
		}
		valArg, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		casesArg, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		value := fmt.Sprintf("%v", valArg)
		cases := toStringStringMap(casesArg)
		return tr.Select(value, cases), true, nil

	case "exactly":
		if len(args) < 1 {
			return nil, true, fmt.Errorf("i18n.exactly requires 1 argument")
		}
		nVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		return goi18n.Exactly(ToInt(nVal)), true, nil

	case "plural":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.plural requires 2 arguments")
		}
		countVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Plural(ToInt(countVal), forms), true, nil

	case "selectordinal":
		if len(args) < 2 {
			return nil, true, fmt.Errorf("i18n.selectordinal requires 2 arguments")
		}
		countVal, err := env.Eval(args[0].Value)
		if err != nil {
			return nil, true, err
		}
		forms, err := env.evalPluralKeyMap(args[1].Value)
		if err != nil {
			return nil, true, err
		}
		return tr.Selectordinal(ToInt(countVal), forms), true, nil

	case "defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return goi18n.DefaultLocale(), true, nil
	}
	return nil, false, nil
}

// toStringAnyMap coerces a runtime value to map[string]any. An i18n argument
// bag is written `{n = 1}`, which is a map literal or a struct literal
// depending on what the call's declared parameter type made of it.
func toStringAnyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(*Struct); ok {
		m := make(map[string]any, len(s.Fields))
		for _, f := range s.Fields {
			m[f.Name] = f.Value
		}
		return m
	}
	return map[string]any{}
}

// toStringStringMap coerces a runtime value to map[string]string.
func toStringStringMap(v any) map[string]string {
	m := map[string]string{}
	for k, val := range toStringAnyMap(v) {
		m[k] = fmt.Sprintf("%v", val)
	}
	return m
}

// evalPluralKeyMap evaluates a map[PluralKey, string] literal expr, converting
// keys to goi18n.PluralKey values. It handles ir.MapLitIR (the normal form after
// checking) and falls back to a generic eval for other expr kinds.
func (env *Env) evalPluralKeyMap(expr ir.Expr) (map[goi18n.PluralKey]string, error) {
	m := make(map[goi18n.PluralKey]string)
	if ml, ok := expr.(*ir.MapLitIR); ok {
		for _, entry := range ml.Entries {
			kv, err := env.Eval(entry.Key)
			if err != nil {
				return nil, err
			}
			vv, err := env.Eval(entry.Value)
			if err != nil {
				return nil, err
			}
			// Key is either a goi18n.PluralKey (from i18n.zero/one/etc. or
			// i18n.exactly(n)) or something else we can't handle.
			pk, ok := kv.(goi18n.PluralKey)
			if !ok {
				continue
			}
			m[pk] = fmt.Sprintf("%v", vv)
		}
		return m, nil
	}
	// Generic fallback: evaluate the entire map literal.
	v, err := env.Eval(expr)
	if err != nil {
		return nil, err
	}
	return toPluralKeyStringMap(v), nil
}

// toPluralKeyStringMap coerces a runtime value to map[goi18n.PluralKey]string.
// Keys that are goi18n.PluralKey values pass through; nil keys (from unresolved
// i18n.zero/one/etc. constants that were registered with nil Init) are mapped
// to PluralKey sentinel values based on their position in the i18n var list.
func toPluralKeyStringMap(v any) map[goi18n.PluralKey]string {
	m := map[goi18n.PluralKey]string{}
	if src, ok := v.(map[string]any); ok {
		for k, val := range src {
			var pk goi18n.PluralKey
			switch k {
			case "0", "zero":
				pk = goi18n.PluralZero
			case "1", "one":
				pk = goi18n.PluralOne
			case "2", "two":
				pk = goi18n.PluralTwo
			case "3", "few":
				pk = goi18n.PluralFew
			case "4", "many":
				pk = goi18n.PluralMany
			default:
				pk = goi18n.PluralOther
			}
			m[pk] = fmt.Sprintf("%v", val)
		}
	}
	return m
}

// Eval evaluates an IR expression and returns its value.
func (env *Env) Eval(e ir.Expr) (any, error) {
	switch n := e.(type) {
	case *ir.Literal:
		return env.evalLiteral(n)
	case *ir.Ident:
		return env.evalIdent(n)
	case *ir.Binary:
		return env.evalBinary(n)
	case *ir.Unary:
		return env.evalUnary(n)
	case *ir.Ternary:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.Eval(n.Then)
		}
		return env.Eval(n.Else)
	case *ir.MatchExpr:
		i, err := env.matchArm(&n.MatchArms)
		if err != nil || i < 0 {
			return nil, err
		}
		return env.Eval(n.Arms[i].Value)
	case *ir.Select:
		return env.evalSelect(n)
	case *ir.Index:
		return env.evalIndex(n)
	case *ir.Call:
		return env.evalCall(n)
	case *ir.Conversion:
		return env.evalConversion(n)
	case *ir.ListLit:
		return env.evalListLit(n)
	case *ir.StructLit:
		return env.evalStructLit(n)
	case *ir.MapLitIR:
		return env.evalMapLitIR(n)
	case *ir.Lambda:
		return newLambda(n.Func, env), nil
	case *ir.ContextRead:
		return env.ContextVal(n.Ref), nil
	case *ir.SlotEmpty:
		return env.slotEmpty(n)
	case *ir.Closure:
		return newLambda(n.Func, env), nil
	}
	if e == nil {
		return nil, fmt.Errorf("cannot evaluate <nil> expression")
	}
	panic(fmt.Sprintf("testrunner.Eval: unhandled ir.Expr %T", e))
}

func (env *Env) evalLiteral(e *ir.Literal) (any, error) {
	if e.Suffix != "" {
		return env.makeUnitValue(e)
	}
	if e.Type == nil {
		return e.Value, nil
	}
	switch e.Type.Kind {
	case ir.TypeBool:
		return e.Value == "true", nil
	case ir.TypeNull:
		return nil, nil
	case ir.TypeInt:
		raw := strings.ReplaceAll(e.Value, "_", "")
		if e.Type.Unsigned && e.Type.Bits == 64 {
			// uint64 carries as Go uint64 to keep the range above 2^63 exact.
			u, err := strconv.ParseUint(raw, 0, 64)
			if err != nil {
				return uint64(0), nil
			}
			return u, nil
		}
		n, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			return 0, nil
		}
		return int(n), nil
	case ir.TypeFloat:
		raw := strings.ReplaceAll(e.Value, "_", "")
		f, _ := strconv.ParseFloat(raw, 64)
		if e.Type.Bits == 32 {
			// Pre-round to single precision so float32 arithmetic matches
			// native float32 targets.
			return float64(float32(f)), nil
		}
		return f, nil
	case ir.TypeString:
		return e.Value, nil
	}
	return e.Value, nil
}

func (env *Env) evalIdent(e *ir.Ident) (any, error) {
	if e.Member != "" {
		if ir.PayloadEnum(e.Type) != nil {
			return EnumValue{Type: e.Type, Member: e.Member}, nil
		}
		return e.Member, nil
	}
	// i18n's predeclared PluralKey constants, whose value is the runtime's.
	if ir.IsI18nPluralKey(e) {
		if pk, ok := i18nPluralConst(e.Name); ok {
			return pk, nil
		}
	}
	// A build-target node read as a value, bare inside its own package.
	if _, name, ok := ir.TargetNode(e.Sym); ok {
		return name, nil
	}
	if e.Sym != nil {
		if v, ok := env.Value(e.Sym); ok {
			return v, nil
		}
	}
	if env.hasRecv && readsReceiver(e) {
		return env.recv, nil
	}
	if e.Sym != nil {
		return env.lookup(e.Sym)
	}
	return nil, fmt.Errorf("undefined variable %q", e.Name)
}

// readsReceiver reports whether e reads the implicit component receiver: a
// resolved `this` param, or the bare name for a reference the test harness
// synthesized outside the checker and so without a symbol.
func readsReceiver(e *ir.Ident) bool {
	if p, ok := e.Sym.(*ir.Param); ok && p.Receiver {
		return true
	}
	return e.Sym == nil && e.Name == ir.ReceiverParam
}

// resolveCallableFunc resolves name to a function without invoking it, walking
// the parent chain. Needed alongside the symbol on the callee: where a bare
// name is both a #id node handle and a component function (`button #bump(…)`
// next to `func bump()`), the checker resolves it to the handle's Var, so the
// call has to find the function by name.
func (env *Env) resolveCallableFunc(name string) *ir.Func {
	for e := env; e != nil; e = e.parent {
		if e.Comp != nil {
			for _, fn := range e.Comp.Funcs {
				if fn.Name == name && !fn.IsMethod() {
					return fn
				}
			}
			if fn, ok := e.Comp.Methods[name]; ok {
				return fn
			}
		}
		if e.Pkg != nil {
			for _, fn := range e.Pkg.Funcs {
				if fn.Name == name && !fn.IsMethod() {
					return fn
				}
			}
		}
	}
	return nil
}

// methodOn resolves the method named method on the receiver named recv. The
// declaration named recv owns its members, so this is a scope lookup followed
// by a member lookup rather than a table of qualified names.
func (env *Env) methodOn(recv, method string) (*ir.Func, bool) {
	if env.Comp != nil && env.Comp.Name == recv {
		if fn, ok := env.Comp.Methods[method]; ok {
			return fn, true
		}
	}
	for e := env; e != nil; e = e.parent {
		if e.Pkg != nil && e.Pkg.Symbols != nil {
			if fn, ok := e.Pkg.Symbols.LookupMethod(recv, method); ok {
				return fn, true
			}
		}
	}
	return nil, false
}

// A write to a package-level var declared in a parent env mutates the
// binding there, so assignment and toggle resolve the owner first.
func (env *Env) findVarOwner(sym ir.Symbol) *Env {
	for e := env; e != nil; e = e.parent {
		if _, ok := e.vals[sym]; ok {
			return e
		}
	}
	return nil
}

// registerBoundary notes that a boundary with a fallback renders in this
// scope, so a raise reaching its handler has somewhere to record that it did.
// Idempotent: a re-render must not forget that the boundary already caught.
func (env *Env) registerBoundary(h *ir.EventHandler) {
	if h == nil {
		return
	}
	if env.caught == nil {
		env.caught = map[*ir.EventHandler]bool{}
	}
	if _, ok := env.caught[h]; !ok {
		env.caught[h] = false
	}
}

// markCaught records that h's boundary caught, in the scope that rendered it.
// The raise runs in that scope or one nested inside it, so the walk is up.
func (env *Env) markCaught(h *ir.EventHandler) {
	if h == nil {
		return
	}
	for e := env; e != nil; e = e.parent {
		if _, ok := e.caught[h]; ok {
			e.caught[h] = true
			return
		}
	}
}

// hasCaught reports whether h's boundary has caught in this scope.
func (env *Env) hasCaught(h *ir.EventHandler) bool {
	for e := env; e != nil; e = e.parent {
		if v, ok := e.caught[h]; ok {
			return v
		}
	}
	return false
}

// varInScope reports whether sym is bound in this env or an enclosing one.
// Unlike lookup it never auto-invokes a zero-arg function, so callers can
// distinguish a func-typed variable from a named function.
func (env *Env) varInScope(sym ir.Symbol) bool {
	return sym != nil && env.findVarOwner(sym) != nil
}

// lookup returns the value bound to sym. A zero-arg *ir.Func auto-invokes:
// a computed field is read by naming it, and the symbol says it is a function
// without a separate table having to.
func (env *Env) lookup(sym ir.Symbol) (any, error) {
	if owner := env.findVarOwner(sym); owner != nil {
		return owner.vals[sym], nil
	}
	if fn, ok := sym.(*ir.Func); ok {
		if effective := len(fn.Params); effective == 0 ||
			(effective == 1 && fn.Params[0].Receiver) {
			return env.EvalUserFunc(fn, nil)
		}
		return funcValue(fn, env), nil
	}
	// A declaration the environment never bound but that carries its own
	// value: a constant or a var of another package, which is in no list
	// this program walks. The symbol has the initializer, so evaluate it.
	if v, ok := sym.(*ir.Var); ok && v.Init != nil {
		return env.Eval(v.Init)
	}
	return nil, fmt.Errorf("undefined variable %q", sym.SymName())
}

func (env *Env) evalSelect(e *ir.Select) (any, error) {
	if e.Populated {
		node, err := env.Eval(e.Via)
		if err != nil {
			return nil, err
		}
		return populationRef(e, node)
	}
	obj, cached := env.spreadVals[e.Spread]
	if e.Spread == 0 || !cached {
		var err error
		if obj, err = env.Eval(e.Operand); err != nil {
			return nil, err
		}
	}
	if cv, ok := obj.(ComponentValue); ok {
		if e.Ref == ir.RefNone {
			return cv.GetField(e.Field)
		}
		set, err := cv.ElementRef(e.Field, nil)
		if err != nil {
			return nil, err
		}
		return shapeElementRef(e, set)
	}
	if t := e.Operand.ExprType(); e.Field == "length" && t != nil {
		if id, ok := lengthIntrinsics[t.Kind]; ok {
			return intrinsics[id]([]any{obj})
		}
	}
	if v, ok := enumMemberRead(obj, e.Field); ok {
		return v, nil
	}
	if s, ok := obj.(*Struct); ok {
		if s.Handle != nil && s.Handle.released {
			var at ast.Pos
			if e.AST != nil {
				at = e.AST.Pos
			}
			return nil, env.useAfterRelease(s, "read", at)
		}
		v, _ := s.Get(e.Field)
		return v, nil
	}
	if m, ok := obj.(map[string]any); ok {
		// A computed the handle's declaration answers natively, selected
		// bare: `pages.current` off a stack.
		if fn := intrinsicMethod(e.Operand.ExprType(), e.Field); fn != nil {
			return fn([]any{obj})
		}
		return m[e.Field], nil
	}
	if u, ok := obj.(unitValue); ok {
		return u.baseAmount(e.Field)
	}
	// The checker types `.length` on a string or list as a select, not a call.
	if e.Field == "length" {
		switch obj.(type) {
		case string:
			return intrinsics["string.length"]([]any{obj})
		case []any:
			return intrinsics["list.length"]([]any{obj})
		}
	}
	return nil, fmt.Errorf("cannot select field %q on %T", e.Field, obj)
}

// intrinsicMethod is the intrinsic behind a component type's method named
// field, or nil.
func intrinsicMethod(t *ir.Type, field string) func([]any) (any, error) {
	if t == nil || t.Kind != ir.TypeComponent {
		return nil
	}
	comp, ok := t.Decl.(*ir.Component)
	if !ok {
		return nil
	}
	fn := comp.Methods[field]
	if fn == nil || fn.Intrinsic == "" {
		return nil
	}
	return intrinsics[fn.Intrinsic]
}

// i18nPluralConst maps the predeclared i18n PluralKey field names to their
// Go runtime values. Returns (PluralKey, true) if found; (nil, false) otherwise.
func i18nPluralConst(field string) (goi18n.PluralKey, bool) {
	switch field {
	case "zero":
		return goi18n.PluralZero, true
	case "one":
		return goi18n.PluralOne, true
	case "two":
		return goi18n.PluralTwo, true
	case "few":
		return goi18n.PluralFew, true
	case "many":
		return goi18n.PluralMany, true
	case "other":
		return goi18n.PluralOther, true
	}
	return goi18n.PluralKey{}, false
}

func (env *Env) evalIndex(e *ir.Index) (any, error) {
	obj, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	idx, err := env.Eval(e.Idx)
	if err != nil {
		return nil, err
	}
	if list, ok := obj.([]any); ok {
		i := ToInt(idx)
		if i < 0 || i >= len(list) {
			return nil, fmt.Errorf("index %d out of range (len %d)", i, len(list))
		}
		return list[i], nil
	}
	// One copy of a node a loop rendered under one id, where the loop rendered
	// it once: ResolveElementRef hands a single node back as itself rather
	// than as a list of one.
	if e.Type != nil && e.Type.Kind == ir.TypeComponent && obj != nil {
		if _, isList := obj.([]any); !isList {
			if i := ToInt(idx); i != 0 {
				return nil, fmt.Errorf("index %d out of range (len 1)", i)
			}
			return obj, nil
		}
	}
	if m, ok := obj.(map[string]any); ok {
		key := fmt.Sprintf("%v", idx)
		if v, ok := m[key]; ok {
			return v, nil
		}
		// Miss → the zero value of the map's element type.
		return zeroValueFor(e.Type), nil
	}
	return nil, fmt.Errorf("cannot index %T", obj)
}

func (env *Env) evalConversion(e *ir.Conversion) (any, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	if e.Type == nil {
		return v, nil
	}
	switch e.Type.Kind {
	case ir.TypeInt:
		if r, ok := opeval.ConvertInt(v, e.Type.Bits, e.Type.Unsigned); ok {
			return r, nil
		}
		return ToInt(v), nil
	case ir.TypeFloat:
		if r, ok := opeval.ConvertFloat(v, e.Type.Bits); ok {
			return r, nil
		}
		return toFloat(v), nil
	case ir.TypeString:
		return fmt.Sprintf("%v", v), nil
	case ir.TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return false, nil
	}
	return v, nil
}

func (env *Env) evalListLit(e *ir.ListLit) (any, error) {
	var list []any
	for _, el := range e.Elems {
		if sp, ok := el.(*ir.Spread); ok {
			v, err := env.Eval(sp.Operand)
			if err != nil {
				return nil, err
			}
			if items, ok := copyOf(v, sp.Operand.ExprType()).([]any); ok {
				list = append(list, items...)
			} else {
				list = append(list, v)
			}
			continue
		}
		v, err := env.Eval(el)
		if err != nil {
			return nil, err
		}
		list = append(list, copyOf(v, el.ExprType()))
	}
	if list == nil {
		list = []any{}
	}
	return list, nil
}

func (env *Env) evalStructLit(e *ir.StructLit) (any, error) {
	if member, payload, ok := ir.EnumLitMember(e); ok {
		v, err := env.Eval(payload)
		if err != nil {
			return nil, err
		}
		return EnumValue{Type: e.Type, Member: member, Payload: copyOf(v, payload.ExprType())}, nil
	}
	if e.Def != nil && e.Def.Intrinsic == ir.BuilderIntrinsic {
		return &Builder{}, nil
	}
	s := NewStruct(e.Def, e.Type)
	if e.Def != nil && e.Def.Release != nil {
		var at ast.Pos
		if e.AST != nil {
			at = e.AST.Pos
		}
		s.Handle = env.owned().alloc(e.Def, at)
	}
	for _, f := range e.Fields {
		v, err := env.Eval(f.Value)
		if err != nil {
			return nil, err
		}
		v = copyOf(v, f.Value.ExprType())
		if f.Spread {
			if src, ok := v.(*Struct); ok {
				s.Merge(src)
			}
			continue
		}
		s.Set(f.Name, v)
	}
	return s, env.fillOmittedFields(s, e.Def)
}

// fillOmittedFields gives every field of def a literal left out the value the
// declaration means by omitting it: its written default, or its type's zero,
// as every compiled target's struct holds. The fields end in def's order,
// which is the order every backend emits.
func (env *Env) fillOmittedFields(s *Struct, def *ir.StructDef) error {
	if def == nil || !slices.ContainsFunc(def.Fields, func(f *ir.StructField) bool {
		_, ok := s.Get(f.Name)
		return !ok
	}) {
		return nil
	}
	fields := make([]Field, 0, len(def.Fields))
	for _, f := range def.Fields {
		if i := slices.IndexFunc(s.Fields, func(g Field) bool { return g.Name == f.Name }); i >= 0 {
			fields = append(fields, s.Fields[i])
			continue
		}
		init := f.Default
		if init == nil {
			init = ir.ZeroExpr(f.Type)
		}
		if init == nil {
			// A type with no zero of its own here -- a type parameter the
			// declaration left unbound -- stays absent, as before.
			continue
		}
		v, err := env.Eval(init)
		if err != nil {
			return err
		}
		fields = append(fields, Field{Name: f.Name, Value: copyOf(v, f.Type)})
	}
	// A spread may have supplied a field def does not name; keep it after.
	for _, f := range s.Fields {
		if !slices.ContainsFunc(fields, func(g Field) bool { return g.Name == f.Name }) {
			fields = append(fields, f)
		}
	}
	s.Fields = fields
	return nil
}

func (env *Env) evalMapLitIR(e *ir.MapLitIR) (any, error) {
	m := make(map[string]any, len(e.Entries))
	for _, entry := range e.Entries {
		k, err := env.Eval(entry.Key)
		if err != nil {
			return nil, err
		}
		v, err := env.Eval(entry.Value)
		if err != nil {
			return nil, err
		}
		// Keys may be PluralKey values (from i18n.exactly/i18n.one etc.) or
		// plain strings. Store them as formatted strings so the map is
		// map[string]any; toPluralKeyStringMap will re-interpret them.
		m[fmt.Sprintf("%v", k)] = copyOf(v, entry.Value.ExprType())
	}
	return m, nil
}

func (env *Env) evalBinary(e *ir.Binary) (any, error) {
	if sel, path, present, ok := ir.NullTestedCopy(e); ok && len(path) > 0 {
		return env.copyNullTest(sel, path, present)
	}
	left, err := env.Eval(e.Left)
	if err != nil {
		return nil, err
	}
	if lb, ok := left.(bool); ok && (e.Op == ast.BinAnd && !lb || e.Op == ast.BinOr && lb) {
		return lb, nil
	}
	right, err := env.Eval(e.Right)
	if err != nil {
		return nil, err
	}
	return env.binaryValues(e, left, right)
}

// binaryValues is e applied to its operands' values, left and right.
func (env *Env) binaryValues(e *ir.Binary, left, right any) (any, error) {
	lu, leftIsUnit := left.(unitValue)
	ru, rightIsUnit := right.(unitValue)

	if leftIsUnit || rightIsUnit {
		switch e.Op {
		case ast.BinEq:
			if leftIsUnit && rightIsUnit {
				return lu.Equal(ru), nil
			}
			return false, nil
		case ast.BinNeq:
			if leftIsUnit && rightIsUnit {
				return !lu.Equal(ru), nil
			}
			return true, nil
		case ast.BinAdd:
			if leftIsUnit && rightIsUnit {
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot add %s and %s units", lu.Suffix, ru.Suffix)
				}
				return lu.Add(ru), nil
			}
		case ast.BinSub:
			if leftIsUnit && rightIsUnit {
				if !lu.sameFamily(ru) {
					return nil, fmt.Errorf("cannot subtract %s and %s units", lu.Suffix, ru.Suffix)
				}
				return lu.Sub(ru), nil
			}
		case ast.BinMul:
			if leftIsUnit && !rightIsUnit {
				return lu.Scale(toFloat(right)), nil
			}
			if !leftIsUnit && rightIsUnit {
				return ru.Scale(toFloat(left)), nil
			}
			return nil, fmt.Errorf("cannot multiply two unit values")
		case ast.BinDiv:
			if leftIsUnit && !rightIsUnit {
				r := toFloat(right)
				if r == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return lu.Scale(1 / r), nil
			}
			// unit / unit is the dimensionless ratio the checker types as
			// float. Only for a single-base unit: a multi-base value is a
			// magnitude per base, so a ratio would need one answer per base
			// and there is none to pick -- the same premise that refuses
			// ordering two of them.
			if lu.sameFamily(ru) && lu.singleBase() && ru.singleBase() {
				d := ru.magnitude()
				if d == 0 {
					return nil, fmt.Errorf("division by zero")
				}
				return lu.magnitude() / d, nil
			}
			return nil, fmt.Errorf("cannot divide %s by %s", lu.Suffix, ru.Suffix)
		}
	}

	switch e.Op {
	case ast.BinEq:
		return equals(left, right), nil
	case ast.BinNeq:
		return !equals(left, right), nil
	case ast.BinAnd:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("&& requires bool operands, got %T and %T", left, right)
		}
		return lb && rb, nil
	case ast.BinOr:
		lb, ok1 := left.(bool)
		rb, ok2 := right.(bool)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("|| requires bool operands, got %T and %T", left, right)
		}
		return lb || rb, nil
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		if err := orderable(left, right); err != nil {
			return nil, err
		}
		switch cmp := compareOrdered(left, right); e.Op {
		case ast.BinLt:
			return cmp < 0, nil
		case ast.BinLte:
			return cmp <= 0, nil
		case ast.BinGt:
			return cmp > 0, nil
		default:
			return cmp >= 0, nil
		}
	case ast.BinAdd:
		if ls, ok := left.(string); ok {
			return ls + fmt.Sprintf("%v", right), nil
		}
		if rs, ok := right.(string); ok {
			return fmt.Sprintf("%v", left) + rs, nil
		}
		return opeval.Arith(ast.BinAdd, left, right, numKindOf(e.Type))
	case ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
		return opeval.Arith(e.Op, left, right, numKindOf(e.Type))
	}
	return nil, fmt.Errorf("unknown binary op %d", e.Op)
}

// numKindOf maps an IR result type to the opeval width descriptor so arithmetic
// wraps to the right width. A nil or non-numeric type yields the zero NumKind,
// which is default int / float-fallback.
func numKindOf(t *ir.Type) opeval.NumKind {
	if t == nil || !t.IsNumeric() {
		return opeval.NumKind{}
	}
	return opeval.NumKind{Bits: t.Bits, Unsigned: t.Unsigned, Float: t.Kind == ir.TypeFloat}
}

func (env *Env) evalUnary(e *ir.Unary) (any, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case ast.UnaryNot:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("! requires bool operand, got %T", v)
		}
		return !b, nil
	case ast.UnaryNeg:
		// Negation is 0 - v at the result width, so sized integers wrap
		// correctly (e.g. -(int8 -128) is -128) and floats stay floats.
		return opeval.Arith(ast.BinSub, 0, v, numKindOf(e.Type))
	case ast.UnaryDeref:
		// `*t` for an &-bound loop element (`for var &t = list`). The operand is a
		// listRef into the live list; reading derefs to the current element.
		if ref, ok := v.(*listRef); ok {
			return ref.get(), nil
		}
		// A non-ref operand (e.g. the interpreter already binds structs by
		// reference) derefs to itself.
		return v, nil
	}
	return nil, fmt.Errorf("unknown unary op %d", e.Op)
}

// listRef is an interpreter lvalue into a list element, produced when a loop
// binds its element variable with `&` (`for var &t = list`). Dereferencing reads
// the live element; assigning through the deref writes it back by index.
type listRef struct {
	list []any
	idx  int
	// iter is the loop's iterable and at the scope it was evaluated in, which
	// is where a write through the element lands (listRef.cell).
	iter ir.Expr
	at   *Env
}

func (r *listRef) get() any  { return r.list[r.idx] }
func (r *listRef) set(v any) { r.list[r.idx] = v }

func (env *Env) evalCall(call *ir.Call) (any, error) {
	defer env.enterCall(call)()
	v, err := env.evalCallSite(call)
	if raised, ok := err.(*RaisedError); ok {
		if call.ErrorMode == ir.ErrorRender {
			return env.caughtRender(call, raised)
		}
		return nil, env.dispatchRaise(call, raised)
	}
	return v, err
}

// caughtRender is what a view's own expression is when its call raised: the
// boundary around what rendered hears it, and the render goes on with the
// call's zero value. The handler runs in the scope that rendered its
// boundary, the frame mounting pushed -- a loop's row renders in a snapshot,
// where its write was lost -- and one the checker named none for, its
// component holding no boundary, is the nearest frame's. A handler raising
// again is offered outward, as catchEscaped offers an event handler's.
func (env *Env) caughtRender(call *ir.Call, raised *RaisedError) (any, error) {
	frames, _ := env.ContextVals[raiseScope].([]raiseFrame)
	i := 0
	if h := call.ResolvedHandler; h != nil {
		i = -1
		for j, f := range frames {
			if f.handler == h {
				i = j
				break
			}
		}
		if i < 0 {
			if err := env.invokeHandler(h, raised.Event); err != nil && !IsReturn(err) {
				if again, ok := err.(*RaisedError); ok {
					raised, i = again, len(frames)
				} else {
					return nil, err
				}
			} else {
				return env.zeroOf(call.Type)
			}
		}
	}
	if i >= len(frames) {
		return nil, raised
	}
	if err := catchEscaped(map[*ir.Context]any{raiseScope: frames[i:]}, raised); err != nil {
		return nil, err
	}
	return env.zeroOf(call.Type)
}

// zeroOf is t's zero value, as the lowering spells it (ir.ZeroExpr).
func (env *Env) zeroOf(t *ir.Type) (any, error) {
	if z := ir.ZeroExpr(t); z != nil {
		return env.Eval(z)
	}
	return zeroValueFor(t), nil
}

func (env *Env) evalCallSite(call *ir.Call) (any, error) {
	if call.Func != nil && call.Func.BuildOnly {
		return env.callBuildOnly(call)
	}
	if isCNative(call.Func) {
		return env.callCNative(call)
	}
	// i18n by the id, before the call shape is examined: the `i18n._*`
	// primitives arrive plain once the wrapper is inlined, so they are not
	// reliably a namespace call by the time they get here. The interpreter is
	// the only implementation of these -- the primitives' bodies were dropped
	// as placeholders after checking. An entry point with a body runs it, as
	// every target does: the body reads the `locale` context and hands it to
	// the primitive, and its parameters bind their defaults. Answered by its
	// id, `i18n.numberFloat(x)` lost the `style` it defaults and formatted for
	// env.Locale whatever a provider or a test's t.setContext said.
	if call.Func != nil {
		if id := call.Func.Intrinsic; strings.HasPrefix(id, "i18n._") {
			if result, handled, err := env.evalI18nPrimitiveCall(id, call.Args); handled {
				return result, err
			}
		} else if strings.HasPrefix(id, "i18n.") && len(call.Func.Block) == 0 {
			if result, handled, err := env.evalI18nCall(strings.TrimPrefix(id, "i18n."), call.Args); handled {
				return result, err
			}
		}
	}

	if result, handled, err := env.evalCodecCall(call); handled {
		return result, err
	}

	// Namespace call (ns.foo / html.div) — receiver preserved.
	if call.Receiver != nil {
		return env.evalNamespaceCall(call)
	}

	// Type-attached method call (checker-normalized): Args[0] is the receiver.
	if call.Func != nil && call.Func.IsMethod() {
		return env.evalTypeMethodCall(call)
	}

	if call.Func != nil {
		// An intrinsic this interpreter implements answers a bare call too,
		// as it does a qualified one: `follow(to, params)` in the package that
		// declares it. The declaration's body is a placeholder.
		if call.Func.Intrinsic != "" {
			values, err := env.evalCallArgs(call.Args)
			if err != nil {
				return nil, err
			}
			args, err := env.boundArgValues(call.Func, call.Args, values)
			if err != nil {
				return nil, err
			}
			if result, handled, err := env.runClockIntrinsic(call.Func, args); handled {
				return result, err
			}
			if _, ok := intrinsics[call.Func.Intrinsic]; ok {
				result, _, err := runIntrinsic(call.Func.Intrinsic, args)
				return result, err
			}
			return env.callUserFuncArgValues(call.Func, call.Args, values)
		}
		return env.EvalUserFuncCallArgs(call.Func, call.Args)
	}

	// Callee expression (func-typed var).
	if call.Callee != nil {
		// Bare named-function / component-method call, e.g. `bump()`. The
		// checker leaves Func nil for component methods (they aren't in plain
		// scope) and sets Callee to the bare ident. Resolve and invoke directly,
		// honoring the call's args. This must precede Eval(callee): lookup()
		// auto-invokes a zero-arg function when its name is *read*, which would
		// fire the body as a side effect here and then discard the result.
		if id, ok := call.Callee.(*ir.Ident); ok && !env.varInScope(id.Sym) {
			if fn, ok := id.Sym.(*ir.Func); ok {
				return env.EvalUserFuncCallArgs(fn, call.Args)
			}
			if fn := env.resolveCallableFunc(id.Name); fn != nil {
				return env.EvalUserFuncCallArgs(fn, call.Args)
			}
		}
		v, err := env.Eval(call.Callee)
		if err != nil {
			return nil, err
		}
		if lv, ok := v.(*LambdaValue); ok {
			args, err := env.evalCallArgs(call.Args)
			if err != nil {
				return nil, err
			}
			return lv.Call(args)
		}
	}
	return nil, fmt.Errorf("cannot call unresolved expression")
}

func (env *Env) evalTypeMethodCall(call *ir.Call) (any, error) {
	method := call.Func.Name
	receiverName := call.Func.RecvName()

	// ErrorRaise: construct an error payload and bubble a RaisedError
	// up the Go error chain. The originating CallStmt's ErrorMode then
	// routes it into the resolved handler (or propagates).
	if call.Func.Intrinsic == "error.raise" {
		evt := map[string]any{"message": "", "kind": ""}
		if len(call.Args) >= 1 {
			if v, err := env.Eval(call.Args[0].Value); err == nil {
				evt["message"] = v
			}
		}
		if len(call.Args) >= 2 {
			if v, err := env.Eval(call.Args[1].Value); err == nil {
				evt["kind"] = v
			}
		}
		return nil, &RaisedError{Event: evt}
	}

	// Alert and File have visible effects a headless run cannot perform, so
	// the interpreter records them and answers with a fixed value.
	switch call.Func.Intrinsic {
	case "Alert.toast":
		return env.logAlertToast(call.Args)
	case "Alert.info", "Alert.warn", "Alert.error":
		return env.logAlertSingle(method, call.Args)
	case "Alert.confirm":
		return env.logAlertConfirm(call.Args)
	case "File.pick":
		env.Log = append(env.Log, "[File.pick]")
		return "/mock/file.txt", nil
	case "File.pickFolder":
		env.Log = append(env.Log, "[File.pickFolder]")
		return "/mock/folder", nil
	}

	// The receiver alone first: a test or component receiver evaluates the
	// rest itself, and evaluating them here too ran each twice.
	var evalArgs []any
	if len(call.Args) > 0 {
		recv, err := env.Eval(call.Args[0].Value)
		if err != nil {
			return nil, err
		}
		if tv, ok := recv.(TestingT); ok {
			return tv.CallMethod(env, method, argExprs(call.Args[1:]))
		}
		if cv, ok := recv.(ComponentValue); ok {
			if result, handled, err := cv.InvokeMethod(env, method, call.Args[1:]); handled {
				return result, err
			}
		}
		rest, err := env.evalCallArgs(call.Args[1:])
		if err != nil {
			return nil, err
		}
		// A method called through a node's `#id` runs in the instance the
		// handle names, where its props and the cells of its unbound two-way
		// props are: `details.open()` writes the window's own `visible`.
		if m, ok := recv.(map[string]any); ok {
			inst, _ := m["__inst"].(*ir.NodeInst)
			if ce, ok := m["__compEnv"].(*Env); ok && ce != nil && inst != nil && inst.Component != nil && slices.Contains(inst.Component.Funcs, call.Func) {
				return ce.callUserFuncArgValues(call.Func, call.Args[1:], rest)
			}
		}
		evalArgs = append([]any{recv}, rest...)
	}

	if result, handled, err := env.runClockIntrinsic(call.Func, evalArgs); handled {
		return result, err
	}

	if result, handled, err := runIntrinsic(call.Func.Intrinsic, evalArgs); handled {
		return result, err
	}

	// List mutation (push/remove) needs writeback; evaluate before user funcs
	// since stdlib push/remove delegate to untranslated intrinsics.
	if receiverName == "list" && isListMutation(method) && len(call.Args) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}

	// Reached only when the id above found no implementation. Every
	// #[intrinsic] carries a body now, and most are placeholders standing in
	// for a backend's — running one answers with a plausible wrong value
	// rather than an error, which is why the id is tried first.
	// The checker already resolved which member this call names; re-deriving
	// it from the receiver's name would fail for a type reached through an
	// import alias, whose name here is not the name it was declared under.
	if len(call.Func.Block) > 0 {
		callEnv, args, values := env, call.Args, evalArgs
		// A method on a generic receiver declares no receiver parameter and
		// names the value `this`, so the leading argument the checker
		// normalized in has nothing to bind to. Supply it the way a component
		// method gets its receiver, in a scope of its own.
		if len(call.Args) == len(call.Func.Params)+1 && len(evalArgs) > 0 {
			callEnv = env.Snapshot()
			callEnv.SetReceiver(evalArgs[0])
			args, values = call.Args[1:], evalArgs[1:]
		}
		return callEnv.callUserFuncArgValues(call.Func, args, values)
	}

	// List/string higher-order and other built-in methods.
	if len(evalArgs) >= 1 {
		return env.evalBuiltinMethod(call, method, evalArgs)
	}
	return nil, fmt.Errorf("unknown method %q", receiverName+"."+method)
}

func (env *Env) evalNamespaceCall(call *ir.Call) (any, error) {
	// Static-form receiver: Type.method(args) where Type is a type name. The
	// checker leaves Func nil when the method is a user-defined type method
	// (component-scoped) the symbol table doesn't see. Dispatch by qualified
	// name through the declaration it names.
	if ident, ok := call.Receiver.(*ir.Ident); ok {
		if _, valErr := env.evalIdent(ident); valErr != nil {
			method := methodNameFromCall(call)

			// Resolve the member once: its mark says whether this interpreter
			// implements it, and its body says whether there is anything to
			// run if it does not. A namespace's members include bodyless
			// intrinsic declarations, and running one of those returns null.
			if fn, ok := env.methodOn(ident.Name, method); ok && (fn.Intrinsic != "" || len(fn.Block) > 0) {
				evalArgs, err := env.evalCallArgs(call.Args)
				if err != nil {
					return nil, err
				}
				if result, handled, err := runIntrinsic(fn.Intrinsic, evalArgs); handled {
					return result, err
				}
				if len(fn.Block) > 0 {
					return env.callUserFuncArgValues(fn, call.Args, evalArgs)
				}
			}
		}
	}

	// If the receiver is a value-bearing expression (variable ident, not a
	// type/namespace marker), treat this as an instance method call on that
	// value. This covers c.method(...) on component values and t.method(...)
	// on the Test value where the checker left Func nil (unresolved method).
	recv, err := env.Eval(call.Receiver)
	if err == nil {
		method := methodNameFromCall(call)
		if method != "" {
			// Element-ref event invocation: `c.btn.click()` (the handler is
			// stored at key "@click" on the rendered element map). The name
			// arrives "@"-prefixed when the checker statically tagged it, or
			// bare when the receiver's type was dynamic (e.g.
			// c.children[0].click()) and the checker couldn't resolve the
			// host component — so try the "@"-prefixed key regardless.
			explicitEvent := strings.HasPrefix(method, "@")
			event := strings.TrimPrefix(method, "@")
			if m, ok := recv.(map[string]any); ok {
				res, handled, err := env.InvokeElementEvent(m, event, func() ([]any, error) { return env.evalCallArgs(call.Args) })
				if handled {
					return res, err
				}
			}
			if explicitEvent {
				// A component instance answers for itself: what it renders
				// may report the event.
				if cv, ok := recv.(ComponentValue); ok {
					if result, handled, err := cv.InvokeMethod(env, method, call.Args); handled {
						return result, err
					}
				}
				// Checker tagged this as an event but no handler is installed:
				// the caller never bound it, so the invocation is a no-op.
				return nil, nil
			}
			if tv, ok := recv.(TestingT); ok {
				return tv.CallMethod(env, method, argExprs(call.Args))
			}
			if cv, ok := recv.(ComponentValue); ok {
				if result, handled, err := cv.InvokeMethod(env, method, call.Args); handled {
					return result, err
				}
			}
			// Instance method on a primitive value: dispatch by runtime type.
			evalArgs := make([]any, 0, len(call.Args)+1)
			evalArgs = append(evalArgs, recv)
			for _, a := range call.Args {
				v, err := env.Eval(a.Value)
				if err != nil {
					return nil, err
				}
				evalArgs = append(evalArgs, v)
			}
			// The receiver's runtime type names the declaration whose member
			// this is; the member's mark says how to run it.
			recvFn, recvOK := env.methodOn(runtimeTypeName(recv), method)
			if recvOK {
				if result, handled, err := runIntrinsic(recvFn.Intrinsic, evalArgs); handled {
					return result, err
				}
			}
			// Mutation methods on lists need a writeback; dispatch before
			// user-defined stdlib bodies that delegate to intrinsics.
			if isListMutation(method) || method == "filter" || method == "map" {
				return env.evalBuiltinMethodFromRecv(call.Receiver, method, recv, evalArgs[1:])
			}
			if fn := recvFn; recvOK && len(fn.Block) > 0 {
				// A method on a generic receiver (list[T], map[K,V]) declares
				// no receiver parameter and names the value `this`, so there
				// is nothing in the argument list to bind it to. Supply it the
				// way a component method gets its receiver, in a scope of its
				// own so the binding does not outlive the call.
				callEnv, values := env, evalArgs
				if len(fn.Params) == len(call.Args) {
					callEnv = env.Snapshot()
					callEnv.SetReceiver(recv)
					values = evalArgs[1:]
				}
				return callEnv.EvalUserFuncWithValues(fn, values)
			}
			// Enum value method fallback: when recv is a bare string and no
			// dispatch succeeded, scan user-defined enums for a matching
			// member and try the method against that enum's namespace: an
			// enum member the interpreter holds as its bare name.
			if s, ok := recv.(string); ok && env.Pkg != nil {
				for _, ed := range env.Pkg.Enums {
					for _, m := range ed.Members {
						if m.Name == s {
							if fn, ok := ed.Methods[method]; ok {
								return env.EvalUserFuncWithValues(fn, evalArgs)
							}
						}
					}
				}
			}
			return env.evalBuiltinMethodFromRecv(call.Receiver, method, recv, evalArgs[1:])
		}
	}

	// Resolved namespace call delegates to the type-method path.
	if call.Func != nil {
		return env.evalTypeMethodCall(&ir.Call{
			AST:  call.AST,
			Type: call.Type,
			Func: call.Func,
			Args: call.Args,
		})
	}
	return nil, fmt.Errorf("unresolved namespace call")
}

// runEventHandlerValues runs a handler against values rather than expressions,
// which is the shape an event arrives in from a host: the widget already
// evaluated them.
func (env *Env) runEventHandlerValues(fn *ir.Func, vals []any) (any, error) {
	for i, p := range fn.Params {
		if i >= len(vals) {
			break
		}
		env.Set(p, bindArg(vals[i], p))
	}
	return env.execBlockForResult(fn.Block)
}

// evalBuiltinMethodFromRecv dispatches list/string built-in methods when the
// receiver expression is known separately from the rest of the args.
func (env *Env) evalBuiltinMethodFromRecv(recvExpr ir.Expr, method string, recv any, rest []any) (any, error) {
	var recvType *ir.Type
	if recvExpr != nil {
		recvType = recvExpr.ExprType()
	}
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				if b, ok := v.(bool); ok && b {
					out = append(out, item)
				}
			}
			if out == nil {
				out = []any{}
			}
			return out, nil
		}
	case "map":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			rest[0] = copyOf(rest[0], elemType(recvType))
			newList := append(list, rest[0])
			return env.writeBackList(recvExpr, newList, writeInsert, len(list), rest[0])
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			idx := ToInt(rest[0])
			if idx < 0 || idx >= len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			removed := list[idx]
			newList := append(list[:idx], list[idx+1:]...)
			return env.writeBackList(recvExpr, newList, writeRemove, idx, removed)
		}
	case "insert":
		if list, ok := recv.([]any); ok && len(rest) == 2 {
			rest[1] = copyOf(rest[1], elemType(recvType))
			idx := ToInt(rest[0])
			if idx < 0 || idx > len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			return env.writeBackList(recvExpr, slices.Insert(slices.Clone(list), idx, rest[1]), writeInsert, idx, rest[1])
		}
	case "clear":
		if _, ok := recv.([]any); ok && len(rest) == 0 {
			return env.writeBackList(recvExpr, []any{}, writeClear, 0, nil)
		}
	case "contains":
		if s, ok := recv.(string); ok && len(rest) == 1 {
			return strings.Contains(s, fmt.Sprintf("%v", rest[0])), nil
		}
	}
	// --- Map methods ---
	if m, ok := recv.(map[string]any); ok {
		return mapMethodResult(method, m, rest)
	}
	return nil, fmt.Errorf("unsupported method %q on %T", method, recv)
}

// isListMutation is whether method is one of list's in-place mutations, which
// write their receiver back rather than returning a new list.
func isListMutation(method string) bool {
	switch method {
	case "push", "remove", "insert", "clear":
		return true
	}
	return false
}

// methodNameFromCall recovers the method name for a Receiver-bearing call from
// the AST back-reference (set by the checker when Func couldn't be resolved).
// For an element-ref event trigger (`c.btn.click()`) the name is prefixed
// with "@", which is the interpreter's own key and not source syntax.
func methodNameFromCall(call *ir.Call) string {
	// Element-ref event triggers (`c.btn.click()`) are tagged by the
	// checker; the interpreter keys handlers under "@<event>" internally.
	if call.Event != "" {
		return "@" + call.Event
	}
	if call.Func != nil {
		return call.Func.Name
	}
	if call.AST == nil {
		return ""
	}
	if sel, ok := call.AST.Func.(*ast.SelectExpr); ok {
		return sel.Field
	}
	return ""
}

func (env *Env) evalBuiltinMethod(call *ir.Call, method string, evalArgs []any) (any, error) {
	recv := evalArgs[0]
	rest := evalArgs[1:]
	var recvType *ir.Type
	if len(call.Args) > 0 && call.Args[0].Value != nil {
		recvType = call.Args[0].Value.ExprType()
	}
	switch method {
	case "filter":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("filter requires a lambda, got %T", rest[0])
			}
			var out []any
			for _, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				if b, ok := v.(bool); ok && b {
					out = append(out, item)
				}
			}
			if out == nil {
				out = []any{}
			}
			return out, nil
		}
	case "map":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			lv, ok := rest[0].(*LambdaValue)
			if !ok {
				return nil, fmt.Errorf("map requires a lambda, got %T", rest[0])
			}
			out := make([]any, len(list))
			for i, item := range list {
				v, err := lv.Call([]any{item})
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
	case "push":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			rest[0] = copyOf(rest[0], elemType(recvType))
			newList := append(list, rest[0])
			return env.writeBackList(call.Args[0].Value, newList, writeInsert, len(list), rest[0])
		}
	case "remove":
		if list, ok := recv.([]any); ok && len(rest) == 1 {
			idx := ToInt(rest[0])
			if idx < 0 || idx >= len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			removed := list[idx]
			newList := append(list[:idx], list[idx+1:]...)
			return env.writeBackList(call.Args[0].Value, newList, writeRemove, idx, removed)
		}
	case "insert":
		if list, ok := recv.([]any); ok && len(rest) == 2 {
			rest[1] = copyOf(rest[1], elemType(recvType))
			idx := ToInt(rest[0])
			if idx < 0 || idx > len(list) {
				return list, nil // documented: out of range leaves the list unchanged
			}
			return env.writeBackList(call.Args[0].Value, slices.Insert(slices.Clone(list), idx, rest[1]), writeInsert, idx, rest[1])
		}
	case "clear":
		if _, ok := recv.([]any); ok && len(rest) == 0 {
			return env.writeBackList(call.Args[0].Value, []any{}, writeClear, 0, nil)
		}
	case "contains":
		if s, ok := recv.(string); ok && len(rest) == 1 {
			return strings.Contains(s, fmt.Sprintf("%v", rest[0])), nil
		}
	}
	// --- Map methods ---
	if m, ok := recv.(map[string]any); ok {
		return mapMethodResult(method, m, rest)
	}
	return nil, fmt.Errorf("unsupported method %q on %T", method, recv)
}

// writeBackList applies a mutated list back to its originating variable.
func (env *Env) writeBackList(target ir.Expr, newList []any, kind writeKind, index int, item any) (any, error) {
	defer env.noteWrite(target, kind, index, item)
	switch t := target.(type) {
	case *ir.Ident:
		if owner := env.findVarOwner(t.Sym); owner != nil {
			owner.vals[t.Sym] = newList
			// Noted like an assignment, because that is what it is: a mutating
			// method writes its receiver, and RebindFrom carries back only what
			// a scope wrote. Without this a bare `xs.push(v)` inside a loop
			// iteration mutated that iteration's snapshot and nothing else --
			// which the effect fixtures caught the moment they stopped spelling
			// it `xs = xs.push(v)`, an ir.Assign that noted itself.
			owner.noteAssigned(t.Sym)
		} else {
			env.Set(t.Sym, newList)
			env.noteAssigned(t.Sym)
		}
	case *ir.Select:
		obj, err := env.Eval(t.Operand)
		if err != nil {
			return nil, err
		}
		if cv, ok := obj.(ComponentValue); ok {
			if err := cv.WriteBackList(t.Field, newList); err != nil {
				return nil, err
			}
		}
		// A list in a struct's field: the struct is shared, so writing the
		// field is the write. Without it `h.items.push(2)` grew a slice
		// nothing held.
		if st, ok := obj.(*Struct); ok {
			st.Set(t.Field, newList)
		}
	case *ir.Index:
		// A list held in an element or an entry: `xs[0].push(5)`. The holder
		// is shared, as a struct is, so writing its slot is the write.
		obj, err := env.Eval(t.Operand)
		if err != nil {
			return nil, err
		}
		idx, err := env.Eval(t.Idx)
		if err != nil {
			return nil, err
		}
		switch o := obj.(type) {
		case []any:
			i := ToInt(idx)
			if i < 0 || i >= len(o) {
				return nil, fmt.Errorf("index %d out of range [0:%d]", i, len(o))
			}
			o[i] = newList
		case map[string]any:
			o[fmt.Sprintf("%v", idx)] = newList
		default:
			return nil, fmt.Errorf("testrunner.writeBackList: cannot index %T", obj)
		}
	default:
		return nil, fmt.Errorf("testrunner.writeBackList: unhandled target ir.Expr %T", target)
	}
	return newList, nil
}

// mapMethodResult dispatches map built-in methods. All runtime maps use
// map[string]any regardless of the declared K type (keys are stringified in
// evalMapLitIR), so a single helper suffices.
func mapMethodResult(method string, m map[string]any, rest []any) (any, error) {
	switch method {
	case "length":
		return len(m), nil
	case "keys":
		out := make([]any, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		return out, nil
	case "values":
		out := make([]any, 0, len(m))
		for _, v := range m {
			out = append(out, v)
		}
		return out, nil
	case "contains":
		if len(rest) != 1 {
			return nil, fmt.Errorf("contains requires 1 argument")
		}
		k := fmt.Sprintf("%v", rest[0])
		_, ok := m[k]
		return ok, nil
	case "get":
		if len(rest) != 2 {
			return nil, fmt.Errorf("get requires 2 arguments")
		}
		k := fmt.Sprintf("%v", rest[0])
		if v, ok := m[k]; ok {
			return v, nil
		}
		return rest[1], nil // default
	}
	return nil, fmt.Errorf("unsupported method %q on map", method)
}

func (env *Env) evalCallArgs(args []ir.CallArg) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		v, err := env.Eval(a.Value)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, env.checkPassed(out)
}

func argExprs(args []ir.CallArg) []ir.Expr {
	out := make([]ir.Expr, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

// CallUserFuncValues invokes fn with pre-evaluated Go-side argument values
// (no expression evaluation needed). Used by the optimizer's interpretFunc
// adapter, which already has folded constant values in hand.
func (env *Env) CallUserFuncValues(fn *ir.Func, args []any) (any, error) {
	defer env.enterFunc(fn)()
	child := env.Snapshot()
	for i, p := range fn.Params {
		if i < len(args) {
			child.Set(p, bindArg(args[i], p))
		}
	}
	return child.execBlockForResult(fn.Block)
}

// EvalUserFuncWithValues is like EvalUserFunc but takes pre-evaluated arg
// values. Used by callers that evaluate args in a different env from the
// one used to execute the function body (e.g. component methods).
func (env *Env) EvalUserFuncWithValues(fn *ir.Func, args []any) (any, error) {
	return env.evalUserFuncCore(fn, args)
}

// CopyDepth transfers the call-depth counter from src to dst. Used by
// out-of-tree dispatchers (e.g. testrunner component-method dispatch) to
// keep depth tracking accurate across env boundaries.
func CopyDepth(src, dst *Env) {
	if src == nil || dst == nil {
		return
	}
	dst.depth = src.depth
}

func (env *Env) EvalUserFunc(fn *ir.Func, argExprs []ir.Expr) (any, error) {
	args := make([]any, len(argExprs))
	for i, a := range argExprs {
		v, err := env.Eval(a)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return env.evalUserFuncCore(fn, args)
}

// EvalUserFuncCallArgs evaluates fn using named CallArg binding. Args with a
// Name field are bound to the matching parameter by name; args without a Name
// are bound positionally. Parameters with no supplied arg use their default.
func (env *Env) EvalUserFuncCallArgs(fn *ir.Func, callArgs []ir.CallArg) (any, error) {
	values, err := env.evalCallArgs(callArgs)
	if err != nil {
		return nil, err
	}
	return env.callUserFuncArgValues(fn, callArgs, values)
}

// callUserFuncArgValues is EvalUserFuncCallArgs over arguments already
// evaluated, values[i] being callArgs[i]'s: a caller that had to read them
// first must not evaluate them a second time.
func (env *Env) callUserFuncArgValues(fn *ir.Func, callArgs []ir.CallArg, values []any) (any, error) {
	args, err := env.boundArgValues(fn, callArgs, values)
	if err != nil {
		return nil, err
	}
	return env.evalUserFuncCore(fn, args)
}

// boundArgValues puts already-evaluated arguments in fn's parameter order,
// by name where one was written and from the default where none was given.
func (env *Env) boundArgValues(fn *ir.Func, callArgs []ir.CallArg, values []any) ([]any, error) {
	params := fn.Params
	// A method of a component called in its own body passes no receiver, and
	// evalUserFuncCore binds it from the scope when the arguments are one short.
	if fn.OnComponent() && len(params) > 0 && params[0].Receiver && (len(callArgs) == 0 || callArgs[0].Name != "") {
		params = params[1:]
	}
	vals := make(map[*ir.Param]any, len(params))
	positional := 0
	for i, a := range callArgs {
		if a.Name == "" {
			if positional < len(params) {
				vals[params[positional]] = values[i]
			}
			positional++
			continue
		}
		for _, p := range params {
			if p.Name == a.Name {
				vals[p] = values[i]
				break
			}
		}
	}
	args := make([]any, len(params))
	for i, p := range params {
		v, ok := vals[p]
		if !ok && p.Default != nil {
			var err error
			if v, err = env.Eval(p.Default); err != nil {
				return nil, err
			}
		}
		args[i] = v
	}
	return args, nil
}

func (env *Env) evalUserFuncCore(fn *ir.Func, args []any) (any, error) {
	if ir.IsRelease(fn) {
		return env.releaseCall(fn, args, func() (any, error) { return env.runUserFunc(fn, args) })
	}
	if ir.IsRetain(fn) {
		return env.retainCall(fn, args, func() (any, error) { return env.runUserFunc(fn, args) })
	}
	if err := env.checkPassed(args); err != nil {
		return nil, err
	}
	return env.runUserFunc(fn, args)
}

func (env *Env) runUserFunc(fn *ir.Func, args []any) (any, error) {
	defer env.enterFunc(fn)()
	env.depth++
	if env.depth > maxCallDepth {
		env.depth--
		return nil, fmt.Errorf("stack overflow: call depth exceeded %d", maxCallDepth)
	}
	defer func() { env.depth-- }()

	isPure := fn.Return != nil
	for {
		var execEnv *Env
		if !isPure {
			execEnv = env
		} else {
			execEnv = env.Snapshot()
		}

		// If the function declares a leading `this` receiver but the caller
		// supplied one-fewer arguments (intra-component method calls compile
		// as plain `foo(args)`), shift bindings so user args land in n,
		// not in this. `this` is expected to be bound in execEnv already.
		argOffset := 0
		if len(fn.Params) > 0 && fn.Params[0].Receiver && len(args) == len(fn.Params)-1 {
			argOffset = 1
		}
		// A recursive call reuses the same Param symbols in the same env, so
		// the previous frame's bindings still need saving and restoring.
		savedVars := make(map[ir.Symbol]any, len(fn.Params))
		for i, p := range fn.Params {
			if v, ok := execEnv.vals[p]; ok {
				savedVars[p] = v
			}
			argIdx := i - argOffset
			if argIdx >= 0 && argIdx < len(args) {
				execEnv.Set(p, bindArg(args[argIdx], p))
			}
		}
		var localVars []ir.Symbol
		restoreVoid := func() {
			if isPure {
				// A return type does not make a body pure: what it wrote to
				// state outside its own frame has to reach the caller.
				for _, p := range fn.Params {
					delete(execEnv.assigned, p)
				}
				for _, sym := range localVars {
					delete(execEnv.assigned, sym)
				}
				env.RebindFrom(execEnv)
			} else {
				for _, p := range fn.Params {
					if orig, ok := savedVars[p]; ok {
						execEnv.vals[p] = orig
					} else {
						delete(execEnv.vals, p)
					}
				}
			}
		}

		// Walk block looking for a Return at the top of the body: that one is
		// the tail call candidate, so it is evaluated below rather than here.
		// A Return anywhere deeper arrives as a returnSignal from Exec and
		// ends the call with the value it carries, without trampolining.
		var tailExpr ir.Expr
		nested := false
		var nestedResult any
		for _, stmt := range fn.Block {
			if ret, ok := stmt.(*ir.Return); ok {
				tailExpr = ret.Value
				break
			}
			if lv, ok := stmt.(*ir.LocalVar); ok {
				// A local written with no initializer is bound all the same,
				// as Exec binds one: skipped, a read of it found nothing.
				var v any
				if lv.Init != nil {
					var err error
					if v, err = execEnv.Eval(lv.Init); err != nil {
						restoreVoid()
						return nil, err
					}
				}
				if lv.Init != nil {
					v = copyOf(v, lv.Init.ExprType())
				}
				execEnv.Set(lv.Sym, v)
				localVars = append(localVars, lv.Sym)
				continue
			}
			if err := execEnv.Exec(stmt); err != nil {
				if ret, ok := err.(*returnSignal); ok {
					nested, nestedResult = true, ret.value
					break
				}
				restoreVoid()
				return nil, err
			}
		}

		if nested {
			restoreVoid()
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			return nestedResult, nil
		}

		if tailExpr == nil {
			restoreVoid()
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			return nil, nil
		}

		if isPure {
			result, newArgs, isTail, err := execEnv.evalTailAware(tailExpr, fn)
			restoreVoid()
			for _, sym := range localVars {
				delete(execEnv.vals, sym)
			}
			if err != nil {
				return nil, err
			}
			if isTail {
				args = newArgs
				continue
			}
			return result, nil
		}

		result, err := execEnv.Eval(tailExpr)
		restoreVoid()
		for _, sym := range localVars {
			delete(execEnv.vals, sym)
		}
		return result, err
	}
}

// execBlockForResult runs a function body, returning the value of whichever
// Return it reaches — including one nested inside an if or a for.
func (env *Env) execBlockForResult(block []ir.Stmt) (any, error) {
	for _, stmt := range block {
		err := env.Exec(stmt)
		if ret, ok := err.(*returnSignal); ok {
			return ret.value, nil
		}
		// A loop escape may not cross a call. The checker refuses one that
		// would (loopDepth resets at every imperative body), so reaching here
		// means that refusal has a hole -- and passing the signal on would
		// hand it to the *caller's* loop, which would break or continue for
		// reasons its own body never asked for. Fail loudly instead.
		switch err.(type) {
		case *breakSignal, *continueSignal:
			return nil, fmt.Errorf("%w: it left the body it was written in", err)
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// evalTailAware detects self-tail calls for trampolining.
func (env *Env) evalTailAware(e ir.Expr, fn *ir.Func) (any, []any, bool, error) {
	switch n := e.(type) {
	case *ir.Call:
		if n.Func == fn {
			newArgs, err := env.evalCallArgs(n.Args)
			if err != nil {
				return nil, nil, false, err
			}
			return nil, newArgs, true, nil
		}
		result, err := env.Eval(e)
		return result, nil, false, err
	case *ir.Ternary:
		cond, err := env.Eval(n.Cond)
		if err != nil {
			return nil, nil, false, err
		}
		b, ok := cond.(bool)
		if !ok {
			return nil, nil, false, fmt.Errorf("ternary condition must be bool, got %T", cond)
		}
		if b {
			return env.evalTailAware(n.Then, fn)
		}
		return env.evalTailAware(n.Else, fn)
	case *ir.MatchExpr:
		i, err := env.matchArm(&n.MatchArms)
		if err != nil || i < 0 {
			return nil, nil, false, err
		}
		return env.evalTailAware(n.Arms[i].Value, fn)
	default:
		result, err := env.Eval(e)
		return result, nil, false, err
	}
}

// unitTableOf is the table of the unit lit is a literal of: its type's
// declaration where the checker recorded one, and the table its suffix names
// otherwise. A suffix alone is ambiguous -- a program declaring its own `unit
// length { px, … }` claims `px` for the whole environment, and a library body
// writing `16px` for a measurement was then a length the measurement it was
// added to could not be added to.
func (env *Env) unitTableOf(lit *ir.Literal) *unitTable {
	if ud := ir.UnitDeclOf(lit.Type); ud != nil {
		if t := env.Units[lit.Suffix]; t != nil && t.Decl == ud {
			return t
		}
		for _, t := range env.Units {
			if t.Decl == ud {
				return t
			}
		}
		return buildUnitTableFromDef(ud)
	}
	return env.Units[lit.Suffix]
}

// makeUnitValue converts a unit literal to a unitValue.
func (env *Env) makeUnitValue(lit *ir.Literal) (unitValue, error) {
	suffix := lit.Suffix
	raw := lit.Value
	if before, ok := strings.CutSuffix(raw, suffix); ok {
		raw = before
	}
	raw = strings.ReplaceAll(raw, "_", "")
	num, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return unitValue{}, fmt.Errorf("invalid unit literal %q: %w", lit.Value, err)
	}
	table := env.unitTableOf(lit)
	base, amount := suffix, num
	if table != nil {
		if factor, ok := table.Conversions[suffix]; ok {
			amount = num * factor
		}
		if b, ok := table.BaseOf[suffix]; ok {
			base = b
		}
	}
	amounts := map[string]float64{}
	if amount != 0 {
		amounts[base] = amount
	}
	return newUnitValue(table, suffix, amounts), nil
}

func (env *Env) logAlertToast(args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	msg, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	variant := any("info")
	if len(args) > 1 {
		v, err := env.Eval(args[1].Value)
		if err != nil {
			return nil, err
		}
		variant = v
	}
	env.Log = append(env.Log, fmt.Sprintf("[toast:%v] %v", variant, msg))
	return nil, nil
}

func (env *Env) logAlertSingle(method string, args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return nil, nil
	}
	v, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	env.Log = append(env.Log, fmt.Sprintf("[%s] %v", method, v))
	return nil, nil
}

func (env *Env) logAlertConfirm(args []ir.CallArg) (any, error) {
	if len(args) < 1 {
		return true, nil
	}
	v, err := env.Eval(args[0].Value)
	if err != nil {
		return nil, err
	}
	env.Log = append(env.Log, fmt.Sprintf("[confirm] %v", v))
	return true, nil
}

// --- helpers ---

// equals is ==: Equal, and where it cannot say, the two rendered alike.
func equals(a, b any) bool {
	if eq, ok := Equal(a, b); ok {
		return eq
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// Equal compares two values as SNGL's == does, and is what the optimizer folds
// == with: a unit base by base, a struct field by field by name -- they hold
// the same fields whatever order they were written in -- a list element by
// element, a handle by the instance it is, a number by value whatever its
// width, and a string or a bool as itself. ok is false for a pair it cannot
// say that of: a map, a host value, two scalars of different kinds.
func Equal(a, b any) (eq, ok bool) {
	if a == nil || b == nil {
		return a == nil && b == nil, true
	}
	if au, isUnit := a.(unitValue); isUnit {
		bu, isUnit := b.(unitValue)
		return isUnit && au.Equal(bu), true
	}
	switch x := a.(type) {
	case EnumValue:
		y, isEnum := b.(EnumValue)
		if !isEnum || x.Member != y.Member {
			return false, isEnum
		}
		return Equal(x.Payload, y.Payload)
	case *Struct:
		y, isStruct := b.(*Struct)
		if !isStruct || len(x.Fields) != len(y.Fields) {
			return false, true
		}
		for _, f := range x.Fields {
			other, found := y.Get(f.Name)
			if !found {
				return false, true
			}
			if eq, ok := Equal(f.Value, other); !ok || !eq {
				return false, ok
			}
		}
		return true, true
	case []any:
		y, isList := b.([]any)
		if !isList || len(x) != len(y) {
			return false, isList
		}
		for i := range x {
			if eq, ok := Equal(x[i], y[i]); !ok || !eq {
				return false, ok
			}
		}
		return true, true
	}
	// A handle read as a value is its instance, and two are equal when they
	// are one instance: the scope the interpreter keeps for it.
	if ae, be := instanceEnv(a), instanceEnv(b); ae != nil || be != nil {
		return ae == be, true
	}
	if af, isNum := numberOf(a); isNum {
		bf, isNum := numberOf(b)
		return af == bf, isNum
	}
	switch a.(type) {
	case string, bool:
		if reflect.TypeOf(a) == reflect.TypeOf(b) {
			return a == b, true
		}
	}
	return false, false
}

// numberOf is v as a float, for a value of any of the numeric widths.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func toFloat(v any) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case uint64:
		return float64(val)
	case float64:
		return val
	case bool:
		if val {
			return 1
		}
		return 0
	case unitValue:
		return val.magnitude()
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	return 0
}

func ToInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case uint64:
		return int(val)
	case float64:
		return int(val)
	case unitValue:
		// The magnitude in the unit's base, which is what every backend's cast
		// produces -- without a case here a unit fell through to the zero below.
		return int(val.magnitude())
	case bool:
		if val {
			return 1
		}
		return 0
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}

// orderable reports why a pair cannot be ordered, or nil. Only a multi-base
// unit has no answer: `3px` and `2em` are each larger on their own base. The
// checker refuses that pair outright (`IsSingleBaseUnit` gates <, <=, >, >=),
// so this is for IR the checker did not see.
func orderable(a, b any) error {
	for _, v := range [...]any{a, b} {
		if u, ok := v.(unitValue); ok && u.Table != nil && len(u.Table.Bases) > 1 {
			return fmt.Errorf("cannot order %s: a magnitude per base has no single number to compare", u)
		}
	}
	return nil
}

// compareOrdered orders two values `<` may compare: numbers and units by
// magnitude, strings byte by byte as Go and the constant folder do. Read as
// numbers, every string was 0 and none came before another.
func compareOrdered(a, b any) int {
	if sa, ok := a.(string); ok {
		if sb, ok := b.(string); ok {
			return strings.Compare(sa, sb)
		}
	}
	fa, fb := toFloat(a), toFloat(b)
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

func runtimeTypeName(v any) string {
	switch x := v.(type) {
	case int:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	case *Struct:
		// The declared type name, so method dispatch reaches user-defined
		// methods (`v.dot()` → Vec2.dot).
		if name := x.Name(); name != "" {
			return name
		}
		return "struct"
	case map[string]any:
		// Not "map": the name is looked up as a declared type, and the
		// built-in map declares methods with no body to run.
		return "struct"
	default:
		return ""
	}
}

// zeroValueFor returns the runtime zero value for an IR type.
func zeroValueFor(t *ir.Type) any {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case ir.TypeInt:
		return 0
	case ir.TypeFloat:
		return 0.0
	case ir.TypeBool:
		return false
	case ir.TypeString:
		return ""
	}
	return nil
}

// pushContext overrides ctx with value for the duration of a provider's
// subtree and returns the restore.
//
// The value is evaluated in the scope *enclosing* the provider, which is what
// lets `depth(depth + 1)` read the value it is overriding and accumulate
// across nesting -- the same rule internal/lower/context.go applies on a
// target that lowers contexts away. The interpreter honors providers itself,
// since passContext does not run for a target with no host language, so
// without this a provider's value was never evaluated at all and every read
// beneath one answered the context's default.
func (env *Env) pushContext(ctx *ir.Context, value ir.Expr) (func(), error) {
	v, err := env.Eval(value)
	if err != nil {
		return func() {}, err
	}
	prev, had := env.ContextVals[ctx]
	prevLocale := env.Locale
	env.SetContext(ctx, v)
	return func() {
		if had {
			env.ContextVals[ctx] = prev
		} else {
			delete(env.ContextVals, ctx)
		}
		env.Locale = prevLocale
	}, nil
}

// lengthIntrinsics answers the `.length` the checker types as int on a list, a
// map or a string. Chosen by the checked type, because evalSelect otherwise
// reads a map[string]any by key and a map may hold a "length" key.
var lengthIntrinsics = map[ir.TypeKind]string{
	ir.TypeList:   "list.length",
	ir.TypeMap:    "map.length",
	ir.TypeString: "string.length",
}

// underContext runs fn with vals provided over whatever env already holds.
// A handler or an effect runs after the mount that placed it has unwound
// every provider, so it carries the values it was mounted under and is run
// with them here -- which is what a lowered target's threading gives it.
func (env *Env) underContext(vals map[*ir.Context]any, fn func() (any, error)) (any, error) {
	if len(vals) == 0 {
		return fn()
	}
	prev, prevLocale := env.ContextVals, env.Locale
	env.ContextVals = maps.Clone(prev)
	for ctx, v := range vals {
		env.SetContext(ctx, v)
	}
	defer func() { env.ContextVals, env.Locale = prev, prevLocale }()
	return fn()
}

// underHandler is underContext for an event handler's entry. A raise the
// handler lets out is offered to the frames it was mounted under -- but only by
// the outermost one, since a handler run from inside another (an emitted event)
// is part of the raise that one is making.
func (env *Env) underHandler(vals map[*ir.Context]any, fn func() (any, error)) (any, error) {
	if env.handling == nil {
		env.handling = new(int)
	}
	*env.handling++
	res, err := env.underContext(vals, fn)
	*env.handling--
	if *env.handling == 0 {
		err = catchEscaped(vals, err)
	}
	return res, err
}

// InvokeElementEvent runs the handler a rendered node carries for event, the
// way a host reporting it would, in the scope the node was written in. It
// reports false when the node handles nothing of the event.
//
// A component whose body hands the event on enters where the host would
// report it (forwardingDescendant): the override's handler writes the prop
// the control reports, and its emit runs the call site's handler.
//
// A component whose call site handled nothing of the event still renders
// something that reports it: the event reaches that, and the component's own
// forwarding emit, if it has one, runs the call site's handler from there.
//
// A program's component emits its events from the program's own code, so a
// test firing one stands in for that emit and runs the call site's handler
// with the test's arguments, as every compiled target does.
func (env *Env) InvokeElementEvent(m map[string]any, event string, args func() ([]any, error)) (any, bool, error) {
	n, _ := m["__node"].(*Node)
	_, handled := m["@"+event].(*ir.Func)
	if programComponent(n) && handled {
		return env.runElementHandler(m, event, args)
	}
	if d, name := forwardingDescendant(instanceBody(m, n), event); d != nil {
		return env.InvokeElementEvent(d.Map(), name, args)
	}
	h, _ := m["@"+event].(*ir.Func)
	if h == nil {
		if n == nil || !n.Expanded {
			return nil, false, nil
		}
		d := handlingDescendant(n, event)
		if d == nil {
			return nil, false, nil
		}
		return env.InvokeElementEvent(d.Map(), event, args)
	}
	return env.runElementHandler(m, event, args)
}

// programComponent reports whether n is an instance of a component the
// program declares.
func programComponent(n *Node) bool {
	return n != nil && n.Component != nil && n.Component.PkgPath() == "" && !n.Component.Primitive()
}

// runElementHandler runs the handler the node m names carries for event, in the
// scope its call site was written in.
func (env *Env) runElementHandler(m map[string]any, event string, args func() ([]any, error)) (any, bool, error) {
	h, _ := m["@"+event].(*ir.Func)
	if h == nil {
		return nil, false, nil
	}
	handlerEnv := env
	if oe, ok := m["__ownerEnv"].(*Env); ok && oe != nil {
		handlerEnv = oe
	}
	if owner, ok := m["__ownerComponent"]; ok && owner != nil {
		handlerEnv.SetReceiver(owner)
	}
	vals, err := args()
	if err != nil {
		return nil, true, err
	}
	provided, _ := m["__ownerContext"].(map[*ir.Context]any)
	res, err := handlerEnv.underHandler(provided, func() (any, error) {
		for i, p := range h.Params {
			if i < len(vals) {
				handlerEnv.Set(p, bindArg(vals[i], p))
			}
		}
		return handlerEnv.execBlockForResult(h.Block)
	})
	handlerEnv.writeBack()
	return res, true, err
}

// elemType is the element type of a list type, nil for anything else.
func elemType(t *ir.Type) *ir.Type {
	if t == nil || t.Kind != ir.TypeList || len(t.Elems) == 0 {
		return nil
	}
	return t.Elems[0]
}

// slotEmpty answers `src.empty` in a component's body: whether what the call
// site of the component declaring the slot supplied for it renders nothing,
// asked of that content in the caller's scope -- the answer the splicer writes
// in for a compiled target (ir.RendersNothing), evaluated rather than spliced.
// The slot is matched by declaration, as the splicer matches it: the nearest
// instance may be another component's, one the content was forwarded into. A
// component entry is answered by the insertion whose population forwarded it,
// which is one of the entry frames current where the asking instance was
// mounted.
func (env *Env) slotEmpty(x *ir.SlotEmpty) (any, error) {
	for e := env; e != nil; e = e.parent {
		if e.inst != nil && e.inst.Component != nil && slices.Contains(e.inst.Component.Slots, x.Slot) {
			body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: x.Slot.Name, Rest: x.Slot.Rest}, e.inst)
			if !supplied {
				return true, nil
			}
			caller := e.parent
			if caller == nil {
				caller = e
			}
			return caller.answerEmpty(x, body, e.entries)
		}
		for _, f := range slices.Backward(e.entries) {
			if f.ins.Decl == nil || !slices.Contains(f.ins.Decl.Slots, x.Slot) {
				continue
			}
			content := f.ins.Slots[x.Slot.Name]
			if content == nil {
				return true, nil
			}
			return f.env.answerEmpty(x, content.Body, f.env.entries)
		}
	}
	return true, nil
}

// answerEmpty evaluates in env whether body renders nothing. A forward of a
// component entry in it is answered from entries, the frames current where
// body was handed on, which env -- the scope body was written in -- need not
// hold.
func (env *Env) answerEmpty(x *ir.SlotEmpty, body []ir.Stmt, entries []entryFrame) (any, error) {
	answer, ok := ir.RendersNothing(body)
	if !ok {
		return nil, fmt.Errorf("%s.%s: what the call site supplied cannot say whether it renders anything", x.Slot.Name, ir.SlotEmptyMember)
	}
	if len(entries) > 0 {
		env = env.Snapshot()
		env.entries = entries
	}
	return env.Eval(answer)
}

// copyNullTest answers `c.x[i] == null` (or `!=`, present): a copy past the
// copies its loops rendered now is not rendered, which is how every compiled
// target answers it too, looking the copy up by its key.
func (env *Env) copyNullTest(sel *ir.Select, path []ir.Expr, present bool) (any, error) {
	// A copy reached through a copy of an outer loop is absent where that
	// copy is: `c.r[1].cl[0]` once the rows shrink to one.
	if there, err := env.refPresent(sel.Operand); err != nil || !there {
		return !present, err
	}
	v, err := env.Eval(sel)
	if err != nil {
		return nil, err
	}
	for _, ix := range path {
		idx, err := env.Eval(ix)
		if err != nil {
			return nil, err
		}
		i := ToInt(idx)
		switch list := v.(type) {
		case []any:
			if i < 0 || i >= len(list) {
				return !present, nil
			}
			v = list[i]
		case nil:
			return !present, nil
		default:
			// A loop that rendered the node once hands it back as itself
			// (evalIndex).
			if i != 0 {
				return !present, nil
			}
		}
	}
	return (v != nil) == present, nil
}

// refPresent reports whether the instance e reaches through each copy it
// indexes is rendered now.
func (env *Env) refPresent(e ir.Expr) (bool, error) {
	probe := &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: e, Right: &ir.Literal{Type: ir.TypNull}}
	if sel, path, _, ok := ir.NullTestedCopy(probe); ok && len(path) > 0 {
		v, err := env.copyNullTest(sel, path, true)
		if err != nil {
			return false, err
		}
		return v == true, nil
	}
	if sel, ok := e.(*ir.Select); ok {
		return env.refPresent(sel.Operand)
	}
	return true, nil
}
