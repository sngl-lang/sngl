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
