package interp

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// matchArm is the index of the arm a match takes, or -1 when none is: the
// subject is evaluated once and compared with each arm's patterns in order as
// `==` compares, or, headless, each pattern is tested as a condition.
func (env *Env) matchArm(m *ir.MatchArms) (int, error) {
	var subject any
	if m.Subject != nil {
		v, err := env.Eval(m.Subject)
		if err != nil {
			return -1, err
		}
		subject = v
	}
	for i, arm := range m.Arms {
		if m.Unasked(i) {
			return i, nil
		}
		for _, p := range arm.Patterns {
			v, err := env.Eval(p)
			if err != nil {
				return -1, err
			}
			if m.Subject != nil {
				v, err = env.binaryValues(&ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: m.Subject, Right: p}, subject, v)
				if err != nil {
					return -1, err
				}
			}
			b, ok := v.(bool)
			if !ok {
				return -1, fmt.Errorf("match arm test must be bool, got %T", v)
			}
			if b {
				return i, nil
			}
		}
	}
	return -1, nil
}

// execMatch runs the arm a match statement takes.
func (env *Env) execMatch(s *ir.Match) error {
	i, err := env.matchArm(&s.MatchArms)
	if err != nil || i < 0 {
		return err
	}
	for _, st := range s.Arms[i].Body {
		if err := env.Exec(st); err != nil {
			return err
		}
	}
	return nil
}
