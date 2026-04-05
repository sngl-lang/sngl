package purepkg

// Double returns x * 2.
//
//sngl:pure
func Double(x int) int { return x * 2 }

// Greet returns a greeting string.
//
//sngl:pure
func Greet(name string) string { return "Hello, " + name + "!" }

// Item is a test struct for list returns.
type Item struct {
	Name  string
	Value int
}

// GetItems returns a fixed list of items.
//
//sngl:pure
func GetItems() []Item {
	return []Item{
		{Name: "alpha", Value: 1},
		{Name: "beta", Value: 2},
	}
}
