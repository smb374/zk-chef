package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitConfig reads one git config key of the repo in dir (v1 git_cfg).
func gitConfig(t *testing.T, dir, key string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "config", key).Output()
	return strings.TrimSpace(string(out))
}

// tree maps every file under root, except .git, to its bytes.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInitFreshVault(t *testing.T) {
	env := setupVault(t, "1.2.3", nil)
	got := run(env, "init", "v")
	if got.code != 0 {
		t.Fatalf("init exit %d, stderr %q", got.code, got.stderr)
	}
	v, err := filepath.Abs("v")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".zk/config.toml", ".zk-chef/config.toml", ".githooks/pre-commit", "AGENTS.md", "hubs/000000000000idx0.md"} {
		if _, err := os.Stat(filepath.Join(v, p)); err != nil {
			t.Errorf("missing file %s: %v", p, err)
		}
	}
	for _, f := range []string{"notes", "hubs", "sources", "projects", "raw", "keys"} {
		if st, err := os.Stat(filepath.Join(v, f)); err != nil || !st.IsDir() {
			t.Errorf("missing folder %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(v, ".git")); err != nil {
		t.Errorf("no git repo: %v", err)
	}
	if hp := gitConfig(t, v, "core.hooksPath"); hp != ".githooks" {
		t.Errorf("core.hooksPath = %q, want .githooks", hp)
	}
	hook := filepath.Join(v, ".githooks", "pre-commit")
	if st, err := os.Stat(hook); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("hook not executable: %v %v", st, err)
	}
	b, _ := os.ReadFile(hook)
	if strings.Contains(string(b), "/home") {
		t.Errorf("hook holds /home:\n%s", b)
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "exec")))
		if len(f) > 0 && strings.HasPrefix(f[0], "/") {
			t.Errorf("hook runs absolute path: %q", line)
		}
	}
	if lint := run(env, "lint", "--root", "v"); lint.code != 0 {
		t.Errorf("fresh vault lint exit %d:\n%s%s", lint.code, lint.stdout, lint.stderr)
	}
}
func TestInitAgentsMd(t *testing.T) {
	block, err := os.ReadFile(filepath.Join("..", "..", "scaffold", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	const wellFormed = "intro\n<!-- zk-chef -->\nmy edited block\n<!-- /zk-chef -->\noutro\n"
	tests := []struct {
		name     string
		existing *string // nil: no AGENTS.md
		wantCode int
		want     string // wanted AGENTS.md bytes; ignored when wantCode is 2 (must be unchanged)
	}{
		{"absent", nil, 0, string(block)},
		{"existing without block", ptr("# Mine\n\nkeep me"), 0, "# Mine\n\nkeep me\n\n" + string(block)},
		{"existing with block", ptr(wellFormed), 0, wellFormed},
		{"only open marker", ptr("a\n<!-- zk-chef -->\nb\n"), 2, ""},
		{"only close marker", ptr("a\n<!-- /zk-chef -->\nb\n"), 2, ""},
		{"close before open", ptr("<!-- /zk-chef -->\n<!-- zk-chef -->\n"), 2, ""},
		{"duplicated", ptr(wellFormed + wellFormed), 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.existing != nil {
				files["AGENTS.md"] = *tt.existing
			}
			env := setupVault(t, "1.2.3", files)
			got := run(env, "init")
			if got.code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", got.code, tt.wantCode, got.stderr)
			}
			b, err := os.ReadFile("AGENTS.md")
			if tt.wantCode == 2 {
				if string(b) != *tt.existing {
					t.Errorf("AGENTS.md changed to %q", b)
				}
				if !strings.Contains(got.stderr, "AGENTS.md") {
					t.Errorf("stderr %q does not name AGENTS.md", got.stderr)
				}
				if _, err := os.Stat(".githooks"); err == nil {
					t.Error("malformed AGENTS.md but init changed the vault")
				}
				return
			}
			if err != nil || string(b) != tt.want {
				t.Errorf("AGENTS.md = %q (%v), want %q", b, err, tt.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
func TestInitRerun(t *testing.T) {
	t.Run("changes no file", func(t *testing.T) {
		env := setupVault(t, "1.2.3", map[string]string{"AGENTS.md": "# Mine\n"})
		if got := run(env, "init"); got.code != 0 {
			t.Fatalf("first init exit %d: %s", got.code, got.stderr)
		}
		before := tree(t, ".")
		got := run(env, "init")
		if got.code != 0 {
			t.Fatalf("second init exit %d: %s", got.code, got.stderr)
		}
		after := tree(t, ".")
		for p, b := range before {
			if after[p] != b {
				t.Errorf("%s changed on re-run", p)
			}
		}
		if len(after) != len(before) {
			t.Errorf("file count %d -> %d", len(before), len(after))
		}
		if strings.Contains(got.stdout, "created") {
			t.Errorf("re-run reports creation:\n%s", got.stdout)
		}
	})
	t.Run("keeps user edits and re-sets hooksPath", func(t *testing.T) {
		env := setupVault(t, "dev", nil)
		run(env, "init")
		const edited = "word_count_warn = 1\n"
		if err := os.WriteFile(".zk-chef/config.toml", []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("git", "config", "core.hooksPath", "elsewhere").Run(); err != nil {
			t.Fatal(err)
		}
		got := run(env, "init")
		if b, _ := os.ReadFile(".zk-chef/config.toml"); string(b) != edited {
			t.Errorf("config.toml = %q, want %q", b, edited)
		}
		if strings.Contains(got.stdout, "created") {
			t.Errorf("re-run reports creation:\n%s", got.stdout)
		}
		if !strings.Contains(got.stdout, "kept your .zk-chef/config.toml") {
			t.Errorf("no kept-your line:\n%s", got.stdout)
		}
		if hp := gitConfig(t, ".", "core.hooksPath"); hp != ".githooks" {
			t.Errorf("core.hooksPath = %q, want .githooks", hp)
		}
	})
}
func TestInitMinVersion(t *testing.T) {
	const scaffoldConfig = "scaffold/.zk-chef/config.toml"
	tests := []struct {
		name, binVersion, existing string
		want                       string // wanted min_version line, "" for none
		wantWarning                bool
	}{
		{"release version is written", "1.2.3", "", `min_version = "1.2.3"`, false},
		{"dev writes nothing and warns", "dev", "", "", true},
		{"existing key is kept", "2.0.0", "word_count_warn = 1\nmin_version = \"1.0.0\"\n", `min_version = "1.0.0"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{}
			if tt.existing != "" {
				files[".zk-chef/config.toml"] = tt.existing
			}
			env := setupVault(t, tt.binVersion, files)
			got := run(env, "init", "--json")
			if got.code != 0 {
				t.Fatalf("init exit %d: %s", got.code, got.stderr)
			}
			b, _ := os.ReadFile(".zk-chef/config.toml")
			var lines []string
			for _, l := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(l, "min_version") {
					lines = append(lines, l)
				}
			}
			if tt.want == "" && len(lines) != 0 || tt.want != "" && (len(lines) != 1 || lines[0] != tt.want) {
				t.Errorf("min_version lines %q, want %q\n%s", lines, tt.want, b)
			}
			warning := "warning: dev build; min_version not set"
			if strings.Contains(got.stderr, warning) != tt.wantWarning {
				t.Errorf("stderr %q, want warning %v", got.stderr, tt.wantWarning)
			}
			if !strings.HasPrefix(string(b), tt.existing) {
				t.Errorf("existing text not kept:\n%s", b)
			}
		})
	}
	t.Run("scaffold comments survive the append", func(t *testing.T) {
		env := setupVault(t, "1.2.3", nil)
		run(env, "init")
		want, err := fs.ReadFile(Assets, scaffoldConfig)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(".zk-chef/config.toml")
		if string(got) != string(want)+"min_version = \"1.2.3\"\n" {
			t.Errorf("config.toml = %q", got)
		}
	})
	t.Run("gate accepts the written version", func(t *testing.T) {
		env := setupVault(t, "1.2.3", nil)
		run(env, "init")
		if got := run(env, "lint"); got.code != 0 {
			t.Errorf("lint after init exit %d: %s%s", got.code, got.stdout, got.stderr)
		}
	})
}
func TestInitJSON(t *testing.T) {
	env := setupVault(t, "1.2.3", nil)
	got := run(env, "init", "-y", "--json", "v")
	if got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	var out struct {
		OK         bool     `json:"ok"`
		Dir        string   `json:"dir"`
		Created    []string `json:"created"`
		HooksPath  bool     `json:"hooks_path"`
		MinVersion *string  `json:"min_version"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, got.stdout)
	}
	wd, _ := os.Getwd()
	if !out.OK || out.Dir != filepath.Join(wd, "v") || !out.HooksPath || out.MinVersion == nil || *out.MinVersion != "1.2.3" {
		t.Errorf("unexpected JSON: %+v", out)
	}
	for _, want := range []string{".githooks/pre-commit", ".zk-chef/config.toml", "AGENTS.md"} {
		if !slices.Contains(out.Created, want) {
			t.Errorf("created lacks %s: %v", want, out.Created)
		}
	}
}

func TestInitJSONDevMinVersionNull(t *testing.T) {
	env := setupVault(t, "dev", nil)
	got := run(env, "init", "--json")
	if !strings.Contains(got.stdout, `"min_version":null`) {
		t.Errorf("stdout %q", got.stdout)
	}
}

func TestInitZkHint(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	const hint = "zk not found; run zk-chef install"
	for _, withZk := range []bool{false, true} {
		t.Run(fmt.Sprintf("zk on PATH %v", withZk), func(t *testing.T) {
			env := setupVault(t, "1.2.3", nil)
			bin := t.TempDir()
			if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
				t.Fatal(err)
			}
			if withZk {
				if err := os.WriteFile(filepath.Join(bin, "zk"), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			got := run(append(env, "PATH="+bin), "init", "-y")
			if got.code != 0 {
				t.Fatalf("exit %d: %s", got.code, got.stderr)
			}
			if strings.Contains(got.stderr, hint) == withZk {
				t.Errorf("stderr %q, want hint %v", got.stderr, !withZk)
			}
		})
	}
}
