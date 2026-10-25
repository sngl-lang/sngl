package bubbletea

import "testing"

// settleRunSrc ticks fast and posts back to its loop (async.do), so the test
// build's settle machinery is emitted beside the Model.
const settleRunSrc = `
import async "sngl:async"
import time "sngl:time"
import ui "sngl:ui"

var ticks = 0
var posted = 0

ui.window(title="Settle") {
    time.timer(interval=1ms, @tick {
        ticks += 1
    })
    ui.button(text="post", @click {
        async.do(func() {
            posted += 1
        })
    })
}
`

// A test that settles its own Model runs that Model's schedules alone. The
// agent runs every test in one process with one channel of posts, and a tick
// armed for a test that has ended used to be delivered to its Model during the
// next test's wait -- running its handler there and arming it again, every
// turn, for the rest of the run.
const settleRunDriver = `package ui

import (
	"testing"
	"time"
)

func TestAnEndedTestsTicksAreNotDelivered(t *testing.T) {
	first := New()
	first.__snglSettleModel()
	second := New()
	second.__snglSettleModel()
	for end := time.Now().Add(150 * time.Millisecond); time.Now().Before(end); {
		second.__snglSettleModel()
		__snglRunPosts(5 * time.Millisecond)
	}
	if first.ticks != 0 {
		t.Errorf("the ended test's Model ticked %d times during the next test", first.ticks)
	}
	if second.ticks == 0 {
		t.Error("the running test's Model never ticked")
	}
}
`

func TestAnEndedTestsTicksAreNotDeliveredInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, settleRunSrc, settleRunDriver)
}
