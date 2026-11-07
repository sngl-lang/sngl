package javascript

import "strings"

// WindowOwn is each name a browser's window holds as its own property, which
// a page's script cannot put a function or a const of its own on window by:
// `closed`, `top` and `frames` have only a getter, so assigning one throws,
// and `name` and `status` take the value as a string. A test module finds
// the page's names on window, so a page puts these on window.__snglOwn
// instead, and the module binds each it may name to what is there. An event
// handler property, `onerror`, takes a function as the window's handler:
// put on window, a program's `onerror` ran on the page's next error.
//
// Not sngl:platform/html's reservedNames, which renames every declaration of
// a name, a local and a parameter included: only what a test build puts on
// window meets these.
var WindowOwn = map[string]bool{
	"closed":               true,
	"crossOriginIsolated":  true,
	"crypto":               true,
	"customElements":       true,
	"devicePixelRatio":     true,
	"event":                true,
	"external":             true,
	"frameElement":         true,
	"frames":               true,
	"indexedDB":            true,
	"innerHeight":          true,
	"innerWidth":           true,
	"isSecureContext":      true,
	"length":               true,
	"localStorage":         true,
	"locationbar":          true,
	"menubar":              true,
	"name":                 true,
	"navigator":            true,
	"opener":               true,
	"origin":               true,
	"outerHeight":          true,
	"outerWidth":           true,
	"pageXOffset":          true,
	"pageYOffset":          true,
	"parent":               true,
	"performance":          true,
	"personalbar":          true,
	"screen":               true,
	"screenLeft":           true,
	"screenTop":            true,
	"screenX":              true,
	"screenY":              true,
	"scrollX":              true,
	"scrollY":              true,
	"scrollbars":           true,
	"self":                 true,
	"sessionStorage":       true,
	"status":               true,
	"statusbar":            true,
	"toolbar":              true,
	"top":                  true,
	"visualViewport":       true,
	"onabort":              true,
	"onafterprint":         true,
	"onanimationend":       true,
	"onbeforeprint":        true,
	"onbeforeunload":       true,
	"onblur":               true,
	"onchange":             true,
	"onclick":              true,
	"oncontextmenu":        true,
	"ondblclick":           true,
	"onerror":              true,
	"onfocus":              true,
	"onhashchange":         true,
	"oninput":              true,
	"onkeydown":            true,
	"onkeypress":           true,
	"onkeyup":              true,
	"onlanguagechange":     true,
	"onload":               true,
	"onmessage":            true,
	"onmessageerror":       true,
	"onmousedown":          true,
	"onmousemove":          true,
	"onmouseup":            true,
	"onoffline":            true,
	"ononline":             true,
	"onpagehide":           true,
	"onpageshow":           true,
	"onpointerdown":        true,
	"onpointermove":        true,
	"onpointerup":          true,
	"onpopstate":           true,
	"onrejectionhandled":   true,
	"onresize":             true,
	"onscroll":             true,
	"onstorage":            true,
	"onsubmit":             true,
	"ontouchend":           true,
	"ontouchmove":          true,
	"ontouchstart":         true,
	"onunhandledrejection": true,
	"onunload":             true,
	"onwheel":              true,
}

// ExposeLine is the statement a page's script puts names on window by for the
// test module, those WindowOwn holds on window.__snglOwn, or "" for none.
func ExposeLine(names []string) string {
	var plain, own []string
	for _, n := range names {
		if WindowOwn[n] {
			own = append(own, n)
		} else {
			plain = append(plain, n)
		}
	}
	var out string
	if len(plain) > 0 {
		out += "if (typeof window !== 'undefined') { Object.assign(window, { " + strings.Join(plain, ", ") + " }); }\n"
	}
	if len(own) > 0 {
		out += "if (typeof window !== 'undefined') { window.__snglOwn = Object.assign(window.__snglOwn ?? {}, { " + strings.Join(own, ", ") + " }); }\n"
	}
	return out
}
