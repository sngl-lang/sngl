package testharness

// CanonicalKeys returns the closed set of key names accepted by t.key()
// across all platforms. Each platform's runner translates these names to
// its native key code or message type. Names use PascalCase and follow
// the W3C UI Events "key" attribute where possible.
func CanonicalKeys() []string {
	out := make([]string, 0, len(canonicalKeySet))
	for k := range canonicalKeySet {
		out = append(out, k)
	}
	return out
}

// IsCanonicalKey reports whether name is a member of the canonical key set.
func IsCanonicalKey(name string) bool {
	_, ok := canonicalKeySet[name]
	return ok
}

var canonicalKeySet = func() map[string]struct{} {
	keys := []string{
		"Enter", "Tab", "Escape", "Space", "Backspace", "Delete",
		"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
		"Home", "End", "PageUp", "PageDown",
	}
	for c := 'A'; c <= 'Z'; c++ {
		keys = append(keys, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		keys = append(keys, string(c))
	}
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}()
