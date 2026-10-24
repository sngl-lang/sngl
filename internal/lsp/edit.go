package lsp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/internal/lspcore"
	"duckfam.us/sngl/internal/parser"
)

func (s *Server) handleFormatting(id json.RawMessage, params json.RawMessage) {
	var p DocumentFormattingParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil {
		s.sendResult(id, []TextEdit{})
		return
	}
	doc, err := parser.Parse(uriToPath(fs.URI), []byte(fs.Content))
	if err != nil {
		s.sendError(id, -32603, "sngl fmt: the file does not parse")
		return
	}
	s.sendResult(id, lineEdit(fs.Content, parser.Format(doc)))
}

// lineEdit is the one edit that turns old into new, replacing only the lines
// between what they share at either end, which leaves an editor's cursor,
// marks and folds where they were.
func lineEdit(old, new string) []TextEdit {
	if old == new {
		return []TextEdit{}
	}
	a, b := strings.SplitAfter(old, "\n"), strings.SplitAfter(new, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	mid := a[pre : len(a)-suf]
	end := Position{Line: pre + len(mid)}
	if n := len(mid); n > 0 && !strings.HasSuffix(mid[n-1], "\n") {
		end = Position{Line: pre + n - 1, Character: lspcore.UTF16Column(mid[n-1], len([]rune(mid[n-1])))}
	}
	return []TextEdit{{
		Range:   Range{Start: Position{Line: pre}, End: end},
		NewText: strings.Join(b[pre:len(b)-suf], ""),
	}}
}

func (s *Server) handlePrepareRename(id json.RawMessage, params json.RawMessage) {
	var p TextDocumentPositionParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	r := s.refAt(fs, p.Position)
	if r == nil {
		s.sendResult(id, nil)
		return
	}
	if err := lspcore.CanRename(fs.Index, r); err != nil {
		s.sendError(id, -32603, "cannot rename: "+err.Error())
		return
	}
	loc, _ := s.location(r.Pos, r.Len)
	s.sendResult(id, loc.Range)
}

func (s *Server) handleRename(id json.RawMessage, params json.RawMessage) {
	var p RenameParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	r := s.refAt(fs, p.Position)
	if r == nil {
		s.sendError(id, -32603, "cannot rename: no name here")
		return
	}
	edits, err := lspcore.RenameEdits(fs.Index, r, p.NewName)
	if err != nil {
		s.sendError(id, -32603, "cannot rename: "+err.Error())
		return
	}
	if err := s.checkRename(fs, edits); err != nil {
		s.sendError(id, -32603, "cannot rename: "+err.Error())
		return
	}
	changes := map[string][]TextEdit{}
	for _, es := range edits {
		for _, e := range es {
			loc, ok := s.location(e.Pos, e.Len)
			if !ok {
				continue
			}
			changes[loc.URI] = append(changes[loc.URI], TextEdit{Range: loc.Range, NewText: e.Text})
		}
	}
	s.sendResult(id, WorkspaceEdit{Changes: changes})
}

// checkRename checks the package with edits made, and refuses them if that
// reports more errors than the package had: a name the new one collides
// with, or a use the index does not know. It counts rather than matching
// messages, since an error that was already there may name the old name.
func (s *Server) checkRename(fs *fileState, edits map[string][]lspcore.Edit) error {
	filename := uriToPath(fs.URI)
	over := map[string]string{}
	for file, es := range edits {
		text, ok := s.source(file)
		if !ok {
			return fmt.Errorf("cannot read %s", file)
		}
		over[file] = applyEdits(text, es)
	}
	content := fs.Content
	if text, ok := over[filename]; ok {
		content = text
	}
	before, _, _ := s.check(filename, fs.Content, nil)
	after, _, _ := s.check(filename, content, over)
	had := map[string]bool{}
	n := 0
	for _, d := range before.Diags {
		if d.Severity == lspcore.SeverityError {
			had[d.Message] = true
			n--
		}
	}
	first := ""
	for _, d := range after.Diags {
		if d.Severity == lspcore.SeverityError {
			n++
			if first == "" && !had[d.Message] {
				first = d.Message
			}
		}
	}
	if n > 0 {
		return fmt.Errorf("it would leave an error: %s", first)
	}
	return nil
}

// applyEdits makes edits, which do not overlap, in text.
func applyEdits(text string, edits []lspcore.Edit) string {
	edits = slices.Clone(edits)
	slices.SortFunc(edits, func(a, b lspcore.Edit) int {
		if a.Pos.Line != b.Pos.Line {
			return b.Pos.Line - a.Pos.Line
		}
		return b.Pos.Column - a.Pos.Column
	})
	lines := strings.Split(text, "\n")
	for _, e := range edits {
		if e.Pos.Line < 1 || e.Pos.Line > len(lines) {
			continue
		}
		l := []rune(lines[e.Pos.Line-1])
		start := e.Pos.Column - 1
		if start < 0 || start+e.Len > len(l) {
			continue
		}
		lines[e.Pos.Line-1] = string(l[:start]) + e.Text + string(l[start+e.Len:])
	}
	return strings.Join(lines, "\n")
}
