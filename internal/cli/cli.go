// Package cli is the zk-chef entry point: Run dispatches verbs.
package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/smb374/zk-chef/internal/version"
)

// verbs lists every verb in usage order.
var verbs = []string{"init", "install", "self-update", "upgrade", "doctor", "key", "ingest", "lint", "fix", "review", "status"}

// invocation is what a verb handler receives after flag parsing.
type invocation struct {
	verb           string
	args           []string // positional arguments after flags
	json           bool
	stdin          io.Reader
	stdout, stderr io.Writer
	env            []string
	root           string // --root, for verbs that take it
	staged         bool   // --staged, for lint
	yes            bool   // -y, for init, install and upgrade
	skillsOnly     bool   // --skills-only, for install
	noCommit       bool   // --no-commit, for review
	title          string // --title, for ingest
}

// handlers maps each verb to its code.
var handlers = map[string]func(*invocation) int{
	"init":        initVerb,
	"doctor":      doctorVerb,
	"install":     installVerb,
	"lint":        lintVerb,
	"fix":         fixVerb,
	"key":         keyVerb,
	"ingest":      ingestVerb,
	"review":      reviewVerb,
	"status":      statusVerb,
	"upgrade":     upgradeVerb,
	"self-update": selfUpdateVerb,
}

// verbFlags registers the flags a verb takes besides --json.
var verbFlags = map[string]func(*flag.FlagSet, *invocation){
	"init":    initFlags,
	"doctor":  rootFlag,
	"install": installFlags,
	"lint":    lintFlags,
	"fix":     fixFlags,
	"key":     rootFlag,
	"ingest":  ingestFlags,
	"review":  reviewFlags,
	"status":  rootFlag,
	"upgrade": upgradeFlags,
}

// Run executes one zk-chef invocation and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	if len(args) == 0 {
		return usage(stderr)
	}
	if args[0] == "--version" {
		fmt.Fprintln(stdout, version.Current())
		return 0
	}
	verb := args[0]
	if !slices.Contains(verbs, verb) {
		return usage(stderr)
	}
	fs := flag.NewFlagSet("zk-chef "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, "usage: zk-chef %s [--json] [args]\n", verb) }
	inv := &invocation{verb: verb, stdin: stdin, stdout: stdout, stderr: stderr, env: env}
	fs.BoolVar(&inv.json, "json", false, "print one JSON object")
	if register, ok := verbFlags[verb]; ok {
		register(fs, inv)
	}
	// Flags may follow positional arguments (zk-chef ingest PATH --title T); "--" ends flags.
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		left := fs.Args()
		if len(left) == 0 {
			break
		}
		if len(rest) > len(left) && rest[len(rest)-len(left)-1] == "--" {
			inv.args = append(inv.args, left...)
			break
		}
		inv.args = append(inv.args, left[0])
		rest = left[1:]
	}
	if !ungated[verb] && !gate(inv.root, stderr) {
		return 2
	}
	return handlers[verb](inv)
}

func usage(stderr io.Writer) int {
	fmt.Fprintf(stderr, "usage: zk-chef <verb> [--json] [args]\nverbs: %s\n       zk-chef --version\n", strings.Join(verbs, ", "))
	return 2
}
