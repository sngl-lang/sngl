package fyne

import (
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// Fyne's widgets belong to the goroutine running the driver, and fyne.Do is
// how anything else reaches it -- the same call the remote store's settle
// already goes through. Do rather than DoAndWait: the goroutine that posts has
// nothing left to do, and waiting for the UI to catch up would only hold it
// open. A test build queues the closure instead (postFunc), and a test's wait
// runs what arrived on the test's own goroutine, which is the driver's there.
const (
	postFunc      = "__snglDo"
	testPostsVar  = "__snglTestPosts"
	runPostsFunc  = "__snglRunPosts"
	dropPostsFunc = "__snglDropPosts"
)

// agentPosts is what a test build declares beside the model for t.wait: it
// turns the queue on where the program posts, and runs what arrived; where
// nothing arrives it only lets time pass.
//
// What the runtime posts (fynelayout.Post: a scroll's @reachEnd) is queued
// too, on a queue of its own, since fyne's test driver runs fyne.Do at once
// and a report made while the snapshot lays the tree out would run there.
//
// Each test starts from a fresh Model, so what the last test's Model posted
// and the last test never waited for is dropped as the next one starts
// (dropPostsFunc), rather than drawn inside the next test's waits.
func agentPosts(pkg *ir.Package) string {
	programPosts, drain := "", ""
	if usesPosts(pkg) {
		programPosts = `
	case f := <-` + testPostsVar + `:
		f()`
		drain = `
		case <-` + testPostsVar + `:`
	}
	return `
var ` + layoutPostsVar + ` = make(chan func(), 256)

func init() {
	fynelayout.Post = func(f func()) { ` + layoutPostsVar + ` <- f }` + initPosts(pkg) + `
}

func ` + runPostsFunc + `(d time.Duration) {
	select {` + programPosts + `
	case f := <-` + layoutPostsVar + `:
		f()
	case <-time.After(d):
	}
}

func ` + dropPostsFunc + `() {
	for {
		select {` + drain + `
		case <-` + layoutPostsVar + `:
		default:
			return
		}
	}
}
`
}

// layoutPostsVar is the queue a test build gives fynelayout.Post.
const layoutPostsVar = "__snglLayoutPosts"

// initPosts turns the program's queue on, where it posts.
func initPosts(pkg *ir.Package) string {
	if !usesPosts(pkg) {
		return ""
	}
	return "\n\t" + testPostsVar + " = make(chan func(), 256)"
}

func init() {
	codegen.RegisterPlatformIntrinsic("fyne", lower.AsyncDoIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return postFunc + "(" + tr(args[0]) + ")", nil
	})
	codegen.RegisterPlatformIntrinsic("fyne", ir.TestWaitID, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return "for __snglWait, __snglEnd := " + tr(args[1]) + ", time.Now().Add(time.Duration(" + tr(args[2]) + ")*time.Millisecond); !__snglWait() && time.Now().Before(__snglEnd); { " + runPostsFunc + "(5 * time.Millisecond) }", []string{"time"}
	})
}

// usesPosts reports whether anything posts back to the driver: a program's
// async.do, or the tail passAsyncOffload moved after a blocking call.
func usesPosts(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	found := false
	_ = ir.Walk(pkg, func(node ir.Node) error {
		if c, ok := node.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == lower.AsyncDoIntrinsic {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// emitPostDecls declares what work off the driver posts back to it through
// (async.do). A test build queues it for t.wait to run on the test's
// goroutine instead: fyne's test driver runs fyne.Do on the goroutine that
// calls it, so a request's answer rebuilt widgets while the test was
// clicking others.
func emitPostDecls(b *strings.Builder, gc *golang.GoIRContext) {
	gc.RequireImport("fyne.io/fyne/v2")
	b.WriteString(`// ` + testPostsVar + ` is set by a test build, which runs what is posted when it waits.
var ` + testPostsVar + ` chan func()

// ` + postFunc + ` hands f to the goroutine that owns the widgets.
func ` + postFunc + `(f func()) {
	if ` + testPostsVar + ` != nil {
		` + testPostsVar + ` <- f
		return
	}
	fyne.Do(f)
}

`)
}
