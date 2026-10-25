package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"rsc.io/script"
)

// `sngl run` of a server does not return, and the `sngl` command runs in
// process with the process's stdout as its own. So `serve` runs `sngl` as a
// program of its own -- built once from this checkout -- in a process group
// of its own, which holds whatever it starts: html's static server, or the
// binary a server language builds. It waits for the URL the server prints,
// sets $URL to it, and leaves the server up until the script ends, when
// stopServers kills the group.

var (
	snglBinOnce sync.Once
	snglBin     string
	snglBinErr  error

	serversMu sync.Mutex
	servers   []*exec.Cmd
)

// serveStartTimeout is how long a server has to print its URL: a server
// language builds its program first.
const serveStartTimeout = 5 * time.Minute

// buildSnglBin builds this checkout's sngl outside the tree, so it runs the
// code under test rather than proxying to ./cmd/sngl of whatever directory a
// script stands in.
func buildSnglBin() (string, error) {
	snglBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sngl-script-bin-*")
		if err != nil {
			snglBinErr = err
			return
		}
		snglBin = filepath.Join(dir, "sngl")
		out, err := exec.Command("go", "build", "-o", snglBin, ".").CombinedOutput()
		if err != nil {
			snglBinErr = fmt.Errorf("go build sngl: %w\n%s", err, out)
		}
	})
	return snglBin, snglBinErr
}

// removeSnglBin removes the binary buildSnglBin built, if it did.
func removeSnglBin() {
	if snglBin != "" {
		os.RemoveAll(filepath.Dir(snglBin))
	}
}

func serveCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "start `sngl args...` as a server, set $URL to the URL it prints, and stop it when the script ends",
			Args:    "args...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			bin, err := buildSnglBin()
			if err != nil {
				return nil, err
			}
			c := exec.Command(bin, args...)
			c.Dir = s.Getwd()
			c.Env = s.Environ()
			c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			var stderr strings.Builder
			c.Stderr = &stderr
			out, err := c.StdoutPipe()
			if err != nil {
				return nil, err
			}
			if err := c.Start(); err != nil {
				return nil, err
			}
			serversMu.Lock()
			servers = append(servers, c)
			serversMu.Unlock()

			found := make(chan string, 1)
			go func() {
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "http://") {
						found <- line
						break
					}
				}
				close(found)
				io.Copy(io.Discard, out)
			}()
			select {
			case url, ok := <-found:
				if !ok {
					c.Wait()
					return nil, fmt.Errorf("sngl %s exited without printing a URL:\n%s", strings.Join(args, " "), stderr.String())
				}
				s.Setenv("URL", url)
				return nil, nil
			case <-time.After(serveStartTimeout):
				return nil, fmt.Errorf("sngl %s printed no URL in %s:\n%s", strings.Join(args, " "), serveStartTimeout, stderr.String())
			}
		},
	)
}

// stopServers kills each server serve started, with everything it started,
// and waits for it.
func stopServers() {
	serversMu.Lock()
	defer serversMu.Unlock()
	for _, c := range servers {
		syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		c.Wait()
	}
	servers = nil
}

func fetchCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "GET url and print the body; fail on a status that is not 2xx",
			Args:    "url",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 1 {
				return nil, script.ErrUsage
			}
			resp, err := http.Get(args[0])
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode/100 != 2 {
				return nil, errors.New(resp.Status)
			}
			return func(*script.State) (string, string, error) {
				return string(body), "", nil
			}, nil
		},
	)
}
