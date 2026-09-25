package bubbletea

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A timer written under a loop is a schedule per copy of the loop, and this
// platform arms schedules from Init and Update rather than from the tree. So
// each one keeps the copies it is running, keyed as a cell of that copy is
// (lower.CopyKey): sync walks the loops the way the view does, starts a tick
// for a copy it has none for and forgets one the loops no longer produce, and
// a tick walks them again to find the copy it was armed for and run its handler
// with that copy's loop variables bound -- the way a key press finds the row
// under the focus cursor.
//
// A tick carries the generation its copy was started at, so one armed before
// the copy went away and came back is not a second schedule for it.

func loopTimerMsg(i int) string  { return fmt.Sprintf("loopTimerTickMsg%d", i) }
func loopTimerLive(i int) string { return fmt.Sprintf("__loopTimer%d", i) }
func loopTimerSeq(i int) string  { return fmt.Sprintf("__loopTimer%dSeq", i) }
func loopTimerSync(i int) string { return fmt.Sprintf("__loopTimer%dSync", i) }

func emitLoopTimerTypes(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "type %s struct {\n\tkey string\n\tgen int\n}\n\n", loopTimerMsg(i))
	}
}

func emitLoopTimerFields(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "\t%s map[string]int\n", loopTimerLive(i))
		fmt.Fprintf(b, "\t%s map[string]int\n", loopTimerSeq(i))
	}
}

func emitLoopTimerInits(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "\tm.%s = map[string]int{}\n", loopTimerLive(i))
		fmt.Fprintf(b, "\tm.%s = map[string]int{}\n", loopTimerSeq(i))
	}
}

// emitLoopTimerSyncCalls appends every sync's commands to `cmds`: from Init,
// and after every Update, since either may have changed which copies exist.
func emitLoopTimerSyncCalls(b *strings.Builder, timers []codegen.LoopTimer, indent string) {
	for i := range timers {
		fmt.Fprintf(b, "%scmds = append(cmds, m.%s()...)\n", indent, loopTimerSync(i))
	}
}

func emitLoopTimerSyncs(b *strings.Builder, timers []codegen.LoopTimer, gc *golang.GoIRContext) {
	for i, t := range timers {
		fmt.Fprintf(b, "func (m *Model) %s() []tea.Cmd {\n", loopTimerSync(i))
		b.WriteString("\tvar cmds []tea.Cmd\n")
		b.WriteString("\tseen := map[string]bool{}\n")
		reads := []ir.Expr{t.Interval, t.Enabled}
		indent, lgc := emitLoopTimerHeads(b, t.Loops, reads, nil, gc)
		fmt.Fprintf(b, "%skey := %s\n", indent, lgc.EvalExpr(lower.CopyKey(t.Loops)))
		fmt.Fprintf(b, "%sif %s {\n", indent, loopTimerGate(t, lgc))
		fmt.Fprintf(b, "%s\tseen[key] = true\n", indent)
		fmt.Fprintf(b, "%s\tif _, ok := m.%s[key]; !ok {\n", indent, loopTimerLive(i))
		fmt.Fprintf(b, "%s\t\tm.%s[key]++\n", indent, loopTimerSeq(i))
		fmt.Fprintf(b, "%s\t\tgen := m.%s[key]\n", indent, loopTimerSeq(i))
		fmt.Fprintf(b, "%s\t\tm.%s[key] = gen\n", indent, loopTimerLive(i))
		fmt.Fprintf(b, "%s\t\tcmds = append(cmds, %s)\n", indent, loopTimerTick(i, t, lgc, "key", "gen"))
		fmt.Fprintf(b, "%s\t}\n", indent)
		fmt.Fprintf(b, "%s}\n", indent)
		closeLoopTimerHeads(b, len(t.Loops))
		fmt.Fprintf(b, "\tfor key := range m.%s {\n", loopTimerLive(i))
		b.WriteString("\t\tif !seen[key] {\n")
		fmt.Fprintf(b, "\t\t\tdelete(m.%s, key)\n", loopTimerLive(i))
		b.WriteString("\t\t}\n\t}\n")
		b.WriteString("\treturn cmds\n}\n\n")
	}
}

// emitLoopTimerCases is Update's arm for each timer's tick: the copy it was
// armed for, if that copy is still running the generation it was armed at,
// runs the handler and re-arms.
func emitLoopTimerCases(b *strings.Builder, timers []codegen.LoopTimer, gc *golang.GoIRContext) {
	for i, t := range timers {
		fmt.Fprintf(b, "\tcase %s:\n", loopTimerMsg(i))
		var body strings.Builder
		reads := []ir.Expr{t.Interval}
		indent, lgc := emitLoopTimerHeads(&body, t.Loops, reads, t.Handler.Block, gc)
		fmt.Fprintf(&body, "%sif %s == msg.key && m.%s[msg.key] == msg.gen {\n", indent, lgc.EvalExpr(lower.CopyKey(t.Loops)), loopTimerLive(i))
		for _, stmt := range t.Handler.Block {
			for _, line := range lgc.EvalStmt(stmt) {
				fmt.Fprintf(&body, "%s\t%s\n", indent, line)
			}
		}
		fmt.Fprintf(&body, "%s\tcmds = append(cmds, %s)\n", indent, loopTimerTick(i, t, lgc, "msg.key", "msg.gen"))
		fmt.Fprintf(&body, "%s}\n", indent)
		closeLoopTimerHeads(&body, len(t.Loops))
		b.WriteString(indentLines(body.String(), "\t"))
	}
}

func loopTimerGate(t codegen.LoopTimer, gc *golang.GoIRContext) string {
	if t.Enabled == nil {
		return "true"
	}
	return gc.EvalExpr(t.Enabled)
}

func loopTimerTick(i int, t codegen.LoopTimer, gc *golang.GoIRContext, key, gen string) string {
	return fmt.Sprintf("tea.Tick(%s, func(time.Time) tea.Msg { return %s{%s, %s} })", gc.EvalExpr(t.Interval), loopTimerMsg(i), key, gen)
}

// emitLoopTimerHeads opens the loops a timer is under, binding in each only
// the variables what follows reads -- the copy key reads every index, and
// reads and body say what else is -- and returns the indent inside them and
// a context their variables are locals of.
func emitLoopTimerHeads(b *strings.Builder, loops []*ir.For, reads []ir.Expr, body []ir.Stmt, gc *golang.GoIRContext) (string, *golang.GoIRContext) {
	indent := "\t"
	lgc := gc
	for i, f := range loops {
		// What decides which variables the head declares is its body, so the
		// copy handed to ForHead is given one reading everything below.
		used := []ir.Stmt{&ir.Return{Value: &ir.Ident{Name: f.Key}}}
		for _, e := range reads {
			used = append(used, &ir.Return{Value: e})
		}
		for _, inner := range loops[i+1:] {
			used = append(used, &ir.Return{Value: inner.Iter})
		}
		used = append(used, body...)
		head := *f
		head.Body, head.Else = used, nil
		fmt.Fprintf(b, "%s%s\n", indent, lgc.ForHead(&head, lgc.EvalExpr(f.Iter)))
		if f.Key != "" && f.Key != "_" {
			lgc = lgc.WithLocal(f.Key)
		}
		if f.Value != "" && f.Value != "_" {
			lgc = lgc.WithLocal(f.Value)
		}
		indent += "\t"
	}
	return indent, lgc
}

func closeLoopTimerHeads(b *strings.Builder, n int) {
	for i := n; i > 0; i-- {
		fmt.Fprintf(b, "%s}\n", strings.Repeat("\t", i))
	}
}

func indentLines(s, indent string) string {
	lines := strings.SplitAfter(s, "\n")
	var out strings.Builder
	for _, l := range lines {
		if l != "" && l != "\n" {
			out.WriteString(indent)
		}
		out.WriteString(l)
	}
	return out.String()
}
