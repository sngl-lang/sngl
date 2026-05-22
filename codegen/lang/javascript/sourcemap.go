package javascript

import (
	"bytes"
	"encoding/json"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// renderJSSourceMap produces a source-map v3 sidecar and appends a
// sourceMappingURL footer to the body.
//
// Mappings are organized one segment per output line. Each segment is four
// VLQ-encoded integers (deltas from the previous segment): generated column,
// source-file index, source line, source column. Name mappings are not
// emitted; the names array is empty.
//
// We emit at most one mapping per generated line — the first PosEntry whose
// byte offset falls in that line. Finer-grained column maps would require
// per-token marking, which we don't have today.
func renderJSSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	if len(positions) == 0 {
		return codegen.SourceMapResult{}
	}

	// Collect unique source files (preserve insertion order).
	srcIdx := map[string]int{}
	var sources []string
	for _, p := range positions {
		if _, ok := srcIdx[p.Pos.File]; !ok {
			srcIdx[p.Pos.File] = len(sources)
			sources = append(sources, p.Pos.File)
		}
	}

	lineStart := computeLineStarts(body)
	type seg struct {
		genCol  int
		srcIdx  int
		srcLine int // 0-based
		srcCol  int // 0-based
	}
	perLine := make([]*seg, len(lineStart))
	for _, p := range positions {
		if !p.Pos.IsValid() {
			continue
		}
		gl, gc := genLineCol(lineStart, p.ByteOffset)
		if gl < 0 || gl >= len(perLine) {
			continue
		}
		if perLine[gl] != nil {
			continue
		}
		perLine[gl] = &seg{
			genCol:  gc,
			srcIdx:  srcIdx[p.Pos.File],
			srcLine: p.Pos.Line - 1,
			srcCol:  max0(p.Pos.Column - 1),
		}
	}

	var mb strings.Builder
	var prevGenCol, prevSrcIdx, prevSrcLine, prevSrcCol int
	for i, s := range perLine {
		if i > 0 {
			mb.WriteByte(';')
			prevGenCol = 0 // generated column resets per line
		}
		if s == nil {
			continue
		}
		mb.WriteString(encodeVLQ(s.genCol - prevGenCol))
		mb.WriteString(encodeVLQ(s.srcIdx - prevSrcIdx))
		mb.WriteString(encodeVLQ(s.srcLine - prevSrcLine))
		mb.WriteString(encodeVLQ(s.srcCol - prevSrcCol))
		prevGenCol = s.genCol
		prevSrcIdx = s.srcIdx
		prevSrcLine = s.srcLine
		prevSrcCol = s.srcCol
	}

	doc := struct {
		Version    int      `json:"version"`
		File       string   `json:"file"`
		SourceRoot string   `json:"sourceRoot,omitempty"`
		Sources    []string `json:"sources"`
		Names      []string `json:"names"`
		Mappings   string   `json:"mappings"`
	}{
		Version:  3,
		File:     name,
		Sources:  sources,
		Names:    []string{},
		Mappings: mb.String(),
	}
	sidecar, _ := json.Marshal(doc)

	var inline bytes.Buffer
	inline.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		inline.WriteByte('\n')
	}
	inline.WriteString("//# sourceMappingURL=")
	inline.WriteString(name)
	inline.WriteString(".map\n")

	return codegen.SourceMapResult{
		InlineBody:  inline.Bytes(),
		Sidecar:     sidecar,
		SidecarName: name + ".map",
	}
}

func computeLineStarts(body []byte) []int {
	starts := []int{0}
	for i, b := range body {
		if b == '\n' && i+1 < len(body) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func genLineCol(lineStart []int, off int) (line, col int) {
	lo, hi := 0, len(lineStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStart[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, off - lineStart[lo]
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}
