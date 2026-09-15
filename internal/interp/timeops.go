package interp

import (
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

// snglDateTime is the layout a datetime value is written back in: RFC3339, to
// the millisecond a duration counts in, which is what the compiled targets
// produce too.
const snglDateTime = "2006-01-02T15:04:05.000Z07:00"

// runClockIntrinsic implements the sngl:time operations that need something
// the argument list does not carry -- the session's clock, and the unit table
// a duration result has to be built against. The entries in builtins.go are
// handed neither, which is why these three are here instead.
func (env *Env) runClockIntrinsic(fn *ir.Func, args []any) (any, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	switch fn.Intrinsic {
	case "time.now":
		return env.clock().Now().UTC().Format(snglDateTime), true, nil
	case "datetime.add":
		if len(args) != 2 {
			return nil, false, nil
		}
		t, ok := parseSnglDateTime(args[0])
		if !ok {
			return nil, false, nil
		}
		return t.Add(durationOf(args[1])).UTC().Format(snglDateTime), true, nil
	case "time.between":
		if len(args) != 2 {
			return nil, false, nil
		}
		start, sok := parseSnglDateTime(args[0])
		end, eok := parseSnglDateTime(args[1])
		if !sok || !eok {
			return nil, false, nil
		}
		return env.duration(fn.Return, float64(end.Sub(start))/float64(time.Millisecond)), true, nil
	}
	return nil, false, nil
}

// clock is the wall clock for an env built outside a Session; a Session hands
// in its own, so a test's `now()` and its timer schedule cannot disagree.
func (env *Env) clock() Clock {
	if env == nil || env.Clock == nil {
		return Real{}
	}
	return env.Clock
}

// parseSnglDateTime accepts every shape the checker lets a datetime literal be
// written in: the seconds, the fraction and the offset are each optional. So a
// literal and a clock reading are interchangeable operands even though only
// the reading is written back in full.
func parseSnglDateTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{snglDateTime, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// durationOf reads a duration operand as a Go Duration. A unit value carries a
// magnitude per base, and duration's base is the millisecond.
func durationOf(v any) time.Duration {
	if u, ok := v.(unitValue); ok {
		return time.Duration(u.amountIn("ms") * float64(time.Millisecond))
	}
	return time.Duration(toFloat(v) * float64(time.Millisecond))
}

// duration builds a value of the unit the declaration returns, counted in its
// base. Env.Units is keyed by *suffix* rather than by unit name, so the table
// is reached through one of the declaration's own -- which is also what makes
// the pointer the one a literal carries, and unitValue.Equal compares tables
// by identity.
func (env *Env) duration(t *ir.Type, ms float64) any {
	def := ir.UnitDeclOf(t)
	if def == nil || env == nil {
		return ms
	}
	bases := def.Bases()
	if len(bases) == 0 {
		return ms
	}
	base := bases[0].Name
	return newUnitValue(env.Units[base], base, map[string]float64{base: ms})
}
