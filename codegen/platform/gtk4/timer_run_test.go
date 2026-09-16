//go:build !js

package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// timerRunSrc has a timer in `main` and a second one in a component, so the run
// asserts both that a timer fires at all and that one written somewhere other
// than the root is emitted.
//
// No label reads a counter, deliberately: a tick that wrote one would carry the
// reactive `gtk4rt.LabelSetText` beside it, and the widget it names is built by
// buildWidgetTree, which is gtk_init and a display. What a tick writes to a
// widget is a claim the `timer_every_target.txtar` golden makes; what this one
// makes is that the schedule runs at all, and that has to run in CI.
const timerRunSrc = `
import . "sngl:ui"
import . "sngl:time"

component beat(label = "") node {
    var beats = 0

    timer(interval=10ms, enabled=true, @tick { beats += 1 })

    text(value=label)
}

window {
    var (
        seconds = 0
        running = true
    )

    timer(interval=10ms, enabled=running, @tick { seconds += 1 })

    beat(label="b")
    text #out(value="tick")
}
`

// TestATimerFires runs the emitted program rather than reading it.
//
// gtk4 emitted no timer at all before `timer` became an ordinary component: the
// analysis read a list the checker had hoisted every timer onto, and this
// platform never looked at it. A program with a timer compiled clean, reported
// success, and never ticked -- which is exactly why compiling the output is not
// enough here and the GLib loop has to actually run.
//
// The schedule is an `effect` over gtk4rt.Every now, so what arms it is the
// settle rather than a startTimers the model called from New.
func TestATimerFires(t *testing.T) {
	skipWithoutGIR(t)
	model := generateGTK4ModelBuilt(t, timerRunSrc)
	if !strings.Contains(model, "gtk4rt.Every(") {
		t.Fatalf("no schedule was armed at all:\n%s", model)
	}
	runGTK4Model(t, "gtk4-timer-", model, `package main

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
)

// The settle is what arms the schedules, and buildWidgetTree is where the
// emitted model calls it. It is called directly here because that function is
// gtk_init and a display; the brackets themselves need neither.
//
// Pumping the loop is what lets them fire; without it the sources exist and
// nothing ever runs them.
//
// Deliberately no gtk4rt.Init(): that is gtk_init(), which needs a display, and
// CI has none -- it failed here with "cannot open display". A timer is a GLib
// timeout on the default main context, so arming and pumping one needs no GTK
// and no display. Skipping the test headlessly would have been the other
// option, and would have meant the only evidence that a gtk4 timer fires never
// runs in CI.
func TestTimersTick(t *testing.T) {
	m := New()
	m.__effects0_settle()
	gtk4rt.PumpFor(200)
	if m.Seconds() == 0 {
		t.Error("the root component's timer never fired")
	}
	if m.Beats__inst2() == 0 {
		t.Error("the child component's timer never fired")
	}
	m.__snglTeardown()
	stopped := m.Seconds()
	gtk4rt.PumpFor(100)
	if m.Seconds() != stopped {
		t.Errorf("a released timer kept firing: %d then %d", stopped, m.Seconds())
	}
}
`)
}

// runGTK4Model writes the model plus a test beside it, under the main module so
// gtk4rt resolves through the project's go.mod, and runs `go test` there.
func runGTK4Model(t *testing.T, prefix, model, testSrc string) {
	t.Helper()
	tmp, err := os.MkdirTemp(".", prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("model.go", model)
	// The emission is `package main` without a main(), which links only under
	// `sngl run`'s own scaffold.
	write("entry.go", "package main\n\nfunc main() {}\n")
	write("timer_test.go", testSrc)

	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Errorf("the emitted program's timers did not run: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			runErr, combined, model)
	}
}
