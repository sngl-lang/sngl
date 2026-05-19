package lsp

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/none"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html"
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
	banner := []byte(fmt.Sprintf(`<div style="position:fixed;bottom:0;left:0;right:0;background:#fee;color:#900;padding:8px;font-family:monospace;border-top:2px solid #c00;z-index:9999">%s</div>`, htmlEscape(msg)))
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
// HTML for the named window. Returns an error if the window doesn't exist
// or codegen fails.
func renderDocAsHTML(pkg *ir.Package, windowName string) ([]byte, error) {
	if pkg == nil {
		return nil, fmt.Errorf("nil package")
	}
	var found *ir.Window
	for _, w := range pkg.Windows {
		if w.Name == windowName {
			found = w
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("window %q not found in package", windowName)
	}

	gen := &html.Generator{}
	req := &codegen.Request{
		Pkg:    pkg,
		Lang:   &none.Translator{},
		Source: "preview.sngl",
	}
	resp, err := gen.Generate(req)
	if err != nil {
		return nil, fmt.Errorf("html generate: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("html generate: %s", resp.Error)
	}

	// The html platform writes one .html file per window. Find the one
	// whose path matches the window name. Convention: <name>.html for
	// routes / index.html for the lone window.
	matchSuffix := windowName + ".html"
	var single []byte
	for _, f := range resp.Files {
		if strings.HasSuffix(f.Name, matchSuffix) || (len(resp.Files) == 1 && strings.HasSuffix(f.Name, ".html")) {
			var buf bytes.Buffer
			if _, err := f.WriteTo(&buf); err != nil {
				return nil, fmt.Errorf("html generate: read output: %w", err)
			}
			single = buf.Bytes()
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
