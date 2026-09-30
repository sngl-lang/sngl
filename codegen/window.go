package codegen

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Intrinsic ids of the window methods a platform answers with the host window
// the Model keeps.
const (
	WindowOpenIntrinsic  = "window.open"
	WindowCloseIntrinsic = "window.close"
)

// OpensWindows reports whether pkg calls `open` or `close` on a window. A host
// keeps its window in a Model field only then: an `#id` on a window is also
// how a route or a title is read, and a field nothing opens would be one more
// declaration in every such program.
func OpensWindows(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil &&
			(c.Func.Intrinsic == WindowOpenIntrinsic || c.Func.Intrinsic == WindowCloseIntrinsic) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// HostWindow is one window a host that holds several keeps a record for: the
// toplevel the host shows, around a widget tree the program builds once.
type HostWindow struct {
	*WindowCtx
	// Field is the Model field holding the record: the window's `#id`, which
	// is what `open` and `close` spell as the receiver, or `__win<N>` for a
	// window nobody names.
	Field string
	// Root is the box the window's content is built into, which is also the
	// parent its top-level render slots re-render into (ir.WindowRootName).
	Root string
	// Close is the window's `@close`, or nil.
	Close *ir.EventHandler
	// Lifetime says the window comes and goes with a condition
	// (ir.NodeInst.Presence): it is not mounted while the tree is built, and
	// the effect in its place calls lower.WindowMountIntrinsic and
	// lower.WindowUnmountIntrinsic on its handle.
	Lifetime bool
}

// HostWindows is the record each window of ctx gets on a host that holds
// windows the program can create, show and hide (gtk4, fyne), or nil when one
// window with no record says everything the program does: a single window no
// handler opens or closes and that declares no `@close`.
//
// Nil is kept for that case so a single-window program's output is what it
// was before a window had a record, which is every program but the few that
// ask for one.
func HostWindows(ctx *CodegenCtx) []HostWindow {
	wins := ctx.Windows()
	if len(wins) == 0 || wins[0].Window == nil {
		return nil
	}
	need := len(wins) > 1 || OpensWindows(ctx.Pkg)
	for _, w := range wins {
		if windowClose(w.Window) != nil || w.Window.Presence != nil {
			need = true
		}
	}
	if !need {
		return nil
	}
	out := make([]HostWindow, 0, len(wins))
	for i, w := range wins {
		field := w.Window.ID
		if field == "" {
			field = "__win" + strconv.Itoa(i)
		}
		out = append(out, HostWindow{
			WindowCtx: w,
			Field:     field,
			Root:      ir.WindowRootName(ctx.Pkg, w.Window),
			Close:     windowClose(w.Window),
			Lifetime:  w.Window.Presence != nil,
		})
	}
	return out
}

func windowClose(w *ir.Window) *ir.EventHandler {
	for i := range w.Handlers {
		if w.Handlers[i].Name == "close" && w.Handlers[i].Func != nil {
			return &w.Handlers[i]
		}
	}
	return nil
}
