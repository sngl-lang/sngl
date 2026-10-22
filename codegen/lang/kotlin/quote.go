package kotlin

import (
	"fmt"
	"strings"
	"unicode"
)

// Quote is s as a Kotlin string literal. Go's %q is not one: it writes `\f`,
// `\v`, `\a` and `\x0c`, none of which Kotlin accepts, and leaves `$`, which
// begins a template in a Kotlin string.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		default:
			if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) {
				if r > 0xffff {
					hi, lo := surrogates(r)
					fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
					continue
				}
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// surrogates is the UTF-16 pair r is written as.
func surrogates(r rune) (hi, lo rune) {
	r -= 0x10000
	return 0xd800 + (r>>10)&0x3ff, 0xdc00 + r&0x3ff
}
