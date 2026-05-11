// Command verify runs project checks and tests, reporting per-package coverage.
//
// Steps:
//  1. go generate ./...
//  2. go fmt ./...
//  3. go fix ./...
//  4. go vet ./...
//  5. go test -coverpkg=./... -coverprofile=... ./...
//
// Step 5 collects cross-package coverage so packages exercised by integration
// tests (e.g. codegen/lang/* through codegen/platform/*) are credited for the
// statements they execute, not just statements covered by their own package's
// tests.
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
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var failRE = regexp.MustCompile(`^FAIL\s+(\S+)`)

type pkgResult struct {
	name     string
	stmts    int
	covered  int
	failed   bool
	hadTests bool
}

func (r pkgResult) coverage() float64 {
	if r.stmts == 0 {
		return 0
	}
	return 100 * float64(r.covered) / float64(r.stmts)
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

	// Step 6: SNGL test matrix across every TestRunner-capable platform
	// whose probe says it can run on this host. Unavailable platforms are
	// skipped, not failed.
	//
	// --opt goModExtra="replace git.duckfam.us/jonathan/sngl => <root>"
	// points temp Go modules built by platform test runners (today: fyne)
	// at the project root, so generated code that imports
	// `git.duckfam.us/jonathan/sngl/pkg/go/*` resolves locally instead of
	// failing on the public proxy.
	root, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	replaceDirective := fmt.Sprintf("replace git.duckfam.us/jonathan/sngl => %s", root)
	if !runStep("sngl-test", "go", "tool", "sngl", "test",
		"--platform=all", "--opt", "goModExtra="+replaceDirective, "./...") {
		os.Exit(1)
	}
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
	profile, err := os.CreateTemp("", "sngl-verify-cover-*.out")
	if err != nil {
		log.Fatalf("create coverprofile: %v", err)
	}
	profile.Close()
	defer os.Remove(profile.Name())

	args := []string{"test", "-coverpkg=./...", "-coverprofile=" + profile.Name()}
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

	failed := map[string]bool{}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)
		if m := failRE.FindStringSubmatch(line); m != nil {
			failed[m[1]] = true
		}
	}

	cmdErr := cmd.Wait()

	results, err := readProfile(profile.Name())
	if err != nil {
		log.Printf("coverage profile: %v", err)
	}
	for name := range failed {
		found := false
		for i := range results {
			if results[i].name == name {
				results[i].failed = true
				found = true
			}
		}
		if !found {
			results = append(results, pkgResult{name: name, failed: true})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })

	fmt.Println()
	fmt.Println("=== Coverage Summary ===")

	var totalStmts, totalCovered int
	var count int
	for _, r := range results {
		switch {
		case r.failed:
			fmt.Printf("  %-60s FAIL\n", shortPkg(r.name))
		case r.stmts == 0:
			// No statements counted (e.g. interface-only or empty package).
			continue
		default:
			fmt.Printf("  %-60s %5.1f%%  (%d/%d)\n", shortPkg(r.name), r.coverage(), r.covered, r.stmts)
			totalStmts += r.stmts
			totalCovered += r.covered
			count++
		}
	}

	if totalStmts > 0 {
		fmt.Println()
		fmt.Printf("overall coverage: %.1f%% (%d/%d statements across %d packages)\n",
			100*float64(totalCovered)/float64(totalStmts), totalCovered, totalStmts, count)
	}

	if cmdErr != nil {
		os.Exit(1)
	}
}

// readProfile parses a Go coverage profile and returns per-package totals.
// Each profile line has the form
//
//	<pkg>/<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStatements> <count>
//
// With `-coverpkg=./... -coverprofile=p ./...`, every test binary appends a
// view of every covered block, so the same block appears multiple times.
// Dedup keyed by `<pkg>/<file>:<range>` and OR the counts: a block counts as
// covered if any test binary executed it.
func readProfile(path string) ([]pkgResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]*block{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			// Mode line, e.g. "mode: set".
			first = false
			continue
		}
		if line == "" {
			continue
		}
		// Split off the trailing " <stmts> <count>".
		sp := strings.LastIndex(line, " ")
		if sp < 0 {
			continue
		}
		count, err := strconv.Atoi(line[sp+1:])
		if err != nil {
			continue
		}
		head := line[:sp]
		sp2 := strings.LastIndex(head, " ")
		if sp2 < 0 {
			continue
		}
		stmts, err := strconv.Atoi(head[sp2+1:])
		if err != nil {
			continue
		}
		key := head[:sp2] // "<pkg>/<file>:<range>"
		b := blocks[key]
		if b == nil {
			b = &block{stmts: stmts}
			blocks[key] = b
		}
		if count > 0 {
			b.covered = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	type agg struct{ stmts, covered int }
	pkgs := map[string]*agg{}
	for key, b := range blocks {
		before, _, ok := strings.Cut(key, ":")
		if !ok {
			continue
		}
		pkgDir := pkgPathDir(before)
		a := pkgs[pkgDir]
		if a == nil {
			a = &agg{}
			pkgs[pkgDir] = a
		}
		a.stmts += b.stmts
		if b.covered {
			a.covered += b.stmts
		}
	}

	out := make([]pkgResult, 0, len(pkgs))
	for name, a := range pkgs {
		out = append(out, pkgResult{name: name, stmts: a.stmts, covered: a.covered, hadTests: true})
	}
	return out, nil
}

// pkgPathDir returns the package import path for a profile entry. Profile
// lines start with the full file path like "git.duckfam.us/jonathan/sngl/foo/bar.go".
func pkgPathDir(p string) string {
	return path.Dir(p)
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
