//go:build unix

// Package gtkprobe is what gtk4rt's tests reach GTK and the compositor
// through where gtk4rt has no call of its own: a test file cannot use cgo.
//
// Keyboard is a key pressed on the compositor's seat, the way a person
// presses one: a Wayland client of its own announces a virtual keyboard
// (zwp_virtual_keyboard_v1, which cage offers) and types on it, so the key
// reaches the focused window as any key does and GTK routes it from there.
package gtkprobe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Evdev codes of the keys a test presses, which the keymap below names.
const (
	KeyEsc       = 1
	Key7         = 8
	Key9         = 10
	KeyTab       = 15
	KeyE         = 18
	KeyU         = 22
	KeyEnter     = 28
	KeyLeftCtrl  = 29
	KeyA         = 30
	KeyS         = 31
	KeyLeftShift = 42
	KeySpace     = 57
	KeyCapsLock  = 58
)

// The keymap the keyboard announces: the evdev codes, laid out as a US
// keyboard, which the compositor and GTK compile from the host's xkb data.
const keymap = `xkb_keymap {
	xkb_keycodes { include "evdev+aliases(qwerty)" };
	xkb_types { include "complete" };
	xkb_compat { include "complete" };
	xkb_symbols { include "pc+us+inet(evdev)" };
};
`

// A Keyboard is a virtual keyboard on the compositor's seat.
type Keyboard struct {
	conn   *net.UnixConn
	nextID uint32
	kbd    uint32
	start  time.Time
	// locked is the locks on, which outlast a chord.
	locked uint32
}

const (
	displayID  = 1
	registryID = 2
)

// NewKeyboard connects to the compositor WAYLAND_DISPLAY names and makes a
// keyboard on its seat.
func NewKeyboard() (*Keyboard, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), name)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, err
	}
	k := &Keyboard{conn: c, nextID: registryID + 1, start: time.Now()}
	// wl_display.get_registry
	if err := k.send(displayID, 1, nil, u32(registryID)); err != nil {
		return nil, err
	}
	globals, err := k.roundtrip()
	if err != nil {
		return nil, err
	}
	seat, okSeat := globals["wl_seat"]
	mgr, okMgr := globals["zwp_virtual_keyboard_manager_v1"]
	if !okSeat || !okMgr {
		c.Close()
		return nil, errors.New("the compositor offers no seat or no virtual keyboard")
	}
	seatID, mgrID := k.bind(seat, "wl_seat", 1), k.bind(mgr, "zwp_virtual_keyboard_manager_v1", 1)
	k.kbd = k.id()
	// zwp_virtual_keyboard_manager_v1.create_virtual_keyboard(seat, id)
	if err := k.send(mgrID, 0, nil, u32(seatID), u32(k.kbd)); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "keymap")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.WriteString(keymap + "\x00"); err != nil {
		return nil, err
	}
	// zwp_virtual_keyboard_v1.keymap(format XKB_V1, fd, size)
	if err := k.send(k.kbd, 0, syscall.UnixRights(int(f.Fd())), u32(1), u32(uint32(len(keymap)+1))); err != nil {
		return nil, err
	}
	if _, err := k.roundtrip(); err != nil {
		return nil, err
	}
	return k, nil
}

// Press presses and releases the key with evdev code key.
func (k *Keyboard) Press(key uint32) error {
	return k.Chord(key)
}

// modMasks are the modifiers the keymap's modifier keys hold, as xkb's
// "complete" types number them, and lockMasks the ones a lock key toggles.
var (
	modMasks  = map[uint32]uint32{KeyLeftShift: 1 << 0, KeyLeftCtrl: 1 << 2}
	lockMasks = map[uint32]uint32{KeyCapsLock: 1 << 1}
)

// Chord presses the keys with evdev codes keys in order, holding each, and
// releases them in the reverse: a modifier first, then the key it changes. A
// virtual keyboard says which modifiers it holds itself, as a modifier key
// goes down and comes up, and which locks are on.
func (k *Keyboard) Chord(keys ...uint32) error {
	var mods uint32
	for i := range 2 * len(keys) {
		var key, state uint32
		if i < len(keys) {
			key, state = keys[i], 1
		} else {
			key, state = keys[2*len(keys)-1-i], 0
		}
		t := uint32(time.Since(k.start).Milliseconds())
		// zwp_virtual_keyboard_v1.key(time, key, state)
		if err := k.send(k.kbd, 1, nil, u32(t), u32(key), u32(state)); err != nil {
			return err
		}
		mask, isMod := modMasks[key]
		lock, isLock := lockMasks[key]
		if isMod {
			mods ^= mask
		}
		if isLock && state == 1 {
			k.locked ^= lock
		}
		if isMod || isLock {
			// zwp_virtual_keyboard_v1.modifiers(depressed, latched, locked, group)
			if err := k.send(k.kbd, 2, nil, u32(mods), u32(0), u32(k.locked), u32(0)); err != nil {
				return err
			}
		}
	}
	_, err := k.roundtrip()
	return err
}

// Close takes the keyboard off the seat.
func (k *Keyboard) Close() error {
	_ = k.send(k.kbd, 3, nil)
	return k.conn.Close()
}

func (k *Keyboard) id() uint32 {
	id := k.nextID
	k.nextID++
	return id
}

// bind is wl_registry.bind of the global named name, as a new object.
func (k *Keyboard) bind(name uint32, iface string, version uint32) uint32 {
	id := k.id()
	_ = k.send(registryID, 0, nil, u32(name), str(iface), u32(version), u32(id))
	return id
}

// roundtrip is wl_display.sync and the events read until it is done: the
// globals the registry announced among them, by interface, and a protocol
// error as the error.
func (k *Keyboard) roundtrip() (map[string]uint32, error) {
	cb := k.id()
	if err := k.send(displayID, 0, nil, u32(cb)); err != nil {
		return nil, err
	}
	globals := map[string]uint32{}
	k.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 8)
	for {
		if _, err := readFull(k.conn, header); err != nil {
			return nil, err
		}
		obj := binary.LittleEndian.Uint32(header)
		word := binary.LittleEndian.Uint32(header[4:])
		op, size := word&0xffff, word>>16
		body := make([]byte, size-8)
		if _, err := readFull(k.conn, body); err != nil {
			return nil, err
		}
		switch {
		case obj == cb && op == 0:
			return globals, nil
		case obj == displayID && op == 0:
			n := binary.LittleEndian.Uint32(body[8:])
			return nil, fmt.Errorf("wayland error on object %d, code %d: %s",
				binary.LittleEndian.Uint32(body), binary.LittleEndian.Uint32(body[4:]), body[12:12+n-1])
		case obj == registryID && op == 0:
			name := binary.LittleEndian.Uint32(body)
			n := binary.LittleEndian.Uint32(body[4:])
			globals[string(body[8:8+n-1])] = name
		}
	}
}

func readFull(c *net.UnixConn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func u32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func str(s string) []byte {
	b := u32(uint32(len(s) + 1))
	b = append(b, s...)
	b = append(b, 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func (k *Keyboard) send(obj, op uint32, oob []byte, args ...[]byte) error {
	size := 8
	for _, a := range args {
		size += len(a)
	}
	msg := u32(obj)
	msg = append(msg, u32(uint32(size)<<16|op)...)
	for _, a := range args {
		msg = append(msg, a...)
	}
	_, _, err := k.conn.WriteMsgUnix(msg, oob, nil)
	return err
}
