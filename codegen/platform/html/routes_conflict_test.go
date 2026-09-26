package html

import (
	"net/http"
	"testing"
)

// routesConflict answers what ServeMux would, so it is asked beside the mux
// itself: a pair conflicts exactly when registering both panics.
func TestRoutesConflictAgreesWithServeMux(t *testing.T) {
	paths := []string{
		"/", "/a", "/a/", "/b", "/p/{a}", "/p/{b}", "/{b}/q", "/p/q",
		"/{x}", "/{x}/", "/p/{a}/r", "/p/{a...}", "/{$}", "/a/{$}",
	}
	muxConflicts := func(p, q string) (conflict bool) {
		defer func() { conflict = recover() != nil }()
		mux := http.NewServeMux()
		h := func(http.ResponseWriter, *http.Request) {}
		mux.HandleFunc("GET "+p, h)
		mux.HandleFunc("GET "+q, h)
		return false
	}
	for _, p := range paths {
		for _, q := range paths {
			if got, want := routesConflict(p, q), muxConflicts(p, q); got != want {
				t.Errorf("routesConflict(%q, %q) = %v; ServeMux says %v", p, q, got, want)
			}
		}
	}
}
