package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
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
	before := time.Now().UTC().Truncate(time.Second)
	got := run(env, "init", "v")
	after := time.Now().UTC()
	if got.code != 0 {
		t.Fatalf("init exit %d, stderr %q", got.code, got.stderr)
	}
	v, err := filepath.Abs("v")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".zk/config.toml", ".zk/templates/note.md", ".zk/templates/source.md", ".zk/templates/hub.md", ".zk/templates/log.md", ".zk-chef/config.toml", ".githooks/pre-commit", "AGENTS.md", "index.md"} {
		if _, err := os.Stat(filepath.Join(v, p)); err != nil {
			t.Errorf("missing file %s: %v", p, err)
		}
	}
	for _, f := range []string{"notes", "raw", "keys"} {
		if st, err := os.Stat(filepath.Join(v, f)); err != nil || !st.IsDir() {
			t.Errorf("missing folder %s: %v", f, err)
		}
	}
	for _, f := range []string{"hubs", "sources", "projects"} {
		if _, err := os.Stat(filepath.Join(v, f)); err == nil {
			t.Errorf("v0.2 folder %s exists, want only notes/, raw/ and keys/", f)
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
	for line := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "exec")))
		if len(f) > 0 && strings.HasPrefix(f[0], "/") {
			t.Errorf("hook runs absolute path: %q", line)
		}
	}
	if lint := run(env, "lint", "--root", "v"); lint.code != 0 || lint.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("fresh vault lint exit %d:\n%s%s", lint.code, lint.stdout, lint.stderr)
	}

	t.Run("index.md holds a UTC date and its kind tag", func(t *testing.T) {
		index := readFile(t, filepath.Join(v, "index.md"))
		m := regexp.MustCompile(`(?m)^date: (\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ)$`).FindStringSubmatch(index)
		if m == nil {
			t.Fatalf("index.md has no UTC date with Z:\n%s", index)
		}
		date, err := time.Parse(time.RFC3339, m[1])
		if err != nil || date.Before(before) || date.After(after) {
			t.Errorf("index.md date %s (%v), want a time between %s and %s", m[1], err, before, after)
		}
		if !strings.Contains(index, "\ntags: [kind/hub]\n") || strings.Contains(index, "\nid:") {
			t.Errorf("index.md:\n%s\nwant tags: [kind/hub] and no id: field", index)
		}
	})

	t.Run("vault config drops the seed hub IDs", func(t *testing.T) {
		var cfg map[string]any
		if err := toml.Unmarshal([]byte(readFile(t, filepath.Join(v, ".zk-chef", "config.toml"))), &cfg); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"index_id", "projects_id", "sources_id"} {
			if _, ok := cfg[k]; ok {
				t.Errorf("vault config has %s, want it dropped", k)
			}
		}
		if cfg["min_version"] != "1.2.3" || cfg["word_count_warn"] != int64(400) || cfg["pinned_fingerprints"] == nil {
			t.Errorf("vault config %v, want min_version 1.2.3, word_count_warn 400 and pinned_fingerprints", cfg)
		}
	})

	t.Run("zk config makes 6-character lowercase stems and flag-only aliases", func(t *testing.T) {
		var cfg struct {
			Note struct {
				Filename string   `toml:"filename"`
				Charset  string   `toml:"id-charset"`
				Length   int64    `toml:"id-length"`
				Case     string   `toml:"id-case"`
				Exclude  []string `toml:"exclude"`
			} `toml:"note"`
			Format struct {
				Markdown struct {
					LinkFormat string `toml:"link-format"`
					ColonTags  bool   `toml:"colon-tags"`
				} `toml:"markdown"`
			} `toml:"format"`
			Group map[string]any    `toml:"group"`
			Alias map[string]string `toml:"alias"`
			Tool  struct {
				Shell string `toml:"shell"`
			} `toml:"tool"`
		}
		text := readFile(t, filepath.Join(v, ".zk", "config.toml"))
		if err := toml.Unmarshal([]byte(text), &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Note.Filename != "{{id}}" || cfg.Note.Charset != "alphanum" || cfg.Note.Length != 6 || cfg.Note.Case != "lower" || !slices.Equal(cfg.Note.Exclude, []string{"raw"}) {
			t.Errorf("[note] = %+v, want filename {{id}}, alphanum, 6, lower, exclude [raw]", cfg.Note)
		}
		if md := cfg.Format.Markdown; md.LinkFormat != "[[{{filename}}|{{title}}]]" || md.ColonTags {
			t.Errorf("[format.markdown] = %+v, want link-format [[{{filename}}|{{title}}]] and colon-tags false", md)
		}
		if len(cfg.Group) != 0 {
			t.Errorf("config has groups %v, want none", cfg.Group)
		}
		// zk drops the arguments of an alias without "$@", and fish rejects "$@": aliases pass them and run with sh.
		if cfg.Tool.Shell != "/bin/sh" {
			t.Errorf("[tool] shell = %q, want /bin/sh", cfg.Tool.Shell)
		}
		for _, kind := range []string{"note", "source", "hub", "log"} {
			if want := "zk new notes --template " + kind + ".md --print-path --no-input \"$@\""; cfg.Alias[kind] != want {
				t.Errorf("alias %s = %q, want %q", kind, cfg.Alias[kind], want)
			}
		}
	})

	t.Run("one template per kind", func(t *testing.T) {
		for _, tt := range []struct{ kind, tags string }{
			{"note", "[kind/note]"}, {"source", "[kind/source, review/unreviewed]"}, {"hub", "[kind/hub]"}, {"log", "[kind/log]"},
		} {
			text := readFile(t, filepath.Join(v, ".zk", "templates", tt.kind+".md"))
			for _, want := range []string{"\ntitle: {{json title}}\n", "\ntags: " + tt.tags + "\n", "\ndate: {{sh 'date -u +%Y-%m-%dT%H:%M:%SZ'}}\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s.md lacks %q:\n%s", tt.kind, want, text)
				}
			}
			if strings.Contains(text, "\nid:") || strings.Contains(text, "\nkind:") {
				t.Errorf("%s.md has an id: or kind: field:\n%s", tt.kind, text)
			}
		}
	})
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
		{"existing without block", new("# Mine\n\nkeep me"), 0, "# Mine\n\nkeep me\n\n" + string(block)},
		{"existing with block", new(wellFormed), 0, wellFormed},
		{"only open marker", new("a\n<!-- zk-chef -->\nb\n"), 2, ""},
		{"only close marker", new("a\n<!-- /zk-chef -->\nb\n"), 2, ""},
		{"close before open", new("<!-- /zk-chef -->\n<!-- zk-chef -->\n"), 2, ""},
		{"duplicated", new(wellFormed + wellFormed), 2, ""},
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
			for l := range strings.SplitSeq(string(b), "\n") {
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

// TestZkAliasesPassTitle: zk note|source|hub|log --title sets the title of the new page, and a title
// with spaces, quotes or shell syntax stays one argument that the shell never runs.
func TestZkAliasesPassTitle(t *testing.T) {
	ingestVault(t)
	titles := []string{"B two words", `Say "hi"`, "it's $(touch injected) ; touch injected2 `touch injected3`"}
	for _, alias := range []string{"note", "source", "hub", "log"} {
		for _, title := range titles {
			out, err := exec.Command("zk", alias, "--title", title).Output()
			if err != nil {
				t.Fatalf("zk %s --title %q: %v", alias, title, err)
			}
			path := strings.TrimSpace(string(out))
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("zk %s printed %q: %v", alias, out, err)
			}
			quoted, _ := json.Marshal(title)
			if want := "\ntitle: " + string(quoted) + "\n"; !strings.Contains(string(b), want) {
				t.Errorf("zk %s --title %q wrote:\n%s\nwant the line %q", alias, title, b, want)
			}
		}
	}
	for _, name := range []string{"injected", "injected2", "injected3"} {
		if _, err := os.Stat(name); err == nil {
			t.Errorf("a title ran a shell command: %s exists", name)
		}
	}
}
