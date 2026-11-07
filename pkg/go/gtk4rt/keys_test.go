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

// A window's `@key` hears each key pressed in it before anything in it,
// pressed on the seat's keyboard as a person presses them: the Enter that
// presses a focused button, the letter typed into a focused entry, the Tab
// that moves focus and the Escape that goes back, each of which still goes on
// when the handler leaves it and goes nowhere when the handler stops it.
// Modifiers are reported with the key's own character, a modifier alone not
// at all, and a key pressed while the entry's input method composes not
// either. A widget written `focusable=false` -- a button, an entry -- is given
// `can-focus` false, as gtk4.sngl's overrides give it, and takes focus by
// neither Tab nor a click, which still presses a button.
//
// In a process of its own, because RunWindows is the application's whole life
// and a process has one. The input method is GTK's own, whose Control+Shift+U
// composes a character from its code.
func TestAWindowHearsEveryKeyFirst(t *testing.T) {
	if os.Getenv(keysChildEnv) == "" {
		if reason := headless.SkipReason(); reason != "" {
			t.Skip(reason)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), keysChildEnv+"=1", "GTK_IM_MODULE=gtk-im-context-simple")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child failed: %v\n%s", err, out)
		}
		want := strings.Join([]string{
			"7 on a focused button: heard=[7] clicks=0",
			"Enter on it is heard and presses it: heard=[Enter] clicks=1",
			"a stopped Enter presses nothing: heard=[Enter] clicks=1",
			"a stopped a types nothing: heard=[<a>] text=",
			"an a left goes on into the entry: heard=[<a>] text=a",
			"a stopped Tab moves no focus: heard=[<Tab>] in entry=true",
			"Tab left moves focus: reached=entry,plain",
			"Control+S: heard=[^s]",
			"Control+Shift+S: heard=[^S]",
			"Control+S with Caps Lock on: heard=[^s S]",
			"a click focuses the plain button: true",
			"and not the unfocusable one, which it presses: false clicks=1",
			"and focus stays where it was: true",
			"neither unfocusable widget grabs focus: false false",
			"a stopped Escape goes no way back: heard=[Escape] backs=0",
			"an Escape left goes back: heard=[Escape] backs=1",
			"what the input method composes is its own: heard=[<^U>] text=é",
			"a test's keys: heard=[Enter Enter <y> Escape <x> <Tab>] stops=3 text=x in entry=false",
			"on a button: heard=[Enter Escape] clicks=3 backs=2",
		}, "\n")
		if !strings.Contains(string(out), want) {
			t.Fatalf("want the steps\n%s\ngot\n%s", want, out)
		}
		return
	}

	Init()
	var keys []string
	stopping := false
	heard := func() string {
		got := "[" + strings.Join(keys, " ") + "]"
		keys = nil
		return got
	}
	clicks, backs := 0, 0
	top := ToplevelNew()
	ToplevelOnKey(top, func(k string, ctrl, alt, shift, meta, editing bool, stop func()) {
		if ctrl {
			k = "^" + k
		}
		if editing {
			k = "<" + k + ">"
		}
		keys = append(keys, k)
		if stopping {
			stop()
		}
	})
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
	back := BackNew()
	BackOnPress(back, func() { backs++ })
	for _, w := range []Handle{entry, plain, keyless, keylessEntry, back} {
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
	press := func(keys ...uint32) {
		if err := kbd.Chord(keys...); err != nil {
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
			fmt.Printf("7 on a focused button: heard=%s clicks=%d\n", heard(), clicks)
			press(gtkprobe.KeyEnter)
		case 3:
			fmt.Printf("Enter on it is heard and presses it: heard=%s clicks=%d\n", heard(), clicks)
			stopping = true
			press(gtkprobe.KeyEnter)
		case 4:
			fmt.Printf("a stopped Enter presses nothing: heard=%s clicks=%d\n", heard(), clicks)
			gtkprobe.GrabFocus(unsafe.Pointer(entry))
			press(gtkprobe.KeyA)
		case 5:
			fmt.Printf("a stopped a types nothing: heard=%s text=%s\n", heard(), EditableGetText(entry))
			stopping = false
			press(gtkprobe.KeyA)
		case 6:
			fmt.Printf("an a left goes on into the entry: heard=%s text=%s\n", heard(), EditableGetText(entry))
			stopping = true
			press(gtkprobe.KeyTab)
		case 7:
			fmt.Printf("a stopped Tab moves no focus: heard=%s in entry=%v\n", heard(), in(entry))
			stopping = false
			note()
			press(gtkprobe.KeyTab)
		case 8, 9, 10:
			note()
			press(gtkprobe.KeyTab)
		case 11:
			note()
			heard()
			fmt.Printf("Tab left moves focus: reached=%s\n", strings.Join(reached, ","))
			gtkprobe.GrabFocus(unsafe.Pointer(plain))
			press(gtkprobe.KeyLeftCtrl, gtkprobe.KeyS)
		case 12:
			fmt.Printf("Control+S: heard=%s\n", heard())
			press(gtkprobe.KeyLeftCtrl, gtkprobe.KeyLeftShift, gtkprobe.KeyS)
		case 13:
			fmt.Printf("Control+Shift+S: heard=%s\n", heard())
			press(gtkprobe.KeyCapsLock)
			press(gtkprobe.KeyLeftCtrl, gtkprobe.KeyS)
			press(gtkprobe.KeyS)
			press(gtkprobe.KeyCapsLock)
		case 14:
			fmt.Printf("Control+S with Caps Lock on: heard=%s\n", heard())
			gtkprobe.GrabFocus(unsafe.Pointer(entry))
			gtkprobe.Click(unsafe.Pointer(plain))
			fmt.Printf("a click focuses the plain button: %v\n", in(plain))
			gtkprobe.Click(unsafe.Pointer(keyless))
			fmt.Printf("and not the unfocusable one, which it presses: %v clicks=%d\n", in(keyless), keylessClicks)
			fmt.Printf("and focus stays where it was: %v\n", in(plain))
			fmt.Printf("neither unfocusable widget grabs focus: %v %v\n",
				gtkprobe.GrabFocus(unsafe.Pointer(keyless)), gtkprobe.GrabFocus(unsafe.Pointer(keylessEntry)))
			stopping = true
			press(gtkprobe.KeyEsc)
		case 15:
			fmt.Printf("a stopped Escape goes no way back: heard=%s backs=%d\n", heard(), backs)
			stopping = false
			press(gtkprobe.KeyEsc)
		case 16:
			fmt.Printf("an Escape left goes back: heard=%s backs=%d\n", heard(), backs)
			gtkprobe.GrabFocus(unsafe.Pointer(entry))
			press(gtkprobe.KeyLeftCtrl, gtkprobe.KeyLeftShift, gtkprobe.KeyU)
		case 17:
			press(gtkprobe.KeyE)
		case 18:
			press(gtkprobe.Key9)
		case 19:
			press(gtkprobe.KeySpace)
		case 20:
			fmt.Printf("what the input method composes is its own: heard=%s text=%s\n", heard(), EditableGetText(entry))
			// A test's key, which names the press rather than pressing one:
			// the handler hears it as given, and a key it leaves goes on.
			stops := 0
			stop := func() { stops++ }
			EditableSetText(entry, "")
			ToplevelKey(top, "Enter", false, false, false, false, false, stop)
			stopping = true
			ToplevelKey(top, "Enter", false, false, false, false, false, stop)
			ToplevelKey(top, "y", false, false, false, false, true, stop)
			ToplevelKey(top, "Escape", false, false, false, false, false, stop)
			stopping = false
			ToplevelKey(top, "x", false, false, false, false, true, stop)
			ToplevelKey(top, "Tab", false, false, false, false, true, stop)
			fmt.Printf("a test's keys: heard=%s stops=%d text=%s in entry=%v\n", heard(), stops, EditableGetText(entry), in(entry))
			gtkprobe.GrabFocus(unsafe.Pointer(plain))
			ToplevelKey(top, "Enter", false, false, false, false, false, stop)
			ToplevelKey(top, "Escape", false, false, false, false, false, stop)
			fmt.Printf("on a button: heard=%s clicks=%d backs=%d\n", heard(), clicks, backs)
			sched.Cancel()
			kbd.Close()
			ToplevelRequestClose(top)
		}
	})
	RunWindows(true)
}
