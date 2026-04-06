package main

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
)

var _ = fmt.Sprint
var _ = html.EscapeString

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// Handler returns an http.Handler that serves all routes.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleHome)
	mux.HandleFunc("GET /about", handleAbout)
	return mux
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	sidebarOpen := false
	_ = sidebarOpen
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Dashboard</title></head><body>`)
	fmt.Fprint(w, `<div style="display:flex;flex-direction:row">`)
	// client-state conditional: s0
	fmt.Fprint(w, `<div id="s0" style="display:none">`)
	fmt.Fprint(w, `<div style="background-color:#f0f0f0;gap:8px;padding:16px;width:200px;display:flex;flex-direction:column">`)
	fmt.Fprint(w, `<span style="font-weight:bold">`)
	fmt.Fprint(w, `Navigation`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `<a href="/">`)
	fmt.Fprint(w, `<span>Dashboard</span>`)
	fmt.Fprint(w, `</a>`)
	fmt.Fprint(w, `<a href="/about">`)
	fmt.Fprint(w, `<span>About</span>`)
	fmt.Fprint(w, `</a>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `<div style="flex:1;gap:16px;padding:24px;display:flex;flex-direction:column">`)
	fmt.Fprint(w, `<div style="align-items:center;gap:12px;display:flex;flex-direction:row">`)
	fmt.Fprint(w, `<button id="s1">`)
	fmt.Fprint(w, `<span>`)
	fmt.Fprint(w, html.EscapeString(fmt.Sprint(ternary(sidebarOpen, "Close Menu", "Open Menu"))))
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `</button>`)
	fmt.Fprint(w, `<span style="font-size:20px;font-weight:bold">`)
	fmt.Fprint(w, `Hello, World!`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `<span>`)
	fmt.Fprint(w, `Welcome to the dashboard!`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `<script>
let state = {sidebarOpen: false};
function $u0() { document.getElementById("s0").style.display = state.sidebarOpen ? "" : "none"; }
document.getElementById("s1").addEventListener("click", function() {
    state.sidebarOpen = !state.sidebarOpen;
    $u0();
});
$u0();
</script>`)
	fmt.Fprint(w, `</body></html>`)
}

func handleAbout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	sidebarOpen := false
	_ = sidebarOpen
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>About</title></head><body>`)
	fmt.Fprint(w, `<div style="gap:12px;padding:24px;display:flex;flex-direction:column">`)
	fmt.Fprint(w, `<span style="font-size:24px;font-weight:bold">`)
	fmt.Fprint(w, `About`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `<span>`)
	fmt.Fprint(w, `This is a server-rendered SNGL app.`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `<a href="/">`)
	fmt.Fprint(w, `<span>Back to Dashboard</span>`)
	fmt.Fprint(w, `</a>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</body></html>`)
}

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	fmt.Fprintf(os.Stderr, "listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, Handler()))
}
