//go:build !js

package gtk4rt

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/headless"
)

const windowsChildEnv = "SNGL_GTK4RT_WINDOWS_CHILD"

// A window's close is its @close's to answer, the tree's to act on, and ends
// the program only when it leaves nothing on screen. Each step closes a window
// the way the window manager does and records what is on screen after it.
//
// In a process of its own, because RunWindows is the application's whole life
// and a process has one.
func TestWindowsCloseRules(t *testing.T) {
	if os.Getenv(windowsChildEnv) == "" {
		if reason := headless.SkipReason(); reason != "" {
			t.Skip(reason)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), windowsChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		want := strings.Join([]string{
			"start: one=true two=true",
			"close one (its @close unmounts it): one=false two=true closes=1",
			"mount one again: one=true two=true",
			"close two (no @close hides it): one=true two=false",
			"close one: the last on screen, so the loop ends",
			"returned",
		}, "\n")
		if !strings.Contains(string(out), want) {
			t.Fatalf("want the steps\n%s\ngot\n%s", want, out)
		}
		return
	}

	Init()
	closes := 0
	var one, two *Window
	one = &Window{Title: "one", Content: BoxNew(OrientationVertical, 0), OnClose: func() {
		closes++
		one.Unmount()
	}}
	two = &Window{Title: "two", Content: BoxNew(OrientationVertical, 0)}
	one.Mount()
	two.Mount()
	step := 0
	var sched *Schedule
	sched = Every(100, func() {
		step++
		switch step {
		case 1:
			fmt.Printf("start: one=%v two=%v\n", one.onScreen(), two.onScreen())
			one.RequestClose()
			fmt.Printf("close one (its @close unmounts it): one=%v two=%v closes=%d\n", one.onScreen(), two.onScreen(), closes)
		case 2:
			one.Mount()
			fmt.Printf("mount one again: one=%v two=%v\n", one.onScreen(), two.onScreen())
		case 3:
			two.RequestClose()
			fmt.Printf("close two (no @close hides it): one=%v two=%v\n", one.onScreen(), two.onScreen())
		case 4:
			fmt.Println("close one: the last on screen, so the loop ends")
			sched.Cancel()
			one.RequestClose()
		}
	})
	RunWindows(true)
	fmt.Println("returned")
}
