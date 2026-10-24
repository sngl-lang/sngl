package fynelayout

import (
	"bytes"
	"image"
	"image/png"
	"path"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/snglimage"
)

// Image is what a `ui.image` is on fyne: a widget showing the source it holds
// fitted into its box, or the placeholder where it holds none. Its children are
// added to Box, which is also what a render slot inside it renders into.
type Image struct {
	widget.BaseWidget
	Box      *fyne.Container
	fill     canvas.ImageFill
	OnTapped func()
	OnLoad   func()
	OnError  func(message string)
}

func NewImage() *Image {
	i := &Image{fill: canvas.ImageFillContain}
	i.Box = container.New(&imageLayout{i})
	i.ExtendBaseWidget(i)
	return i
}

func (i *Image) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(i.Box)
}

func (i *Image) Add(o fyne.CanvasObject) { i.Box.Add(o) }

func (i *Image) Remove(o fyne.CanvasObject) { i.Box.Remove(o) }

// SetFit is how the picture is scaled into the box, as `ui.ObjectFit` spells
// it. `none` and `scaleDown` are `contain`: fyne has no picture shown at its
// own size that is not also the size of its box. So is `cover`: fyne 2.7's
// software painter, which renders a snapshot, dereferences a nil image for
// ImageFillCover.
func (i *Image) SetFit(fit string) {
	switch fit {
	case "fill":
		i.fill = canvas.ImageFillStretch
	default:
		i.fill = canvas.ImageFillContain
	}
	i.Box.Refresh()
}

func (i *Image) Tapped(*fyne.PointEvent) {
	if i.OnTapped != nil {
		i.OnTapped()
	}
}

// imageLayout gives every child the whole box, and each source the box's fit.
type imageLayout struct{ img *Image }

func (l *imageLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		if s, ok := o.(*ImageSource); ok {
			s.attach(l.img)
		}
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (l *imageLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, o := range objs {
		if o.Visible() {
			min = min.Max(o.MinSize())
		}
	}
	return min
}

// ImageSource is an image's source: the picture a URL names, or one a drawing
// painted. It shows the picture itself, and reports to the image holding it
// whether it arrived.
type ImageSource struct {
	widget.BaseWidget
	pic   *canvas.Image
	url   string
	data  []byte
	err   error
	ready bool
	frame *Image
	told  bool
	// onChange is a window's, when the source is its icon.
	onChange func()
}

func NewImageSource() *ImageSource {
	s := &ImageSource{pic: &canvas.Image{FillMode: canvas.ImageFillContain}}
	s.ExtendBaseWidget(s)
	return s
}

func (s *ImageSource) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.pic)
}

// SetURL shows the picture url names: at once for a file the program carries,
// and once it has arrived for one fetched.
func (s *ImageSource) SetURL(url string) {
	s.url = url
	var later atomic.Bool
	snglimage.Fetch(url, func(data []byte, err error) {
		if !later.Load() {
			s.show(url, data, err)
			return
		}
		Post(func() { s.show(url, data, err) })
	})
	later.Store(true)
}

func (s *ImageSource) show(url string, data []byte, err error) {
	if url != s.url {
		return
	}
	s.data, s.err = data, err
	s.pic.Image = nil
	// A URL that fails shows nothing, as gtk4's does, not the last URL's
	// picture as if it were this one's.
	s.pic.Resource = nil
	if err == nil {
		s.pic.Resource = fyne.NewStaticResource(path.Base(url), data)
		if w, h, ok := snglimage.Size(data); ok {
			s.pic.SetMinSize(fyne.NewSize(float32(w), float32(h)))
		}
	}
	s.arrived()
}

// SetImage shows a picture a drawing painted.
func (s *ImageSource) SetImage(img image.Image) {
	s.url, s.data, s.err = "", nil, nil
	s.pic.Resource = nil
	s.pic.Image = img
	b := img.Bounds()
	s.pic.SetMinSize(fyne.NewSize(float32(b.Dx()), float32(b.Dy())))
	s.arrived()
}

func (s *ImageSource) arrived() {
	s.ready, s.told = true, false
	s.pic.Refresh()
	s.Refresh()
	s.report()
	if s.onChange != nil {
		s.onChange()
	}
}

func (s *ImageSource) attach(img *Image) {
	s.pic.FillMode = img.fill
	if s.frame != img {
		s.frame, s.told = img, false
	}
	s.report()
}

func (s *ImageSource) report() {
	if s.frame == nil || !s.ready || s.told {
		return
	}
	s.told = true
	switch {
	case s.err != nil && s.frame.OnError != nil:
		s.frame.OnError(s.err.Error())
	case s.err == nil && s.frame.OnLoad != nil:
		s.frame.OnLoad()
	}
}

func (s *ImageSource) MinSize() fyne.Size { return s.pic.MinSize() }

// Resource is the picture as a file, for a window's icon: the bytes a URL
// named, or a drawing encoded as PNG.
func (s *ImageSource) Resource() fyne.Resource {
	switch {
	case s.data != nil:
		return fyne.NewStaticResource(path.Base(s.url), s.data)
	case s.pic.Image != nil:
		var b bytes.Buffer
		if png.Encode(&b, s.pic.Image) != nil {
			return nil
		}
		return fyne.NewStaticResource("icon.png", b.Bytes())
	}
	return nil
}
