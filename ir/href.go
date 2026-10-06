package ir

import "strings"

// A page's href names its params' fields as `{name}` path segments, filled
// from the params a link or a `go` hands the page. These are the one reading of
// that spelling the lowering and a target's code generator share.

// FillHref replaces each `{name}` segment of href with what fill answers for
// it, failing on the first it does not.
func FillHref(href string, fill func(name string) (string, bool)) (string, bool) {
	segs := strings.Split(href, "/")
	for i, seg := range segs {
		if name, ok := placeholder(seg); ok {
			v, ok := fill(name)
			if !ok {
				return "", false
			}
			segs[i] = v
		}
	}
	return strings.Join(segs, "/"), true
}

// HrefPlaceholders is each `{name}` segment of href, in order.
func HrefPlaceholders(href string) []string {
	var out []string
	for seg := range strings.SplitSeq(href, "/") {
		if name, ok := placeholder(seg); ok {
			out = append(out, name)
		}
	}
	return out
}

func placeholder(seg string) (string, bool) {
	if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
		return seg[1 : len(seg)-1], true
	}
	return "", false
}
