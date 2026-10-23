package bubbletea

import (
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// Bubble Tea lets nothing outside Update touch the Model, so async.do is a
// message: the closure goes into a channel the program listens on with a
// tea.Cmd, and Update runs it when it arrives. The closure carries the Model
// it writes -- the one the event loop owns, never the snapshot work off the
// loop reads (golang.OffLoopSnapshots) -- so whatever runs it, Update or a
// test waiting for it, runs it against the right state.
const (
	postsVar    = "__snglPosts"
	postMsgType = "__snglPostMsg"
	awaitPost   = "__snglAwaitPost"
	// settleVar is what a waiting test calls each turn to run the commands
	// the Model it holds asks for (settleModel): a no-op until a test build
	// points it at that Model (emitSettleHook).
	settleVar   = "__snglSettle"
	settleModel = "__snglSettleModel"
)

// A test waiting for a request's answer runs on the goroutine that owns the
// Model, as Update does, so it runs what was posted while it waits.
func init() {
	// The post outside an off-loop literal, whose closure already holds the
	// Model the loop owns; inside one, golang's evalPost hands the closure
	// that Model in place of the snapshot.
	codegen.RegisterPlatformIntrinsic("bubbletea", lower.AsyncDoIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return golang.PostFunc + "(" + tr(args[0]) + ")", nil
	})
	codegen.RegisterPlatformIntrinsic("bubbletea", ir.TestWaitID, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 3 {
			return "", nil
		}
		return "for __snglWait, __snglEnd := " + tr(args[1]) + ", time.Now().Add(time.Duration(" + tr(args[2]) + ")*time.Millisecond); !__snglWait() && time.Now().Before(__snglEnd); { " + settleVar + "(); " + golang.RunPostsFunc + "(5 * time.Millisecond) }", []string{"time"}
	})
}

// usesPosts reports whether anything in the lowered program posts back to
// the loop -- a program's own async.do, or the tail passAsyncOffload moved
// after a blocking call -- or a test waits, which runs what was posted
// whether or not anything can be.
func usesPosts(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(node ir.Node) error {
		if c, ok := node.(*ir.Call); ok && c.Func != nil && (c.Func.Intrinsic == lower.AsyncDoIntrinsic || c.Func.Intrinsic == ir.TestWaitID) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// emitPostDecls declares the channel, the post, the command that waits for
// one, and what a test runs them with.
func emitPostDecls(b *strings.Builder) {
	b.WriteString(`
// ` + postsVar + ` holds the closures work off the event loop posted back to
// it (async.do), each carrying the Model it writes.
var ` + postsVar + ` = make(chan func(), 64)

// ` + golang.PostFunc + ` hands f to the event loop.
func ` + golang.PostFunc + `(f func()) { ` + postsVar + ` <- f }

// ` + postMsgType + ` is a posted closure arriving at Update.
type ` + postMsgType + ` func()

// ` + awaitPost + ` waits for the next posted closure.
func ` + awaitPost + `() tea.Msg { return ` + postMsgType + `(<-` + postsVar + `) }

// ` + golang.RunPostsFunc + ` runs what was posted within d, for a test, which
// owns the Model as Update does.
func ` + golang.RunPostsFunc + `(d time.Duration) {
	select {
	case f := <-` + postsVar + `:
		f()
	case <-time.After(d):
	}
}

// ` + settleVar + ` runs, for a waiting test, what the Model it holds asks of
// the event loop no program runs under a test.
var ` + settleVar + ` = func() {}

// ` + settleModel + ` is Update after whatever the test did, whose commands --
// each schedule's tick, armed after every Update -- run off the test's
// goroutine, each message they answer posted back to be delivered on it.
func (m *Model) ` + settleModel + `() {
	__snglLive = m
	m.__snglDeliver(struct{}{})
}

// __snglLive is the Model the running test settles. A message answering a
// command another Model's Update returned -- a test that has ended, its tick
// still on the way -- is dropped rather than delivered to it, so it neither
// runs that test's handlers during this one nor arms its tick again.
var __snglLive *Model

// __snglDeliver is Update and the draw after it, as the program's loop runs
// them: a picture a waiting test's view asks for is fetched while it waits.
func (m *Model) __snglDeliver(msg tea.Msg) {
	nm, cmd := m.Update(msg)
	*m = nm.(Model)
	_ = m.View()
	__snglRunCmd(m, cmd)
}

func __snglRunCmd(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				__snglRunCmd(m, c)
			}
			return
		}
		if msg != nil {
			` + golang.PostFunc + `(func() {
				if __snglLive == m {
					m.__snglDeliver(msg)
				}
			})
		}
	}()
}
`)
}

// drawAfterUpdate draws the view after an invoker's Update, as the program
// does after every one: a test drew it only to snapshot, so what the view
// does as it renders -- a picture starting its fetch, a raise a boundary
// hears -- had not happened when the test looked.
func drawAfterUpdate(b *strings.Builder) {
	b.WriteString("\t_ = m.View()\n")
}

// keepCmd ends an invoker: the commands its Update returned run as a waiting
// test runs the Model's, where the program can post back to its loop, and are
// dropped otherwise.
func keepCmd(b *strings.Builder, posts bool) {
	if posts {
		b.WriteString("\t__snglRunCmd(m, cmd)\n")
		return
	}
	b.WriteString("\t_ = cmd\n")
}

// settleHookSrc is the body of the test build's file pointing a waiting
// test's settle at the Model the test holds.
const settleHookSrc = `

func init() {
	` + settleVar + ` = func() {
		if m := currentTestModel(); m != nil {
			m.` + settleModel + `()
		}
	}
}
`
