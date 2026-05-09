package html

import "strings"

// pathFromHref converts a window's folded href (an absolute URL path) into
// the relative filesystem path under the output directory.
//
//	"/"                  → "index.html"
//	"/index.html"        → "index.html"
//	"/foo.html"          → "foo.html"
//	"/foo/"              → "foo/index.html"
//	"/foo"               → "foo/index.html" (no extension ⇒ directory)
//	"/foo/bar.html"      → "foo/bar.html"
func pathFromHref(href string) string {
	href = strings.TrimPrefix(href, "/")
	if href == "" || href == "index.html" {
		return "index.html"
	}
	if strings.HasSuffix(href, "/") {
		return href + "index.html"
	}
	last := href
	if i := strings.LastIndex(href, "/"); i >= 0 {
		last = href[i+1:]
	}
	if !strings.Contains(last, ".") {
		return href + "/index.html"
	}
	return href
}
