package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/diff"
	"github.com/smb374/zk-chef/internal/version"
)

const hookPath = ".githooks/pre-commit"

// ownedFile is a file the scaffold owns, with the bytes the scaffold wants.
type ownedFile struct {
	path string
	want string
}

// ownedFiles lists what upgrade may write: the embedded zk config, templates and hook, then
// AGENTS.md with its zk-chef block brought up to date.
func ownedFiles(scaffold fs.FS, root string) ([]ownedFile, error) {
	var owned []ownedFile
	err := fs.WalkDir(scaffold, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if p != ".zk/config.toml" && p != hookPath && !strings.HasPrefix(p, ".zk/templates/") {
			return nil
		}
		want, err := fs.ReadFile(scaffold, p)
		owned = append(owned, ownedFile{p, string(want)})
		return err
	})
	if err != nil {
		return nil, err
	}
	agents, err := agentsWant(scaffold, root)
	return append(owned, ownedFile{"AGENTS.md", agents}), err
}

// agentsWant returns AGENTS.md as it should be: the block replaces the old block span, or
// is appended (or created) when the file has no markers. Text outside the span stays.
func agentsWant(scaffold fs.FS, root string) (string, error) {
	next, write, err := planAgents(scaffold, root)
	if err != nil || write {
		return string(next), err
	}
	block, err := fs.ReadFile(scaffold, "AGENTS.md")
	if err != nil {
		return "", err
	}
	have, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return "", err
	}
	span := func(s string) (int, int) {
		return strings.Index(s, agentsOpen), strings.Index(s, agentsClose) + len(agentsClose)
	}
	bi, bj := span(string(block))
	hi, hj := span(string(have))
	return string(have)[:hi] + string(block)[bi:bj] + string(have)[hj:], nil
}

// upgradeConfig adds the keys the embedded config has and the vault config lacks, with the
// embedded defaults, and sets min_version when the binary has one. It writes the config only
// when that changes it, and reports whether it did.
func upgradeConfig(root string, scaffold fs.FS, cfg map[string]any, minVersion *string) (bool, error) {
	raw, err := fs.ReadFile(scaffold, configPath)
	if err != nil {
		return false, err
	}
	var defaults map[string]any
	if err := toml.Unmarshal(raw, &defaults); err != nil {
		return false, fmt.Errorf("parse embedded %s: %v", configPath, err)
	}
	changed := false
	for k, v := range defaults {
		if _, ok := cfg[k]; !ok {
			cfg[k], changed = v, true
		}
	}
	if minVersion != nil && cfg["min_version"] != *minVersion {
		cfg["min_version"], changed = *minVersion, true
	}
	if !changed {
		return false, nil
	}
	return true, writeConfig(root, cfg)
}

// commitUpgrade stages and commits paths only, so other staged changes stay staged. It
// reports false, and commits nothing, when the paths now equal HEAD.
func commitUpgrade(inv *invocation, root string, paths []string) (bool, error) {
	if err := gitRun(inv, root, append([]string{"add", "--"}, paths...)...); err != nil {
		return false, err
	}
	cmd := exec.Command("git", append([]string{"-C", root, "diff", "--cached", "--quiet", "--"}, paths...)...)
	cmd.Env = commandEnv(inv)
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if exit := (*exec.ExitError)(nil); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return false, err
	}
	msg := "chore: zk-chef upgrade v" + strings.TrimPrefix(version.Current(), "v")
	return true, gitRun(inv, root, append([]string{"commit", "-m", msg, "--"}, paths...)...)
}

func upgradeVerb(inv *invocation) int {
	if inv.json && !inv.yes {
		return fail(inv, "zk-chef upgrade: --json needs -y")
	}
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef upgrade: "+err.Error())
	}
	cfg, err := readConfig(root)
	if err != nil {
		return fail(inv, err.Error())
	}
	if err := gitRun(inv, root, "rev-parse", "--git-dir"); err != nil {
		return fail(inv, root+": not a git repository; run zk-chef init first")
	}
	scaffold, err := fs.Sub(Assets, "scaffold")
	if err != nil {
		return fail(inv, "zk-chef upgrade: "+err.Error())
	}
	owned, err := ownedFiles(scaffold, root)
	if err != nil {
		return fail(inv, "zk-chef upgrade: "+err.Error())
	}
	changed, skipped := []string{}, []string{}
	in := bufio.NewReader(inv.stdin)
	for _, f := range owned {
		target := filepath.Join(root, filepath.FromSlash(f.path))
		have, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			return fail(inv, "zk-chef upgrade: "+err.Error())
		}
		if string(have) == f.want {
			continue
		}
		if !inv.json {
			fmt.Fprint(inv.stdout, diff.Unified(f.path, string(have), f.want))
		}
		if !inv.yes {
			fmt.Fprintf(inv.stdout, "apply %s? [y/N] ", f.path)
			answer, _ := in.ReadString('\n') // EOF leaves answer empty or partial, and counts as no
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				skipped = append(skipped, f.path)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fail(inv, "zk-chef upgrade: "+err.Error())
		}
		if err := os.WriteFile(target, []byte(f.want), 0o644); err != nil {
			return fail(inv, "zk-chef upgrade: "+err.Error())
		}
		if f.path == hookPath {
			if err := os.Chmod(target, 0o755); err != nil {
				return fail(inv, "zk-chef upgrade: "+err.Error())
			}
		}
		changed = append(changed, f.path)
		if !inv.json {
			fmt.Fprintf(inv.stdout, "updated %s\n", f.path)
		}
	}
	var minVersion *string
	if v := version.Current(); version.Valid(v) {
		minVersion = &v
	} else {
		fmt.Fprintln(inv.stderr, "warning: dev build; min_version not set")
	}
	updated, err := upgradeConfig(root, scaffold, cfg, minVersion)
	if err != nil {
		return fail(inv, "zk-chef upgrade: "+err.Error())
	}
	if updated {
		changed = append(changed, configPath)
		if !inv.json {
			fmt.Fprintf(inv.stdout, "updated %s\n", configPath)
		}
	}
	committed := false
	if len(changed) > 0 {
		if committed, err = commitUpgrade(inv, root, changed); err != nil {
			return fail(inv, "zk-chef upgrade: "+err.Error())
		}
	}
	switch {
	case inv.json:
		writeJSON(inv.stdout, struct {
			OK         bool     `json:"ok"`
			Changed    []string `json:"changed"`
			Skipped    []string `json:"skipped"`
			Committed  bool     `json:"committed"`
			MinVersion *string  `json:"min_version"`
		}{true, changed, skipped, committed, minVersion})
	case len(changed) == 0 && len(skipped) == 0:
		fmt.Fprintln(inv.stdout, "already up to date")
	}
	return 0
}
