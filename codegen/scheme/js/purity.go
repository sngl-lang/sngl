package js

import "strings"

// pureMarkers are the annotations that make a js:// function foldable at build
// time. All are matched case-sensitively and only in a declaration's doc
// comment; each is written exactly as its own ecosystem writes it.
//
//   - @sngl-pure is ours. It is Go's //sngl:pure said the way a .ts/.d.ts file
//     says things: a JSDoc tag name cannot hold a colon, so the namespace
//     separator becomes a hyphen. It is the marker to reach for when opting a
//     function in deliberately, because it changes nothing about how any
//     bundler treats the code.
//   - @__NO_SIDE_EFFECTS__ is Rollup's and esbuild's, and #__NO_SIDE_EFFECTS__
//     is the same annotation's other accepted prefix.
//   - @nosideeffects is Closure Compiler's.
//   - @__PURE__ is defined as a *call-site* annotation — it says one call may
//     be dropped when its result is unused, and strictly speaking says nothing
//     about the declaration. It is accepted on a declaration anyway, because
//     someone who writes it there plainly means the function itself.
//
// The three ecosystem markers promise side-effect-free, which is weaker than
// the determinism folding needs; see the package doc on Purity for why that
// matters and what it costs.
var pureMarkers = map[string]bool{
	"@sngl-pure":           true,
	"@__NO_SIDE_EFFECTS__": true,
	"#__NO_SIDE_EFFECTS__": true,
	"@nosideeffects":       true,
	"@__PURE__":            true,
	"#__PURE__":            true,
}

// docComment returns the doc comment attached to the declaration whose full
// start is pos — the run of comments ending at the declaration with no blank
// line between them. A comment separated by a blank line documents whatever
// came before it, not this declaration, which is the same rule Go's doc
// comments and SNGL's own package comments follow. Without it a file-header
// comment would document, and could accidentally annotate, the first
// declaration in the file.
//
// A TypeScript node's Pos() is its full start, so the trivia between the
// previous declaration and this one sits at the front of its own extent.
func docComment(src string, pos int) string {
	if pos < 0 || pos > len(src) {
		return ""
	}
	var run []string
	newlines := 0
	for i := pos; i < len(src); {
		switch {
		case src[i] == '\n':
			newlines++
			if newlines > 1 {
				run = nil // a blank line ends the run
			}
			i++
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			run = append(run, src[i:i+end])
			i += end
			newlines = 0
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return strings.Join(append(run, src[i:]), "\n")
			}
			end += i + 4
			run = append(run, src[i:end])
			i = end
			newlines = 0
		default:
			return strings.Join(run, "\n")
		}
	}
	return strings.Join(run, "\n")
}

// isPureDoc reports whether a doc comment carries a purity marker.
//
// A marker counts only when it is the whole of a comment line, once the
// comment syntax around it is stripped — which is how every one of them is
// actually written, and what keeps a mention of one in prose ("not
// @__PURE__ in the Rollup sense") from silently making a function foldable.
func isPureDoc(doc string) bool {
	for line := range strings.SplitSeq(doc, "\n") {
		if pureMarkers[stripCommentSyntax(line)] {
			return true
		}
	}
	return false
}

// stripCommentSyntax reduces one line of a comment to its content: the
// delimiters that open and close it, and the `*` that leads a line inside a
// JSDoc block, are not part of what the line says.
func stripCommentSyntax(line string) string {
	line = strings.TrimSpace(line)
	for _, open := range []string{"/**", "/*", "//"} {
		if rest, ok := strings.CutPrefix(line, open); ok {
			line = rest
			break
		}
	}
	line = strings.TrimSuffix(line, "*/")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "*")
	return strings.TrimSpace(line)
}
