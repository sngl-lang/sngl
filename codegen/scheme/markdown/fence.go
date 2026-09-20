package markdown

import (
	"fmt"
	"strings"

	gast "github.com/yuin/goldmark/ast"
)

// fenceMode is what a fence's `mode=` trailer says about the scope its source
// lands in. The zero value is the default, which is why it is `view`.
type fenceMode int

const (
	// modeView is shown, highlighted and not compiled -- what every fence in
	// every other language already is. Never written, being the default.
	modeView fenceMode = iota
	// modeIsland is a component of its own, inserted where the fence was.
	modeIsland
	// modePackage is package-level declarations and renders nothing.
	modePackage
	// modeBody is source placed into the document's own component body, at
	// the position it was written.
	modeBody
)

var fenceModes = map[string]fenceMode{
	"view":    modeView,
	"island":  modeIsland,
	"package": modePackage,
	"body":    modeBody,
}

// The language a live fence has to be in. A mode says what to *compile* the
// source as, and this is the only language this compiler compiles.
const liveLanguage = "sngl"

// fenceModeOf reads the `mode=` trailer off a fence's info line.
//
// goldmark splits the info string at the first space for `Language()`, so the
// trailer costs the default path nothing: the fence still reports `sngl` and
// still highlights through the chroma path every other fence uses.
//
// A trailer this does not recognise is left alone rather than refused. The
// info line is shared vocabulary -- a doc site renderer reads `title=` and
// `linenos` off it -- and refusing one would turn a document that renders
// everywhere else into a build failure. The cost is that `mod=island` is
// silently `view`; `mode=islnd` is not, being a value this does name.
func (e *emitter) fenceModeOf(n *gast.FencedCodeBlock, language string) fenceMode {
	if n.Info == nil {
		return modeView
	}
	fields := strings.Fields(string(n.Info.Segment.Value(e.src)))
	for _, f := range fields {
		spelling, ok := strings.CutPrefix(f, "mode=")
		if !ok {
			continue
		}
		mode, known := fenceModes[spelling]
		if !known {
			e.fail("%s: unknown fence mode %q; write one of view, island, package or body", e.pos(n.Info.Segment.Start), spelling)
			return modeView
		}
		if mode != modeView && language != liveLanguage {
			e.fail("%s: mode=%s on a %q fence; only a %s fence is compiled, so a mode has nothing to say about any other", e.pos(n.Info.Segment.Start), spelling, language, liveLanguage)
			return modeView
		}
		return mode
	}
	return modeView
}

// pos names the markdown file and the line an offset falls on, since the
// position a checker would report is the `import` that read the document and
// says nothing about where in it the mistake was written.
func (e *emitter) pos(offset int) string {
	if offset > len(e.src) {
		offset = len(e.src)
	}
	line := e.doc.lineOffset + 1 + strings.Count(string(e.src[:offset]), "\n")
	return fmt.Sprintf("%s:%d", e.doc.name, line)
}
