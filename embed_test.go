package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbedHasScaffoldAndSkills(t *testing.T) {
	for _, p := range []string{
		"scaffold/.zk-chef/config.toml",
		"scaffold/.githooks/pre-commit",
		"scaffold/.zk/config.toml",
		"skills/zk-chef-ops/SKILL.md",
		"skills/zettel-writing/SKILL.md",
	} {
		if _, err := fs.Stat(assets, p); err != nil {
			t.Errorf("embed FS lacks %s: %v", p, err)
		}
	}
}

func TestEmbedHasNoV1References(t *testing.T) {
	banned := []string{"vault.py", ".vault/", "vault-ops", "vault-writing"}
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				t.Errorf("%s contains %q", p, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSkillNameMatchesDir(t *testing.T) {
	for _, name := range []string{"zk-chef-ops", "zettel-writing"} {
		b, err := fs.ReadFile(assets, "skills/"+name+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), "---\nname: "+name+"\n") {
			t.Errorf("skills/%s/SKILL.md frontmatter name is not %q", name, name)
		}
	}
}

func TestScaffoldAgentsBlock(t *testing.T) {
	b, err := fs.ReadFile(assets, "scaffold/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	open, closing := strings.Index(s, "<!-- zk-chef -->"), strings.Index(s, "<!-- /zk-chef -->")
	if open < 0 || closing < open {
		t.Fatalf("AGENTS.md lacks an ordered zk-chef marker pair")
	}
	for _, want := range []string{"zk-chef-ops", "zettel-writing", "zk-chef review", ".zk-chef/config.toml"} {
		if !strings.Contains(s[open:closing], want) {
			t.Errorf("zk-chef block lacks %q", want)
		}
	}
}

func TestScaffoldConfigKeys(t *testing.T) {
	b, err := fs.ReadFile(assets, "scaffold/.zk-chef/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"pinned_fingerprints", "word_count_warn", "index_id", "projects_id", "sources_id"} {
		if !strings.Contains(string(b), k) {
			t.Errorf("config.toml lacks key %s", k)
		}
	}
	if strings.Contains(string(b), "min_version") {
		t.Error("config.toml must not set min_version")
	}
}

// runHook writes the embedded pre-commit hook to a temp dir and runs it with sh.
func runHook(t *testing.T, path string, home string) (int, string) {
	t.Helper()
	b, err := fs.ReadFile(assets, "scaffold/.githooks/pre-commit")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(t.TempDir(), "pre-commit")
	if err := os.WriteFile(hook, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", hook)
	cmd.Env = []string{"PATH=" + path, "HOME=" + home}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, stderr.String()
}

func fakeBinary(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"fake zk-chef $*\" >&2\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "zk-chef"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPreCommitHook(t *testing.T) {
	t.Run("no binary fails closed", func(t *testing.T) {
		code, stderr := runHook(t, t.TempDir(), t.TempDir())
		if code != 1 || !strings.Contains(stderr, "zk-chef not found; run install.sh or zk-chef install") {
			t.Errorf("got code %d, stderr %q", code, stderr)
		}
	})
	t.Run("uses PATH", func(t *testing.T) {
		bin := t.TempDir()
		fakeBinary(t, bin)
		code, stderr := runHook(t, bin, t.TempDir())
		if code != 7 || !strings.Contains(stderr, "fake zk-chef lint --staged") {
			t.Errorf("got code %d, stderr %q", code, stderr)
		}
	})
	t.Run("falls back to HOME/.local/bin", func(t *testing.T) {
		home := t.TempDir()
		fakeBinary(t, filepath.Join(home, ".local", "bin"))
		code, stderr := runHook(t, t.TempDir(), home)
		if code != 7 || !strings.Contains(stderr, "fake zk-chef lint --staged") {
			t.Errorf("got code %d, stderr %q", code, stderr)
		}
	})
}
