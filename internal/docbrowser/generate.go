package docbrowser

//go:generate go tool sngl generate docbrowser.sngl --lang=go --platform=bubbletea --opt package=docbrowser
//go:generate go tool sngl generate docbrowser.sngl --lang=go --platform=html --opt package=docbrowser --opt noCacheBust=true
