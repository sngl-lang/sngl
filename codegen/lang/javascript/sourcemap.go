package javascript

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
)

// jsPositionMarker matches an inline `/*@SNGL:file:line@*/` marker emitted
// by JsIRContext when EmitPositionMarkers is true. The translator places
// these at statement boundaries; renderJSSourceMap scans for them, builds
// position entries, and strips them from the body before final emission.
var jsPositionMarker = regexp.MustCompile(`/\*@SNGL:([^:@]+):(\d+)@\*/`)

// RenderInlineSourceMap is the exported entry point used by the html
// platform to extract inline SNGL markers from a JS body before passing it
// to esbuild. Builds the SNGL→JS source map and returns it as a
// SourceMapResult with the marker-stripped body in InlineBody and the
// sidecar JSON in Sidecar.
//
// mapDir is the directory the finished map will be read from.
func RenderInlineSourceMap(name, mapDir string, body []byte) codegen.SourceMapResult {
	return renderJSSourceMap(name, mapDir, nil, body)
}

// readSourcesContent returns the text of each source for the map's
// `sourcesContent`. An unreadable one is a null entry rather than an
// omission: the array is positional against sources, so dropping one would
// attribute its text to a different file. Under GOOS=js nothing is readable.
func readSourcesContent(sources []string) []*string {
	content := make([]*string, len(sources))
	any := false
	for i, src := range sources {
		b, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		s := string(b)
		content[i] = &s
		any = true
	}
	if !any {
		return nil
	}
	return content
}

// relativizeSources rewrites each source path to resolve from mapDir: a
// `sources` entry is resolved against the map's own location, not the
// directory the compiler ran in. Relative rather than absolute so golden
// output does not vary per machine; anything Rel cannot express is left
// alone.
func relativizeSources(sources []string, mapDir string) []string {
	if mapDir == "" {
		return sources
	}
	base, err := filepath.Abs(mapDir)
	if err != nil {
		return sources
	}
	out := make([]string, len(sources))
	for i, src := range sources {
		out[i] = src
		abs, err := filepath.Abs(src)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil {
			continue
		}
		// A source map is a URL space, not a filesystem path space.
		out[i] = filepath.ToSlash(rel)
	}
	return out
}

// extractMarkerPositions scans body for jsPositionMarker matches. For each
// match it records a PosEntry at the marker's start offset (so source-map
// segments point at the byte where the marked statement begins) and returns
// the marker-stripped body. Offsets in returned positions are relative to
// the stripped body, not the original.
func extractMarkerPositions(body []byte) ([]codegen.PosEntry, []byte) {
	matches := jsPositionMarker.FindAllSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return nil, body
	}
	var stripped bytes.Buffer
	stripped.Grow(len(body))
	var positions []codegen.PosEntry
	cursor := 0
	for _, m := range matches {
		// m[0]=marker start, m[1]=marker end, m[2..3]=file, m[4..5]=line.
		start, end := m[0], m[1]
		file := string(body[m[2]:m[3]])
		line, _ := strconv.Atoi(string(body[m[4]:m[5]]))
		// Append the gap before this marker.
		stripped.Write(body[cursor:start])
		// Position points at the byte where the marker's content WILL
		// resume after stripping.
		positions = append(positions, codegen.PosEntry{
			ByteOffset: stripped.Len(),
			Pos:        ast.Pos{File: file, Line: line},
		})
		cursor = end
		// Skip a trailing newline immediately after a marker so its line
		// doesn't become a blank line in the stripped body.
		if cursor < len(body) && body[cursor] == '\n' {
			cursor++
		}
	}
	stripped.Write(body[cursor:])
	return positions, stripped.Bytes()
}

// renderJSSourceMap produces a source-map v3 sidecar and appends a
// sourceMappingURL footer to the body.
//
// Mappings are organized one segment per output line. Each segment is four
// VLQ-encoded integers (deltas from the previous segment): generated column,
// source-file index, source line, source column. Name mappings are not
// emitted; the names array is empty.
//
// When the body contains inline `/*@SNGL:file:line@*/` markers (emitted by
// JsIRContext under EmitPositionMarkers), positions are derived from them
// and the markers are stripped from the InlineBody. Otherwise positions
// flow in via the function arg (legacy path; unused today).
func renderJSSourceMap(name, mapDir string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	if markerPositions, strippedBody := extractMarkerPositions(body); len(markerPositions) > 0 {
		positions = markerPositions
		body = strippedBody
	}
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
		Version        int       `json:"version"`
		File           string    `json:"file"`
		SourceRoot     string    `json:"sourceRoot,omitempty"`
		Sources        []string  `json:"sources"`
		SourcesContent []*string `json:"sourcesContent,omitempty"`
		Names          []string  `json:"names"`
		Mappings       string    `json:"mappings"`
	}{
		Version: 3,
		File:    name,
		// Reads the original paths, which are the ones that open here.
		SourcesContent: readSourcesContent(sources),
		Sources:        relativizeSources(sources, mapDir),
		Names:          []string{},
		Mappings:       mb.String(),
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
