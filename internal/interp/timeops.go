package interp

import (
	"fmt"
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
	// handled is true for every id this implements, failures included: a
	// caller reads false as "not mine" and goes on to a fallback that answers
	// null for a declaration with no body, which is the silence this is here
	// to remove.
	switch fn.Intrinsic {
	case "time.now":
		return env.clock().Now().UTC().Format(snglDateTime), true, nil
	case "datetime.add":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("datetime.add takes an instant and a duration, got %d arguments", len(args))
		}
		t, err := parseSnglDateTime(args[0])
		if err != nil {
			return nil, true, err
		}
		return t.Add(durationOf(args[1])).UTC().Format(snglDateTime), true, nil
	case "time.between":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("time.between takes two instants, got %d arguments", len(args))
		}
		start, err := parseSnglDateTime(args[0])
		if err != nil {
			return nil, true, err
		}
		end, err := parseSnglDateTime(args[1])
		if err != nil {
			return nil, true, err
		}
		return env.duration(fn.Return, float64(end.Sub(start))/float64(time.Millisecond)), true, nil
	}
	return nil, false, nil
}

// clock walks the owner chain rather than reading this scope alone: a child
// component's env is built field by field in render.go, so anything it does not
// name is absent there and one clock for a run has to be reachable from every
// scope in it. The wall clock is the answer for an env built outside a session.
func (env *Env) clock() Clock {
	for e := env; e != nil; e = e.parent {
		if e.Clock != nil {
			return e.Clock
		}
	}
	return Real{}
}

// parseSnglDateTime accepts every shape the checker lets a datetime literal be
// written in: the seconds, the fraction and the offset are each optional. So a
// literal and a clock reading are interchangeable operands even though only
// the reading is written back in full.
func parseSnglDateTime(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("not a datetime: %v", v)
	}
	for _, layout := range []string{snglDateTime, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a datetime: %q", s)
}

// durationOf reads a duration operand as a Go Duration. A unit value carries a
// magnitude per base; the value's own first base is read rather than the
// spelling `ms`, so nothing here depends on what the declaration calls it.
func durationOf(v any) time.Duration {
	if u, ok := v.(unitValue); ok {
		return time.Duration(u.amountIn(u.firstBase()) * float64(time.Millisecond))
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
