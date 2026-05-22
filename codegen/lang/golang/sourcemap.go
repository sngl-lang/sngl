package golang

import (
	"bytes"
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// renderGoSourceMap splices //line directives into body at positions where
// the source line changes. Go's compiler reads //line to attribute compile
// errors and panics to the original source.
//
// A //line directive at column 1 of a line attributes the NEXT line to the
// given source. We emit them on their own line just before the byte offset
// they apply to.
func renderGoSourceMap(_ string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	if len(positions) == 0 {
		return codegen.SourceMapResult{}
	}

	var out bytes.Buffer
	out.Grow(len(body) + 64*len(positions))

	cursor := 0
	var lastFile string
	var lastLine int

	for _, p := range positions {
		if p.ByteOffset < cursor || p.ByteOffset > len(body) {
			continue
		}
		if !p.Pos.IsValid() {
			continue
		}
		if p.Pos.File == lastFile && p.Pos.Line == lastLine {
			continue
		}
		// Write body up to this position.
		out.Write(body[cursor:p.ByteOffset])
		// If the previous byte was not a newline, insert one so the
		// //line directive starts at column 1.
		if out.Len() > 0 {
			last := out.Bytes()[out.Len()-1]
			if last != '\n' {
				out.WriteByte('\n')
			}
		}
		fmt.Fprintf(&out, "//line %s:%d\n", p.Pos.File, p.Pos.Line)
		cursor = p.ByteOffset
		lastFile = p.Pos.File
		lastLine = p.Pos.Line
	}
	out.Write(body[cursor:])
	return codegen.SourceMapResult{InlineBody: out.Bytes()}
}
