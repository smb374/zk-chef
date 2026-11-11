// Package cli is the zk-chef entry point: Run dispatches verbs.
package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/smb374/zk-chef/internal/version"
	"github.com/urfave/cli/v3"
)

// rootEnv names the env var that sets the vault root when --root is absent.
const rootEnv = "ZK_CHEF_ROOT"

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
	generator      string // --generator, for export
}

// verb describes one verb: its help text, its flags and its code.
type verb struct {
	name, usage, argsUsage string
	root                   bool              // takes --root
	flags                  func() []cli.Flag // fresh flags per command, so no state outlives one Run
	run                    func(*invocation) int
}

func boolFlag(name, usage string) cli.Flag {
	return &cli.BoolFlag{Name: name, Usage: usage}
}

// usageLine opens every usage message.
const usageLine = "usage: zk-chef <verb> [--json] [args]"

// verbs lists every verb in usage order.
var verbs = []verb{
	{name: "init", usage: "scaffold a new vault", argsUsage: "[DIR]", run: initVerb,
		flags: func() []cli.Flag { return []cli.Flag{boolFlag("y", "accept defaults; init never prompts")} }},
	{name: "install", usage: "install zk and the agent skills", run: installVerb,
		flags: func() []cli.Flag {
			return []cli.Flag{
				boolFlag("y", "download zk without asking"),
				boolFlag("skills-only", "write the skills and nothing else; self-update runs the new binary this way"),
			}
		}},
	{name: "self-update", usage: "replace this binary with the latest release", run: selfUpdateVerb},
	{name: "upgrade", usage: "bring the vault's scaffold files up to this binary's version", root: true, run: upgradeVerb,
		flags: func() []cli.Flag { return []cli.Flag{boolFlag("y", "apply every change without asking")} }},
	{name: "doctor", usage: "check the vault and its tools", root: true, run: doctorVerb},
	{name: "key", usage: "manage pinned reviewer keys", argsUsage: "add [FPR] | list | rm FPR", root: true, run: keyVerb},
	{name: "ingest", usage: "ingest a raw file or URL as a source note", argsUsage: "PATH|URL", root: true, run: ingestVerb,
		flags: func() []cli.Flag { return []cli.Flag{&cli.StringFlag{Name: "title", Usage: "note title (required)"}} }},
	{name: "lint", usage: "check the vault against the rules", root: true, run: lintVerb,
		flags: func() []cli.Flag {
			return []cli.Flag{boolFlag("staged", "check the git index instead of the worktree")}
		}},
	{name: "fix", usage: "rewrite stale link aliases on a note's Hubs line and in its References, and on every line of other kinds", root: true, run: fixVerb},
	{name: "review", usage: "sign a source's review, or list the review queue", argsUsage: "[SRC-ID reviewed|disputed]", root: true, run: reviewVerb,
		flags: func() []cli.Flag { return []cli.Flag{boolFlag("no-commit", "sign the note but do not commit")} }},
	{name: "status", usage: "summarize the vault", root: true, run: statusVerb},
	{name: "export", usage: "write a copy of the vault for a static site generator", argsUsage: "DIR", root: true, run: exportVerb,
		flags: func() []cli.Flag {
			return []cli.Flag{&cli.StringFlag{Name: "generator", Usage: "shape the output for a site generator: hugo (default: generic)"}}
		}},
}

// verbNames lists the verb names in usage order.
func verbNames() []string {
	names := make([]string, len(verbs))
	for i, v := range verbs {
		names[i] = v.name
	}
	return names
}

// envValue returns the last value of key in env (KEY=value entries), or "".
func envValue(env []string, key string) string {
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// command turns a verb into a urfave command. The action stores the exit code in *code.
func (v verb) command(base invocation, code *int) *cli.Command {
	flags := []cli.Flag{boolFlag("json", "print one JSON object")}
	if v.flags != nil {
		flags = append(flags, v.flags()...)
	}
	if v.root {
		flags = append(flags, &cli.StringFlag{Name: "root", Usage: "vault root (default: $" + rootEnv + ", else the current directory)"})
	}
	return &cli.Command{
		Name:            v.name,
		Usage:           v.usage,
		ArgsUsage:       v.argsUsage,
		UsageText:       "zk-chef " + v.name + " [flags] " + v.argsUsage,
		Flags:           flags,
		HideHelpCommand: true,
		OnUsageError:    func(_ context.Context, _ *cli.Command, err error, _ bool) error { return err },
		Action: func(_ context.Context, cmd *cli.Command) error {
			inv := base
			inv.verb = v.name
			inv.args = cmd.Args().Slice()
			inv.json = cmd.Bool("json")
			inv.yes = cmd.Bool("y")
			inv.skillsOnly = cmd.Bool("skills-only")
			inv.staged = cmd.Bool("staged")
			inv.noCommit = cmd.Bool("no-commit")
			inv.title = cmd.String("title")
			inv.generator = cmd.String("generator")
			if v.root {
				inv.root = cmd.String("root")
				if inv.root == "" {
					inv.root = envValue(inv.env, rootEnv)
				}
				if inv.root == "" {
					inv.root = "."
				}
			}
			if !ungated[v.name] && !gate(inv.root, inv.stderr) {
				*code = 2
				return nil
			}
			*code = v.run(&inv)
			return nil
		},
	}
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
	if name := args[0]; !slices.Contains(verbNames(), name) && !slices.Contains([]string{"help", "-h", "--help"}, name) {
		return usage(stderr)
	}
	code := 0
	base := invocation{stdin: stdin, stdout: stdout, stderr: stderr, env: env}
	root := &cli.Command{
		Name:           "zk-chef",
		Usage:          "maintain an LLM-written Zettelkasten vault",
		UsageText:      "zk-chef <verb> [flags] [args]\n   zk-chef <verb> --help\n   zk-chef --version",
		Writer:         stdout,
		ErrWriter:      stderr,
		OnUsageError:   func(_ context.Context, _ *cli.Command, err error, _ bool) error { return err },
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	for _, v := range verbs {
		root.Commands = append(root.Commands, v.command(base, &code))
	}
	if err := root.Run(context.Background(), append([]string{"zk-chef"}, args...)); err != nil {
		hint := "zk-chef --help"
		if slices.Contains(verbNames(), args[0]) {
			hint = "zk-chef " + args[0] + " --help"
		}
		fmt.Fprintf(stderr, "zk-chef: %v\n%s; run %s\n", err, usageLine, hint)
		return 2
	}
	return code
}

func usage(stderr io.Writer) int {
	fmt.Fprintf(stderr, usageLine+"\nverbs: %s\n       zk-chef --version\n       zk-chef <verb> --help\n", strings.Join(verbNames(), ", "))
	return 2
}
