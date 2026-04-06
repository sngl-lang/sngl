// Package session provides a simple in-memory session store.
package session

// Greeting returns a personalized greeting.
//
//sngl:pure
func Greeting(name string) string {
	if name == "" {
		return "Welcome, guest!"
	}
	return "Hello, " + name + "!"
}

// IsLoggedIn reports whether the given name is non-empty.
//
//sngl:pure
func IsLoggedIn(name string) bool {
	return name != ""
}
