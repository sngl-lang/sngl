package codegen

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Screen is the one surface a target that shows one draws, found in the
// statements that are its view: the node, and the conditions it is drawn
// under, outermost first.
type Screen struct {
	Node  *ir.NodeInst
	Conds []ScreenCond
	// Owner is the component built at run time whose body holds the screen,
	// and nil where the view itself does. Conds are then that body's, which
	// is the scope a target evaluates them in.
	Owner *ir.Component
}

// OneWindowError is SoleScreen's refusal. It is a type so that a harness
// compiling every fixture for one platform can tell a program this platform
// refuses by design from a failure of the platform.
type OneWindowError struct{ msg string }

func (e *OneWindowError) Error() string { return e.msg }

// NewOneWindowError is the refusal for a platform other than SoleScreen's that
// cannot yet show a second window -- html, whose second window will be a
// `<dialog>` and is a second document until then.
func NewOneWindowError(format string, a ...any) error {
	return &OneWindowError{fmt.Sprintf(format, a...)}
}

// Shown is whether the screen is drawn, as the target spells an expression:
// the conditions around it and its own `visible`.
func (sc *Screen) Shown(eval func(ir.Expr) string) string {
	var parts []string
	for _, c := range sc.Conds {
		e := eval(c.Cond)
		if c.Else {
			e = "!(" + e + ")"
		}
		parts = append(parts, e)
	}
	if v := sc.Visible(); v != nil {
		parts = append(parts, eval(v))
	}
	return strings.Join(parts, " && ")
}

// Visible is the screen's own `visible` prop, or nil where it has none.
func (sc *Screen) Visible() ir.Expr { return NodeProp(sc.Node, "visible") }

// ScreenCond is one `if` between the view's root and the screen. Else says
// the screen is in the `else`, so it is drawn when Cond is false.
type ScreenCond struct {
	Cond ir.Expr
	Else bool
}

// SoleScreen finds the node of the primitive `intrinsic` in stmts -- the
// package body, on a target that answers `ui.window` with such a primitive --
// and refuses a second with the positions of both, since a target showing one
// surface would otherwise draw the first and drop the rest without a word. A
// second in the other branch of an `if` is still a second: the program says it
// exists. Under a `for` there is a copy per element, so one there is refused
// too. It returns nil where there is none, which is a harness that isolated a
// component.
//
// A component built at run time is followed into its body, where a window
// under a reactive `if` lands on a target that keeps an instance's state
// (android); the conditions start again there.
func SoleScreen(stmts []ir.Stmt, intrinsic, platform string, pos func(*ir.NodeInst) string) (*Screen, error) {
	var found *Screen
	var owner *ir.Component
	entered := map[*ir.Component]bool{}
	var walk func(stmts []ir.Stmt, conds []ScreenCond, inLoop bool) error
	walk = func(stmts []ir.Stmt, conds []ScreenCond, inLoop bool) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.If:
				if n.FromTernary {
					continue
				}
				if err := walk(n.Body, append(conds[:len(conds):len(conds)], ScreenCond{Cond: n.Cond}), inLoop); err != nil {
					return err
				}
				if err := walk(n.Else, append(conds[:len(conds):len(conds)], ScreenCond{Cond: n.Cond, Else: true}), inLoop); err != nil {
					return err
				}
			case *ir.For:
				if err := walk(n.Body, conds, true); err != nil {
					return err
				}
				if err := walk(n.Else, conds, inLoop); err != nil {
					return err
				}
			case *ir.ErrorBoundary:
				if err := walk(n.Children, conds, inLoop); err != nil {
					return err
				}
			case *ir.NodeInst:
				if n.Component == nil {
					continue
				}
				// A primitive is marked as one too where it stands in a
				// reactive position; only a body is followed.
				if n.Component.RuntimeInstance && n.Component.Intrinsic == "" && !entered[n.Component] {
					entered[n.Component] = true
					outer := owner
					owner = n.Component
					err := walk(n.Component.Body, nil, inLoop)
					owner = outer
					if err != nil {
						return err
					}
					continue
				}
				if n.Component.Intrinsic != intrinsic {
					continue
				}
				if inLoop {
					return &OneWindowError{fmt.Sprintf("%s: %s shows one window, and a `for` makes a copy of this one per element", pos(n), platform)}
				}
				if found != nil {
					return &OneWindowError{fmt.Sprintf("%s: %s shows one window, and this is a second; the first is at %s", pos(n), platform, pos(found.Node))}
				}
				found = &Screen{Node: n, Conds: conds, Owner: owner}
			}
		}
		return nil
	}
	if err := walk(stmts, nil, false); err != nil {
		return nil, err
	}
	return found, nil
}
