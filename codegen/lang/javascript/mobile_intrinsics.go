package javascript

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// sngl:test/mobile does to the page what a finger does to a phone's browser.
// A swipe is a touch's pointer events across the node, dispatched on what in
// it hears a swipe -- the `sngl-swipe` a tabs' panels are in -- or on the
// element itself, from four fifths of its width to a fifth, at half its
// height.
func init() {
	codegen.RegisterIntrinsic(langJS, "mobile.swipe", func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		left := fmt.Sprintf(`String(%s) === "left"`, tr(args[1]))
		if member, ok := codegen.EnumMemberName(args[1]); ok {
			left = fmt.Sprint(member == "left")
		}
		// `void` so the call is never read as a call of the line before it.
		return fmt.Sprintf(`void ((node, left) => {
	const el = node.matches("sngl-swipe") ? node : node.querySelector("sngl-swipe") ?? node;
	const r = el.getBoundingClientRect();
	const y = r.top + r.height / 2, from = r.left + r.width * (left ? 0.8 : 0.2), to = r.left + r.width * (left ? 0.2 : 0.8);
	const at = (type, x) => el.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, composed: true, pointerId: 1, pointerType: "touch", isPrimary: true, clientX: x, clientY: y }));
	at("pointerdown", from);
	at("pointermove", (from + to) / 2);
	at("pointermove", to);
	at("pointerup", to);
})(%s, %s)`, domElement(args[0], tr), left), nil
	})
}
