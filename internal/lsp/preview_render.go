package lsp

import (
	"bytes"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/none"
	"git.duckfam.us/jonathan/sngl/codegen/platform/html"
	"git.duckfam.us/jonathan/sngl/ir"
)

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
