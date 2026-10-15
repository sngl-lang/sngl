package docbrowser

//go:generate go tool sngl generate --allow-eval=go:duckfam.us/sngl/docs --allow-eval=go:duckfam.us/sngl/docs/lookup docbrowser.sngl --lang=go --platform=bubbletea --opt package=docbrowser
//go:generate go tool sngl generate --allow-eval=go:duckfam.us/sngl/docs --allow-eval=go:duckfam.us/sngl/docs/lookup docbrowser.sngl --lang=go --platform=html --opt package=docbrowser --opt noCacheBust=true
