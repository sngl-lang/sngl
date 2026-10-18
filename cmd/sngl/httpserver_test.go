package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// testHTTPAddr is where a script's program makes its requests: a fixed
// address, since the address is in the program and a recorded run's digest
// covers the program, so a port picked per run would ask for every record
// again. A script writes it as http://127.0.0.1:47819.
const testHTTPAddr = "127.0.0.1:47819"

var startTestHTTPOnce sync.Once

// startTestHTTP serves the routes a script's requests ask, for the life of
// the test binary. A launch answered from its record makes no request, so a
// failure to listen fails only a script that runs one for real.
func startTestHTTP(t *testing.T) {
	startTestHTTPOnce.Do(func() {
		l, err := net.Listen("tcp", testHTTPAddr)
		if err != nil {
			t.Logf("test HTTP server not started: %v", err)
			return
		}
		go func() { _ = http.Serve(l, http.HandlerFunc(testHTTPHandler)) }()
	})
}

// The routes:
//
//	/pokemon/{name}  a Pokémon as PokéAPI writes one, or a 404
//	/echo            what the request was: method, query, body, X-Sngl header
//	/status/{code}   that status, with "status {code}" as the body
//
// Every answer carries the CORS headers a page served from elsewhere needs
// to read it, and a preflight is answered for any method and header.
func testHTTPHandler(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "*")
	h.Set("Access-Control-Allow-Headers", "*")
	h.Set("Access-Control-Expose-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/pokemon/"):
		name := strings.TrimPrefix(path, "/pokemon/")
		mon, ok := testPokemon[name]
		h.Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		io.WriteString(w, mon)
	case path == "/echo":
		body, _ := io.ReadAll(r.Body)
		h.Set("Content-Type", "application/json")
		h.Set("X-Sngl-Echo", "yes")
		out, _ := json.Marshal(map[string]string{
			"method": r.Method,
			"query":  r.URL.RawQuery,
			"body":   string(body),
			"header": r.Header.Get("X-Sngl"),
		})
		w.Write(out)
	case strings.HasPrefix(path, "/status/"):
		code, err := strconv.Atoi(strings.TrimPrefix(path, "/status/"))
		if err != nil {
			code = http.StatusBadRequest
		}
		w.WriteHeader(code)
		io.WriteString(w, "status "+strconv.Itoa(code))
	default:
		http.NotFound(w, r)
	}
}

var testPokemon = map[string]string{
	"bulbasaur": `{"id": 1, "name": "bulbasaur", "base_experience": 64, "height": 7, "weight": 69,
		"sprites": {"front_default": "https://example.com/1.png", "front_shiny": null},
		"types": [{"slot": 1, "type": {"name": "grass", "url": "https://pokeapi.co/api/v2/type/12/"}},
		          {"slot": 2, "type": {"name": "poison", "url": "https://pokeapi.co/api/v2/type/4/"}}]}`,
	"mr mime": `{"id": 122, "name": "mr-mime", "base_experience": null, "height": 13, "weight": 545,
		"sprites": {"front_default": null, "front_shiny": null},
		"types": [{"slot": 1, "type": {"name": "psychic", "url": "https://pokeapi.co/api/v2/type/14/"}}]}`,
}
