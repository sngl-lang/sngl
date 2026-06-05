package golang_test

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestServerActionE2E generates the server_action fixture's server.go, then
// actually RUNS it and exercises the PRG (Post/Redirect/Get) cycle over real
// HTTP with a cookie jar.
//
// How the server is run: the generated package is `package ui` exposing
// `Handler() http.Handler`. We drop a tiny harness main.go into the fixture
// module that imports that package and serves Handler() on a random localhost
// port, then `go run` it as a subprocess and hit it with net/http. This is more
// robust than CDP for the backend PRG path, which has NO client JS — interaction
// is a plain <form method=post> submit.
//
// Assertions:
//   - GET / -> body contains "count 0" (initial render).
//   - POST / with _action=0 -> 303 redirect.
//   - GET / (same jar) -> body now contains "count 1".
//   - A second client with a fresh cookie jar sees "count 0" (per-session
//     isolation): the first client's increment does not leak.
func TestServerActionE2E(t *testing.T) {
	repoRoot := repoRootDir(t)
	fixture := filepath.Join(repoRoot, "codegen", "lang", "golang", "testdata", "parity", "server_action")

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "snglbin")

	// Build the CLI.
	build := exec.Command("go", "build", "-o", bin, "./cmd/sngl")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	// Copy the fixture into a writable temp module and generate the server into
	// a subpackage so the api import resolves against the fixture's go.mod.
	work := filepath.Join(tmp, "work")
	copyTree(t, fixture, work)

	out := filepath.Join(work, "out")
	gen := exec.Command(bin, "generate", "--platform", "html", "--lang", "go", "-o", out, filepath.Join(work, "app.sngl"))
	gen.Dir = work
	if combined, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, combined)
	}

	// Drop a tiny harness that serves the generated Handler() on :0 and prints
	// the chosen addr so the test can dial it.
	harnessDir := filepath.Join(work, "cmd", "serve")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := `package main

import (
	"fmt"
	"net"
	"net/http"

	ui "example.com/route-post/out"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Printf("LISTENING %s\n", ln.Addr().String())
	if err := http.Serve(ln, ui.Handler()); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(harnessDir, "main.go"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the harness to a binary first (rather than `go run`, which execs a
	// grandchild that would keep our stdout pipe open after we kill the parent).
	serveBin := filepath.Join(tmp, "serveBin")
	hbuild := exec.Command("go", "build", "-o", serveBin, "./cmd/serve")
	hbuild.Dir = work
	if combined, err := hbuild.CombinedOutput(); err != nil {
		t.Fatalf("build harness: %v\n%s", err, combined)
	}

	// Run the harness as a subprocess in its own process group so we can reap it
	// (and any children) cleanly on teardown.
	srv := exec.Command(serveBin)
	srv.Dir = work
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server harness: %v", err)
	}
	defer func() {
		// Kill the whole process group, then drain/close the pipe.
		if srv.Process != nil {
			_ = syscall.Kill(-srv.Process.Pid, syscall.SIGKILL)
		}
		go func() { _, _ = io.Copy(io.Discard, stdout) }()
		_ = srv.Wait()
	}()

	// Read the LISTENING line to learn the address.
	addrCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			if after, ok := strings.CutPrefix(line, "LISTENING "); ok {
				addrCh <- after
				return
			}
		}
		addrCh <- ""
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(120 * time.Second):
		t.Fatal("timed out waiting for harness to start listening")
	}
	if addr == "" {
		t.Fatal("harness did not report a listening address")
	}
	base := "http://" + addr

	// Wait until the server actually answers.
	if !waitReady(base) {
		t.Fatal("server never became ready")
	}

	countRe := regexp.MustCompile(`count\s+(\d+)`)
	countOf := func(body string) string {
		m := countRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no count found in body:\n%s", body)
		}
		return m[1]
	}

	// --- Client A: GET -> POST -> GET ---
	jarA, _ := cookiejar.New(nil)
	clientA := &http.Client{
		Jar: jarA,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Stop at the redirect so we can assert the 303 explicitly.
			return http.ErrUseLastResponse
		},
	}

	body := getBody(t, clientA, base+"/")
	if got := countOf(body); got != "0" {
		t.Fatalf("initial GET: want count 0, got count %s\nbody:\n%s", got, body)
	}

	// POST the action; expect a 303 redirect.
	resp, err := clientA.PostForm(base+"/", map[string][]string{"_action": {"0"}})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST: want 303 See Other, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("POST redirect Location: want /, got %q", loc)
	}

	body = getBody(t, clientA, base+"/")
	if got := countOf(body); got != "1" {
		t.Fatalf("after POST: want count 1, got count %s\nbody:\n%s", got, body)
	}

	// --- Client B: fresh jar, must be isolated (count 0) ---
	jarB, _ := cookiejar.New(nil)
	clientB := &http.Client{Jar: jarB}
	body = getBody(t, clientB, base+"/")
	if got := countOf(body); got != "0" {
		t.Fatalf("per-session isolation: second client should see count 0, got count %s\nbody:\n%s", got, body)
	}

	// Client A is still at 1 (unaffected by B).
	body = getBody(t, clientA, base+"/")
	if got := countOf(body); got != "1" {
		t.Fatalf("client A should still see count 1 after B's request, got count %s", got)
	}
}

// TestServerActionConcurrentSession pins fix #3a: many concurrent requests on
// the SAME session must not race on the per-session *State. Before the fix the
// store released its map mutex before the handler read/mutated *State, so
// concurrent GET (render reads s.Count) + POST (mutates s.Count) on one session
// raced. Run this with -race.
func TestServerActionConcurrentSession(t *testing.T) {
	repoRoot := repoRootDir(t)
	fixture := filepath.Join(repoRoot, "codegen", "lang", "golang", "testdata", "parity", "server_action")

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "snglbin")
	build := exec.Command("go", "build", "-o", bin, "./cmd/sngl")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	work := filepath.Join(tmp, "work")
	copyTree(t, fixture, work)
	out := filepath.Join(work, "out")
	gen := exec.Command(bin, "generate", "--platform", "html", "--lang", "go", "-o", out, filepath.Join(work, "app.sngl"))
	gen.Dir = work
	if combined, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, combined)
	}

	harnessDir := filepath.Join(work, "cmd", "serve")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := `package main

import (
	"fmt"
	"net"
	"net/http"

	ui "example.com/route-post/out"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Printf("LISTENING %s\n", ln.Addr().String())
	if err := http.Serve(ln, ui.Handler()); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(harnessDir, "main.go"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the harness WITH -race so the running server itself is race-detected
	// (the test process's own -race flag does not propagate to a subprocess).
	serveBin := filepath.Join(tmp, "serveBin")
	hbuild := exec.Command("go", "build", "-race", "-o", serveBin, "./cmd/serve")
	hbuild.Dir = work
	if combined, err := hbuild.CombinedOutput(); err != nil {
		t.Fatalf("build harness: %v\n%s", err, combined)
	}

	srv := exec.Command(serveBin)
	srv.Dir = work
	srv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var srvErr bytes.Buffer
	srv.Stderr = &srvErr
	if err := srv.Start(); err != nil {
		t.Fatalf("start server harness: %v", err)
	}
	defer func() {
		if srv.Process != nil {
			_ = syscall.Kill(-srv.Process.Pid, syscall.SIGKILL)
		}
		go func() { _, _ = io.Copy(io.Discard, stdout) }()
		_ = srv.Wait()
	}()

	addrCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "LISTENING ") {
				addrCh <- strings.TrimPrefix(line, "LISTENING ")
				return
			}
		}
		addrCh <- ""
	}()
	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(120 * time.Second):
		t.Fatal("timed out waiting for harness to start listening")
	}
	if addr == "" {
		t.Fatal("harness did not report a listening address")
	}
	base := "http://" + addr
	if !waitReady(base) {
		t.Fatal("server never became ready")
	}

	// Single shared cookie jar => all goroutines hit the SAME session.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	// Prime the cookie so every goroutine reuses one session id.
	getBody(t, client, base+"/")

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				resp, err := client.PostForm(base+"/", map[string][]string{"_action": {"0"}})
				if err == nil {
					resp.Body.Close()
				}
			} else {
				resp, err := client.Get(base + "/")
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
		}(i)
	}
	wg.Wait()

	// If the race detector fired in the server, it exited non-zero; surface it.
	if srv.Process != nil {
		_ = syscall.Kill(-srv.Process.Pid, syscall.SIGKILL)
	}
	_ = srv.Wait()
	if strings.Contains(srvErr.String(), "DATA RACE") {
		t.Fatalf("data race detected in generated server:\n%s", srvErr.String())
	}
}

func waitReady(base string) bool {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/")
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func getBody(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
