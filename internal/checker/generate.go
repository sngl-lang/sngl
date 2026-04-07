package checker

// Generate stdlib example screenshots for each platform.
// Run with: go generate ./internal/checker/...

//go:generate go tool sngl snapshot examples --stdlib --platform=html
//go:generate go tool sngl snapshot examples --stdlib --platform=bubbletea
//go:generate go tool sngl snapshot examples --stdlib --platform=fyne
//go:generate go tool sngl snapshot examples --stdlib --platform=android
