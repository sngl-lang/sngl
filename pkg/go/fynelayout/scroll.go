package fynelayout

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// ReachScroll is a vertical scroll that calls OnReachEnd when the end of its
// content comes within half a viewport of showing: as it arrives, and again
// when the content grows while it is there.
//
// Fyne reports a scroll (OnScrolled) and a resize of the viewport (Layout),
// but nothing when the content grows, which is what a handler adding a page
// does. So each report is followed by one look a moment later, and a look
// that finds the content grown with its end still showing reports again.
type ReachScroll struct {
	widget.BaseWidget
	Content    fyne.CanvasObject
	OnReachEnd func()

	scroll *container.Scroll
	near   bool
	height float32
}

// reachRecheck is how long after a report the scroll looks again.
const reachRecheck = 100 * time.Millisecond

func NewReachScroll() *ReachScroll {
	s := &ReachScroll{scroll: container.NewVScroll(nil), height: -1}
	s.scroll.OnScrolled = func(fyne.Position) { s.check() }
	s.ExtendBaseWidget(s)
	return s
}

func (s *ReachScroll) CreateRenderer() fyne.WidgetRenderer {
	s.scroll.Content = s.Content
	return &reachRenderer{s: s}
}

// check reports the end if it has just come into view, or if the content has
// grown while it was there.
func (s *ReachScroll) check() {
	if s.Content == nil {
		return
	}
	page := s.scroll.Size().Height
	if page <= 0 {
		return
	}
	height := s.Content.MinSize().Height
	near := height-(s.scroll.Offset.Y+page) <= page/2
	fire := near && (!s.near || height != s.height)
	s.near, s.height = near, height
	if !fire || s.OnReachEnd == nil {
		return
	}
	s.OnReachEnd()
	time.AfterFunc(reachRecheck, func() { fyne.Do(s.check) })
}

type reachRenderer struct{ s *ReachScroll }

func (r *reachRenderer) Layout(size fyne.Size) {
	r.s.scroll.Resize(size)
	r.s.check()
}

func (r *reachRenderer) MinSize() fyne.Size { return r.s.scroll.MinSize() }

func (r *reachRenderer) Refresh() {
	if r.s.scroll.Content != r.s.Content {
		r.s.scroll.Content = r.s.Content
	}
	r.s.scroll.Refresh()
	r.s.check()
}

func (r *reachRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.s.scroll} }

func (r *reachRenderer) Destroy() {}
