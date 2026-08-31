package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A fallback to dyn is the checker giving up on a type without saying so, and
// downstream phases have no way to tell one apart from a type that is dyn
// because the program said so. dynFallback marks every such site: it yields
// TypDyn as before, and under SNGL_PANIC_ON_DYN it reports the site and the
// reason, so a test that only passes because of a silent degradation fails
// loudly instead.
//
// A `return TypDyn` written directly is error recovery — c.error has already
// reported at that point, and the type only keeps the rest of the file
// checkable. Those are not fallbacks and stay as they are.
//
// SNGL_PANIC_ON_DYN=1 panics at the first fallback. =report prints each
// distinct site to stderr and carries on, which is how a whole test run's
// fallbacks get counted in one pass.
var (
	dynMode     = os.Getenv("SNGL_PANIC_ON_DYN")
	panicOnDyn  = dynMode != "" && dynMode != "report"
	reportOnDyn = dynMode == "report"

	dynSeenMu sync.Mutex
	dynSeen   = map[string]bool{}
)

func dynFallback(format string, args ...any) *ir.Type {
	return dynAt(2, format, args...)
}

// dynSpread is a fallback reached through an operand that is already dyn or
// invalid: the dyn spreads from there rather than originating here, and a
// panic would name this site for a decision made elsewhere. Anything else
// reaching it is a fallback like any other.
func dynSpread(from *ir.Type, format string, args ...any) *ir.Type {
	if from != nil && (from.Kind == ir.TypeDyn || from.Kind == ir.TypeInvalid) {
		return ir.TypDyn
	}
	return dynAt(2, format, args...)
}

// dynDeferred is a dyn a later pass is required to replace — a shell type
// standing in until the pass that can compute it runs. It never reports; the
// contract it names is that something else fills it in.
func dynDeferred(what string) *ir.Type {
	_ = what
	return ir.TypDyn
}

func dynAt(skip int, format string, args ...any) *ir.Type {
	if !panicOnDyn && !reportOnDyn {
		return ir.TypDyn
	}
	_, file, line, _ := runtime.Caller(skip)
	site := fmt.Sprintf("%s:%d", filepath.Base(file), line)
	msg := fmt.Sprintf(format, args...)
	if panicOnDyn {
		panic(fmt.Sprintf("dyn fallback at %s: %s", site, msg))
	}
	dynSeenMu.Lock()
	defer dynSeenMu.Unlock()
	if key := site + "\t" + msg; !dynSeen[key] {
		dynSeen[key] = true
		fmt.Fprintf(os.Stderr, "DYNFALLBACK\t%s\n", key)
	}
	return ir.TypDyn
}
