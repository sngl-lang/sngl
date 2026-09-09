package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

var pkgCmd = &cobra.Command{
	Use:   "pkg",
	Short: "Manage remote package dependencies",
}

var pkgDownloadCmd = &cobra.Command{
	Use:   "download [file|dir...]",
	Short: "Populate the cache with every remote dependency referenced by the given mains",
	Args:  cobra.ArbitraryArgs,
	RunE:  runPkgDownload,
}

var pkgUpdateCmd = &cobra.Command{
	Use:   "update [file|dir...]",
	Short: "Refresh each remote dependency's cache and print its new integrity hash",
	Args:  cobra.ArbitraryArgs,
	RunE:  runPkgUpdate,
}

var pkgCacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Inspect or modify the shared SNGL cache",
}

var pkgCacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove the entire SNGL cache directory",
	Args:  cobra.NoArgs,
	RunE:  runPkgCacheClear,
}

func init() {
	pkgCacheClearCmd.Flags().BoolP("yes", "y", false, "skip confirmation prompt")
	pkgCacheCmd.AddCommand(pkgCacheClearCmd)
	pkgCmd.AddCommand(pkgDownloadCmd)
	pkgCmd.AddCommand(pkgUpdateCmd)
	pkgCmd.AddCommand(pkgCacheCmd)
	rootCmd.AddCommand(pkgCmd)
}

// Records every FS-scheme URI resolved, de-duplicated and in discovery order,
// so `pkg update` can iterate them afterwards.
type capturingResolver struct {
	*build.Resolver
	mu     sync.Mutex
	seen   map[string]bool
	schema []schemeRef
}

type schemeRef struct {
	scheme string
	uri    string
}

func newCapturingResolver(dir string) *capturingResolver {
	return &capturingResolver{
		Resolver: build.NewResolver(dir),
		seen:     map[string]bool{},
	}
}

func (r *capturingResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	r.mu.Lock()
	key := scheme + ":" + uri
	if !r.seen[key] {
		r.seen[key] = true
		r.schema = append(r.schema, schemeRef{scheme: scheme, uri: uri})
	}
	r.mu.Unlock()
	return r.Resolver.ResolveSchemeFS(scheme, uri, dir)
}

// The caches are populated as a side effect of the checker's transitive import
// resolution. A checker error is logged rather than fatal: a bad hash on one
// dep must not prevent downloading the others.
func walkMains(units []unit, resolver checker.ImportResolver, dir string) {
	langs, plats := build.RegisteredTargets()
	for _, u := range units {
		doc, err := u.doc()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", u.name, err)
			continue
		}
		_, diags := checker.Check(doc, &checker.Config{
			FS:        os.DirFS(u.dir),
			Dir:       u.dir,
			IsMain:    true,
			Resolver:  resolver,
			Languages: langs,
			Platforms: plats,
		})
		for _, d := range diags {
			if d.Severity == ir.Error {
				slog.Info("pkg walk diagnostic", "unit", u.name, "msg", d.Error())
			}
		}
	}
}

// pkg commands drive from programs only: a library package should not trigger
// remote fetches on its own — whatever it imports, the program importing it
// imports too.
//
// A program is a package that declares a window, which is build.Emit's rule.
// Answered from the parse rather than the check -- a type check for this alone
// would double the work -- so the window is recognised by the name written and
// not by the `#[builtin]` mark only the checker holds. A package declaring its
// own `component window` is read as a program here and is not one; the cost is
// a fetch nobody needed.
func mainUnits(units []unit) ([]unit, error) {
	var mains []unit
	for _, u := range units {
		doc, err := u.doc()
		if err != nil {
			// Not this command's to report: `check` says so with a position.
			continue
		}
		if declaresWindow(doc) {
			mains = append(mains, u)
		}
	}
	return mains, nil
}

func declaresWindow(doc *ast.Document) bool {
	if doc == nil {
		return false
	}
	for _, stmt := range doc.Stmts {
		vn, ok := stmt.(*ast.VisualNode)
		if !ok {
			continue
		}
		switch t := vn.Target.(type) {
		case *ast.IdentExpr:
			if t.Name == "window" {
				return true
			}
		case *ast.SelectExpr:
			if t.Field == "window" {
				return true
			}
		}
	}
	return false
}

func runPkgDownload(cmd *cobra.Command, args []string) error {
	units, err := resolveUnits(args)
	if err != nil {
		return err
	}
	mains, err := mainUnits(units)
	if err != nil {
		return err
	}
	if len(mains) == 0 {
		return fmt.Errorf("no main files found in %v", args)
	}
	resolver := newCapturingResolver(".")
	walkMains(mains, resolver, ".")

	if len(resolver.schema) == 0 {
		fmt.Println("no remote dependencies")
		return nil
	}
	for _, s := range resolver.schema {
		fmt.Printf("cached %s://%s\n", s.scheme, s.uri)
	}
	return nil
}

func runPkgUpdate(cmd *cobra.Command, args []string) error {
	units, err := resolveUnits(args)
	if err != nil {
		return err
	}
	mains, err := mainUnits(units)
	if err != nil {
		return err
	}
	if len(mains) == 0 {
		return fmt.Errorf("no main files found in %v", args)
	}
	resolver := newCapturingResolver(".")
	// First pass seeds the cache so we know every URL the project references.
	walkMains(mains, resolver, ".")

	if len(resolver.schema) == 0 {
		fmt.Println("no remote dependencies")
		return nil
	}
	var failed bool
	for _, s := range resolver.schema {
		imp := codegen.LookupFSScheme(s.scheme)
		updater, ok := imp.(codegen.FSSchemeUpdater)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s://%s: scheme does not support update\n", s.scheme, s.uri)
			failed = true
			continue
		}
		newHash, err := updater.Refresh(s.uri, ".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s://%s: %s\n", s.scheme, s.uri, err)
			failed = true
			continue
		}
		fmt.Printf("%s://%s  new hash: %s\n", s.scheme, s.uri, newHash)
	}
	if failed {
		return fmt.Errorf("one or more dependencies failed to update")
	}
	return nil
}

func runPkgCacheClear(cmd *cobra.Command, args []string) error {
	dir := codegen.SnglCacheDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Printf("cache empty (%s does not exist)\n", dir)
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		fmt.Fprintf(os.Stderr, "remove entire SNGL cache at %s? [y/N] ", dir)
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" {
			fmt.Println("aborted")
			return nil
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	fmt.Printf("removed %s\n", dir)
	return nil
}
