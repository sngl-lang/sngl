package sngl

// Generate todo example screenshots for the website.
// Run with: go generate

//go:generate go tool sngl snapshot --out public/snapshots/todo examples/todo/todo.sngl
