package docbrowser

import (
	"fmt"
	"html"
	"net/http"

	"git.duckfam.us/jonathan/sngl/docs"
)

var _ = fmt.Sprint
var _ = html.EscapeString

// Handler returns an http.Handler that serves all routes.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("GET /{name}", handleDetail)
	return mux
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	selectedIndex := 0
	_ = selectedIndex
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>SNGL Documentation</title></head><body>`)
	fmt.Fprint(w, `<div style="display:flex;flex-direction:row">`)
	{
		homeHref := "/"
		_ = homeHref
		fmt.Fprint(w, `<div style="gap:4px;padding:8px;width:260px;display:flex;flex-direction:column">`)
		fmt.Fprintf(w, `<a href="%s">`, html.EscapeString(fmt.Sprint(homeHref)))
		fmt.Fprint(w, `<span>SNGL</span>`)
		fmt.Fprint(w, `</a>`)
		fmt.Fprint(w, `<hr>`)
		for index, comp := range docs.Components() {
			_ = index
			_ = comp
			{
				href := ("/" + comp.Name)
				_ = href
				text := comp.Name
				_ = text
				active := false
				_ = active
				fmt.Fprintf(w, `<a href="%s">`, html.EscapeString(fmt.Sprint(href)))
				fmt.Fprint(w, `<span>`)
				fmt.Fprint(w, html.EscapeString(fmt.Sprint(text)))
				fmt.Fprint(w, `</span>`)
				fmt.Fprint(w, `</a>`)
			}
		}
		fmt.Fprint(w, `</div>`)
	}
	fmt.Fprint(w, `<div style="gap:16px;padding:24px;display:flex;flex-direction:column">`)
	fmt.Fprint(w, `<span style="font-size:24px;font-weight:bold">`)
	fmt.Fprint(w, `SNGL Documentation`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `<span>`)
	fmt.Fprint(w, `Select a component from the sidebar.`)
	fmt.Fprint(w, `</span>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</body></html>`)
}

func handleDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := r.PathValue("name")
	_ = name
	selectedIndex := 0
	_ = selectedIndex
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Component — SNGL Docs</title></head><body>`)
	fmt.Fprint(w, `<div style="display:flex;flex-direction:row">`)
	{
		homeHref := "/"
		_ = homeHref
		fmt.Fprint(w, `<div style="gap:4px;padding:8px;width:260px;display:flex;flex-direction:column">`)
		fmt.Fprintf(w, `<a href="%s">`, html.EscapeString(fmt.Sprint(homeHref)))
		fmt.Fprint(w, `<span>SNGL</span>`)
		fmt.Fprint(w, `</a>`)
		fmt.Fprint(w, `<hr>`)
		for index, comp := range docs.Components() {
			_ = index
			_ = comp
			{
				href := ("/" + comp.Name)
				_ = href
				text := comp.Name
				_ = text
				active := (comp.Name == name)
				_ = active
				fmt.Fprintf(w, `<a href="%s">`, html.EscapeString(fmt.Sprint(href)))
				fmt.Fprint(w, `<span>`)
				fmt.Fprint(w, html.EscapeString(fmt.Sprint(text)))
				fmt.Fprint(w, `</span>`)
				fmt.Fprint(w, `</a>`)
			}
		}
		fmt.Fprint(w, `</div>`)
	}
	fmt.Fprint(w, `<div style="padding:24px;display:flex;flex-direction:column">`)
	{
		name := name
		_ = name
		fmt.Fprint(w, `<div style="gap:12px;display:flex;flex-direction:column">`)
		fmt.Fprint(w, `<span style="font-size:24px;font-weight:bold">`)
		fmt.Fprint(w, html.EscapeString(fmt.Sprint(docs.Lookup(name).Name)))
		fmt.Fprint(w, `</span>`)
		{
			value := docs.Lookup(name).Tier
			_ = value
			var variant string
			_ = variant
			var size string
			_ = size
			fmt.Fprint(w, `<span style="border-radius:12px;display:inline-block;padding:2px 8px">`)
			fmt.Fprint(w, `<span>`)
			fmt.Fprint(w, html.EscapeString(fmt.Sprint(value)))
			fmt.Fprint(w, `</span>`)
			fmt.Fprint(w, `</span>`)
		}
		fmt.Fprint(w, `<span>`)
		fmt.Fprint(w, html.EscapeString(fmt.Sprint(docs.Lookup(name).Doc)))
		fmt.Fprint(w, `</span>`)
		fmt.Fprint(w, `<span style="color:#888">`)
		fmt.Fprint(w, html.EscapeString(fmt.Sprint(("Children: " + docs.Lookup(name).Children))))
		fmt.Fprint(w, `</span>`)
		fmt.Fprint(w, `</div>`)
	}
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</div>`)
	fmt.Fprint(w, `</body></html>`)
}
