package checker

// Generate stdlib example screenshots for each platform.
// Run with: go generate ./internal/checker/...

//go:generate go tool sngl snapshot --platform=html stdlib
//go:generate go tool sngl snapshot --platform=bubbletea stdlib
//go:generate go tool sngl snapshot --platform=fyne stdlib
//go:generate go tool sngl snapshot --platform=android stdlib
