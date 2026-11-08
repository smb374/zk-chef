// Package cli is the zk-chef entry point: Run dispatches verbs.
package cli

import (
	"encoding/json"
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
	yes            bool   // -y, for init and install
	noCommit       bool   // --no-commit, for review
	title          string // --title, for ingest
}

// handlers maps implemented verbs to their code; any other verb is a stub.
var handlers = map[string]func(*invocation) int{
	"init":    initVerb,
	"install": installVerb,
	"lint":    lintVerb,
	"fix":     fixVerb,
	"key":     keyVerb,
	"ingest":  ingestVerb,
	"review":  reviewVerb,
	"status":  statusVerb,
}

// verbFlags registers the flags a verb takes besides --json.
var verbFlags = map[string]func(*flag.FlagSet, *invocation){
	"init":    initFlags,
	"install": installFlags,
	"lint":    lintFlags,
	"fix":     fixFlags,
	"key":     rootFlag,
	"ingest":  ingestFlags,
	"review":  reviewFlags,
	"status":  rootFlag,
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
	if !ungated[verb] && !gate(stderr) {
		return 2
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
	if h, ok := handlers[verb]; ok {
		return h(inv)
	}
	return notImplemented(inv)
}

func notImplemented(inv *invocation) int {
	fmt.Fprintf(inv.stderr, "zk-chef %s: not implemented\n", inv.verb)
	if inv.json {
		b, _ := json.Marshal(struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}{false, "not implemented"})
		fmt.Fprintf(inv.stdout, "%s\n", b)
	}
	return 2
}

func usage(stderr io.Writer) int {
	fmt.Fprintf(stderr, "usage: zk-chef <verb> [--json] [args]\nverbs: %s\n       zk-chef --version\n", strings.Join(verbs, ", "))
	return 2
}
