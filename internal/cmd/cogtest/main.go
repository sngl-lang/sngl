// Command cogtest runs all project tests and reports coverage.
//
// Usage: go tool cogtest [-v]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var coverRE = regexp.MustCompile(`^ok\s+\S+\s+\S+\s+coverage:\s+([\d.]+)%\s+of\s+statements`)
var noTestRE = regexp.MustCompile(`^\?\s+\S+\s+\[no test files\]`)
var noCoverRE = regexp.MustCompile(`^ok\s+(\S+).*coverage:\s+\[no statements\]`)
var failRE = regexp.MustCompile(`^FAIL\s+(\S+)`)
var skipRE = regexp.MustCompile(`^\s+\S+\s+coverage: 0\.0% of statements`)

type pkgResult struct {
	name     string
	coverage float64
	status   string // "ok", "fail", "skip", "no tests"
}

func main() {
	verbose := flag.Bool("v", false, "pass -v to go test")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("cogtest: ")

	args := []string{"test", "-cover", "-coverpkg=./...", "-count=1"}
	if *verbose {
		args = append(args, "-v")
	}
	args = append(args, "./...")

	cmd := exec.Command("go", args...)
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
			pct, _ := strconv.ParseFloat(m[1], 64)
			name := strings.Fields(line)[1]
			results = append(results, pkgResult{name: name, coverage: pct, status: "ok"})
		} else if noTestRE.MatchString(line) {
			// skip packages with no test files
		} else if noCoverRE.MatchString(line) {
			name := strings.Fields(line)[1]
			results = append(results, pkgResult{name: name, status: "ok"})
		} else if failRE.MatchString(line) {
			name := strings.Fields(line)[1]
			results = append(results, pkgResult{name: name, status: "fail"})
		} else if skipRE.MatchString(line) {
			// packages without test files that show 0.0% coverage
		}
	}

	cmdErr := cmd.Wait()

	// Print summary.
	fmt.Println()
	fmt.Println("=== Coverage Summary ===")

	var totalPct float64
	var covered int
	for _, r := range results {
		if r.status == "fail" {
			fmt.Printf("  %-60s FAIL\n", shortPkg(r.name))
		} else if r.coverage > 0 {
			fmt.Printf("  %-60s %5.1f%%\n", shortPkg(r.name), r.coverage)
			totalPct += r.coverage
			covered++
		}
	}

	if covered > 0 {
		avg := totalPct / float64(covered)
		fmt.Println()
		fmt.Printf("total coverage: %.1f%% of statements\n", avg)
	}

	if cmdErr != nil {
		os.Exit(1)
	}
}

func shortPkg(full string) string {
	const prefix = "git.duckfam.us/jonathan/sngl/"
	if strings.HasPrefix(full, prefix) {
		return full[len(prefix):]
	}
	return full
}
