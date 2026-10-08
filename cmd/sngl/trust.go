package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/trust"
	"duckfam.us/sngl/ir"
)

// cliTrust is what this invocation may run of the project's own code: the
// grants its flags, SNGL_ALLOW and the user's config file hold.
var cliTrust *trust.Policy

// allowKinds are the per-call grants, each a repeatable --allow-<kind>.
var allowKinds = []trust.Kind{trust.Eval, trust.Command, trust.Env, trust.File, trust.Dir, trust.Net}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringArray("allow-eval", nil, `build and run an evaluated package's pure functions: "go:<import path>" or "js:<module>"`)
	pf.StringArray("allow-command", nil, `let a plugin run a command: "<plugin>=<command prefix>"`)
	pf.StringArray("allow-env", nil, `let a plugin read an environment variable: "<plugin>=<name>"`)
	pf.StringArray("allow-file", nil, `let a plugin read a file outside the import root: "<plugin>=<path>"`)
	pf.StringArray("allow-dir", nil, `let a plugin read a directory outside the import root: "<plugin>=<path>"`)
	pf.StringArray("allow-net", nil, `let an import fetch from a host: "<host>", or "*.<domain>" for its subdomains`)
	pf.Bool("allow-all", false, "trust the project's code with everything, for this invocation only")
	rootCmd.AddCommand(trustCmd)
}

// setupTrust builds cliTrust and takes SNGL_ALLOW out of the environment, so
// no process this one starts -- the go command, node, the evaluator, a
// plugin's command -- can hand the grants to a nested build in some other
// project.
func setupTrust(cmd *cobra.Command) error {
	env, hadEnv := os.LookupEnv(trust.EnvVar)
	if hadEnv {
		os.Unsetenv(trust.EnvVar)
	}
	p := &trust.Policy{ConfigPath: trust.ConfigPath()}
	if p.ConfigPath != "" {
		if err := p.Load(p.ConfigPath); err != nil {
			return fmt.Errorf("reading the trust config: %w", err)
		}
	}
	envGrants, err := trust.ParseEnv(env)
	if err != nil {
		return err
	}
	p.Add(envGrants...)
	flagGrants, all, err := allowFlags(cmd)
	if err != nil {
		return err
	}
	p.Add(flagGrants...)
	if all {
		p.SetAll()
	}
	if isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stderr.Fd()) {
		p.Prompt = terminalPrompt{}
	}
	cliTrust = p
	return nil
}

func allowFlags(cmd *cobra.Command) ([]trust.Grant, bool, error) {
	var out []trust.Grant
	for _, k := range allowKinds {
		vs, _ := cmd.Flags().GetStringArray("allow-" + string(k))
		for _, v := range vs {
			g, err := trust.ParseFlag(k, v)
			if err != nil {
				return nil, false, err
			}
			out = append(out, g)
		}
	}
	all, _ := cmd.Flags().GetBool("allow-all")
	return out, all, nil
}

// terminalPrompt asks at the terminal the build was started from.
type terminalPrompt struct{}

func (terminalPrompt) Ask(r trust.Request, origin trust.Origin) trust.Answer {
	w := os.Stderr
	switch r.Kind {
	case trust.Eval:
		fmt.Fprintf(w, "sngl: building and running %s at compile time, as native code\n", r.Subject.Name)
	case trust.Command:
		fmt.Fprintf(w, "sngl: %s asks to run: %s\n", r.Subject.Name, trust.ShellQuote(r.Cmd))
		bans := "(none)"
		if len(r.BanFlags) > 0 {
			bans = strings.Join(r.BanFlags, " ")
		}
		fmt.Fprintf(w, "  declared prefix: %s   banned flags: %s\n", trust.ShellQuote(r.Prefix), bans)
	case trust.Env:
		fmt.Fprintf(w, "sngl: %s asks to read $%s\n", r.Subject.Name, r.Value)
	case trust.File:
		fmt.Fprintf(w, "sngl: %s asks to read %s, outside %s\n", r.Subject.Name, r.Value, r.Root)
	case trust.Dir:
		fmt.Fprintf(w, "sngl: %s asks to list %s, outside %s\n", r.Subject.Name, r.Value, r.Root)
	case trust.Net:
		fmt.Fprintf(w, "sngl: an import fetches from %s over the network\n", r.Value)
	}
	fmt.Fprintf(w, "  from: %s\n", origin.Spec)
	fmt.Fprintf(w, "Allow? [o]nce, [a]lways (recorded in %s), [N]o: ", trust.ConfigPath())
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "o", "once":
		return trust.Once
	case "a", "always":
		return trust.Always
	}
	return trust.No
}

// cliResolver resolves a package's imports with what this invocation may
// fetch.
func cliResolver(dir string) *build.Resolver {
	r := build.NewResolver(dir)
	r.Trust = cliTrust
	return r
}

// printWarnings writes each warning once, in the form the checker's take.
func printWarnings(ws []ir.Diagnostic, seen map[string]bool) {
	for _, w := range ws {
		msg := w.Error()
		if seen[msg] {
			continue
		}
		seen[msg] = true
		fmt.Fprintln(os.Stderr, "warning: "+msg)
	}
}

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Record what the project's code may run, in the user's config",
	Long: `Records the grants given as --allow-* flags in the user's config file, so a
later build has them without asking. Each is resolved to where the code comes
from -- its directory, or the module version go.sum pins -- and recorded by
that, never by its import path: an import path is a name a repository chooses.
A plugin's import path is relative to the current directory, which should be
the program's import root.

--allow-all is for one invocation and is never recorded.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := trust.ConfigPath()
		if path == "" {
			return errors.New("there is no user config directory to record grants in")
		}
		list, _ := cmd.Flags().GetBool("list")
		remove, _ := cmd.Flags().GetString("remove")
		switch {
		case list:
			allows, err := trust.ReadConfig(path)
			if err != nil {
				return err
			}
			for i, a := range allows {
				fmt.Printf("%d  %s\n", i+1, a)
			}
			return nil
		case remove != "":
			removed, err := trust.Remove(path, remove)
			if err != nil {
				return err
			}
			for _, a := range removed {
				fmt.Printf("removed %s  %s\n", remove, a)
			}
			return nil
		}
		if all, _ := cmd.Flags().GetBool("allow-all"); all {
			return errors.New("--allow-all is never recorded: it trusts everything, and is for one invocation")
		}
		grants, _, err := allowFlags(cmd)
		if err != nil {
			return err
		}
		if len(grants) == 0 {
			return errors.New("nothing to record: give an --allow-* flag, --list or --remove")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		everywhere, _ := cmd.Flags().GetBool("everywhere")
		var record []trust.Grant
		for _, g := range grants {
			if everywhere {
				if g.Kind != trust.Net || g.Subject != "" {
					return errors.New("--everywhere records only --allow-net=<host>: every other grant names the code it trusts")
				}
				g.Source = ""
				record = append(record, g)
				continue
			}
			if g.Kind == trust.Net && g.Subject == "" {
				// The project in the current directory, which is what writes
				// the imports.
				g.Subject = "."
			}
			origin, err := resolveOrigin(cwd, g)
			if err != nil {
				return err
			}
			g.Subject, g.Module, g.Digest = origin.Spec, origin.Module, origin.Digest
			if g.Kind == trust.File || g.Kind == trust.Dir {
				g.Value = trust.Canonical(g.Value)
			}
			g.Source = ""
			record = append(record, g)
		}
		lines, err := trust.Record(path, record)
		if err != nil {
			return err
		}
		fmt.Printf("recorded in %s:\n", path)
		for _, l := range lines {
			fmt.Println(l)
		}
		return nil
	},
}

func init() {
	trustCmd.Flags().Bool("list", false, "list the recorded grants, numbered")
	trustCmd.Flags().String("remove", "", "remove a recorded grant, by its number or its origin")
	trustCmd.Flags().Bool("everywhere", false, "record --allow-net for every project rather than the one in the current directory")
}

// resolveOrigin is where the code a grant names comes from, seen from dir.
func resolveOrigin(dir string, g trust.Grant) (trust.Origin, error) {
	if trust.IsOrigin(g.Subject) {
		return trust.Origin{Spec: g.Subject}, nil
	}
	if p, ok := strings.CutPrefix(g.Subject, "go:"); ok {
		return optimize.GoOrigin(dir, p)
	}
	if p, ok := strings.CutPrefix(g.Subject, "js:"); ok {
		return optimize.JSOrigin(dir, p)
	}
	if strings.Contains(g.Subject, ":") {
		return trust.Origin{}, fmt.Errorf("%s: a fetched package is recorded by the build's prompt, which knows its content; name it by its origin here", g.Subject)
	}
	return trust.DirOrigin(filepath.Join(dir, filepath.FromSlash(g.Subject))), nil
}
