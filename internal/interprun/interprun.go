// Package interprun drives an interpreted program against a host.
//
// It is the loop that makes a window live: patches out, events and timer
// deadlines back in. The loop is separated from the process that hosts it so it
// can be tested against a pipe -- a window is not something a test suite can
// open, but everything about driving one is.
package interprun

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/pkg/go/snglhost"
)

// Drive runs a session against a host on the far end of rw, until that host
// goes away.
//
// The session is single-threaded and this is the only goroutine that touches
// it: events and timer deadlines are selected over, never handled concurrently.
// That is the queue Session's own note asks for.
func Drive(s *interp.Session, rw io.ReadWriteCloser) error {
	h := snglhost.NewRPCHost(rw)
	defer h.Close()

	if err := s.Attach(h); err != nil {
		return fmt.Errorf("mounting the tree: %w", err)
	}

	// One timer for the whole loop, reset each pass. A fresh one per iteration
	// with a deferred Stop pins both until Drive returns, and Drive returns when
	// the window closes -- so a 100ms tick accumulated tens of thousands.
	wake := time.NewTimer(0)
	if !wake.Stop() {
		<-wake.C
	}
	defer wake.Stop()

	for {
		// A timer already due fires immediately; with none there is nothing to
		// wake for and the loop waits only on the host.
		var due <-chan time.Time
		if next, ok := s.Timers.Next(); ok {
			wake.Reset(max(time.Until(next), 0))
			due = wake.C
		}

		select {
		case <-h.Done():
			return h.Err()

		case ev := <-h.Events():
			stopTimer(wake, due)
			patches, err := s.Invoke(ev.Key, ev.Name)
			if err != nil {
				// A handler that fails is the program's problem, not the
				// window's: report it and keep the window alive, the way a
				// reload with a syntax error does.
				fmt.Fprintf(os.Stderr, "sngl: @%s on %s: %v\n", ev.Name, ev.Key, err)
				continue
			}
			if err := interp.Apply(h, patches); err != nil {
				return err
			}

		case <-due:
			patches, err := s.FireDue()
			if err != nil {
				fmt.Fprintf(os.Stderr, "sngl: timer: %v\n", err)
				continue
			}
			if err := interp.Apply(h, patches); err != nil {
				return err
			}
		}
	}
}

// stopTimer drains a reset timer that did not fire, so the next Reset starts
// clean rather than seeing a stale tick.
func stopTimer(t *time.Timer, armed <-chan time.Time) {
	if armed == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

// Options configure a run.
type Options struct {
	// Component is the entry point. Empty means "main".
	Component string
	// Worker is the host program to spawn. Empty means Locate decides.
	Worker string
	// Dir is the directory the worker is built and run in, which is what makes
	// a user's module -- their replace directives and pinned versions -- the
	// one it resolves against.
	Dir string
	// Args are passed to the worker.
	Args []string
}

// Run interprets pkg and renders it in a spawned worker.
func Run(pkg *ir.Package, opts Options) error {
	comp := opts.Component
	if comp == "" {
		comp = "main"
	}
	worker := opts.Worker
	if worker == "" {
		var err error
		if worker, err = Locate(opts.Dir); err != nil {
			return err
		}
	}

	s, err := interp.NewSession(pkg, comp, interp.Real{})
	if err != nil {
		return err
	}

	cmd := exec.Command(worker, opts.Args...)
	cmd.Dir = opts.Dir
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", worker, err)
	}
	defer func() { _ = cmd.Wait() }()

	if err := Drive(s, pipe{r: stdout, w: stdin}); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// pipe joins a child's stdout and stdin into one stream.
type pipe struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p pipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p pipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p pipe) Close() error                { _ = p.w.Close(); return p.r.Close() }
