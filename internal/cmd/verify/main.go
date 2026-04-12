// Command verify runs project checks and tests, reporting per-package coverage.
//
// Steps:
//  1. go generate ./...
//  2. go fmt ./...
//  3. go fix ./...
//  4. go vet ./...
//  5. go test -cover ./...
//
// The -dry flag skips file-mutating steps: generate is skipped entirely,
// fmt and fix run in check-only mode (reporting differences without writing),
// and SNGL_FMT_DOCS is not set for tests.
//
// Usage: go tool verify [-v] [-dry]
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var coverRE = regexp.MustCompile(`^ok\s+(\S+)\s+\S+\s+coverage:\s+([\d.]+)%\s+of\s+statements`)
var failRE = regexp.MustCompile(`^FAIL\s+(\S+)`)

type pkgResult struct {
	name     string
	coverage float64
	failed   bool
}

func main() {
	verbose := flag.Bool("v", false, "pass -v to go test")
	dry := flag.Bool("dry", false, "skip file-mutating steps")
	flag.Parse()

	log.SetFlags(0)

	// Step 1: go generate (skip in dry mode)
	if !*dry {
		if !runStep("generate", "go", "generate", "./...") {
			os.Exit(1)
		}
	}

	// Step 2: go fmt
	if *dry {
		if !runCheckStep("fmt", "gofmt", "-l", ".") {
			os.Exit(1)
		}
	} else {
		if !runStep("fmt", "go", "fmt", "./...") {
			os.Exit(1)
		}
	}

	// Step 3: go fix
	if *dry {
		if !runCheckStep("fix", "go", "fix", "-diff", "./...") {
			os.Exit(1)
		}
	} else {
		if !runStep("fix", "go", "fix", "./...") {
			os.Exit(1)
		}
	}

	// Step 4: go vet
	if !runStep("vet", "go", "vet", "./...") {
		os.Exit(1)
	}

	// Step 5: go test with coverage
	runTests(*verbose, !*dry)
}

func runStep(name string, command string, args ...string) bool {
	fmt.Printf(">>> %s %s\n", command, strings.Join(args, " "))
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("%s failed: %v", name, err)
		return false
	}
	return true
}

// runCheckStep runs a command and fails if it produces any stdout output,
// indicating that files need changes.
func runCheckStep(name string, command string, args ...string) bool {
	fmt.Printf(">>> %s %s\n", command, strings.Join(args, " "))
	cmd := exec.Command(command, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if buf.Len() > 0 {
		fmt.Print(buf.String())
		log.Printf("%s: files need changes", name)
		return false
	}
	if err != nil {
		log.Printf("%s failed: %v", name, err)
		return false
	}
	return true
}

func runTests(verbose, fmtDocs bool) {
	args := []string{"test", "-cover"}
	if verbose {
		args = append(args, "-v")
	}
	args = append(args, "./...")

	fmt.Printf(">>> go %s\n", strings.Join(args, " "))

	cmd := exec.Command("go", args...)
	if fmtDocs {
		cmd.Env = append(os.Environ(), "SNGL_FMT_DOCS=1")
	}
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	var results []pkgResult
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)

		if m := coverRE.FindStringSubmatch(line); m != nil {
			pct, _ := strconv.ParseFloat(m[2], 64)
			results = append(results, pkgResult{name: m[1], coverage: pct})
		} else if m := failRE.FindStringSubmatch(line); m != nil {
			results = append(results, pkgResult{name: m[1], failed: true})
		}
	}

	cmdErr := cmd.Wait()

	fmt.Println()
	fmt.Println("=== Coverage Summary ===")

	var totalPct float64
	var count int
	for _, r := range results {
		if r.failed {
			fmt.Printf("  %-60s FAIL\n", shortPkg(r.name))
		} else if r.coverage > 0 {
			fmt.Printf("  %-60s %5.1f%%\n", shortPkg(r.name), r.coverage)
			totalPct += r.coverage
			count++
		}
	}

	if count > 0 {
		fmt.Println()
		fmt.Printf("average coverage: %.1f%% across %d packages\n", totalPct/float64(count), count)
	}

	if cmdErr != nil {
		os.Exit(1)
	}
}

func shortPkg(full string) string {
	const prefix = "git.duckfam.us/jonathan/sngl/"
	if strings.HasPrefix(full, prefix) {
		s := full[len(prefix):]
		if s == "" {
			return "."
		}
		return s
	}
	return full
}
