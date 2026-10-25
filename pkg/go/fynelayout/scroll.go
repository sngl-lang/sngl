package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// ReachScroll is a scroll that calls OnReachEnd when the end of its content
// comes within half a viewport of showing: as it arrives, and again when the
// content grows while it is there. The end is the right edge of a horizontal
// scroll and the bottom of any other. It is gtk4rt's scroll's rule, on what
// Fyne reports.
//
// Fyne reports a scroll (OnScrolled) and a resize of the viewport (Layout),
// and nothing of its own when the content grows, which is what a handler
// adding a page does. So the content sits in a probe: a box whose layout Fyne
// runs again when the content's minimum changes, which measures as it does.
//
// The handler is posted rather than called (Post), as gtk4 posts it to idle:
// a measure runs while Fyne lays the tree out, and a handler adding rows then
// would change the tree mid-layout. One report answers every measure before
// it runs.
type ReachScroll struct {
	widget.BaseWidget
	Content    fyne.CanvasObject
	OnReachEnd func()

	scroll   *container.Scroll
	probe    *fyne.Container
	sideways bool
	near     bool
	extent   float32
	// asked says a report is posted and has not run.
	asked bool
}

// NewReachScroll is a vertical scroll until SetDirection says otherwise.
func NewReachScroll() *ReachScroll {
	s := &ReachScroll{extent: -1}
	s.probe = container.New(probeLayout{s})
	s.scroll = container.NewVScroll(s.probe)
	s.scroll.OnScrolled = func(fyne.Position) { s.check() }
	s.ExtendBaseWidget(s)
	return s
}

// SetDirection is the axis the content scrolls along, as ui.ScrollDirection
// names it: "horizontal", "both", or vertical for anything else.
func (s *ReachScroll) SetDirection(direction string) {
	switch direction {
	case "horizontal":
		s.scroll.Direction = container.ScrollHorizontalOnly
	case "both":
		s.scroll.Direction = container.ScrollBoth
	default:
		s.scroll.Direction = container.ScrollVerticalOnly
	}
	s.sideways = direction == "horizontal"
	s.near, s.extent = false, -1
	s.scroll.Refresh()
	s.check()
}

func (s *ReachScroll) CreateRenderer() fyne.WidgetRenderer {
	s.adopt()
	return &reachRenderer{s: s}
}

// adopt puts the content the program set in the probe.
func (s *ReachScroll) adopt() {
	if len(s.probe.Objects) == 1 && s.probe.Objects[0] == s.Content {
		return
	}
	s.probe.Objects = nil
	if s.Content != nil {
		s.probe.Objects = []fyne.CanvasObject{s.Content}
	}
}

// check reports the end if it has just come into view, or if the content has
// grown while it was there.
func (s *ReachScroll) check() {
	if s.Content == nil {
		return
	}
	page, extent, offset := s.scroll.Size().Height, s.Content.MinSize().Height, s.scroll.Offset.Y
	if s.sideways {
		page, extent, offset = s.scroll.Size().Width, s.Content.MinSize().Width, s.scroll.Offset.X
	}
	if page <= 0 {
		// Not laid out yet: nothing is shown, so no end is.
		return
	}
	near := extent-(offset+page) <= page/2
	fire := near && (!s.near || extent > s.extent)
	s.near, s.extent = near, extent
	if !fire || s.asked {
		return
	}
	s.asked = true
	Post(func() {
		s.asked = false
		if s.OnReachEnd != nil {
			s.OnReachEnd()
		}
	})
}

// probeLayout lays the content out at the probe's size, and measures: Fyne
// runs it when the content's minimum changes as well as when the scroll
// resizes the probe.
type probeLayout struct{ s *ReachScroll }

func (l probeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
	l.s.check()
}

func (l probeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var min fyne.Size
	for _, o := range objects {
		min = min.Max(o.MinSize())
	}
	return min
}

type reachRenderer struct{ s *ReachScroll }

func (r *reachRenderer) Layout(size fyne.Size) {
	r.s.scroll.Resize(size)
	r.s.check()
}

func (r *reachRenderer) MinSize() fyne.Size { return r.s.scroll.MinSize() }

func (r *reachRenderer) Refresh() {
	r.s.adopt()
	r.s.scroll.Refresh()
	r.s.check()
}

func (r *reachRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.s.scroll} }

func (r *reachRenderer) Destroy() {}
