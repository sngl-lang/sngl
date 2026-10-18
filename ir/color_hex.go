package ir

// HexChannels parses #rgb, #rgba, #rrggbb, or #rrggbbaa to 0..255
// channels. The 3- and 4-digit short forms double each nibble (CSS-style, so
// #fff is #ffffff and #f00a is #ff0000aa). Alpha defaults to 255 for the
// 3- and 6-digit forms.
func HexChannels(raw string) (r, g, b, a int, ok bool) {
	if len(raw) == 0 || raw[0] != '#' {
		return 0, 0, 0, 0, false
	}
	hex := raw[1:]
	nibble := func(c byte) (int, bool) {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0'), true
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10, true
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10, true
		}
		return 0, false
	}
	byteOf := func(hi, lo byte) (int, bool) {
		h, ok1 := nibble(hi)
		l, ok2 := nibble(lo)
		if !(ok1 && ok2) {
			return 0, false
		}
		return h*16 + l, true
	}
	switch len(hex) {
	case 3:
		rv, ok1 := nibble(hex[0])
		gv, ok2 := nibble(hex[1])
		bv, ok3 := nibble(hex[2])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv*16 + rv, gv*16 + gv, bv*16 + bv, 255, true
	case 4:
		rv, ok1 := nibble(hex[0])
		gv, ok2 := nibble(hex[1])
		bv, ok3 := nibble(hex[2])
		av, ok4 := nibble(hex[3])
		if !(ok1 && ok2 && ok3 && ok4) {
			return 0, 0, 0, 0, false
		}
		return rv*16 + rv, gv*16 + gv, bv*16 + bv, av*16 + av, true
	case 6:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, 255, true
	case 8:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		av, ok4 := byteOf(hex[6], hex[7])
		if !(ok1 && ok2 && ok3 && ok4) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, av, true
	}
	return 0, 0, 0, 0, false
}
