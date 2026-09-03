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
//
// Report mode needs `go test -v` (or -json): it writes to stderr, and go test
// discards a *passing* package's stderr, so a plain `go test ./...` prints
// nothing and looks clean when it is not.
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

// dynNoSymType is the type of a symbol that has none by design: *ir.Namespace
// and *ir.Import name a package rather than a value, so their SymType is nil
// and the Select around them discards the operand's type anyway. Reported
// under its own tag so the category stays visible without counting as the
// checker giving up. Every other symbol kind carries a type field, so a nil
// from one of those is a genuine fallback.
func dynNoSymType(sym ir.Symbol, format string, args ...any) *ir.Type {
	switch sym.(type) {
	case *ir.Namespace, *ir.Import:
		if reportOnDyn {
			reportSite("DYNBYDESIGN", 1, format, args...)
		}
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
	if panicOnDyn {
		_, file, line, _ := runtime.Caller(skip)
		panic(fmt.Sprintf("dyn fallback at %s:%d: %s",
			filepath.Base(file), line, fmt.Sprintf(format, args...)))
	}
	reportSite("DYNFALLBACK", skip, format, args...)
	return ir.TypDyn
}

// reportSite prints one line per distinct site+message pair. skip is relative
// to reportSite's own caller, matching dynAt's convention.
func reportSite(tag string, skip int, format string, args ...any) {
	_, file, line, _ := runtime.Caller(skip + 1)
	key := fmt.Sprintf("%s:%d\t%s", filepath.Base(file), line, fmt.Sprintf(format, args...))
	dynSeenMu.Lock()
	defer dynSeenMu.Unlock()
	if !dynSeen[tag+"\t"+key] {
		dynSeen[tag+"\t"+key] = true
		fmt.Fprintf(os.Stderr, "%s\t%s\n", tag, key)
	}
}
