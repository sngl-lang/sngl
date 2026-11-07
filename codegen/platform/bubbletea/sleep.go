package bubbletea

import (
	"fmt"
	"strings"
)

// A widget that ticks itself -- a spinner, whose init is its own Tick and
// whose every tick asks for the next -- sleeps while the view does not draw
// it. Bubble Tea draws the view again on every message, so a spinner on a
// page not shown, or under an `if` not taken, drew the whole view ten times a
// second for nothing, and four of them were most of what a Pokédex did.
//
// The view says which it drew (`__drawn`, reset as it starts). Update hands a
// sleeper's message to it as to any widget, and keeps the command it answers
// with -- its next tick -- only while it was drawn; dropped, the ticks stop
// and the widget is asleep. A view that draws a sleeping widget wakes it: it
// cannot answer with a command, so it signals `__wake`, which a command
// waiting since Init turns into a message, and Update answers that with the
// widget's own init again.

const (
	drawnField  = "__drawn"
	asleepField = "__asleep"
	wakeField   = "__wake"
	wakeMsgType = "snglWakeMsg"
	awaitWake   = "m.__awaitWake()"
)

func drawnMethod(w widgetInfo) string { return "__" + w.fieldName + "_drawn" }

func emitSleepDecls(b *strings.Builder) {
	fmt.Fprintf(b, "// %s says the view drew a widget that was asleep.\n", wakeMsgType)
	fmt.Fprintf(b, "type %s struct{}\n\n", wakeMsgType)
	b.WriteString("// __awaitWake waits for the view to draw a widget that is asleep.\n")
	fmt.Fprintf(b, "func (m Model) __awaitWake() tea.Cmd {\n\treturn func() tea.Msg {\n\t\t<-m.%s\n\t\treturn %s{}\n\t}\n}\n\n", wakeField, wakeMsgType)
}

func emitSleepFields(b *strings.Builder, info *irAnalysis) {
	if info.sleepers > 0 {
		fmt.Fprintf(b, "\t%s, %s [%d]bool\n", drawnField, asleepField, info.sleepers)
	}
	if info.wakes() {
		fmt.Fprintf(b, "\t%s chan struct{}\n", wakeField)
	}
}

// wakes reports whether the view may wake Update: it drew a widget asleep,
// or a scroll's end came into view (scroll.go).
func (info *irAnalysis) wakes() bool { return info.sleepers > 0 || info.reaches }

// emitWakeCase writes Update's answer to the view drawing a widget asleep:
// each one drawn is woken with its init.
func emitWakeCase(b *strings.Builder, info *irAnalysis, gc goEvaluator) {
	if !info.wakes() {
		return
	}
	fmt.Fprintf(b, "\tcase %s:\n", wakeMsgType)
	for _, w := range info.widgets {
		if w.sleeper < 0 {
			continue
		}
		fmt.Fprintf(b, "\t\tif m.%s[%d] && m.%s[%d] {\n", asleepField, w.sleeper, drawnField, w.sleeper)
		fmt.Fprintf(b, "\t\t\tm.%s[%d] = false\n", asleepField, w.sleeper)
		fmt.Fprintf(b, "\t\t\tcmds = append(cmds, %s)\n", w.model.Init)
		b.WriteString("\t\t}\n")
	}
	emitScrollReach(b, info, gc)
	fmt.Fprintf(b, "\t\tcmds = append(cmds, %s)\n", awaitWake)
}

// emitSleeperCmd keeps the command a sleeper answered a message with while it
// was drawn, and puts it to sleep where the command is dropped.
func emitSleeperCmd(b *strings.Builder, w widgetInfo, indent string) {
	fmt.Fprintf(b, "%sif m.%s[%d] {\n", indent, drawnField, w.sleeper)
	fmt.Fprintf(b, "%s\tcmds = append(cmds, cmd)\n", indent)
	fmt.Fprintf(b, "%s} else if cmd != nil {\n", indent)
	fmt.Fprintf(b, "%s\tm.%s[%d] = true\n", indent, asleepField, w.sleeper)
	fmt.Fprintf(b, "%s}\n", indent)
}

// emitDrawnMethods writes, for each sleeper, what the view calls as it draws
// it: the widget is drawn, and woken where it was asleep.
func emitDrawnMethods(b *strings.Builder, info *irAnalysis) {
	for _, w := range info.widgets {
		if w.sleeper < 0 {
			continue
		}
		fmt.Fprintf(b, "// %s is the view drawing %s, which wakes it where it was asleep.\n", drawnMethod(w), w.fieldName)
		fmt.Fprintf(b, "func (m Model) %s() {\n", drawnMethod(w))
		fmt.Fprintf(b, "\tm.%s[%d] = true\n", drawnField, w.sleeper)
		fmt.Fprintf(b, "\tif m.%s[%d] {\n", asleepField, w.sleeper)
		fmt.Fprintf(b, "\t\tselect {\n\t\tcase m.%s <- struct{}{}:\n\t\tdefault:\n\t\t}\n", wakeField)
		b.WriteString("\t}\n}\n\n")
	}
}
