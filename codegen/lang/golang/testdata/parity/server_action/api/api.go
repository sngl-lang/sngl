package api

// Persist is a go:// native func with no error return. A handler that calls it
// is classified as a server-side action, producing a POST handler whose body
// is lowered through the legacy ContextVar path (r.Context()).
func Persist(n int) int { return n }
