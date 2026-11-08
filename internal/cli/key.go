package cli

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/lint"
)

// runGPG runs gpg with the caller's environment (GNUPGHOME included) and returns stdout.
func runGPG(inv *invocation, args ...string) ([]byte, error) {
	cmd := exec.Command("gpg", append([]string{"--batch"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Env = &stdout, &stderr, append(os.Environ(), inv.env...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gpg %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// firstFpr returns the first fpr: record of gpg --with-colons output.
func firstFpr(colons []byte) string {
	for _, ln := range strings.Split(string(colons), "\n") {
		if f := strings.Split(ln, ":"); f[0] == "fpr" && len(f) > 9 {
			return f[9]
		}
	}
	return ""
}

// ponytail: the config is decoded to a map and re-encoded, so comments are lost and keys come out sorted; upgrade to an editing TOML library if that matters.
func readConfig(root string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(root, lint.ConfigPath))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: no %s; run zk-chef init first", root, lint.ConfigPath)
	}
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", lint.ConfigPath, err)
	}
	return cfg, nil
}

// pinnedOf returns pinned_fingerprints from a decoded config; an absent key is empty.
func pinnedOf(cfg map[string]any) ([]string, error) {
	list, ok := cfg["pinned_fingerprints"].([]any)
	if _, present := cfg["pinned_fingerprints"]; present && !ok {
		return nil, fmt.Errorf("%s: pinned_fingerprints must be a list of strings", lint.ConfigPath)
	}
	pins := []string{}
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: pinned_fingerprints must be a list of strings", lint.ConfigPath)
		}
		pins = append(pins, s)
	}
	return pins, nil
}

func writeConfig(root string, cfg map[string]any) error {
	b, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, lint.ConfigPath), b, 0o644)
}

// keyFlags re-parses inv.args so --json and --root also work after the subcommand and its argument.
func keyFlags(inv *invocation) bool {
	fs := flag.NewFlagSet("zk-chef key", flag.ContinueOnError)
	fs.SetOutput(inv.stderr)
	fs.BoolVar(&inv.json, "json", inv.json, "print one JSON object")
	fs.StringVar(&inv.root, "root", inv.root, "vault root")
	var positional []string
	for rest := inv.args; ; {
		if fs.Parse(rest) != nil {
			return false
		}
		if fs.NArg() == 0 {
			break
		}
		positional, rest = append(positional, fs.Arg(0)), fs.Args()[1:]
	}
	inv.args = positional
	return true
}

func keyVerb(inv *invocation) int {
	if !keyFlags(inv) {
		return 2
	}
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef key: "+err.Error())
	}
	cfg, err := readConfig(root)
	if err != nil {
		return fail(inv, err.Error())
	}
	pins, err := pinnedOf(cfg)
	if err != nil {
		return fail(inv, err.Error())
	}
	var sub string
	if len(inv.args) > 0 {
		sub = inv.args[0]
	}
	switch {
	case sub == "add" && len(inv.args) <= 2:
		return keyAdd(inv, root, cfg, pins)
	case sub == "list" && len(inv.args) == 1:
		return keyList(inv, root, pins)
	case sub == "rm" && len(inv.args) == 2:
		return keyRm(inv, root, cfg, pins)
	}
	return fail(inv, "usage: zk-chef key add [FPR] | list | rm FPR")
}

func keyAdd(inv *invocation, root string, cfg map[string]any, pins []string) int {
	var fpr string
	if len(inv.args) > 1 {
		colons, err := runGPG(inv, "--with-colons", "--fingerprint", "--", inv.args[1])
		if err != nil {
			return fail(inv, "zk-chef key add: "+err.Error())
		}
		fpr = lint.NormFpr(firstFpr(colons))
	} else {
		colons, err := runGPG(inv, "--with-colons", "--list-secret-keys")
		if err != nil {
			return fail(inv, "zk-chef key add: "+err.Error())
		}
		if fpr = lint.NormFpr(firstFpr(colons)); fpr == "" {
			return fail(inv, "no secret key; pass a fingerprint")
		}
	}
	armor, err := runGPG(inv, "--armor", "--export", "--", fpr)
	if err != nil {
		return fail(inv, "zk-chef key add: "+err.Error())
	}
	file := "keys/" + fpr + ".asc"
	if err := os.MkdirAll(filepath.Join(root, "keys"), 0o755); err != nil {
		return fail(inv, "zk-chef key add: "+err.Error())
	}
	if err := os.WriteFile(filepath.Join(root, file), armor, 0o644); err != nil {
		return fail(inv, "zk-chef key add: "+err.Error())
	}
	if !slices.ContainsFunc(pins, func(p string) bool { return lint.NormFpr(p) == fpr }) {
		cfg["pinned_fingerprints"] = append(pins, fpr)
		if err := writeConfig(root, cfg); err != nil {
			return fail(inv, "zk-chef key add: "+err.Error())
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK          bool   `json:"ok"`
			Fingerprint string `json:"fingerprint"`
			File        string `json:"file"`
		}{true, fpr, file})
		return 0
	}
	fmt.Fprintf(inv.stdout, "added %s %s\n", fpr, file)
	return 0
}

func keyRm(inv *invocation, root string, cfg map[string]any, pins []string) int {
	fpr := lint.NormFpr(inv.args[1])
	kept := slices.DeleteFunc(slices.Clone(pins), func(p string) bool { return lint.NormFpr(p) == fpr })
	if len(kept) == len(pins) {
		return fail(inv, "not pinned: "+fpr)
	}
	cfg["pinned_fingerprints"] = kept
	if err := writeConfig(root, cfg); err != nil {
		return fail(inv, "zk-chef key rm: "+err.Error())
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK          bool   `json:"ok"`
			Fingerprint string `json:"fingerprint"`
		}{true, fpr})
		return 0
	}
	fmt.Fprintf(inv.stdout, "removed %s\n", fpr)
	return 0
}

// keyList prints each pin with its keys/ file, then the keys/ files no pin names.
func keyList(inv *invocation, root string, pins []string) int {
	entries, err := os.ReadDir(filepath.Join(root, "keys"))
	if err != nil && !os.IsNotExist(err) {
		return fail(inv, "zk-chef key list: "+err.Error())
	}
	unpinned := map[string]bool{} // keys/ file name -> no pin names it yet
	byFpr := map[string]string{}
	for _, e := range entries {
		if e.Type().IsRegular() {
			unpinned[e.Name()] = true
			byFpr[lint.NormFpr(strings.TrimSuffix(e.Name(), ".asc"))] = e.Name()
		}
	}
	type pin struct {
		Fingerprint string  `json:"fingerprint"`
		File        *string `json:"file"`
	}
	listed := []pin{}
	for _, p := range pins {
		fpr := lint.NormFpr(p)
		if name, ok := byFpr[fpr]; ok {
			delete(unpinned, name)
			file := "keys/" + name
			listed = append(listed, pin{fpr, &file})
		} else {
			listed = append(listed, pin{fpr, nil})
		}
	}
	stray := []string{}
	for _, e := range entries {
		if unpinned[e.Name()] {
			stray = append(stray, "keys/"+e.Name())
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK            bool     `json:"ok"`
			Pinned        []pin    `json:"pinned"`
			UnpinnedFiles []string `json:"unpinned_files"`
		}{true, listed, stray})
		return 0
	}
	for _, p := range listed {
		if p.File != nil {
			fmt.Fprintf(inv.stdout, "pinned  %s  %s\n", p.Fingerprint, *p.File)
		} else {
			fmt.Fprintf(inv.stdout, "pinned  %s  (no file)\n", p.Fingerprint)
		}
	}
	for _, s := range stray {
		fmt.Fprintf(inv.stdout, "unpinned file %s\n", s)
	}
	return 0
}
