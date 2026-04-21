package docbrowser

//go:generate go tool sngl compile docbrowser.sngl --lang=go --platform=bubbletea --opt package=docbrowser

// HTTP codegen disabled: http platform is a v2 not-ported stub (see codegen/platform/http/http.go:15).
// Re-enable once the platform is ported:
// go:generate go tool sngl compile docbrowser.sngl --lang=go --platform=http --opt package=docbrowser
