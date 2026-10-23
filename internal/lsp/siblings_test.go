package lsp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	declaresGreet = "func greet(n int) => n + 1\n"
	usesGreet     = "func welcome() => greet(1)\n"
)

type siblingSession struct {
	t    *testing.T
	dir  string
	in   *io.PipeWriter
	out  chan json.RawMessage
	next int
}

func newSiblingSession(t *testing.T, onDisk map[string]string) *siblingSession {
	t.Helper()
	dir := t.TempDir()
	for name, src := range onDisk {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, in, r := testServer()
	s := &siblingSession{t: t, dir: dir, in: in, out: make(chan json.RawMessage, 64), next: 1}
	// The pipes are unbuffered: a server blocked writing a reply nobody reads
	// stops reading the next request, and the test blocks writing it.
	go func() {
		defer close(s.out)
		for {
			msg, err := readLSP(r)
			if err != nil {
				return
			}
			s.out <- msg
		}
	}()
	s.request("initialize", map[string]any{"processId": 1, "rootUri": "file://" + dir})
	t.Cleanup(func() {
		s.request("shutdown", nil)
		sendLSP(in, map[string]any{"jsonrpc": "2.0", "method": "exit"})
		in.Close()
	})
	return s
}

func (s *siblingSession) uri(name string) string { return "file://" + filepath.Join(s.dir, name) }

func (s *siblingSession) request(method string, params any) {
	sendLSP(s.in, map[string]any{"jsonrpc": "2.0", "id": s.next, "method": method, "params": params})
	s.next++
}

func (s *siblingSession) open(name, text string) {
	sendLSP(s.in, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": s.uri(name), "languageId": "sngl", "version": 1, "text": text},
		},
	})
}

func (s *siblingSession) change(name, text string) {
	sendLSP(s.in, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didChange",
		"params": map[string]any{
			"textDocument":   map[string]any{"uri": s.uri(name), "version": 2},
			"contentChanges": []map[string]any{{"text": text}},
		},
	})
}

// diagnostics reads until the server publishes diagnostics for name, skipping
// whatever else it sends first.
func (s *siblingSession) diagnostics(name string) []Diagnostic {
	s.t.Helper()
	for {
		var raw json.RawMessage
		select {
		case msg, ok := <-s.out:
			if !ok {
				s.t.Fatalf("server closed before publishing diagnostics on %s", name)
			}
			raw = msg
		case <-time.After(time.Minute):
			s.t.Fatalf("no diagnostics on %s", name)
		}
		var notif struct {
			Method string                   `json:"method"`
			Params PublishDiagnosticsParams `json:"params"`
		}
		if json.Unmarshal(raw, &notif) == nil && notif.Method == "textDocument/publishDiagnostics" && notif.Params.URI == s.uri(name) {
			return notif.Params.Diagnostics
		}
	}
}

func messages(diags []Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString("\n  ")
		b.WriteString(d.Message)
	}
	return b.String()
}

func TestDiagnosticsSeeSiblingOnDisk(t *testing.T) {
	s := newSiblingSession(t, map[string]string{"lib.sngl": declaresGreet, "main.sngl": usesGreet})
	s.open("main.sngl", usesGreet)
	if diags := s.diagnostics("main.sngl"); len(diags) != 0 {
		t.Errorf("main.sngl uses what lib.sngl declares, got:%s", messages(diags))
	}
}

func TestDiagnosticsSeeOpenSiblingBuffer(t *testing.T) {
	s := newSiblingSession(t, map[string]string{"lib.sngl": "", "main.sngl": usesGreet})
	s.open("lib.sngl", declaresGreet)
	s.diagnostics("lib.sngl")
	s.open("main.sngl", usesGreet)
	if diags := s.diagnostics("main.sngl"); len(diags) != 0 {
		t.Errorf("main.sngl uses what lib.sngl's unsaved buffer declares, got:%s", messages(diags))
	}

	s.change("lib.sngl", "")
	s.diagnostics("lib.sngl")
	if diags := s.diagnostics("main.sngl"); len(diags) == 0 {
		t.Error("main.sngl was not rechecked when lib.sngl stopped declaring greet")
	}
}

func TestDiagnosticsNameUnparseableSibling(t *testing.T) {
	s := newSiblingSession(t, map[string]string{"lib.sngl": "func greet(\n", "main.sngl": usesGreet})
	s.open("main.sngl", usesGreet)
	diags := s.diagnostics("main.sngl")
	if !strings.Contains(messages(diags), "lib.sngl does not parse") {
		t.Errorf("want a diagnostic naming lib.sngl, got:%s", messages(diags))
	}
}

func TestPreviewCheckSeesSiblings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.sngl"), []byte(declaresGreet), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New()
	fs := srv.ws.open("file://"+filepath.Join(dir, "main.sngl"), usesGreet, 1)
	srv.analyze(fs)
	before := len(fs.Doc.Stmts)
	if _, err := srv.checkForPreview(fs); err != nil {
		t.Errorf("preview check without lib.sngl's declarations: %v", err)
	}
	if len(fs.Doc.Stmts) != before {
		t.Error("the preview check wrote siblings into the open file's document")
	}
}

func TestDiagnosticsLeaveSiblingProblemsToTheSibling(t *testing.T) {
	broken := "func greet(n int) => n + 1\nfunc other() => missing\n"
	s := newSiblingSession(t, map[string]string{"lib.sngl": broken, "main.sngl": usesGreet})
	s.open("main.sngl", usesGreet)
	if diags := s.diagnostics("main.sngl"); len(diags) != 0 {
		t.Errorf("main.sngl reported lib.sngl's problems:%s", messages(diags))
	}
}

func TestSemanticTokensStayInTheirFile(t *testing.T) {
	dir := t.TempDir()
	lib := "func greet(n int) => n + 1\nfunc twice(n int) => greet(greet(n))\n"
	if err := os.WriteFile(filepath.Join(dir, "lib.sngl"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	main := "// a comment as long as the lines of lib.sngl are\nfunc welcome() => twice(1)\n"
	srv := New()
	fs := srv.ws.open("file://"+filepath.Join(dir, "main.sngl"), main, 1)
	srv.analyze(fs)
	pkg, err := srv.checkForPreview(fs)
	if err != nil {
		t.Fatal(err)
	}
	data := computeSemanticTokens(main, fs.Doc, pkg)
	if len(data) == 0 {
		t.Fatal("no tokens")
	}
	if data[0] == 0 {
		t.Errorf("a token on the comment line, from lib.sngl's code: %v", data[:5])
	}
}
