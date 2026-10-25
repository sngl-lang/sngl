//go:build !js

package html

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"duckfam.us/sngl/codegen/platform/html/internal/webtest"
	"github.com/go-rod/rod"
)

// errorTrap is installed ahead of a page's own script, so a raise while it
// loads -- which no test could otherwise ask about -- is left on the window.
const errorTrap = `<script>window.__snglErr = "";` +
	`window.addEventListener("error", function (e) { window.__snglErr = String(e.message); });</script>`

// inBrowser drives the page src renders in Chromium -- unless a record says
// this page, driven by this test as it is now written, already passed, in
// which case nothing is launched. The digest covers the page, the test's own
// source file and this one, so an edit to any asks for a run; a Chromium upgrade
// moves neither, which is what -verify is for.
//
// A record is testdata/browser_runs/<test>. -update drives a page whose digest
// moved or has none and records it once the test passes; -verify drives every
// page and records only under -update.
func inBrowser(t *testing.T, src string, drive func(page *rod.Page)) {
	t.Helper()
	page := renderComponentHTML(t, src)
	i := strings.Index(page, "<script")
	if i < 0 {
		t.Fatalf("generated page carries no script to trap: %q", page)
	}
	served := []byte(page[:i] + errorTrap + page[i:])

	h := sha256.New()
	fmt.Fprintf(h, "%d\x00", len(served))
	h.Write(served)
	for _, depth := range []int{1, 0} {
		_, file, _, _ := runtime.Caller(depth)
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%d\x00", len(src))
		h.Write(src)
	}
	want := "sha256:" + hex.EncodeToString(h.Sum(nil)[:16]) + "\n"

	record := filepath.Join("testdata", "browser_runs", strings.ReplaceAll(t.Name(), "/", "_"))
	have, _ := os.ReadFile(record)
	update, verify := flagSet("update"), flagSet("verify")
	matched := bytes.Equal(have, []byte(want))
	switch {
	case matched && !verify:
		return
	case !update && !verify && have == nil:
		t.Fatalf("%s: no record of this browser run; rerun with -update on a host with Chromium", record)
	case !update && !verify:
		t.Fatalf("%s: the page or the test changed since it ran; rerun with -update -- `go tool verify -update` rerecords every package's", record)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(served)
	})
	engine := webtest.New(mux)
	defer engine.Close()
	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		t.Fatalf("%s: cannot record: %v", record, err)
	}
	defer browser.Close()
	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = browser.WaitStable(200 * time.Millisecond)
	if update {
		t.Cleanup(func() {
			if t.Failed() {
				return
			}
			if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
				t.Error(err)
			}
		})
	}
	drive(browser.Page())
}

// flagSet reads a boolean flag the package's test binary defines elsewhere.
func flagSet(name string) bool {
	f := flag.Lookup(name)
	return f != nil && f.Value.String() == "true"
}
