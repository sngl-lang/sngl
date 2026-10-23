package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// ReachScroll is a vertical scroll that calls OnReachEnd when the end of its
// content comes within half a viewport of showing: as it arrives, and again
// when the content grows while it is there. It is gtk4rt's scroll's rule, on
// what Fyne reports.
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

	scroll *container.Scroll
	probe  *fyne.Container
	near   bool
	height float32
	// asked says a report is posted and has not run.
	asked bool
}

// Post runs f once what is running now has finished: fyne.Do, which a running
// app queues for its loop. A test that pumps no loop replaces it.
var Post = fyne.Do

func NewReachScroll() *ReachScroll {
	s := &ReachScroll{height: -1}
	s.probe = container.New(probeLayout{s})
	s.scroll = container.NewVScroll(s.probe)
	s.scroll.OnScrolled = func(fyne.Position) { s.check() }
	s.ExtendBaseWidget(s)
	return s
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
	page := s.scroll.Size().Height
	if page <= 0 {
		// Not laid out yet: nothing is shown, so no end is.
		return
	}
	height := s.Content.MinSize().Height
	near := height-(s.scroll.Offset.Y+page) <= page/2
	fire := near && (!s.near || height > s.height)
	s.near, s.height = near, height
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
