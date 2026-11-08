package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/version"
)

// Assets holds the embedded "scaffold" and "skills" trees; main sets it.
var Assets fs.FS

var vaultFolders = []string{"notes", "hubs", "sources", "projects", "raw", "keys"}

func initFlags(fs *flag.FlagSet, inv *invocation) {
	fs.BoolVar(&inv.yes, "y", false, "accept defaults; init never prompts")
}

// copyMissing copies files from src to dst without overwriting. It returns the created
// paths and the paths whose existing file differs from the source, both slash-separated.
func copyMissing(src fs.FS, dst string, skip string) (created, differing []string, err error) {
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == skip {
			return err
		}
		want, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		have, err := os.ReadFile(target)
		if err == nil {
			if !bytes.Equal(have, want) {
				differing = append(differing, p)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// embed.FS reports every mode as 0444, so keep only the execute bits.
		if err := os.WriteFile(target, want, 0o644|info.Mode()&0o111); err != nil {
			return err
		}
		created = append(created, p)
		return nil
	})
	return created, differing, err
}

func initVerb(inv *invocation) int {
	dir := "."
	if len(inv.args) > 0 {
		dir = inv.args[0]
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	scaffold, err := fs.Sub(Assets, "scaffold")
	if err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	agents, writeAgents, err := planAgents(scaffold, dir)
	if err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := git(inv, dir, "init", "-q", dir); err != nil {
			return fail(inv, "zk-chef init: "+err.Error())
		}
		if !inv.json {
			fmt.Fprintln(inv.stdout, "git init")
		}
	}
	created, differing, err := copyMissing(scaffold, dir, "AGENTS.md")
	if err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	if writeAgents {
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), agents, 0o644); err != nil {
			return fail(inv, "zk-chef init: "+err.Error())
		}
		created = append(created, "AGENTS.md")
	}
	for _, f := range vaultFolders {
		if err := os.MkdirAll(filepath.Join(dir, f), 0o755); err != nil {
			return fail(inv, "zk-chef init: "+err.Error())
		}
	}
	hook := filepath.Join(dir, ".githooks", "pre-commit")
	if st, err := os.Stat(hook); err == nil {
		if err := os.Chmod(hook, st.Mode()|0o111); err != nil {
			return fail(inv, "zk-chef init: "+err.Error())
		}
	}
	minVersion, err := setMinVersion(inv, dir)
	if err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	if err := git(inv, dir, "-C", dir, "config", "core.hooksPath", ".githooks"); err != nil {
		return fail(inv, "zk-chef init: "+err.Error())
	}
	if _, err := exec.LookPath("zk"); err != nil {
		fmt.Fprintln(inv.stderr, "zk not found; run zk-chef install")
	}
	if inv.json {
		var mv *string
		if minVersion != "" {
			mv = &minVersion
		}
		if created == nil {
			created = []string{}
		}
		writeJSON(inv.stdout, struct {
			OK         bool     `json:"ok"`
			Dir        string   `json:"dir"`
			Created    []string `json:"created"`
			HooksPath  bool     `json:"hooks_path"`
			MinVersion *string  `json:"min_version"`
		}{true, dir, created, true, mv})
		return 0
	}
	for _, p := range created {
		fmt.Fprintf(inv.stdout, "created %s\n", p)
	}
	for _, p := range differing {
		fmt.Fprintf(inv.stdout, "kept your %s (differs from the scaffold)\n", p)
	}
	return 0
}

const (
	agentsOpen  = "<!-- zk-chef -->"
	agentsClose = "<!-- /zk-chef -->"
)

// planAgents decides what AGENTS.md holds after init. It returns the new bytes and whether
// to write them. An existing file with one well-formed marker pair stays as it is; any other
// marker layout is an error.
func planAgents(scaffold fs.FS, dir string) ([]byte, bool, error) {
	block, err := fs.ReadFile(scaffold, "AGENTS.md")
	if err != nil {
		return nil, false, err
	}
	target := filepath.Join(dir, "AGENTS.md")
	have, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return block, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	text := string(have)
	opens, closes := strings.Count(text, agentsOpen), strings.Count(text, agentsClose)
	switch {
	case opens == 0 && closes == 0:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return append([]byte(text+"\n"), block...), true, nil
	case opens == 1 && closes == 1 && strings.Index(text, agentsOpen) < strings.Index(text, agentsClose):
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("%s: malformed %s ... %s markers; fix or remove them", target, agentsOpen, agentsClose)
}

// setMinVersion appends min_version = "<binary version>" to the vault config when the key
// is absent, keeping the existing text. It returns the version it wrote, or "" when it wrote
// none; a binary without a semantic version writes none and warns.
func setMinVersion(inv *invocation, dir string) (string, error) {
	target := filepath.Join(dir, configPath)
	have, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	var cfg map[string]any
	if err := toml.Unmarshal(have, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %v", target, err)
	}
	if _, ok := cfg["min_version"]; ok {
		return "", nil
	}
	v := version.Current()
	if v == "dev" {
		fmt.Fprintln(inv.stderr, "warning: dev build; min_version not set")
		return "", nil
	}
	if !version.Valid(v) {
		fmt.Fprintf(inv.stderr, "warning: version %q is not a semantic version; min_version not set\n", v)
		return "", nil
	}
	text := string(have)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return v, os.WriteFile(target, []byte(text+fmt.Sprintf("min_version = %q\n", v)), 0o644)
}

// git runs git with args in the verb's environment.
func git(inv *invocation, dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = inv.env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %v: %s", args, err, out)
	}
	return nil
}
