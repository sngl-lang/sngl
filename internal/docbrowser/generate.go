package docbrowser

//go:generate go tool sngl generate --allow-eval=go:git.duckfam.us/jonathan/sngl/docs --allow-eval=go:git.duckfam.us/jonathan/sngl/docs/lookup docbrowser.sngl --lang=go --platform=bubbletea --opt package=docbrowser
//go:generate go tool sngl generate --allow-eval=go:git.duckfam.us/jonathan/sngl/docs --allow-eval=go:git.duckfam.us/jonathan/sngl/docs/lookup docbrowser.sngl --lang=go --platform=html --opt package=docbrowser --opt noCacheBust=true
