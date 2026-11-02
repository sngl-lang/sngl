//go:build !js

package gtk4rt

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"duckfam.us/sngl/internal/headless"
	"duckfam.us/sngl/pkg/go/gtk4rt/internal/gtkprobe"
)

const keysChildEnv = "SNGL_GTK4RT_KEYS_CHILD"

// A window's `@key` hears the keys its content leaves, pressed on the seat's
// keyboard as a person presses them: a key a focused button does not take,
// and not the Enter that presses it, the text typed into a focused entry, or
// the Tab that moves focus. A widget written `focusable=false` -- a button,
// an entry -- is given `can-focus` false, as gtk4.sngl's overrides give it,
// and takes focus by neither Tab nor a click, which still presses a button.
//
// In a process of its own, because RunWindows is the application's whole life
// and a process has one.
func TestAWindowHearsTheKeysItsContentLeaves(t *testing.T) {
	if os.Getenv(keysChildEnv) == "" {
		if reason := headless.SkipReason(); reason != "" {
			t.Skip(reason)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), keysChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		want := strings.Join([]string{
			"7 on a focused button: keys=[7] clicks=0",
			"Enter on it presses it: keys=[7] clicks=1",
			"a into a focused entry: keys=[7] text=a",
			"Tab moves focus, heard by neither: keys=[7] reached=entry,plain",
			"a click focuses the plain button: true",
			"and not the unfocusable one, which it presses: false clicks=1",
			"and focus stays where it was: true",
			"neither unfocusable widget grabs focus: false false",
			"Escape on a button: keys=[7 Escape]",
		}, "\n")
		if !strings.Contains(string(out), want) {
			t.Fatalf("want the steps\n%s\ngot\n%s", want, out)
		}
		return
	}

	Init()
	var keys []string
	clicks := 0
	top := ToplevelNew()
	ToplevelOnKey(top, func(k string) { keys = append(keys, k) })
	entry := EntryNew()
	plain := ButtonNew()
	ButtonSetLabel(plain, "plain")
	Connect(plain, "clicked", func() { clicks++ })
	keylessClicks := 0
	keyless := ButtonNew()
	ButtonSetLabel(keyless, "keyless")
	SetBool(keyless, "can-focus", false)
	Connect(keyless, "clicked", func() { keylessClicks++ })
	keylessEntry := EntryNew()
	SetBool(keylessEntry, "can-focus", false)
	for _, w := range []Handle{entry, plain, keyless, keylessEntry} {
		BoxAppend(top, w)
	}
	AppAttach(top)

	kbd, err := gtkprobe.NewKeyboard()
	if err != nil {
		fmt.Println("no keyboard:", err)
		os.Exit(1)
	}
	win := func() unsafe.Pointer { return unsafe.Pointer(topOf(top).win) }
	focus := func() unsafe.Pointer { return gtkprobe.Focus(win()) }
	in := func(w Handle) bool { return gtkprobe.IsAncestor(focus(), unsafe.Pointer(w)) }
	press := func(key uint32) {
		if err := kbd.Press(key); err != nil {
			fmt.Println("press:", err)
			os.Exit(1)
		}
	}
	var reached []string
	note := func() {
		for _, w := range []struct {
			name string
			h    Handle
		}{{"entry", entry}, {"plain", plain}, {"keyless", keyless}, {"keylessEntry", keylessEntry}} {
			if in(w.h) && !slices.Contains(reached, w.name) {
				reached = append(reached, w.name)
			}
		}
	}
	step := 0
	var sched *Schedule
	sched = Every(150, func() {
		step++
		switch step {
		case 1:
			gtkprobe.GrabFocus(unsafe.Pointer(plain))
			press(gtkprobe.Key7)
		case 2:
			fmt.Printf("7 on a focused button: keys=%v clicks=%d\n", keys, clicks)
			press(gtkprobe.KeyEnter)
		case 3:
			fmt.Printf("Enter on it presses it: keys=%v clicks=%d\n", keys, clicks)
			gtkprobe.GrabFocus(unsafe.Pointer(entry))
			press(gtkprobe.KeyA)
		case 4:
			fmt.Printf("a into a focused entry: keys=%v text=%s\n", keys, EditableGetText(entry))
			note()
			press(gtkprobe.KeyTab)
		case 5, 6, 7:
			note()
			press(gtkprobe.KeyTab)
		case 8:
			note()
			fmt.Printf("Tab moves focus, heard by neither: keys=%v reached=%s\n", keys, strings.Join(reached, ","))
			gtkprobe.GrabFocus(unsafe.Pointer(entry))
			gtkprobe.Click(unsafe.Pointer(plain))
			fmt.Printf("a click focuses the plain button: %v\n", in(plain))
			gtkprobe.Click(unsafe.Pointer(keyless))
			fmt.Printf("and not the unfocusable one, which it presses: %v clicks=%d\n", in(keyless), keylessClicks)
			fmt.Printf("and focus stays where it was: %v\n", in(plain))
			fmt.Printf("neither unfocusable widget grabs focus: %v %v\n",
				gtkprobe.GrabFocus(unsafe.Pointer(keyless)), gtkprobe.GrabFocus(unsafe.Pointer(keylessEntry)))
			press(gtkprobe.KeyEsc)
		case 9:
			fmt.Printf("Escape on a button: keys=%v\n", keys)
			sched.Cancel()
			kbd.Close()
			ToplevelRequestClose(top)
		}
	})
	RunWindows(true)
}
