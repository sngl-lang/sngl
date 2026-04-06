// Command docsgen builds the documentation site with platform snapshots.
//
// Usage: go tool docsgen [-out _site] [-http :3580]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

func main() {
	outDir := flag.String("out", "_site", "output directory")
	httpAddr := flag.String("http", "", "start HTTP server after build (e.g., :3580)")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("docsgen: ")

	// Compile website.sngl → output files.
	if err := compileSNGL("website.sngl", *outDir); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Site built in %s/\n", *outDir)

	if *httpAddr != "" {
		fmt.Printf("Serving on http://localhost%s\n", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, http.FileServer(http.Dir(*outDir))))
	}
}

func compileSNGL(filename, outDir string) error {
	cmd := exec.Command("go", "tool", "sngl", "compile", "--out", outDir, filename)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
