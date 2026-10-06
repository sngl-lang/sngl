package build

import (
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

// installRun makes the `@run` handler the output block wrote on t's platform
// node a function of the program's, ir.Package.Run.
//
// A function rather than a handler kept on the output: a handler reached only
// through ir.Output is in no body, so every pass that enumerates what a
// program runs would skip it -- the timer-in-a-window history, where a
// schedule on an owner no walk named compiled and never ran. As a function it
// is lowered like any other, and the host's entry point is the one caller.
func installRun(pkg *ir.Package, t Target) {
	var h *ir.EventHandler
	for _, o := range pkg.Outputs {
		if o.Lang == t.Lang && o.Platform == t.Platform && o.Run != nil {
			h = o.Run
		}
	}
	if h == nil || h.Func == nil {
		return
	}
	fn := &ir.Func{
		Name:   uniqueFuncName(pkg, "__run"),
		Params: h.Func.Params,
		Return: ir.TypVoid,
		Block:  h.Func.Block,
	}
	checker.AnalyzeSynthesizedFunc(pkg, nil, fn)
	pkg.Funcs = append(pkg.Funcs, fn)
	pkg.Run = fn
}
