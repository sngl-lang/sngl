package bubbletea

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// Every schedule is armed from Init and Update rather than from the tree, and
// each keeps the copies it is running, keyed as a cell of that copy is
// (lower.CopyKey): sync walks the loops the way the view does, starts a tick
// for a copy whose gate holds and that has none, and forgets one the loops no
// longer produce or whose gate no longer holds; a tick walks them again to find
// the copy it was armed for and run its handler with that copy's loop
// variables bound -- the way a key press finds the row under the focus cursor.
//
// A timer under no loop is the same with one copy, keyed "". It used to be
// armed once from Init and re-armed from its own tick, gated by a Model field
// only when the gate was a bare state read: a gate folded from an enclosing
// `if` or written as an expression was dropped, so the timer ran while its
// branch was off, and one that a gate did stop was never armed again when the
// gate came back. Sync runs after every Update, so the gate is asked there.
//
// A tick carries the generation its copy was started at, so one armed before
// the copy went away and came back is not a second schedule for it.

func timerMsg(i int) string  { return fmt.Sprintf("timerTickMsg%d", i) }
func timerLive(i int) string { return fmt.Sprintf("__timer%d", i) }
func timerSeq(i int) string  { return fmt.Sprintf("__timer%dSeq", i) }
func timerSync(i int) string { return fmt.Sprintf("__timer%dSync", i) }

// timerKey is the key of the copy the loops' variables name, and "" for a
// schedule under none.
func timerKey(loops []*ir.For) ir.Expr {
	if len(loops) == 0 {
		return &ir.Literal{Type: ir.TypString, Value: ""}
	}
	return lower.CopyKey(loops)
}

func emitTimerTypes(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "type %s struct {\n\tkey string\n\tgen int\n}\n\n", timerMsg(i))
	}
}

func emitTimerFields(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "\t%s map[string]int\n", timerLive(i))
		fmt.Fprintf(b, "\t%s map[string]int\n", timerSeq(i))
	}
}

func emitTimerInits(b *strings.Builder, timers []codegen.LoopTimer) {
	for i := range timers {
		fmt.Fprintf(b, "\tm.%s = map[string]int{}\n", timerLive(i))
		fmt.Fprintf(b, "\tm.%s = map[string]int{}\n", timerSeq(i))
	}
}

// emitTimerSyncCalls appends every sync's commands to `cmds`: from Init,
// and after every Update, since either may have changed which copies exist.
func emitTimerSyncCalls(b *strings.Builder, timers []codegen.LoopTimer, indent string) {
	for i := range timers {
		fmt.Fprintf(b, "%scmds = append(cmds, m.%s()...)\n", indent, timerSync(i))
	}
}

func emitTimerSyncs(b *strings.Builder, timers []codegen.LoopTimer, gc *golang.GoIRContext) {
	for i, t := range timers {
		fmt.Fprintf(b, "func (m *Model) %s() []tea.Cmd {\n", timerSync(i))
		b.WriteString("\tvar cmds []tea.Cmd\n")
		b.WriteString("\tseen := map[string]bool{}\n")
		reads := []ir.Expr{t.Interval, t.Enabled}
		indent, lgc := emitTimerHeads(b, t.Loops, reads, nil, gc)
		fmt.Fprintf(b, "%skey := %s\n", indent, lgc.EvalExpr(timerKey(t.Loops)))
		fmt.Fprintf(b, "%sif %s {\n", indent, timerGate(t, lgc))
		fmt.Fprintf(b, "%s\tseen[key] = true\n", indent)
		fmt.Fprintf(b, "%s\tif _, ok := m.%s[key]; !ok {\n", indent, timerLive(i))
		fmt.Fprintf(b, "%s\t\tm.%s[key]++\n", indent, timerSeq(i))
		fmt.Fprintf(b, "%s\t\tgen := m.%s[key]\n", indent, timerSeq(i))
		fmt.Fprintf(b, "%s\t\tm.%s[key] = gen\n", indent, timerLive(i))
		fmt.Fprintf(b, "%s\t\tcmds = append(cmds, %s)\n", indent, timerTick(i, t, lgc, "key", "gen"))
		fmt.Fprintf(b, "%s\t}\n", indent)
		fmt.Fprintf(b, "%s}\n", indent)
		closeTimerHeads(b, len(t.Loops))
		fmt.Fprintf(b, "\tfor key := range m.%s {\n", timerLive(i))
		b.WriteString("\t\tif !seen[key] {\n")
		fmt.Fprintf(b, "\t\t\tdelete(m.%s, key)\n", timerLive(i))
		b.WriteString("\t\t}\n\t}\n")
		b.WriteString("\treturn cmds\n}\n\n")
	}
}

// emitTimerCases is Update's arm for each timer's tick: the copy it was
// armed for, if that copy is still running the generation it was armed at and
// its gate holds, runs the handler and re-arms.
func emitTimerCases(b *strings.Builder, timers []codegen.LoopTimer, gc *golang.GoIRContext) {
	for i, t := range timers {
		fmt.Fprintf(b, "\tcase %s:\n", timerMsg(i))
		var body strings.Builder
		reads := []ir.Expr{t.Interval, t.Enabled}
		indent, lgc := emitTimerHeads(&body, t.Loops, reads, t.Handler.Block, gc)
		// The gate is asked again here as well as in sync: a setter called
		// outside Update closes it with no sync after.
		conds := []string{fmt.Sprintf("m.%s[msg.key] == msg.gen", timerLive(i))}
		if len(t.Loops) > 0 {
			conds = append([]string{lgc.EvalExpr(timerKey(t.Loops)) + " == msg.key"}, conds...)
		}
		if t.Enabled != nil {
			conds = append(conds, timerGate(t, lgc))
		}
		fmt.Fprintf(&body, "%sif %s {\n", indent, strings.Join(conds, " && "))
		for _, stmt := range t.Handler.Block {
			for _, line := range lgc.EvalStmt(stmt) {
				fmt.Fprintf(&body, "%s\t%s\n", indent, line)
			}
		}
		fmt.Fprintf(&body, "%s\tcmds = append(cmds, %s)\n", indent, timerTick(i, t, lgc, "msg.key", "msg.gen"))
		fmt.Fprintf(&body, "%s}\n", indent)
		closeTimerHeads(&body, len(t.Loops))
		b.WriteString(indentLines(body.String(), "\t"))
	}
}

func timerGate(t codegen.LoopTimer, gc *golang.GoIRContext) string {
	if t.Enabled == nil {
		return "true"
	}
	return gc.EvalExpr(t.Enabled)
}

func timerTick(i int, t codegen.LoopTimer, gc *golang.GoIRContext, key, gen string) string {
	return fmt.Sprintf("tea.Tick(%s, func(time.Time) tea.Msg { return %s{%s, %s} })", gc.EvalExpr(t.Interval), timerMsg(i), key, gen)
}

// emitTimerHeads opens the loops a timer is under, binding in each only
// the variables what follows reads -- the copy key reads every index, and
// reads and body say what else is -- and returns the indent inside them and
// a context their variables are locals of.
func emitTimerHeads(b *strings.Builder, loops []*ir.For, reads []ir.Expr, body []ir.Stmt, gc *golang.GoIRContext) (string, *golang.GoIRContext) {
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

func closeTimerHeads(b *strings.Builder, n int) {
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
