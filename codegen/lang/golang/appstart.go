package golang

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitAppStart writes the part of a Go host's main that runs the application
// and tears it down, one tab in. start(show) is the statement running the
// loop with the windows on screen or not, and refuse the runtime's
// RefuseRunAgain.
//
// Each start is followed by the teardown, so where the program wrote `@run`
// the application has torn down before `run` returns to the handler, and
// code after `run()` holds nothing the windows allocated. A handler that never
// calls run gets the loop hidden once it returns; a run called after that, or
// a second time, is refused, since the tree is built once before the handler.
func EmitAppStart(b *strings.Builder, pkg *ir.Package, args string, start func(show bool) string, refuse string) {
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "m." + pkg.Teardown.Name + "()"
	}
	line := func(indent, s string) {
		if s != "" {
			b.WriteString(indent + s + "\n")
		}
	}
	switch {
	case pkg.Run == nil:
		line("\t", start(true))
		line("\t", teardown)
		return
	case len(pkg.Run.Block) == 0:
		// An empty handler is emitted as no function at all, and never calls
		// run.
		line("\t", start(false))
		line("\t", teardown)
		return
	}
	b.WriteString("\tstarted := false\n")
	fmt.Fprintf(b, "\t%s(%s, func() {\n", ModelCallee(pkg, pkg.Run, "m"), args)
	b.WriteString("\t\tif started {\n")
	fmt.Fprintf(b, "\t\t\t%s(%q)\n", refuse, pkg.RunPos.String())
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tstarted = true\n")
	line("\t\t", start(true))
	line("\t\t", teardown)
	b.WriteString("\t})\n")
	b.WriteString("\tif !started {\n")
	b.WriteString("\t\tstarted = true\n")
	line("\t\t", start(false))
	line("\t\t", teardown)
	b.WriteString("\t}\n")
}
