package main

import (
	"fmt"
	"os"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func main() {
	src, err := os.ReadFile("examples/todo/todo.sngl")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, err = parser.Parse("examples/todo/todo.sngl", src)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
