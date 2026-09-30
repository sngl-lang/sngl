package gtk4

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A window's `open` and `close` present and hide the gtk4rt.Window the Model
// keeps under the window's `#id`: the receiver is the handle, which the Go
// context already spells as that field.
func init() {
	codegen.RegisterPlatformIntrinsic("gtk4", codegen.WindowOpenIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Open()", nil
	})
	// A window that comes and goes with a condition is mounted and unmounted
	// by the effect passWindowLifetimes leaves in its place.
	codegen.RegisterPlatformIntrinsic("gtk4", lower.WindowMountIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Mount()", nil
	})
	codegen.RegisterPlatformIntrinsic("gtk4", lower.WindowUnmountIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Unmount()", nil
	})
	codegen.RegisterPlatformIntrinsic("gtk4", codegen.WindowCloseIntrinsic, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if len(args) != 1 {
			return "", nil
		}
		return tr(args[0]) + ".Close()", nil
	})
}

// windowBuild is one window's share of buildWidgetTree in a program that
// holds several: the statements building its content and the refs no
// AppendChild consumed, which go into the window's root box.
type windowBuild struct {
	codegen.HostWindow
	buf   strings.Builder
	tops  []string
	title string
}

// windowCloseMethod is the Model method a window's `@close` becomes.
func windowCloseMethod(field string) string {
	return "__" + strings.TrimPrefix(field, "__") + "_close"
}

// appendWidgetFieldOnce adds f unless a field of its name is declared.
func appendWidgetFieldOnce(fields []widgetField, f widgetField) []widgetField {
	for _, x := range fields {
		if x.name == f.name {
			return fields
		}
	}
	return append(fields, f)
}

// emitBuildUIWindows is buildWidgetTree for a program holding window records:
// each window's content is built into a root box of its own, then wrapped in
// the gtk4rt.Window that shows it, and every window is mounted before the
// first settles run. BuildUI is what a snapshot takes, and shows the first
// window's content in a window of its own.
func emitBuildUIWindows(b *strings.Builder, wins []windowBuild, mounts *strings.Builder, fields map[string]bool) {
	mref := func(ref string) string {
		if !fields[ref] {
			return ref
		}
		return "m." + ref
	}
	b.WriteString("func (m *Model) buildWidgetTree() {\n")
	b.WriteString("\tif m.__root != nil {\n\t\treturn\n\t}\n")
	for i := range wins {
		wb := &wins[i]
		fmt.Fprintf(b, "\tm.%s = gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)\n", wb.Root)
		b.WriteString(wb.buf.String())
		for _, ref := range wb.tops {
			fmt.Fprintf(b, "\tgtk4rt.BoxAppend(m.%s, %s)\n", wb.Root, mref(ref))
		}
		fmt.Fprintf(b, "\tm.%s = &gtk4rt.Window{", wb.Field)
		if wb.title != "" {
			fmt.Fprintf(b, "Title: %s, ", wb.title)
		}
		fmt.Fprintf(b, "Content: m.%s", wb.Root)
		if wb.Close != nil {
			fmt.Fprintf(b, ", OnClose: m.%s", windowCloseMethod(wb.Field))
		}
		b.WriteString("}\n")
		if !wb.Lifetime {
			fmt.Fprintf(b, "\tm.%s.Mount()\n", wb.Field)
		}
	}
	b.WriteString(mounts.String())
	b.WriteString("}\n\n")
	b.WriteString("// BuildUI constructs the widget tree and returns a window showing the first\n")
	b.WriteString("// window's content.\n")
	b.WriteString("func (m *Model) BuildUI(app gtk4rt.Handle) gtk4rt.Handle {\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	b.WriteString("\twin := gtk4rt.ApplicationWindowNew(app)\n")
	b.WriteString("\tgtk4rt.WindowSetDefaultSize(win, 480, 640)\n")
	if len(wins) > 0 && wins[0].title != "" {
		fmt.Fprintf(b, "\tgtk4rt.WindowSetTitle(win, %s)\n", wins[0].title)
	}
	b.WriteString("\tgtk4rt.WindowSetChild(win, m.__root)\n")
	b.WriteString("\treturn win\n")
	b.WriteString("}\n\n")
}

// emitGTK4WindowsMain is the entry point of a program holding window
// records. The tree is built and every window mounted first -- the first
// settle included -- and gtk4rt.RunWindows then creates the toplevels. `run`
// is what puts them on screen: a program that wrote `@run` and never called
// it runs the loop with none shown.
func emitGTK4WindowsMain(b *strings.Builder, run string, hasRun bool, teardown *ir.Func) {
	b.WriteString("\nfunc main() {\n")
	b.WriteString("\tgtk4rt.Init()\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	switch {
	case !hasRun:
		b.WriteString("\tgtk4rt.RunWindows(true)\n")
	case run == "":
		// An empty handler is emitted as no function at all, and never calls
		// run.
		b.WriteString("\tgtk4rt.RunWindows(false)\n")
	default:
		b.WriteString("\tran := false\n")
		fmt.Fprintf(b, "\t%s(gtk4rt.Args(), func() {\n", run)
		b.WriteString("\t\tran = true\n")
		b.WriteString("\t\tgtk4rt.RunWindows(true)\n")
		b.WriteString("\t})\n")
		b.WriteString("\tif !ran {\n")
		b.WriteString("\t\tgtk4rt.RunWindows(false)\n")
		b.WriteString("\t}\n")
	}
	if teardown != nil {
		fmt.Fprintf(b, "\tm.%s()\n", teardown.Name)
	}
	b.WriteString("}\n")
}
