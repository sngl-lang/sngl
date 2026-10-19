package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
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
//	/dot.png                a 2x2 PNG, for an image a program fetches
//	/api/v1/pets/           a page of names, `limit` (default 20) from `offset`
//	/api/v1/pets/{name}/    a pet as the petstore of openapi_runs.txt declares one, or a 404
//	/pokeapi/api/v2/…       PokéAPI as examples/pokedex reads it: a page of
//	                        testPokemonNames, and each Pokémon, species and
//	                        evolution chain recorded in testdata/pokeapi
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
	case path == "/pokeapi/api/v2/pokemon/":
		h.Set("Content-Type", "application/json")
		w.Write(testPage(r, "/pokeapi/api/v2/pokemon/", testPokemonNames))
	case strings.HasPrefix(path, "/pokeapi/api/v2/"):
		h.Set("Content-Type", "application/json")
		body, err := testPokeAPI.ReadFile("testdata/pokeapi/" + strings.TrimSuffix(strings.TrimPrefix(path, "/pokeapi/api/v2/"), "/") + ".json")
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		w.Write(body)
	case path == "/api/v1/pets/":
		h.Set("Content-Type", "application/json")
		w.Write(testPage(r, "/api/v1/pets/", testPetNames))
	case strings.HasPrefix(path, "/api/v1/pets/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/pets/"), "/")
		pet, ok := testPetDetail[name]
		h.Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		io.WriteString(w, pet)
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
	case path == "/dot.png":
		h.Set("Content-Type", "image/png")
		w.Write(testDotPNG)
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

// testDotPNG is a 2x2 opaque red PNG.
var testDotPNG = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{R: 0xff, A: 0xff}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}()

var testPokemon = map[string]string{
	"bulbasaur": `{"id": 1, "name": "bulbasaur", "base_experience": 64, "height": 7, "weight": 69,
		"sprites": {"front_default": "https://example.com/1.png", "front_shiny": null},
		"types": [{"slot": 1, "type": {"name": "grass", "url": "https://pokeapi.co/api/v2/type/12/"}},
		          {"slot": 2, "type": {"name": "poison", "url": "https://pokeapi.co/api/v2/type/4/"}}]}`,
	"mr mime": `{"id": 122, "name": "mr-mime", "base_experience": null, "height": 13, "weight": 545,
		"sprites": {"front_default": null, "front_shiny": null},
		"types": [{"slot": 1, "type": {"name": "psychic", "url": "https://pokeapi.co/api/v2/type/14/"}}]}`,
}

// testPokemonNames are what /pokeapi/api/v2/pokemon/ pages through.
var testPokemonNames = []string{"bulbasaur", "ivysaur", "venusaur", "charmander", "charmeleon"}

// testPokeAPI is PokéAPI's answers for the Pokémon testPokemonNames names,
// recorded from https://pokeapi.co and trimmed: each long list cut to its
// first entries, every link pointed at /pokeapi on this server, and every
// sprite at /dot.png, so nothing a script runs reaches the network.
//
//go:embed testdata/pokeapi
var testPokeAPI embed.FS

// testPage is the page of names r asks for from the collection at path, its
// next and previous links absolute, or null at either end.
func testPage(r *http.Request, path string, names []string) []byte {
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
	}{Count: len(names), Results: []summary{}}
	base := "http://" + testHTTPAddr + path
	link := func(off int) *string {
		s := base + "?offset=" + strconv.Itoa(off) + "&limit=" + strconv.Itoa(limit)
		return &s
	}
	for i := offset; i < offset+limit && i < len(names); i++ {
		page.Results = append(page.Results, summary{names[i], base + names[i] + "/"})
	}
	if offset+limit < len(names) {
		page.Next = link(offset + limit)
	}
	if offset > 0 {
		page.Previous = link(max(0, offset-limit))
	}
	out, _ := json.Marshal(page)
	return out
}

// testPetNames are what /api/v1/pets/ pages through.
var testPetNames = []string{"rex", "bella", "milo", "luna", "coco"}

var testPetDetail = map[string]string{
	"rex": `{"id": 1, "name": "rex", "adoption_fee": 120, "age": 3, "is_available": true,
		"breed": {"name": "beagle", "url": "https://petstore.example/api/v1/pet-breeds/1/"},
		"photos": {"thumbnail_url": "https://example.com/1.png"},
		"measurements": [{"base_value": 45, "margin": 0, "measure": {"name": "weight", "url": "https://petstore.example/api/v1/measures/1/"}}],
		"tags": [{"slot": 1, "type": {"name": "friendly", "url": "https://petstore.example/api/v1/tags/12/"}},
		         {"slot": 2, "type": {"name": "small", "url": "https://petstore.example/api/v1/tags/4/"}}]}`,
}
