package fynelayout

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
)

// posted collects what a ReachScroll posts, for the test to run when it
// chooses, as the loop would after what is running now.
type posted []func()

func (p *posted) run() {
	q := *p
	*p = nil
	for _, f := range q {
		f()
	}
}

func catchPosts(t *testing.T) *posted {
	var p posted
	old := Post
	Post = func(f func()) { p = append(p, f) }
	t.Cleanup(func() { Post = old })
	return &p
}

func row() fyne.CanvasObject {
	r := canvas.NewRectangle(nil)
	r.SetMinSize(fyne.NewSize(50, 100))
	return r
}

// frame is what Fyne's next frame does once the content's minimum changed:
// it lays out the content's parent, which is the probe.
func frame(s *ReachScroll) {
	s.probe.Layout.Layout(s.probe.Objects, s.probe.Size())
}

// The report is posted, not made while Fyne lays the tree out, and the many
// measures before it runs are one report.
func TestReachEndIsPostedOnce(t *testing.T) {
	fynetest.NewTempApp(t)
	p := catchPosts(t)
	calls := 0
	s := NewReachScroll()
	s.Content = container.NewVBox(row())
	s.OnReachEnd = func() { calls++ }
	w := fynetest.NewWindow(s)
	defer w.Close()
	w.Resize(fyne.NewSize(200, 400))
	frame(s)
	s.Refresh()

	if calls != 0 {
		t.Fatalf("the handler ran %d times while the scroll was laid out, want it posted", calls)
	}
	if len(*p) != 1 {
		t.Fatalf("%d reports posted, want one for the measures before it runs", len(*p))
	}
	p.run()
	if calls != 1 {
		t.Errorf("the handler ran %d times once its report ran, want 1", calls)
	}
}

// A short first page whose next page arrives long after the report -- a fetch
// slower than any recheck -- still asks for the one after: the content
// growing while its end shows is reported, as gtk4 reports the adjustment
// growing.
func TestReachEndAsksAgainWhenTheContentGrows(t *testing.T) {
	fynetest.NewTempApp(t)
	p := catchPosts(t)
	list := container.NewVBox(row())
	calls := 0
	s := NewReachScroll()
	s.Content = list
	s.OnReachEnd = func() { calls++ }
	w := fynetest.NewWindow(s)
	defer w.Close()
	w.Resize(fyne.NewSize(200, 400))
	p.run()
	if calls != 1 {
		t.Fatalf("a first page shorter than the scroll asked %d times, want 1", calls)
	}

	// Nothing grows: nothing more is asked, however often it is laid out.
	frame(s)
	p.run()
	if calls != 1 {
		t.Fatalf("the scroll asked again with nothing grown: %d", calls)
	}

	// The page arrives, and its end still shows.
	list.Add(row())
	frame(s)
	p.run()
	if calls != 2 {
		t.Errorf("the content grew with its end showing and the scroll asked %d times, want 2", calls)
	}

	// A row goes, its end still showing: nothing arrived, so nothing more is
	// asked, as html's end, still in sight, reports nothing new.
	list.Remove(list.Objects[1])
	frame(s)
	p.run()
	if calls != 2 {
		t.Errorf("the content shrank with its end showing and the scroll asked %d times, want 2", calls)
	}
}

// Content long enough that its end is out of sight asks for nothing.
func TestReachEndNotAskedWhileTheEndIsHidden(t *testing.T) {
	fynetest.NewTempApp(t)
	p := catchPosts(t)
	list := container.NewVBox()
	for range 20 {
		list.Add(row())
	}
	calls := 0
	s := NewReachScroll()
	s.Content = list
	s.OnReachEnd = func() { calls++ }
	w := fynetest.NewWindow(s)
	defer w.Close()
	w.Resize(fyne.NewSize(200, 400))
	frame(s)
	p.run()
	if calls != 0 {
		t.Errorf("a scroll whose end is far out of sight asked %d times", calls)
	}
}

func column() fyne.CanvasObject {
	r := canvas.NewRectangle(nil)
	r.SetMinSize(fyne.NewSize(100, 50))
	return r
}

// A horizontal scroll's end is its right edge: a first page narrower than the
// scroll asks for the next, the content growing wider while its edge shows
// asks again, though no taller, and an edge out of sight asks for nothing.
func TestReachEndSidewaysMeasuresWidth(t *testing.T) {
	fynetest.NewTempApp(t)
	p := catchPosts(t)
	list := container.NewHBox(column())
	calls := 0
	s := NewReachScroll()
	s.SetDirection("horizontal")
	s.Content = list
	s.OnReachEnd = func() { calls++ }
	w := fynetest.NewWindow(s)
	defer w.Close()
	w.Resize(fyne.NewSize(400, 200))
	p.run()
	if calls != 1 {
		t.Fatalf("a first page narrower than the scroll asked %d times, want 1", calls)
	}

	list.Add(column())
	frame(s)
	p.run()
	if calls != 2 {
		t.Fatalf("the content grew wider with its edge showing and the scroll asked %d times, want 2", calls)
	}

	for range 20 {
		list.Add(column())
	}
	frame(s)
	p.run()
	if calls != 2 {
		t.Errorf("a scroll whose right edge is far out of sight asked %d more times", calls-2)
	}
	if s.scroll.Direction != container.ScrollHorizontalOnly {
		t.Errorf("the scroll moves along %v, want horizontally only", s.scroll.Direction)
	}
}

// A vertical scroll is not as wide as its content: what is wider is cut off
// at its edge, as html and android cut it, where Fyne's own asks for it all.
func TestAVerticalScrollIsNotAsWideAsItsContent(t *testing.T) {
	fynetest.NewTempApp(t)
	wide := canvas.NewRectangle(nil)
	wide.SetMinSize(fyne.NewSize(600, 40))
	s := NewReachScroll()
	s.Content = wide
	w := fynetest.NewTempWindow(t, container.NewVBox(s))
	w.Resize(fyne.NewSize(200, 200))
	if got := s.MinSize().Width; got >= 600 {
		t.Errorf("a vertical scroll asks for %v across, the width of its content", got)
	}
	if got := s.Size().Width; got > 200 {
		t.Errorf("a vertical scroll is %v wide in a 200 window", got)
	}
}
