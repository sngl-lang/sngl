// Package testhttp is the server the programs `sngl test` runs make their
// requests of: the script harness's (cmd/sngl/script_test.go) and `go tool
// verify`'s, each at Addr.
package testhttp

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Addr is fixed, since the address is in the program and a recorded run's
// digest covers the program, so a port picked per run would ask for every
// record again. A program writes it as http://127.0.0.1:47819.
const Addr = "127.0.0.1:47819"

// Start serves the routes for the life of the process, reading each
// example's recorded answers from the repository at root.
//
//	/books/{title}          a book from testBooks, or a 404
//	/echo                   what the request was: method, query, body, X-Sngl header
//	/status/{code}          that status, with "status {code}" as the body
//	/dot.png                a 2x2 PNG, for an image a program fetches
//	/api/v1/pets/           a page of names, `limit` (default 20) from `offset`
//	/api/v1/pets/{name}/    a pet as the petstore of openapi_runs.txt declares one, or a 404
//	/examples/{name}/{path} examples/{name}/testdata/http/{path}, as exampleFile names it
//
// Every answer carries the CORS headers a page served from elsewhere needs
// to read it, and a preflight is answered for any method and header.
func Start(root string) error {
	l, err := net.Listen("tcp", Addr)
	if err != nil {
		return err
	}
	examples := os.DirFS(filepath.Join(root, "examples"))
	go func() {
		_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(examples, w, r) }))
	}()
	return nil
}

func serve(examples fs.FS, w http.ResponseWriter, r *http.Request) {
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
	case strings.HasPrefix(path, "/examples/"):
		name, body, err := exampleFile(examples, strings.TrimPrefix(path, "/examples/"), r.URL.Query())
		if err != nil {
			h.Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
			h.Set("Content-Type", t)
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
	case strings.HasPrefix(path, "/books/"):
		book, ok := testBooks[strings.TrimPrefix(path, "/books/")]
		h.Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"detail": "Not found."}`)
			return
		}
		io.WriteString(w, book)
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

var testBooks = map[string]string{
	"dune": `{"id": 1, "title": "dune", "page_count": 412, "year": 1965, "edition": 3,
		"covers": {"front_url": "https://example.com/1.png", "back_url": null},
		"genres": [{"rank": 1, "type": {"name": "fiction", "url": "https://example.com/genres/12/"}},
		           {"rank": 2, "type": {"name": "adventure", "url": "https://example.com/genres/4/"}}]}`,
	"war and peace": `{"id": 122, "title": "war-and-peace", "page_count": null, "year": 1869, "edition": 1,
		"covers": {"front_url": null, "back_url": null},
		"genres": [{"rank": 1, "type": {"name": "history", "url": "https://example.com/genres/14/"}}]}`,
}

// exampleFile answers /examples/x/api/items/?offset=2&limit=2 from
// x/testdata/http/api/items@limit=2&offset=2.json, and /examples/x/logo.png
// from x/testdata/http/logo.png.
func exampleFile(examples fs.FS, rest string, query url.Values) (string, []byte, error) {
	example, p, _ := strings.Cut(rest, "/")
	name := example + "/testdata/http/" + strings.TrimSuffix(p, "/")
	if len(query) > 0 {
		name += "@" + query.Encode()
	}
	if body, err := fs.ReadFile(examples, name); err == nil {
		return name, body, nil
	}
	body, err := fs.ReadFile(examples, name+".json")
	return name + ".json", body, err
}

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
	base := "http://" + Addr + path
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
