package cli

import (
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/version"
)

// upgradeVault makes a committed vault with Run init at binary version 1.2.3.
// The commit hook is switched off: it needs a zk-chef binary on PATH.
func upgradeVault(t *testing.T) []string {
	t.Helper()
	env := setupVault(t, "1.2.3", nil)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	if got := run(env, "init"); got.code != 0 {
		t.Fatalf("init exit %d: %s", got.code, got.stderr)
	}
	gitOut(t, "config", "--unset", "core.hooksPath")
	gitOut(t, "add", ".")
	gitOut(t, "commit", "-qm", "base")
	return env
}

// scaffoldBytes reads a scaffold file through Assets; the tests run inside the vault directory.
func scaffoldBytes(t *testing.T, p string) string {
	t.Helper()
	b, err := fs.ReadFile(Assets, "scaffold/"+p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func setBinaryVersion(t *testing.T, v string) {
	t.Helper()
	version.Version = v // setupVault restores the old value on cleanup
}

func TestUpgradeYesAppliesOwnedFiles(t *testing.T) {
	env := upgradeVault(t)
	const hook, tmpl, zkCfg = ".githooks/pre-commit", ".zk/templates/note.md", ".zk/config.toml"
	writeFile(t, hook, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, tmpl, "mine\n")
	writeFile(t, zkCfg, "# mine\n")
	got := runStdin(env, "", "upgrade", "-y")
	if got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	for _, p := range []string{hook, tmpl, zkCfg} {
		if have, want := readFile(t, p), scaffoldBytes(t, p); have != want {
			t.Errorf("%s = %q, want the scaffold bytes", p, have)
		}
		if !strings.Contains(got.stdout, "--- a/"+p+"\n+++ b/"+p+"\n@@ ") {
			t.Errorf("stdout lacks a unified diff header for %s:\n%s", p, got.stdout)
		}
	}
	if !strings.Contains(got.stdout, "\n-exit 0\n") || !strings.Contains(got.stdout, "\n+\texec zk-chef lint --staged --root .\n") {
		t.Errorf("diff lacks the removed and added hook lines:\n%s", got.stdout)
	}
	if st, err := os.Stat(hook); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("hook not executable: %v %v", st, err)
	}
}

func TestUpgradePrompt(t *testing.T) {
	const zkCfg, tmpl = ".zk/config.toml", ".zk/templates/note.md"
	tests := []struct {
		name, stdin       string
		wantCfg, wantTmpl bool // applied
	}{
		{"y applies", "y\ny\n", true, true},
		{"yes applies", "yes\nYES\n", true, true},
		{"y without newline at EOF applies", "y\ny", true, true},
		{"n leaves unchanged", "n\nn\n", false, false},
		{"empty line leaves unchanged", "\n\n", false, false},
		{"other text leaves unchanged", "maybe\nyep\n", false, false},
		{"EOF leaves unchanged", "", false, false},
		{"each file has its own answer", "y\nn\n", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			writeFile(t, zkCfg, "# mine\n")
			writeFile(t, tmpl, "mine\n")
			got := runStdin(env, tt.stdin, "upgrade")
			if got.code != 0 {
				t.Fatalf("exit %d: %s", got.code, got.stderr)
			}
			for _, f := range []struct {
				path, mine string
				applied    bool
			}{{zkCfg, "# mine\n", tt.wantCfg}, {tmpl, "mine\n", tt.wantTmpl}} {
				want := f.mine
				if f.applied {
					want = scaffoldBytes(t, f.path)
				}
				if have := readFile(t, f.path); have != want {
					t.Errorf("%s = %q, want %q", f.path, have, want)
				}
				if !strings.Contains(got.stdout, "apply "+f.path+"? [y/N] ") {
					t.Errorf("stdout lacks the prompt for %s:\n%s", f.path, got.stdout)
				}
			}
		})
	}
}

func TestUpgradeCreatesMissingOwnedFile(t *testing.T) {
	env := upgradeVault(t)
	const hook, tmpl = ".githooks/pre-commit", ".zk/templates/log.md"
	for _, p := range []string{hook, tmpl} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	got := runStdin(env, "y\ny\n", "upgrade")
	if got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	for _, p := range []string{hook, tmpl} {
		if have, want := readFile(t, p), scaffoldBytes(t, p); have != want {
			t.Errorf("%s = %q, want the scaffold bytes", p, have)
		}
		if !strings.Contains(got.stdout, "--- a/"+p+"\n+++ b/"+p+"\n@@ -0,0 +1,") {
			t.Errorf("stdout lacks a diff against empty for %s:\n%s", p, got.stdout)
		}
	}
	if st, err := os.Stat(hook); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("created hook not executable: %v %v", st, err)
	}
}

func TestUpgradeLeavesHumanFilesAlone(t *testing.T) {
	env := upgradeVault(t)
	for p, content := range map[string]string{
		"hubs/000000000000idx0.md": "my own hub\n",
		"notes/n1.md":              "a note\n",
		"sources/s1.md":            "a source\n",
		"raw/s1-a.bin":             "raw",
		"keys/me.asc":              "a key\n",
		".zk/templates/mine.md":    "my own template, not in the scaffold\n",
	} {
		writeFile(t, p, content)
	}
	writeFile(t, ".zk/templates/note.md", "mine\n") // owned: something to upgrade
	before := tree(t, ".")
	got := runStdin(env, "", "upgrade", "-y")
	if got.code != 0 {
		t.Fatalf("exit %d: %s", got.code, got.stderr)
	}
	after := tree(t, ".")
	for p, b := range before {
		if after[p] != b && p != ".zk/templates/note.md" {
			t.Errorf("%s changed", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("file count %d -> %d", len(before), len(after))
	}
}

func TestUpgradeAgentsMd(t *testing.T) {
	const stale = "<!-- zk-chef -->\nold block\n<!-- /zk-chef -->"
	tests := []struct {
		name     string
		existing *string // nil: no AGENTS.md
		wantCode int
		want     string // wanted AGENTS.md bytes, {BLOCK} for the scaffold block; ignored when wantCode is 2 (must be unchanged)
	}{
		{"block replaced, text around it kept", new("intro\n" + stale + "\noutro\n"), 0, "intro\n{BLOCK}\noutro\n"},
		{"block at file start and end", new(stale), 0, "{BLOCK}"},
		{"no markers: block appended", new("# Mine\n\nkeep me"), 0, "# Mine\n\nkeep me\n\n{BLOCK}\n"},
		{"absent: created", nil, 0, "{BLOCK}\n"},
		{"only open marker", new("a\n<!-- zk-chef -->\nb\n"), 2, ""},
		{"only close marker", new("a\n<!-- /zk-chef -->\nb\n"), 2, ""},
		{"close before open", new("<!-- /zk-chef -->\n<!-- zk-chef -->\n"), 2, ""},
		{"duplicated", new(stale + "\n" + stale + "\n"), 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			if tt.existing == nil {
				if err := os.Remove("AGENTS.md"); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, "AGENTS.md", *tt.existing)
			}
			writeFile(t, ".zk/templates/note.md", "mine\n") // owned: must stay when AGENTS.md is malformed
			got := runStdin(env, "y\ny\n", "upgrade")
			if got.code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", got.code, tt.wantCode, got.stderr)
			}
			if tt.wantCode == 2 {
				if b := readFile(t, "AGENTS.md"); b != *tt.existing {
					t.Errorf("AGENTS.md changed to %q", b)
				}
				if b := readFile(t, ".zk/templates/note.md"); b != "mine\n" {
					t.Errorf("template changed to %q, want nothing changed", b)
				}
				if !strings.Contains(got.stderr, "AGENTS.md") {
					t.Errorf("stderr %q does not name AGENTS.md", got.stderr)
				}
				return
			}
			block := strings.TrimSuffix(scaffoldBytes(t, "AGENTS.md"), "\n")
			want := strings.ReplaceAll(tt.want, "{BLOCK}", block)
			if b := readFile(t, "AGENTS.md"); b != want {
				t.Errorf("AGENTS.md = %q, want %q", b, want)
			}
		})
	}
}

func TestUpgradeConfig(t *testing.T) {
	const old = "word_count_warn = 1\npinned_fingerprints = [\"AAAA\"]\nmin_version = \"1.0.0\"\n"
	tests := []struct {
		name, binVersion string
		wantMin          string
		wantWarning      bool
	}{
		{"release sets min_version", "1.3.0", "1.3.0", false},
		{"dev keeps min_version and warns", "dev", "1.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			writeFile(t, ".zk-chef/config.toml", old)
			setBinaryVersion(t, tt.binVersion)
			got := runStdin(env, "", "upgrade")
			if got.code != 0 {
				t.Fatalf("exit %d: %s", got.code, got.stderr)
			}
			var cfg map[string]any
			if err := toml.Unmarshal([]byte(readFile(t, ".zk-chef/config.toml")), &cfg); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"word_count_warn":     int64(1),
				"pinned_fingerprints": []any{"AAAA"},
				"min_version":         tt.wantMin,
				"index_id":            "000000000000idx0",
				"projects_id":         "000000000000prj0",
				"sources_id":          "000000000000src0",
			}
			if !reflect.DeepEqual(cfg, want) {
				t.Errorf("config = %v, want %v", cfg, want)
			}
			if !strings.Contains(got.stdout, "updated .zk-chef/config.toml\n") {
				t.Errorf("stdout lacks the config report:\n%s", got.stdout)
			}
			if strings.Contains(got.stdout, "a/.zk-chef/config.toml") || strings.Contains(got.stdout, "apply .zk-chef") {
				t.Errorf("config was diffed or prompted for:\n%s", got.stdout)
			}
			if warning := strings.Contains(got.stderr, "warning: dev build; min_version not set"); warning != tt.wantWarning {
				t.Errorf("stderr %q, want warning %v", got.stderr, tt.wantWarning)
			}
		})
	}
}

// The version gate reads --root, so an older binary cannot lower the pin of another vault.
func TestUpgradeRefusedByPinFromOtherDirectory(t *testing.T) {
	env := upgradeVault(t)
	var cfg map[string]any
	if err := toml.Unmarshal([]byte(readFile(t, ".zk-chef/config.toml")), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["min_version"] = "2.0.0"
	if err := writeConfig(".", cfg); err != nil {
		t.Fatal(err)
	}
	gitOut(t, "commit", "-aqm", "set pin")
	before := gitOut(t, "rev-parse", "HEAD")
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	setBinaryVersion(t, "1.3.0")
	t.Chdir(t.TempDir())
	got := run(env, "upgrade", "-y", "--root", root)
	if got.code != 2 || !strings.Contains(got.stderr, "min_version") {
		t.Errorf("exit %d stderr %q, want 2 and min_version", got.code, got.stderr)
	}
	t.Chdir(root)
	cfg = nil
	if err := toml.Unmarshal([]byte(readFile(t, ".zk-chef/config.toml")), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["min_version"] != "2.0.0" {
		t.Errorf("min_version = %v, want 2.0.0", cfg["min_version"])
	}
	if after := gitOut(t, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved from %s to %s", before, after)
	}
}

func TestUpgradeCommit(t *testing.T) {
	tests := []struct {
		name, binVersion, wantSubject string
		wantFiles                     string // files of the commit, one per line
	}{
		{"release, config gets min_version", "1.3.0", "chore: zk-chef upgrade v1.3.0", ".githooks/pre-commit\n.zk-chef/config.toml\n.zk/templates/note.md"},
		{"v prefix is dropped", "v1.3.0", "chore: zk-chef upgrade v1.3.0", ".githooks/pre-commit\n.zk-chef/config.toml\n.zk/templates/note.md"},
		{"same version leaves the config out", "1.2.3", "chore: zk-chef upgrade v1.2.3", ".githooks/pre-commit\n.zk/templates/note.md"},
		{"dev", "dev", "chore: zk-chef upgrade vdev", ".githooks/pre-commit\n.zk/templates/note.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			writeFile(t, ".githooks/pre-commit", "#!/bin/sh\nexit 0\n")
			writeFile(t, ".zk/templates/note.md", "mine\n")
			gitOut(t, "commit", "-aqm", "user edits") // so the upgrade changes the vault against HEAD
			writeFile(t, "notes/unrelated.md", "staged by the human\n")
			gitOut(t, "add", "notes/unrelated.md")
			before := gitOut(t, "rev-parse", "HEAD")
			setBinaryVersion(t, tt.binVersion)
			got := runStdin(env, "", "upgrade", "-y")
			if got.code != 0 {
				t.Fatalf("exit %d: %s", got.code, got.stderr)
			}
			if n := gitOut(t, "rev-list", "--count", before+"..HEAD"); n != "1" {
				t.Errorf("%s new commits, want 1", n)
			}
			if subj := gitOut(t, "log", "-1", "--format=%s"); subj != tt.wantSubject {
				t.Errorf("subject %q, want %q", subj, tt.wantSubject)
			}
			if files := gitOut(t, "show", "--name-only", "--format=", "HEAD"); files != tt.wantFiles {
				t.Errorf("commit holds %q, want %q", files, tt.wantFiles)
			}
			if staged := gitOut(t, "diff", "--cached", "--name-only"); staged != "notes/unrelated.md" {
				t.Errorf("still staged %q, want notes/unrelated.md", staged)
			}
		})
	}
}

func TestUpgradeNoCommitWhenNothingApplied(t *testing.T) {
	tests := []struct {
		name, stdin string
		modify      bool
		wantStdout  string // substring
	}{
		{"nothing to do", "", false, "already up to date\n"},
		{"everything declined", "n\nn\n", true, "apply "},
		{"applied edits only restore HEAD", "y\ny\n", true, "updated "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			if tt.modify {
				writeFile(t, ".githooks/pre-commit", "#!/bin/sh\nexit 0\n")
				writeFile(t, ".zk/templates/note.md", "mine\n")
			}
			before := gitOut(t, "rev-parse", "HEAD")
			got := runStdin(env, tt.stdin, "upgrade")
			if got.code != 0 {
				t.Fatalf("exit %d: %s", got.code, got.stderr)
			}
			if after := gitOut(t, "rev-parse", "HEAD"); after != before {
				t.Errorf("HEAD moved from %s to %s, want no commit", before, after)
			}
			if !strings.Contains(got.stdout, tt.wantStdout) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantStdout, got.stdout)
			}
			if tt.modify == strings.Contains(got.stdout, "already up to date") {
				t.Errorf("stdout %q: wrong up-to-date report", got.stdout)
			}
		})
	}
}

func TestUpgradeJSON(t *testing.T) {
	const hook, tmpl = ".githooks/pre-commit", ".zk/templates/note.md"
	tests := []struct {
		name, binVersion string
		modify           bool
		want             string
	}{
		{"changes and commit", "1.3.0", true, `{"ok":true,"changed":[".githooks/pre-commit",".zk/templates/note.md",".zk-chef/config.toml"],"skipped":[],"committed":true,"min_version":"1.3.0"}`},
		{"dev: no min_version", "dev", true, `{"ok":true,"changed":[".githooks/pre-commit",".zk/templates/note.md"],"skipped":[],"committed":true,"min_version":null}`},
		{"nothing to do", "1.2.3", false, `{"ok":true,"changed":[],"skipped":[],"committed":false,"min_version":"1.2.3"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := upgradeVault(t)
			if tt.modify {
				writeFile(t, hook, "#!/bin/sh\nexit 0\n")
				writeFile(t, tmpl, "mine\n")
				gitOut(t, "commit", "-aqm", "user edits")
			}
			setBinaryVersion(t, tt.binVersion)
			got := runStdin(env, "", "upgrade", "-y", "--json")
			if got.code != 0 || got.stdout != tt.want+"\n" {
				t.Errorf("got code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, tt.want+"\n")
			}
		})
	}
}

func TestUpgradeJSONNeedsYes(t *testing.T) {
	env := upgradeVault(t)
	writeFile(t, ".zk/templates/note.md", "mine\n")
	got := runStdin(env, "y\n", "upgrade", "--json")
	if got.code != 2 || !strings.Contains(got.stderr, "--json needs -y") {
		t.Errorf("got code %d stderr %q, want 2 and \"--json needs -y\"", got.code, got.stderr)
	}
	if b := readFile(t, ".zk/templates/note.md"); b != "mine\n" {
		t.Errorf("template changed to %q, want nothing changed", b)
	}
}

func TestUpgradeRefusesBadVault(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		got := runIn(t, "1.2.3", nil, "upgrade", "-y")
		if got.code != 2 || !strings.Contains(got.stderr, "no .zk-chef/config.toml; run zk-chef init first") {
			t.Errorf("got code %d stderr %q", got.code, got.stderr)
		}
		if _, err := os.Stat(".githooks"); err == nil {
			t.Error("upgrade wrote files without a config")
		}
	})
	t.Run("not a git repo", func(t *testing.T) {
		files := map[string]string{".zk-chef/config.toml": "word_count_warn = 400\n"}
		got := runIn(t, "1.2.3", files, "upgrade", "-y")
		if got.code != 2 || !strings.Contains(got.stderr, "git") {
			t.Errorf("got code %d stderr %q", got.code, got.stderr)
		}
		if _, err := os.Stat(".githooks"); err == nil {
			t.Error("upgrade wrote files outside a git repo")
		}
	})
}
