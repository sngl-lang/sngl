package html

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// html's image source primitives, declared in html.sngl. Each is the `<img>`
// in the frame an image is, and the document's favicon is a `<link>` standing
// in for one, whose `src` is its `href` (faviconInit).
const (
	imageURLIntrinsic     = "html:imageUrl"
	imageDrawingIntrinsic = "html:imageDrawing"
	imageURLElement       = "ImageUrl"
	imageDrawingElement   = "ImageDrawing"
	imageTag              = "img"
	imageDrawHelper       = "_snglImageDraw"
)

// imageRules clip an image's frame and fill it with the picture it shows,
// fitted as the frame's class says. The frame carries the description, so
// the `<img>` has none of its own.
var imageRules = []string{
	".sngl-image { overflow: hidden; }",
	".sngl-image img { display: block; width: 100%; height: 100%; object-fit: contain; }",
	".sngl-image-cover img { object-fit: cover; }",
	".sngl-image-fill img { object-fit: fill; }",
	".sngl-image-none img { object-fit: none; }",
	".sngl-image-scaleDown img { object-fit: scale-down; }",
}

// namesImageFrame reports whether a class names an image's frame, whose
// rules the page needs whether or not a source is drawn in it: a frame
// showing only its placeholder was not clipped.
func namesImageFrame(class ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(class, func(e ir.Expr) error {
		if s, ok := codegen.IRLiteralString(e); ok && strings.HasPrefix(s, "sngl-image") {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

func isImageSource(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && ir.IsImageSourceFamily(n.Component.Family)
}

func isImageDrawing(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Intrinsic == imageDrawingIntrinsic
}

// elementTag is the tag a primitive names in place of its own name.
func elementTag(tag string) string {
	if tag == imageURLElement || tag == imageDrawingElement {
		return imageTag
	}
	return tag
}

// snglImageDrawHelper paints a drawing into a canvas of its own and hands the
// picture to the element showing it, as a data URL. The canvas is kept on the
// element, so a repaint reuses it.
const snglImageDrawHelper = `function _snglImageDraw(el,w,h,draw){
  const c=el._snglCanvas||(el._snglCanvas=document.createElement("canvas"));
  c.width=w;c.height=h;
  const ctx=c.getContext("2d");
  ctx.clearRect(0,0,w,h);
  draw(ctx);
  el.src=c.toDataURL();
}
`

// faviconInit makes the document's icon link answer a write of `src`, which
// is how every image source is written, with its `href`.
func faviconInit(id string) string {
	return `Object.defineProperty(` + id + `,"src",{set(v){this.href=v}});`
}

// splitFavicon is a document Window's children without the image source its
// override placed beside the content, and that source: the page's icon.
func splitFavicon(children []ir.Stmt) ([]ir.Stmt, *ir.NodeInst) {
	var icon *ir.NodeInst
	var rest []ir.Stmt
	for _, s := range children {
		if n, ok := s.(*ir.NodeInst); ok && isImageSource(n) {
			icon = n
			continue
		}
		rest = append(rest, s)
	}
	return rest, icon
}

// sourceAttrs is what an image source is written with: its URL as the `<img>`'s
// `src`, or as the icon `<link>`'s `href`. A drawing's size is the canvas it
// paints into and no attribute.
func sourceAttrs(props map[string]ir.Expr, favicon bool) map[string]ir.Expr {
	out := map[string]ir.Expr{}
	if favicon {
		out["rel"] = &ir.Literal{Type: ir.TypString, Value: "icon"}
		if url, ok := props[ir.ImageSourceURL]; ok {
			out["href"] = url
		}
		return out
	}
	if url, ok := props[ir.ImageSourceURL]; ok {
		out["src"] = url
	}
	out["alt"] = &ir.Literal{Type: ir.TypString, Value: ""}
	return out
}

// sourcePropAssign is a prop write to an image source this scope built: its
// URL is the `<img>`'s `src`, and a drawing's size is the canvas it paints
// into, which on the `<img>` would be the size it is shown at.
func (t *htmlTranslator) sourcePropAssign(node ir.Expr, prop string, value ir.Expr) ([]ir.Stmt, bool) {
	id, ok := node.(*ir.Ident)
	if !ok {
		return nil, false
	}
	tag := t.sources[id.Name]
	if tag == "" {
		if c := t.declOf(node); c != nil {
			switch c.Intrinsic {
			case imageURLIntrinsic:
				tag = imageURLElement
			case imageDrawingIntrinsic:
				tag = imageDrawingElement
			}
		}
	}
	switch {
	case tag == imageURLElement && prop == ir.ImageSourceURL:
		return []ir.Stmt{&ir.Assign{Target: &ir.Select{Operand: node, Field: "src", Type: ir.TypString}, Op: ast.AssignSet, Value: value}}, true
	case tag == imageDrawingElement && (prop == "width" || prop == "height"):
		return nil, true
	}
	return nil, false
}
