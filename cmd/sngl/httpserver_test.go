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
//	/pokemon/{name}         a Pokémon as PokéAPI writes one, or a 404
//	/echo                   what the request was: method, query, body, X-Sngl header
//	/status/{code}          that status, with "status {code}" as the body
//	/api/v2/pokemon/        a page of names, `limit` (default 20) from `offset`, as PokéAPI pages
//	/api/v2/pokemon/{name}/ a Pokémon as PokéAPI's spec declares one, or a 404
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
	case path == "/api/v2/pokemon/":
		h.Set("Content-Type", "application/json")
		w.Write(testPokemonPage(r))
	case strings.HasPrefix(path, "/api/v2/pokemon/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v2/pokemon/"), "/")
		mon, ok := testPokemonDetail[name]
		h.Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		io.WriteString(w, mon)
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

// testPokemonNames are what /api/v2/pokemon/ pages through.
var testPokemonNames = []string{"bulbasaur", "ivysaur", "venusaur", "charmander", "charmeleon"}

// testPokemonPage is the page r asks for, its next and previous links as
// PokéAPI writes them: absolute, or null at either end.
func testPokemonPage(r *http.Request) []byte {
	limit, offset := 20, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil {
		offset = v
	}
	type summary struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	page := struct {
		Count    int       `json:"count"`
		Next     *string   `json:"next"`
		Previous *string   `json:"previous"`
		Results  []summary `json:"results"`
	}{Count: len(testPokemonNames), Results: []summary{}}
	link := func(off int) *string {
		s := "http://" + testHTTPAddr + "/api/v2/pokemon/?offset=" + strconv.Itoa(off) + "&limit=" + strconv.Itoa(limit)
		return &s
	}
	for i := offset; i < offset+limit && i < len(testPokemonNames); i++ {
		page.Results = append(page.Results, summary{testPokemonNames[i], "http://" + testHTTPAddr + "/api/v2/pokemon/" + testPokemonNames[i] + "/"})
	}
	if offset+limit < len(testPokemonNames) {
		page.Next = link(offset + limit)
	}
	if offset > 0 {
		page.Previous = link(max(0, offset-limit))
	}
	out, _ := json.Marshal(page)
	return out
}

var testPokemonDetail = map[string]string{
	"bulbasaur": `{"id": 1, "name": "bulbasaur", "base_experience": 64, "height": 7, "is_default": true,
		"species": {"name": "bulbasaur", "url": "https://pokeapi.co/api/v2/pokemon-species/1/"},
		"sprites": {"front_default": "https://example.com/1.png"},
		"stats": [{"base_stat": 45, "effort": 0, "stat": {"name": "hp", "url": "https://pokeapi.co/api/v2/stat/1/"}}],
		"types": [{"slot": 1, "type": {"name": "grass", "url": "https://pokeapi.co/api/v2/type/12/"}},
		          {"slot": 2, "type": {"name": "poison", "url": "https://pokeapi.co/api/v2/type/4/"}}]}`,
}
