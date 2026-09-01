package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A fallback to dyn is the checker giving up on a type without saying so, which
// nothing downstream can tell apart from a dyn the program asked for. Marking
// the sites is what makes them countable.
//
// A `return TypDyn` written directly is error recovery: c.error has already
// reported and the type only keeps the rest of the file checkable. Not a
// fallback.
//
// SNGL_PANIC_ON_DYN=1 panics at the first fallback; =report prints each
// distinct site to stderr and carries on, for counting a whole test run.
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

// dynSpread is a fallback whose operand was already dyn or invalid, so the dyn
// originated elsewhere and reporting here would name the wrong site.
func dynSpread(from *ir.Type, format string, args ...any) *ir.Type {
	if from != nil && (from.Kind == ir.TypeDyn || from.Kind == ir.TypeInvalid) {
		return ir.TypDyn
	}
	return dynAt(2, format, args...)
}

// dynDeferred is a shell a named later pass replaces. The argument is there to
// name that pass at the call site; nothing reads it.
func dynDeferred(pass string) *ir.Type {
	_ = pass
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
