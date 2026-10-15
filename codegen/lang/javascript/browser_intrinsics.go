package javascript

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// sngl:test/browser reads the document a test runs in. Each function takes the
// node a test reaches by its `#id`, which on html is the element carrying it
// as data-sngl-id, and one copy of a loop's is that element's index among
// every element carrying it -- the lowering refuses a reach that order cannot
// answer.
func init() {
	reg := func(id string, fn func(el string, rest []string) string) {
		codegen.RegisterIntrinsic(langJS, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
			rest := make([]string, len(args)-1)
			for i, a := range args[1:] {
				rest[i] = tr(a)
			}
			return fn(domElement(args[0], tr), rest), nil
		})
	}
	reg("browser.text", func(el string, _ []string) string {
		return fmt.Sprintf(`((el) => el.matches("input, textarea, select") ? el.value : el.innerText)(%s)`, el)
	})
	reg("browser.attr", func(el string, a []string) string {
		return fmt.Sprintf("(%s.getAttribute(%s) ?? \"\")", el, a[0])
	})
	reg("browser.style", func(el string, a []string) string {
		return fmt.Sprintf("getComputedStyle(%s).getPropertyValue(%s)", el, a[0])
	})
	codegen.RegisterIntrinsic(langJS, "browser.count", func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return fmt.Sprintf("document.querySelectorAll(%q).length", domSelector(args[0])), nil
	})
	reg("browser.markup", func(el string, _ []string) string {
		return el + `.outerHTML.replace(/>\s+/g, ">").replace(/\s+</g, "<")`
	})
	reg("browser.pixel", func(el string, a []string) string {
		return fmt.Sprintf(`((el, x, y) => {
	const src = el.matches("canvas, img") ? el : el.querySelector("canvas, img");
	if (src.tagName === "IMG" && src.naturalWidth === 0) return { r: 0, g: 0, b: 0, a: 0 };
	let c = src;
	if (src.tagName === "IMG") {
		c = document.createElement("canvas");
		c.width = src.naturalWidth;
		c.height = src.naturalHeight;
		c.getContext("2d").drawImage(src, 0, 0);
	}
	const d = c.getContext("2d").getImageData(x, y, 1, 1).data;
	return { r: d[0], g: d[1], b: d[2], a: d[3] };
})(%s, %s, %s)`, el, a[0], a[1])
	})
}

// domElement is the element a node expression names: `c.label`, or the i-th
// copy `c.row[i]`.
func domElement(e ir.Expr, tr func(ir.Expr) string) string {
	if cv, ok := e.(*ir.Conversion); ok {
		e = cv.Operand
	}
	index := "0"
	if ix, ok := e.(*ir.Index); ok {
		e, index = ix.Operand, tr(ix.Idx)
	}
	sel, ok := e.(*ir.Select)
	if !ok {
		return `(() => { throw new Error("sngl:test/browser: the node is not one a test reaches by its #id"); })()`
	}
	return fmt.Sprintf("document.querySelectorAll(%q)[%s]", domSelector(sel), index)
}

// domSelector is the selector matching every element rendered for the node's
// id.
func domSelector(e ir.Expr) string {
	if cv, ok := e.(*ir.Conversion); ok {
		e = cv.Operand
	}
	if ix, ok := e.(*ir.Index); ok {
		e = ix.Operand
	}
	sel, _ := e.(*ir.Select)
	if sel == nil {
		return "[data-sngl-id]:not(*)"
	}
	return fmt.Sprintf(`[data-sngl-id=%q]`, sel.Field)
}
