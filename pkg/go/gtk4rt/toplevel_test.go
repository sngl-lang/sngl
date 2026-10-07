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

// A window manager's close is reported as `visible = false` and then as
// `@closed`, the tree's to act on, and ends the program only when it leaves
// nothing on screen. Each step closes a toplevel the way the window manager
// does and records what is on screen after it.
//
// In a process of its own, because RunWindows is the application's whole life
// and a process has one.
func TestToplevelCloseRules(t *testing.T) {
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
			"close one (its @closed detaches it): one=false two=true closes=1 reported=false",
			"attach another: three=true two=true",
			"close two (no @closed hides it): three=true two=false",
			"close three: the last on screen, so the loop ends",
			"returned",
		}, "\n")
		if !strings.Contains(string(out), want) {
			t.Fatalf("want the steps\n%s\ngot\n%s", want, out)
		}
		return
	}

	Init()
	closes := 0
	reported := true
	on := func(h Handle) bool { return topOf(h) != nil && topOf(h).onScreen() }
	one := ToplevelNew()
	ToplevelSetTitle(one, "one")
	ToplevelOnVisible(one, func(v bool) { reported = v })
	ToplevelOnClosed(one, func() {
		closes++
		AppDetach(one)
	})
	two := ToplevelNew()
	ToplevelSetTitle(two, "two")
	AppAttach(one)
	AppAttach(two)
	var three Handle
	step := 0
	var sched *Schedule
	sched = Every(100, func() {
		step++
		switch step {
		case 1:
			fmt.Printf("start: one=%v two=%v\n", on(one), on(two))
			ToplevelRequestClose(one)
			fmt.Printf("close one (its @closed detaches it): one=%v two=%v closes=%d reported=%v\n", on(one), on(two), closes, reported)
		case 2:
			three = ToplevelNew()
			ToplevelSetTitle(three, "three")
			AppAttach(three)
			fmt.Printf("attach another: three=%v two=%v\n", on(three), on(two))
		case 3:
			ToplevelRequestClose(two)
			fmt.Printf("close two (no @closed hides it): three=%v two=%v\n", on(three), on(two))
		case 4:
			fmt.Println("close three: the last on screen, so the loop ends")
			sched.Cancel()
			ToplevelRequestClose(three)
		}
	})
	RunWindows(true)
	fmt.Println("returned")
}
