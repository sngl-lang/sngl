package tui

// TokenColor is the terminal color a markup.Token kind read at run time draws
// in, or fallback for a kind with none. It answers as the bubbletea
// generator's tokenColors does for a literal kind, which a test holds it to.
func TokenColor(kind, fallback string) string {
	switch kind {
	case "keyword":
		return "5"
	case "type":
		return "3"
	case "function":
		return "4"
	case "constant", "number":
		return "6"
	case "string":
		return "2"
	case "comment":
		return "8"
	}
	return fallback
}
