//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdlib.h>

extern void snglRtDispatch(int idx);
extern void snglToplevelGone(gpointer data, GObject *where);

static void sngl_img_pressed(GtkGestureClick *g, int n, double x, double y, gpointer data) {
    (void)g; (void)n; (void)x; (void)y;
    snglRtDispatch(GPOINTER_TO_INT(data));
}
static void sngl_img_connect_click(GtkWidget *w, int idx) {
    GtkGesture *g = gtk_gesture_click_new();
    g_signal_connect(g, "pressed", G_CALLBACK(sngl_img_pressed), GINT_TO_POINTER(idx));
    gtk_widget_add_controller(w, GTK_EVENT_CONTROLLER(g));
}
static void sngl_img_watch(GObject *o, int idx) {
    g_object_weak_ref(o, snglToplevelGone, GINT_TO_POINTER(idx));
}
static void sngl_img_notify(GObject *o, GParamSpec *ps, gpointer data) {
    (void)o; (void)ps;
    snglRtDispatch(GPOINTER_TO_INT(data));
}
static void sngl_img_watch_parent(GObject *o, int idx) {
    g_signal_connect(o, "notify::parent", G_CALLBACK(sngl_img_notify), GINT_TO_POINTER(idx));
}
// GdkPixbuf rather than gdk_texture_new_from_bytes: its loaders read SVG as
// well as PNG and JPEG.
static GdkTexture *sngl_img_texture(const guchar *data, gsize len) {
    GdkPixbufLoader *l = gdk_pixbuf_loader_new();
    gboolean ok = gdk_pixbuf_loader_write(l, data, len, NULL);
    ok = gdk_pixbuf_loader_close(l, NULL) && ok;
    GdkPixbuf *pb = ok ? gdk_pixbuf_loader_get_pixbuf(l) : NULL;
    GdkTexture *t = NULL;
    if (pb) {
        GBytes *px = gdk_pixbuf_read_pixel_bytes(pb);
        t = gdk_memory_texture_new(gdk_pixbuf_get_width(pb), gdk_pixbuf_get_height(pb),
            gdk_pixbuf_get_has_alpha(pb) ? GDK_MEMORY_R8G8B8A8 : GDK_MEMORY_R8G8B8,
            px, gdk_pixbuf_get_rowstride(pb));
        g_bytes_unref(px);
    }
    g_object_unref(l);
    return t;
}
// A picture decoded in Go, as non-premultiplied RGBA.
static GdkTexture *sngl_img_rgba_texture(const guchar *px, int w, int h, int stride) {
    GBytes *b = g_bytes_new(px, (gsize)stride * h);
    GdkTexture *t = gdk_memory_texture_new(w, h, GDK_MEMORY_R8G8B8A8, b, stride);
    g_bytes_unref(b);
    return t;
}
// GDK_MEMORY_DEFAULT is cairo's ARGB32, so the surface's pixels are the
// texture's as they stand.
static GdkTexture *sngl_img_surface_texture(cairo_surface_t *s, int w, int h) {
    int stride = cairo_image_surface_get_stride(s);
    GBytes *px = g_bytes_new(cairo_image_surface_get_data(s), (gsize)stride * h);
    GdkTexture *t = gdk_memory_texture_new(w, h, GDK_MEMORY_DEFAULT, px, stride);
    g_bytes_unref(px);
    return t;
}
// The texture's pixels are cairo's ARGB32 once downloaded as GDK_MEMORY_DEFAULT.
static cairo_surface_t *sngl_img_surface(GdkTexture *tex) {
    int tw = gdk_texture_get_width(tex), th = gdk_texture_get_height(tex);
    if (tw <= 0 || th <= 0) return NULL;
    cairo_surface_t *s = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, tw, th);
    gdk_texture_download(tex, cairo_image_surface_get_data(s), cairo_image_surface_get_stride(s));
    cairo_surface_mark_dirty(s);
    return s;
}
static cairo_surface_t *sngl_img_scaled(cairo_surface_t *src, int w, int h) {
    int tw = cairo_image_surface_get_width(src), th = cairo_image_surface_get_height(src);
    cairo_surface_t *s = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, w, h);
    cairo_t *cr = cairo_create(s);
    cairo_scale(cr, (double)w / tw, (double)h / th);
    cairo_set_source_surface(cr, src, 0, 0);
    cairo_paint(cr);
    cairo_destroy(cr);
    return s;
}
static void sngl_img_paint(cairo_t *cr, cairo_surface_t *s, double x, double y) {
    cairo_save(cr);
    cairo_set_source_surface(cr, s, x, y);
    cairo_paint(cr);
    cairo_restore(cr);
}
static void sngl_img_add_search_path(const char *dir) {
    gtk_icon_theme_add_search_path(gtk_icon_theme_get_for_display(gdk_display_get_default()), dir);
}
*/
import "C"

import (
	"errors"
	"fmt"
	goimage "image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"unsafe"

	"duckfam.us/sngl/pkg/go/cbind"
	"duckfam.us/sngl/pkg/go/snglimage"
)

// An image is what a `ui.image` is on gtk4: a box holding the source it shows,
// fitted as the image says, or the placeholder where it holds none. The box is
// the handle, so a source and a render slot inside it are ordinary box
// operations, and BoxAppend fits a source to the image it lands in.
type image struct {
	box     Handle
	fit     C.GtkContentFit
	onClick func()
	onLoad  func()
	onError func(message string)
}

// A source is a GtkPicture showing the texture its picture or its drawing
// make, and reporting to the image holding it whether the picture arrived.
type source struct {
	pic     Handle
	picture *Picture
	ready   bool
	err     error
	tex     *C.GdkTexture
	frame   *image
	told    bool
	// onChange is a window's, when the source is its icon.
	onChange func()
}

// A Picture is what a URL names, decoded once: the program's `ui.file` and
// `ui.remote` on gtk4 derive one from the URL and hold it as long as the URL
// stands (gtk4.sngl's `Picture`, an owned value), so a source shows it and a
// drawing paints it without fetching or decoding again. A carried file is
// there at once; a fetched one arrives later, and the sources showing it are
// told. Painting into a box keeps the surface scaled to that box, made again
// only when the box changes size.
type Picture struct {
	url     string
	arrived bool
	err     error
	tex     *C.GdkTexture
	// surface is the texture's pixels for cairo, and scaled those at the
	// last size a drawing painted them.
	surface *C.cairo_surface_t
	scaled  *C.cairo_surface_t
	sw, sh  C.int
	// watching is each source showing the picture, told when it arrives.
	watching map[*source]bool
	released bool
}

var (
	images  = map[Handle]*image{}
	sources = map[Handle]*source{}
)

func ImageFrameNew() Handle {
	box := BoxNew(OrientationHorizontal, 0)
	img := &image{box: box, fit: C.GTK_CONTENT_FIT_CONTAIN}
	// Horizontal: a GtkPicture measures its height for the width it is given,
	// and a vertical box gives each child its own whole width, so the picture
	// asked for as much height as that width implies. A horizontal box measures
	// it at its own width.
	images[box] = img
	click := cbind.Register(func() {
		if img.onClick != nil {
			img.onClick()
		}
	})
	C.sngl_img_connect_click(widget(box), C.int(click))
	gone := cbind.Register(func() {
		delete(images, box)
		cbind.Release(click)
	})
	C.sngl_img_watch((*C.GObject)(p(box)), C.int(gone))
	return box
}

// ImageFrameSetFit is how the picture is scaled into the box, as `ui.ObjectFit`
// spells it. `none` is `scaleDown`: GTK has no picture that keeps its own size
// whatever room it is given.
func ImageFrameSetFit(h Handle, fit string) {
	img := images[h]
	if img == nil {
		return
	}
	switch fit {
	case "cover":
		img.fit = C.GTK_CONTENT_FIT_COVER
	case "fill":
		img.fit = C.GTK_CONTENT_FIT_FILL
	case "none", "scaleDown":
		img.fit = C.GTK_CONTENT_FIT_SCALE_DOWN
	default:
		img.fit = C.GTK_CONTENT_FIT_CONTAIN
	}
	for _, c := range children(h) {
		if s := sources[c]; s != nil {
			C.gtk_picture_set_content_fit((*C.GtkPicture)(p(c)), img.fit)
		}
	}
}

func ImageFrameOnClick(h Handle, fn func()) {
	if img := images[h]; img != nil {
		img.onClick = fn
	}
}

func ImageFrameOnLoad(h Handle, fn func()) {
	if img := images[h]; img != nil {
		img.onLoad = fn
	}
}

func ImageFrameOnError(h Handle, fn func(message string)) {
	if img := images[h]; img != nil {
		img.onError = fn
	}
}

func ImageSourceNew() Handle {
	pic := Handle(unsafe.Pointer(C.gtk_picture_new()))
	s := &source{pic: pic}
	sources[pic] = s
	moved := cbind.Register(s.parented)
	C.sngl_img_watch_parent((*C.GObject)(p(pic)), C.int(moved))
	gone := cbind.Register(func() {
		delete(sources, pic)
		cbind.Release(moved)
		s.watch(nil)
		s.setTexture(nil)
	})
	C.sngl_img_watch((*C.GObject)(p(pic)), C.int(gone))
	return pic
}

// LoadPicture is the picture url names, decoded once it is read: at once for
// a file the program carries, and from the main loop for one fetched, since
// the fetch may answer from its own goroutine before this returns.
func LoadPicture(url string) *Picture {
	pic := &Picture{url: url, watching: map[*source]bool{}}
	remote := snglimage.IsRemote(url)
	snglimage.Fetch(url, func(data []byte, err error) {
		if !remote {
			pic.arrive(data, err)
			return
		}
		Post(func() { pic.arrive(data, err) })
	})
	return pic
}

func (pic *Picture) arrive(data []byte, err error) {
	if pic.released {
		return
	}
	if err == nil {
		if len(data) == 0 {
			err = errors.New(pic.url + ": empty")
		} else {
			pic.tex = texture(data)
			if pic.tex == nil {
				err = errors.New(pic.url + ": not an image GTK can read")
			}
		}
	}
	pic.err, pic.arrived = err, true
	for s := range pic.watching {
		s.take(pic)
	}
}

// UnrefPicture gives a picture back; the sources showing it keep what they
// show. One not yet arrived is dropped when it does.
func UnrefPicture(pic *Picture) {
	if pic == nil || pic.released {
		return
	}
	pic.released = true
	pic.watching = nil
	if pic.scaled != nil {
		C.cairo_surface_destroy(pic.scaled)
	}
	if pic.surface != nil {
		C.cairo_surface_destroy(pic.surface)
	}
	if pic.tex != nil {
		C.g_object_unref(C.gpointer(unsafe.Pointer(pic.tex)))
	}
	pic.scaled, pic.surface, pic.tex = nil, nil, nil
}

// ImageSourceSetPicture shows pic, now or when it arrives; nil shows nothing.
func ImageSourceSetPicture(h Handle, pic *Picture) {
	s := sources[h]
	if s == nil {
		return
	}
	s.watch(pic)
	if pic == nil {
		s.err = nil
		s.setTexture(nil)
		return
	}
	if pic.arrived {
		s.take(pic)
	}
}

// watch makes pic the picture the source is told about, or none.
func (s *source) watch(pic *Picture) {
	if s.picture != nil && s.picture.watching != nil {
		delete(s.picture.watching, s)
	}
	s.picture = pic
	if pic != nil && pic.watching != nil {
		pic.watching[s] = true
	}
}

// take shows the picture as it arrived, holding a reference of the source's
// own so the picture may be given back while the source still shows it.
func (s *source) take(pic *Picture) {
	tex := pic.tex
	if tex != nil {
		C.g_object_ref(C.gpointer(unsafe.Pointer(tex)))
	}
	s.err = pic.err
	s.setTexture(tex)
}

// ImagePaint paints a drawn source's picture into a surface of its own, with
// draw handed the cairo context, and shows it.
func ImagePaint(h Handle, w, ht int, draw func(cr unsafe.Pointer)) {
	s := sources[h]
	if s == nil || w <= 0 || ht <= 0 {
		return
	}
	surface := C.cairo_image_surface_create(C.CAIRO_FORMAT_ARGB32, C.int(w), C.int(ht))
	cr := C.cairo_create(surface)
	draw(unsafe.Pointer(cr))
	C.cairo_destroy(cr)
	C.cairo_surface_flush(surface)
	tex := C.sngl_img_surface_texture(surface, C.int(w), C.int(ht))
	C.cairo_surface_destroy(surface)
	s.watch(nil)
	s.err = nil
	s.setTexture(tex)
}

func (s *source) setTexture(tex *C.GdkTexture) {
	if s.tex != nil {
		C.g_object_unref(C.gpointer(unsafe.Pointer(s.tex)))
	}
	s.tex = tex
	if _, live := sources[s.pic]; !live {
		return
	}
	C.gtk_picture_set_paintable((*C.GtkPicture)(p(s.pic)), (*C.GdkPaintable)(unsafe.Pointer(tex)))
	s.ready, s.told = true, false
	s.report()
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *source) attach(img *image) {
	C.gtk_picture_set_content_fit((*C.GtkPicture)(p(s.pic)), img.fit)
	if s.frame != img {
		s.frame, s.told = img, false
	}
	s.report()
}

func (s *source) report() {
	if s.frame == nil || !s.ready || s.told {
		return
	}
	s.told = true
	switch {
	case s.err != nil && s.frame.onError != nil:
		s.frame.onError(s.err.Error())
	case s.err == nil && s.frame.onLoad != nil:
		s.frame.onLoad()
	}
}

// parented is the source having been put somewhere: it fits itself to the
// image it is in, and reports to it.
func (s *source) parented() {
	parent := Handle(unsafe.Pointer(C.gtk_widget_get_parent(widget(s.pic))))
	if img := images[parent]; img != nil {
		s.attach(img)
		return
	}
	s.frame = nil
}

// adopted reports whether box took child some other way than as a widget: a
// window taking a source as its icon.
func adopted(box, child Handle) bool {
	t := topOf(box)
	s := sources[child]
	if t == nil || s == nil {
		return false
	}
	t.icon = s
	s.onChange = t.applyIcon
	t.applyIcon()
	return true
}

func (t *toplevel) applyIcon() {
	if t.win != nil && t.icon != nil {
		iconize(t.win, t.icon.tex)
	}
}

// WindowSetIcon makes src the icon of a window this package did not make: the
// one an inline-cgo build makes for itself.
func WindowSetIcon(win, src Handle) {
	s := sources[src]
	if s == nil {
		return
	}
	w := (*C.GtkWindow)(p(win))
	s.onChange = func() { iconize(w, s.tex) }
	iconize(w, s.tex)
}

var (
	iconDir string
	iconN   int
)

// iconize names a window's icon. GTK 4 takes an icon by name only, so the
// picture is written into a directory of the icon theme's search path under a
// name of its own, a new one each time it changes.
func iconize(win *C.GtkWindow, tex *C.GdkTexture) {
	if tex == nil {
		return
	}
	if iconDir == "" {
		dir, err := os.MkdirTemp("", "sngl-icons-")
		if err != nil {
			return
		}
		iconDir = dir
		cs := C.CString(dir)
		C.sngl_img_add_search_path(cs)
		C.free(unsafe.Pointer(cs))
	}
	iconN++
	name := fmt.Sprintf("sngl-icon-%d-%d", os.Getpid(), iconN)
	path := C.CString(filepath.Join(iconDir, name+".png"))
	defer C.free(unsafe.Pointer(path))
	if C.gdk_texture_save_to_png(tex, path) == 0 {
		return
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.gtk_window_set_icon_name(win, cname)
}

// CairoPaintPicture paints pic into the box at x, y sized w by h. cr is a
// *C.cairo_t of whichever package hands it over, which is a type of that
// package's own. The picture is scaled to the box once and painted from that
// until the box changes size; one still on its way paints nothing.
func CairoPaintPicture(cr any, pic *Picture, x, y, w, h float64) {
	if pic == nil || pic.tex == nil || w <= 0 || h <= 0 {
		return
	}
	sw, sh := C.int(math.Ceil(w)), C.int(math.Ceil(h))
	if pic.scaled == nil || pic.sw != sw || pic.sh != sh {
		if pic.surface == nil {
			pic.surface = C.sngl_img_surface(pic.tex)
			if pic.surface == nil {
				return
			}
		}
		if pic.scaled != nil {
			C.cairo_surface_destroy(pic.scaled)
		}
		pic.scaled, pic.sw, pic.sh = C.sngl_img_scaled(pic.surface, sw, sh), sw, sh
	}
	C.sngl_img_paint((*C.cairo_t)(reflect.ValueOf(cr).UnsafePointer()), pic.scaled, C.double(x), C.double(y))
}

// texture is the picture in data, read by GdkPixbuf, whose loaders know SVG,
// or by Go where the host has no loader for it.
func texture(data []byte) *C.GdkTexture {
	buf := C.CBytes(data)
	defer C.free(buf)
	if tex := C.sngl_img_texture((*C.guchar)(buf), C.gsize(len(data))); tex != nil {
		return tex
	}
	img, err := snglimage.Decode(data)
	if err != nil {
		return nil
	}
	b := img.Bounds()
	rgba := goimage.NewNRGBA(goimage.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	px := C.CBytes(rgba.Pix)
	defer C.free(px)
	return C.sngl_img_rgba_texture((*C.guchar)(px), C.int(b.Dx()), C.int(b.Dy()), C.int(rgba.Stride))
}
