package checker

// Generate stdlib example screenshots for each platform.
// Run with: go generate ./internal/checker/...

//go:generate sngl snapshot examples --stdlib --platform=html --force
//go:generate sngl snapshot examples --stdlib --platform=bubbletea --force
//go:generate sngl snapshot examples --stdlib --platform=fyne --force
