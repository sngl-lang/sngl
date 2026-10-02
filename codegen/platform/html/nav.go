package html

import (
	"net/url"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// sngl:ui/nav on html, which declares `navigation` and asks for
// passNavigationHrefs: a page is a document of its own on a static site and a
// route in route mode, so what is left of navigation by the time a script is
// written is a browser following an address.
//
// A stack and its pages are this platform's Stack and Page primitives, and
// optimize.Documents writes one document per page, the window with the stack
// standing for that page alone, `pages.current` folded to its record. A
// nav.link is a ui.link by then, and so is a clickable that only goes to a
// page. What a handler still calls:
//
//	pages.go(pkg, Pkg{name=x})  →  location.assign("/p/" + encodeURIComponent(String(x)))
//	pages.back()                →  history.back()
//
// `back` is the browser's history, so at its bottom it leaves the site rather
// than doing nothing, which is what a browser's own back does. `pages.current`
// is folded per document wherever the fold reaches, and read from the record
// the document publishes where it does not.

func init() {
	codegen.RegisterPlatformIntrinsic("html", "nav.go", emitNavGo)
	codegen.RegisterPlatformIntrinsic("html", "nav.back", emitNavBack)
	codegen.RegisterPlatformIntrinsic("html", "nav.href", emitNavHref)
	codegen.RegisterPlatformIntrinsic("html", "nav.current", emitNavCurrent)
}

// navPageGlobal is where a document publishes the record of the page it was
// written for.
const navPageGlobal = "__sngl_page"

// navPageMarker holds the place of the page's record in a script until the
// rest is written: a document that reads `pages.current` nowhere at run time
// publishes nothing.
const navPageMarker = "\x00sngl:page\x00"

// emitNavCurrent is `pages.current` read where no document's fold reached:
// the page the document was written for, which each publishes. A window holds
// one stack, so the handle naming it is not needed to say which.
func emitNavCurrent(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	return "globalThis." + navPageGlobal, nil
}

// emitNavGo is `go(stack, to, params)`: passNavigationHrefs has written the
// page's own params where the call passed none.
func emitNavGo(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) < 3 {
		return "", nil
	}
	href := navAddressJS(args[1], args[2], tr)
	if href == "" {
		return "", nil
	}
	return "location.assign(" + href + ")", nil
}

func emitNavBack(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	return "history.back()", nil
}

// emitNavHref is `_href(to, params)`, the address a link whose params are not
// known until it is shown follows.
func emitNavHref(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) < 2 {
		return "", nil
	}
	return navAddressJS(args[0], args[1], tr), nil
}

// navAddressJS is the JavaScript for where the page whose record is to is
// reached with params: a string literal where the params are literals, and
// otherwise the href's segments joined around each field, escaped as a path
// segment, with the params evaluated once.
func navAddressJS(to, params ir.Expr, tr func(ir.Expr) string) string {
	href, ok := recordHref(to)
	if !ok {
		// A copy of a page under a `for` whose href the document did not
		// fold: it has no placeholders (it takes no params), so the address
		// is the href as the program computes it.
		if e := recordField(to, "href"); e != nil {
			return tr(e)
		}
		return ""
	}
	if len(ir.HrefPlaceholders(href)) == 0 {
		return strconv.Quote(href)
	}
	if lit, ok := params.(*ir.StructLit); ok {
		values := map[string]string{}
		for _, f := range lit.Fields {
			if l, ok := f.Value.(*ir.Literal); ok {
				values[f.Name] = l.Value
			}
		}
		if s, ok := ir.FillHref(href, func(name string) (string, bool) {
			v, ok := values[name]
			return url.PathEscape(v), ok
		}); ok {
			return strconv.Quote(s)
		}
	}
	var parts []string
	var lit strings.Builder
	for i, seg := range strings.Split(href, "/") {
		if i > 0 {
			lit.WriteByte('/')
		}
		if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			if lit.Len() > 0 {
				parts = append(parts, strconv.Quote(lit.String()))
				lit.Reset()
			}
			parts = append(parts, "encodeURIComponent(String(__p."+seg[1:len(seg)-1]+"))")
			continue
		}
		lit.WriteString(seg)
	}
	if lit.Len() > 0 {
		parts = append(parts, strconv.Quote(lit.String()))
	}
	return "((__p) => " + strings.Join(parts, " + ") + ")(" + tr(params) + ")"
}

// recordHref is the href a page's record holds, where it is a literal.
func recordHref(e ir.Expr) (string, bool) {
	if v := recordField(e, "href"); v != nil {
		return codegen.IRLiteralString(v)
	}
	return "", false
}

// recordField is the value a page's record literal holds for name.
func recordField(e ir.Expr, name string) ir.Expr {
	lit, ok := e.(*ir.StructLit)
	if !ok {
		return nil
	}
	for _, f := range lit.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}

// pageHref is the href of the page a document is written for, read off the
// record its primitive carries.
func pageHref(page *ir.NodeInst) ir.Expr {
	lit, ok := page.Record.(*ir.StructLit)
	if !ok {
		return nil
	}
	for _, f := range lit.Fields {
		if f.Name == "href" {
			return f.Value
		}
	}
	return nil
}
