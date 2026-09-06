package golang

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Running something without waiting for it is the half of the offload the
// *language* answers: `go f()` needs no toolkit and no thread of anyone's in
// particular. Getting back to the thread that draws is the other half, and no
// language has an opinion about that -- each platform registers its own
// async.post.
func init() {
	codegen.RegisterIntrinsic(langGo, lower.AsyncSpawnIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return "go " + tr(args[0]) + "()", nil
	})
}
