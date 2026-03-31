package android

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"golang.org/x/image/draw"
)

// mipmap densities for launcher icons.
var mipmapSizes = []struct {
	density string
	size    int
}{
	{"mdpi", 48},
	{"hdpi", 72},
	{"xhdpi", 96},
	{"xxhdpi", 144},
	{"xxxhdpi", 192},
}

// resourceFiles generates Android resource OutputFiles for icon and color.
func resourceFiles(cfg Config) ([]*codegen.OutputFile, error) {
	var files []*codegen.OutputFile

	// Always generate colors.xml when color is set
	if cfg.Color != "" {
		files = append(files, &codegen.OutputFile{
			Name: "res/values/colors.xml",
			Content: []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="ic_launcher_background">%s</color>
    <color name="theme_primary">%s</color>
</resources>
`, cfg.Color, cfg.Color)),
		})
	}

	if cfg.Icon == "" {
		return files, nil
	}

	// Resolve icon path: try as-is (CWD-relative) first, then project-dir-relative
	iconPath := cfg.Icon
	if !filepath.IsAbs(iconPath) {
		if _, err := os.Stat(iconPath); err != nil && cfg.ProjectDir != "" {
			iconPath = filepath.Join(cfg.ProjectDir, iconPath)
		}
	}

	iconData, err := os.ReadFile(iconPath)
	if err != nil {
		return nil, fmt.Errorf("reading icon %s: %w", cfg.Icon, err)
	}

	isSVG := strings.HasSuffix(strings.ToLower(cfg.Icon), ".svg")

	if isSVG {
		// Convert SVG → VectorDrawable XML for foreground
		vd, err := svgToVectorDrawable(iconData)
		if err != nil {
			return nil, fmt.Errorf("converting SVG to VectorDrawable: %w", err)
		}
		files = append(files, &codegen.OutputFile{
			Name:    "res/drawable/ic_launcher_foreground.xml",
			Content: vd,
		})

		// Also generate raster fallbacks for pre-API-26
		rasterFiles, err := svgToMipmapPNGs(iconData)
		if err != nil {
			// Non-fatal: adaptive icon will still work on API 26+
			fmt.Fprintf(os.Stderr, "sngl: warning: could not rasterize SVG for fallback icons: %v\n", err)
		} else {
			files = append(files, rasterFiles...)
		}
	} else {
		// PNG: resize to each density
		pngFiles, err := pngToMipmaps(iconData)
		if err != nil {
			return nil, fmt.Errorf("resizing icon PNG: %w", err)
		}
		files = append(files, pngFiles...)
	}

	// Adaptive icon XML (API 26+)
	bgColor := cfg.Color
	if bgColor == "" {
		bgColor = "#FFFFFF"
	}

	if isSVG {
		// Reference the vector drawable foreground
		files = append(files, &codegen.OutputFile{
			Name: "res/mipmap-anydpi-v26/ic_launcher.xml",
			Content: []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background"/>
    <foreground android:drawable="@drawable/ic_launcher_foreground"/>
</adaptive-icon>
`)),
		})
		files = append(files, &codegen.OutputFile{
			Name: "res/mipmap-anydpi-v26/ic_launcher_round.xml",
			Content: []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background"/>
    <foreground android:drawable="@drawable/ic_launcher_foreground"/>
</adaptive-icon>
`)),
		})
	}

	return files, nil
}

// pngToMipmaps resizes a PNG to each mipmap density.
func pngToMipmaps(pngData []byte) ([]*codegen.OutputFile, error) {
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}

	var files []*codegen.OutputFile
	for _, m := range mipmapSizes {
		resized := resizeImage(src, m.size, m.size)
		var buf bytes.Buffer
		if err := png.Encode(&buf, resized); err != nil {
			return nil, err
		}
		files = append(files, &codegen.OutputFile{
			Name:    fmt.Sprintf("res/mipmap-%s/ic_launcher.png", m.density),
			Content: buf.Bytes(),
		})
		// Round icon is the same for now
		files = append(files, &codegen.OutputFile{
			Name:    fmt.Sprintf("res/mipmap-%s/ic_launcher_round.png", m.density),
			Content: buf.Bytes(),
		})
	}
	return files, nil
}

// svgToMipmapPNGs rasterizes an SVG to PNG at each mipmap density.
// Uses rsvg-convert if available, otherwise returns an error.
func svgToMipmapPNGs(svgData []byte) ([]*codegen.OutputFile, error) {
	// Try rsvg-convert for high-quality SVG rasterization
	if _, err := exec.LookPath("rsvg-convert"); err == nil {
		return rasterizeSVGExternal(svgData)
	}
	// Fallback: no raster icons for pre-API-26
	return nil, fmt.Errorf("rsvg-convert not found; install librsvg for PNG fallback icons")
}

func rasterizeSVGExternal(svgData []byte) ([]*codegen.OutputFile, error) {
	var files []*codegen.OutputFile
	for _, m := range mipmapSizes {
		pngData, err := rsvgConvert(svgData, m.size, m.size)
		if err != nil {
			return nil, err
		}
		files = append(files, &codegen.OutputFile{
			Name:    fmt.Sprintf("res/mipmap-%s/ic_launcher.png", m.density),
			Content: pngData,
		})
		files = append(files, &codegen.OutputFile{
			Name:    fmt.Sprintf("res/mipmap-%s/ic_launcher_round.png", m.density),
			Content: pngData,
		})
	}
	return files, nil
}

func rsvgConvert(svgData []byte, width, height int) ([]byte, error) {
	cmd := exec.Command("rsvg-convert",
		"-w", fmt.Sprint(width),
		"-h", fmt.Sprint(height),
		"-f", "png",
	)
	cmd.Stdin = bytes.NewReader(svgData)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func resizeImage(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// SVG to VectorDrawable conversion

// svgToVectorDrawable converts a simple SVG to Android VectorDrawable XML.
// Supports <path>, <circle>, <rect>, <polygon>, <ellipse>, <line>, <g> elements.
func svgToVectorDrawable(svgData []byte) ([]byte, error) {
	var svg svgRoot
	if err := xml.Unmarshal(svgData, &svg); err != nil {
		return nil, fmt.Errorf("parsing SVG: %w", err)
	}

	viewBox := svg.ViewBox
	if viewBox == "" {
		w := svg.Width
		h := svg.Height
		if w == "" {
			w = "24"
		}
		if h == "" {
			h = "24"
		}
		viewBox = "0 0 " + stripUnit(w) + " " + stripUnit(h)
	}

	parts := strings.Fields(viewBox)
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid viewBox: %q", viewBox)
	}

	// Adaptive icons use a 108dp canvas where the inner 72dp (66%) is the
	// visible safe zone. We use a 108x108 viewport and wrap the SVG content
	// in a group that scales and centers it to fill the safe zone.
	var svgW, svgH float64
	fmt.Sscanf(parts[2], "%f", &svgW)
	fmt.Sscanf(parts[3], "%f", &svgH)

	const canvasSize = 108.0
	const safeZone = 72.0

	// Scale to fit the safe zone
	scale := safeZone / max(svgW, svgH)
	// Center within the canvas
	tx := (canvasSize - svgW*scale) / 2
	ty := (canvasSize - svgH*scale) / 2

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	fmt.Fprintf(&b, `<vector xmlns:android="http://schemas.android.com/apk/res/android"`+"\n")
	fmt.Fprintf(&b, `    android:width="108dp"`+"\n")
	fmt.Fprintf(&b, `    android:height="108dp"`+"\n")
	fmt.Fprintf(&b, `    android:viewportWidth="108"`+"\n")
	fmt.Fprintf(&b, `    android:viewportHeight="108">`+"\n")

	// Wrap in a group that scales and translates to fill the safe zone
	fmt.Fprintf(&b, `    <group android:translateX="%g" android:translateY="%g" android:scaleX="%g" android:scaleY="%g">`+"\n",
		tx, ty, scale, scale)
	convertSVGElements(&b, svg.Children, "        ")

	b.WriteString("    </group>\n")
	b.WriteString("</vector>\n")
	return []byte(b.String()), nil
}

func convertSVGElements(b *strings.Builder, elements []svgElement, indent string) {
	for _, el := range elements {
		switch el.XMLName.Local {
		case "path":
			d := el.D
			if d == "" {
				continue
			}
			fill := el.Fill
			if fill == "" || fill == "currentColor" {
				fill = "#000000"
			}
			if fill == "none" {
				// stroke-only path
				stroke := el.Stroke
				if stroke == "" {
					continue
				}
				fmt.Fprintf(b, `%s<path android:pathData="%s" android:strokeColor="%s" android:strokeWidth="%s" android:fillColor="#00000000"/>`+"\n",
					indent, d, stroke, defaultStrokeWidth(el.StrokeWidth))
			} else {
				fmt.Fprintf(b, `%s<path android:pathData="%s" android:fillColor="%s"/>`+"\n",
					indent, d, fill)
			}

		case "circle":
			cx, cy, r := el.Cx, el.Cy, el.R
			fill := el.Fill
			if fill == "" || fill == "currentColor" {
				fill = "#000000"
			}
			// Convert circle to path
			pathData := fmt.Sprintf("M%s,%s m-%s,0 a%s,%s 0 1,0 %s,0 a%s,%s 0 1,0 -%s,0",
				cx, cy, r, r, r, doubleStr(r), r, r, doubleStr(r))
			fmt.Fprintf(b, `%s<path android:pathData="%s" android:fillColor="%s"/>`+"\n",
				indent, pathData, fill)

		case "rect":
			x, y, w, h := el.X, el.Y, el.Width, el.Height
			fill := el.Fill
			if fill == "" || fill == "currentColor" {
				fill = "#000000"
			}
			pathData := fmt.Sprintf("M%s,%s h%s v%s h-%s Z", x, y, w, h, w)
			fmt.Fprintf(b, `%s<path android:pathData="%s" android:fillColor="%s"/>`+"\n",
				indent, pathData, fill)

		case "ellipse":
			cx, cy, rx, ry := el.Cx, el.Cy, el.Rx, el.Ry
			fill := el.Fill
			if fill == "" || fill == "currentColor" {
				fill = "#000000"
			}
			pathData := fmt.Sprintf("M%s,%s m-%s,0 a%s,%s 0 1,0 %s,0 a%s,%s 0 1,0 -%s,0",
				cx, cy, rx, rx, ry, doubleStr(rx), rx, ry, doubleStr(rx))
			fmt.Fprintf(b, `%s<path android:pathData="%s" android:fillColor="%s"/>`+"\n",
				indent, pathData, fill)

		case "line":
			fill := el.Stroke
			if fill == "" {
				fill = "#000000"
			}
			pathData := fmt.Sprintf("M%s,%s L%s,%s", el.X1, el.Y1, el.X2, el.Y2)
			fmt.Fprintf(b, `%s<path android:pathData="%s" android:strokeColor="%s" android:strokeWidth="%s" android:fillColor="#00000000"/>`+"\n",
				indent, pathData, fill, defaultStrokeWidth(el.StrokeWidth))

		case "polygon", "polyline":
			points := strings.TrimSpace(el.Points)
			if points == "" {
				continue
			}
			fill := el.Fill
			if fill == "" || fill == "currentColor" {
				fill = "#000000"
			}
			pairs := strings.Fields(points)
			if len(pairs) == 0 {
				continue
			}
			var pathParts []string
			for i, pair := range pairs {
				coords := strings.Split(pair, ",")
				if len(coords) != 2 {
					continue
				}
				if i == 0 {
					pathParts = append(pathParts, "M"+coords[0]+","+coords[1])
				} else {
					pathParts = append(pathParts, "L"+coords[0]+","+coords[1])
				}
			}
			if el.XMLName.Local == "polygon" {
				pathParts = append(pathParts, "Z")
			}
			fmt.Fprintf(b, `%s<path android:pathData="%s" android:fillColor="%s"/>`+"\n",
				indent, strings.Join(pathParts, " "), fill)

		case "g":
			// Group: recurse into children
			fmt.Fprintf(b, "%s<group>\n", indent)
			convertSVGElements(b, el.Children, indent+"    ")
			fmt.Fprintf(b, "%s</group>\n", indent)
		}
	}
}

// SVG XML types for parsing

type svgRoot struct {
	XMLName  xml.Name     `xml:"svg"`
	Width    string       `xml:"width,attr"`
	Height   string       `xml:"height,attr"`
	ViewBox  string       `xml:"viewBox,attr"`
	Children []svgElement `xml:",any"`
}

type svgElement struct {
	XMLName     xml.Name     `xml:""`
	D           string       `xml:"d,attr"`
	Fill        string       `xml:"fill,attr"`
	Stroke      string       `xml:"stroke,attr"`
	StrokeWidth string       `xml:"stroke-width,attr"`
	Cx          string       `xml:"cx,attr"`
	Cy          string       `xml:"cy,attr"`
	R           string       `xml:"r,attr"`
	Rx          string       `xml:"rx,attr"`
	Ry          string       `xml:"ry,attr"`
	X           string       `xml:"x,attr"`
	Y           string       `xml:"y,attr"`
	X1          string       `xml:"x1,attr"`
	Y1          string       `xml:"y1,attr"`
	X2          string       `xml:"x2,attr"`
	Y2          string       `xml:"y2,attr"`
	Width       string       `xml:"width,attr"`
	Height      string       `xml:"height,attr"`
	Points      string       `xml:"points,attr"`
	Children    []svgElement `xml:",any"`
}

func stripUnit(s string) string {
	s = strings.TrimSuffix(s, "px")
	s = strings.TrimSuffix(s, "pt")
	s = strings.TrimSuffix(s, "em")
	return s
}

func doubleStr(s string) string {
	// Multiply a numeric string by 2 (for circle path construction)
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return fmt.Sprintf("%g", f*2)
}

func defaultStrokeWidth(sw string) string {
	if sw == "" {
		return "1"
	}
	return sw
}

