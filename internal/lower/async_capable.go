package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passAsyncCapable refuses a program that names `sngl:async` on a target with
// no answer for it.
//
// The two ids are declared now, so a program can write them -- and what a
// target that cannot emit one produced was neither an error nor working code.
// `RequireIntrinsicFallback` is the guard for this, and it never saw the call:
// a backend consults it at the generic path, and bubbletea's Go emitter had
// already spelled the call as a method on the model. So `async.post` there
// emitted `m.post(func(){…})` and the first anyone heard of it was the Go
// compiler reporting an undefined method, with no position in the SNGL that
// asked for it.
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
			for _, c := range asyncIntrinsicCalls(s) {
				if err := refuseAsyncIntrinsic(c, s, caps, opts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// asyncIntrinsicCalls is every async call a statement holds, at any depth of
// its expressions -- a post is as likely to be an argument or an initializer
// as a statement of its own.
//
// Both ids rather than the first one found: a statement may name one the
// target answers and one it does not, and stopping early checks the wrong half.
func asyncIntrinsicCalls(s ir.Stmt) []*ir.Call {
	var out []*ir.Call
	_ = ir.Walk(s, func(n ir.Node) error {
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case AsyncPostIntrinsic, AsyncSpawnIntrinsic:
			out = append(out, c)
		}
		return nil
	})
	return out
}

// refuseAsyncIntrinsic names the half of the build that has to change, because
// the two are answered by different ones: a platform reaches the thread it
// draws on, a language starts work that does not block.
func refuseAsyncIntrinsic(c *ir.Call, s ir.Stmt, caps Caps, opts Options) error {
	switch c.Func.Intrinsic {
	case AsyncPostIntrinsic:
		if caps.AsyncPost {
			return nil
		}
		return fmt.Errorf("%s: async.post runs a closure back on the thread the target draws on, and %s has no way to reach one", callPos(c, s), platformOrThis(opts.Platform))
	case AsyncSpawnIntrinsic:
		if caps.AsyncSpawn {
			return nil
		}
		// Both halves, because either may have withdrawn it: the language
		// answers the id, and a platform whose handlers are not written in
		// that language takes it back -- html's are JavaScript whatever
		// `--lang` says.
		return fmt.Errorf("%s: async.spawn runs a closure without waiting for it, and %s has no way to start one", callPos(c, s), describeBuild(opts))
	}
	return nil
}

// callPos is the call's own position rather than the statement's. allBlocks
// hands over a view body before the handler bodies hanging off it, so a post
// written inside a `@click` inside a `vbox` was reported at the vbox.
func callPos(c *ir.Call, s ir.Stmt) ast.Pos {
	if c.AST != nil {
		return c.AST.Pos
	}
	return ir.StmtPos(s)
}

func describeBuild(opts Options) string {
	switch {
	case opts.Language != "" && opts.Platform != "":
		return opts.Language + " on " + opts.Platform
	case opts.Language != "":
		return opts.Language
	case opts.Platform != "":
		return opts.Platform
	}
	return "this build"
}
