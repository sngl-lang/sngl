package lsp

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	// The preview renders through these whatever else the binary links.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/html"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/ir"
)

// previewCache retains the last good HTML render per (URI, window) so a
// transient check or codegen failure doesn't blank the preview — the
// stale render is served with an error banner instead.
type previewCache struct {
	mu       sync.Mutex
	lastGood map[string][]byte // key: uri + "|" + window
}

func newPreviewCache() *previewCache {
	return &previewCache{lastGood: map[string][]byte{}}
}

func (c *previewCache) key(uri, window string) string { return uri + "|" + window }

func (c *previewCache) get(uri, window string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.lastGood[c.key(uri, window)]
	return b, ok
}

func (c *previewCache) set(uri, window string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastGood[c.key(uri, window)] = body
}

// appendErrorBanner injects a fixed-position banner showing the most
// recent check or codegen error, so the user knows the displayed render
// is stale.
func appendErrorBanner(stale []byte, msg string) []byte {
	banner := fmt.Appendf(nil, `<div style="position:fixed;bottom:0;left:0;right:0;background:#fee;color:#900;padding:8px;font-family:monospace;border-top:2px solid #c00;z-index:9999">%s</div>`, htmlEscape(msg))
	idx := strings.LastIndex(string(stale), "</body>")
	if idx < 0 {
		return append(stale, banner...)
	}
	out := make([]byte, 0, len(stale)+len(banner))
	out = append(out, stale[:idx]...)
	out = append(out, banner...)
	out = append(out, stale[idx:]...)
	return out
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// renderDocAsHTML compiles pkg through the html platform and returns the
// HTML for the named window: a node the package body renders with that `#id`.
// Returns an error if there is none or codegen fails.
func renderDocAsHTML(pkg *ir.Package, windowName string) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("nil package")
	}
	if !slices.ContainsFunc(pkg.Body, func(s ir.Stmt) bool {
		n, ok := s.(*ir.NodeInst)
		return ok && n.ID == windowName
	}) {
		return nil, fmt.Errorf("window %q not found in package", windowName)
	}

	results, err := build.Emit(pkg, build.Options{Name: "preview.sngl", Lang: "none", Platform: "html"})
	if err != nil {
		return nil, fmt.Errorf("html generate: %w", err)
	}

	// The html platform writes one .html file per window. Find the one
	// whose path matches the window name. Convention: <name>.html for
	// routes / index.html for the lone window.
	matchSuffix := windowName + ".html"
	files := results[0].Files
	var single []byte
	for name, content := range files {
		if strings.HasSuffix(name, matchSuffix) || (len(files) == 1 && strings.HasSuffix(name, ".html")) {
			single = content
			break
		}
	}
	if single == nil {
		return nil, fmt.Errorf("html generate: no output file matched window %q", windowName)
	}
	return injectLiveReloadScript(single), nil
}

// injectLiveReloadScript appends a small inline script that connects to
// /ws and reloads on a {"type":"reload"} message. Inserted before
// </body> if present, otherwise appended.
func injectLiveReloadScript(html []byte) []byte {
	const script = `<script>
(function(){
  try {
    var ws = new WebSocket("ws://" + location.host + "/ws");
    ws.onmessage = function(e){
      try { var m = JSON.parse(e.data); if (m && m.type === "reload") location.reload(); }
      catch (_) {}
    };
  } catch (_) {}
})();
</script>`
	idx := strings.LastIndex(string(html), "</body>")
	if idx < 0 {
		return append(html, []byte(script)...)
	}
	out := make([]byte, 0, len(html)+len(script))
	out = append(out, html[:idx]...)
	out = append(out, []byte(script)...)
	out = append(out, html[idx:]...)
	return out
}

// previewTarget is what the preview renders, and what it has to check
// against: a target selected at check time is what puts its overrides in
// place of the library bodies.
var previewTarget = []ir.StaticTarget{{Platform: "html", Language: "none"}}

func checkPreviewDoc(doc *ast.Document, dir string) (*ir.Package, error) {
	return build.Check(doc, build.CheckConfig{Dir: dir, IsMain: true, Targets: previewTarget})
}
