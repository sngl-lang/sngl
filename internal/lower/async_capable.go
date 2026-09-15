package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passAsyncCapable refuses a program that names `sngl:async` on a target with
// no answer for it.
//
// The two ids are declared now, so a program can write them -- and what a
// target that cannot emit one produced was neither an error nor working code.
// `RequireIntrinsicFallback` is the guard for this, and it never saw the call:
// a backend consults it at the generic path, and bubbletea's Go emitter had
// already spelled the call as a method on the model. So `async.post` on
// bubbletea emitted `m.post(func(){…})` and the first anyone heard of it was
// the Go compiler reporting an undefined method, with no position in the SNGL
// that asked for it.
//
// It runs ahead of passAsyncOffload rather than beside refuseAsyncOffload,
// which answers the same question for a *blocking call* the offload has to
// move. That pass synthesizes calls to both ids itself, so a check after it
// would report the compiler's own rewrites at whatever position they inherited.
// Here every call it sees is one a program wrote.
var passAsyncCapable = pass{
	name:    "AsyncCapable",
	enabled: func(Caps) bool { return true },
	apply:   applyAsyncCapable,
}

func applyAsyncCapable(pkg *ir.Package, caps Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	for _, block := range allBlocks(pkg) {
		for _, s := range *block {
			id, ok := asyncIntrinsicIn(s)
			if !ok {
				continue
			}
			if err := refuseAsyncIntrinsic(id, s, caps, opts); err != nil {
				return err
			}
		}
	}
	return nil
}

// asyncIntrinsicIn reports the first async id a statement names, at any depth
// of its expressions -- a post is as likely to be an argument or an
// initializer as a statement of its own.
func asyncIntrinsicIn(s ir.Stmt) (string, bool) {
	found := ""
	_ = ir.Walk(s, func(n ir.Node) error {
		if found != "" {
			return nil
		}
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case AsyncPostIntrinsic, AsyncSpawnIntrinsic:
			found = c.Func.Intrinsic
		}
		return nil
	})
	return found, found != ""
}

// refuseAsyncIntrinsic names the axis that has to change, because the two are
// answered by different halves of a build: a platform reaches the thread it
// draws on, a language starts work that does not block.
func refuseAsyncIntrinsic(id string, s ir.Stmt, caps Caps, opts Options) error {
	switch id {
	case AsyncPostIntrinsic:
		if caps.AsyncPost {
			return nil
		}
		return fmt.Errorf("%s: async.post runs a closure back on the thread the target draws on, and %s has no way to reach one", ir.StmtPos(s), platformOrThis(opts.Platform))
	case AsyncSpawnIntrinsic:
		if caps.AsyncSpawn {
			return nil
		}
		return fmt.Errorf("%s: async.spawn runs a closure without waiting for it, and %s has no way to start one", ir.StmtPos(s), languageOrThis(opts.Language))
	}
	return nil
}

func languageOrThis(name string) string {
	if name == "" {
		return "this language"
	}
	return name
}
