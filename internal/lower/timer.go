package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passTimer = pass{
	name:    "NoTimer",
	enabled: func(c Caps) bool { return c.NoTimer },
	apply:   lowerTimer,
}

// lowerTimer rewrites timer declarations into explicit scheduler.At() and
// cancel() calls. Depends on reactivity decisions (timer handlers may have
// been wrapped by passReactivity).
// Phase 1: stub — implementation lands in Phase 3.
func lowerTimer(pkg *ir.Package) error { return nil }
