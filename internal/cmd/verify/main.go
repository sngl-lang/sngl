// Command verify runs all project tests and reports per-package coverage.
//
// Each package's coverage reflects how much of its own code is exercised
// by its tests.
//
// Usage: go tool verify [-v]
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

var coverRE = regexp.MustCompile(`^ok\s+(\S+)\s+\S+\s+coverage:\s+([\d.]+)%\s+of\s+statements`)
var failRE = regexp.MustCompile(`^FAIL\s+(\S+)`)

type pkgResult struct {
	name     string
	coverage float64
	failed   bool
}

func main() {
	verbose := flag.Bool("v", false, "pass -v to go test")
	flag.Parse()

	log.SetFlags(0)

	args := []string{"test", "-cover"}
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
