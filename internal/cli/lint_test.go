package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures ported from tests/test_vault.py (IDX, HUB, doc, RAW, SHA, base, codes, with_).
const (
	idxID    = "000000000000idx0"
	hubLine  = "Hubs: [[" + idxID + "|Index]]"
	rawBytes = "raw bytes\n\nwith newlines\n"
)

var rawSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(rawBytes)))

// kv is one extra frontmatter line, in order.
type kv [2]string

func doc(id, title, kind, body string, extra ...kv) string {
	var fm strings.Builder
	for _, e := range extra {
		fm.WriteString(e[0])
		fm.WriteString(": ")
		fm.WriteString(e[1])
		fm.WriteString("\n")
	}
	return "---\nid: " + id + "\ntitle: " + title + "\nkind: " + kind + "\n" + fm.String() + "---\n" + body + "\n"
}

// baseVault is the scaffold hubs and config plus one source and one note.
func baseVault(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	scaffold := filepath.Join("..", "..", "scaffold")
	hubs, err := os.ReadDir(filepath.Join(scaffold, "hubs"))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{".zk-chef/config.toml"}
	for _, h := range hubs {
		paths = append(paths, "hubs/"+h.Name())
	}
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(scaffold, p))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = string(b)
	}
	files["sources/s1.md"] = doc("s1", "Source one", "source",
		"Hubs: [[000000000000src0|Sources]]\n\nWhy.",
		kv{"raw", "raw/s1-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})
	files["raw/s1-a.bin"] = rawBytes
	files["notes/n1.md"] = doc("n1", "Leaders are unique", "note",
		hubLine+"\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [[s1|Source one]]")
	return files
}

// with_ returns the base vault with changes applied (path -> content).
func with_(t *testing.T, changes map[string]string) map[string]string {
	t.Helper()
	files := baseVault(t)
	maps.Copy(files, changes)
	return files
}

type finding struct {
	Code string `json:"code"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

type report struct {
	OK       bool      `json:"ok"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	Findings []finding `json:"findings"`
}

// lintCodes runs lint --json over files and returns the set of finding codes.
func lintCodes(t *testing.T, files map[string]string) map[string]bool {
	t.Helper()
	got := runIn(t, "dev", files, "lint", "--json")
	return codesOf(t, got)
}

func codesOf(t *testing.T, got result) map[string]bool {
	t.Helper()
	var r report
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
		t.Fatalf("stdout %q stderr %q is not a lint report: %v", got.stdout, got.stderr, err)
	}
	codes := map[string]bool{}
	for _, f := range r.Findings {
		codes[f.Code] = true
	}
	return codes
}

func TestLintClean(t *testing.T) {
	got := runIn(t, "dev", baseVault(t), "lint")
	if got.code != 0 || got.stdout != "0 errors, 0 warnings\n" || got.stderr != "" {
		t.Errorf("got code %d stdout %q stderr %q, want 0 \"0 errors, 0 warnings\\n\" \"\"", got.code, got.stdout, got.stderr)
	}
}

func TestLintOutput(t *testing.T) {
	broken := func(t *testing.T) map[string]string {
		return with_(t, map[string]string{"notes/b.md": "no frontmatter", "notes/a.md": "also none"})
	}
	underV := func(files map[string]string) map[string]string {
		out := map[string]string{}
		for p, c := range files {
			out["v/"+p] = c
		}
		return out
	}
	tests := []struct {
		name       string
		files      func(*testing.T) map[string]string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"text sorted by path", broken, []string{"lint"}, 1,
			"notes/a.md:1: E_FRONTMATTER missing frontmatter\nnotes/b.md:1: E_FRONTMATTER missing frontmatter\n2 errors, 0 warnings\n", ""},
		{"json findings", func(t *testing.T) map[string]string {
			return with_(t, map[string]string{"notes/a.md": "also none"})
		}, []string{"lint", "--json"}, 1,
			`{"ok":false,"errors":1,"warnings":0,"findings":[{"code":"E_FRONTMATTER","path":"notes/a.md","line":1,"msg":"missing frontmatter"}]}` + "\n", ""},
		{"json clean has empty findings array", baseVault, []string{"lint", "--json"}, 0,
			`{"ok":true,"errors":0,"warnings":0,"findings":[]}` + "\n", ""},
		{"root flag", func(t *testing.T) map[string]string { return underV(baseVault(t)) }, []string{"lint", "--root", "v"}, 0,
			"0 errors, 0 warnings\n", ""},
		{"missing config", func(*testing.T) map[string]string { return nil }, []string{"lint", "--root", "/zk-chef-no-such-root"}, 2,
			"", "/zk-chef-no-such-root: no .zk-chef/config.toml; run zk-chef init first\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, "dev", tt.files(t), tt.args...)
			if got.code != tt.wantCode || got.stdout != tt.wantStdout || got.stderr != tt.wantStderr {
				t.Errorf("got code %d stdout %q stderr %q, want %d %q %q", got.code, got.stdout, got.stderr, tt.wantCode, tt.wantStdout, tt.wantStderr)
			}
		})
	}
}

// TestLintYAMLDifferences pins the cases where yaml.v3 (YAML 1.2) and PyYAML (YAML 1.1) differ.
func TestLintYAMLDifferences(t *testing.T) {
	linksN1 := hubLine + "\n\n[[n1|Leaders are unique]]"
	tests := []struct {
		name     string
		file     string
		wantMsg  string // substring of one E_FRONTMATTER message; empty means no E_FRONTMATTER at all
		wantPath string
	}{
		{"unquoted numeric id is not a string", doc("123", "Two", "note", linksN1),
			"id must be a non-empty string (quote it in YAML)", "notes/n2.md"},
		{"yes is a string in YAML 1.2, a bool in PyYAML", doc("n2", "yes", "note", linksN1), "", ""},
		{"non-string key is accepted, as in PyYAML", doc("n2", "Two", "note", linksN1, kv{"1", "x"}), "", ""},
		{"duplicate key is an error, PyYAML keeps the last", doc("n2", "Two", "note", linksN1, kv{"title", "Three"}),
			"invalid YAML: ", "notes/n2.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, "dev", with_(t, map[string]string{"notes/n2.md": tt.file}), "lint", "--json")
			var r report
			if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
				t.Fatalf("stdout %q stderr %q: %v", got.stdout, got.stderr, err)
			}
			var msgs []string
			for _, f := range r.Findings {
				if f.Code == "E_FRONTMATTER" {
					if f.Path != tt.wantPath || f.Line != 1 {
						t.Errorf("finding at %s:%d, want %s:1", f.Path, f.Line, tt.wantPath)
					}
					msgs = append(msgs, f.Msg)
				}
			}
			if tt.wantMsg == "" && len(msgs) != 0 {
				t.Errorf("got E_FRONTMATTER %q, want none", msgs)
			}
			if tt.wantMsg != "" && !strings.Contains(strings.Join(msgs, "\n"), tt.wantMsg) {
				t.Errorf("got E_FRONTMATTER %q, want one containing %q", msgs, tt.wantMsg)
			}
		})
	}
}

// codeCase asserts that lint reports (or does not report) a code for a vault.
type codeCase struct {
	name    string
	changes map[string]string
	want    string // code that must be reported
	absent  string // code that must not be reported
}

func runCodeCases(t *testing.T, tests []codeCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes := lintCodes(t, with_(t, tt.changes))
			if tt.want != "" && !codes[tt.want] {
				t.Errorf("codes %v, want %s", codes, tt.want)
			}
			if tt.absent != "" && codes[tt.absent] {
				t.Errorf("codes %v, want no %s", codes, tt.absent)
			}
		})
	}
}

// TestLintErrors ports tests/test_vault.py test_lint_errors. The Python test has no signature case.
func TestLintErrors(t *testing.T) {
	n := func(body string) string { return doc("n2", "Two", "note", body) }
	runCodeCases(t, []codeCase{
		{name: "E_BROKEN_LINK", want: "E_BROKEN_LINK", changes: map[string]string{
			"notes/n2.md": n(hubLine + "\n\nSee [[nope|X]] and [[n1|Leaders are unique]].")}},
		{name: "E_FRONTMATTER", want: "E_FRONTMATTER", changes: map[string]string{"notes/n2.md": "no frontmatter"}},
		{name: "E_WRONG_FOLDER", want: "E_WRONG_FOLDER", changes: map[string]string{"notes/n2.md": doc("n2", "Two", "hub", hubLine)}},
		{name: "E_DUPLICATE_ID", want: "E_DUPLICATE_ID", changes: map[string]string{"notes/n2.md": doc("zz", "Two", "note", hubLine)}},
		{name: "E_HUBS_LINE", want: "E_HUBS_LINE", changes: map[string]string{
			"notes/n2.md": n("Intro\n\n[[n1|Leaders are unique]]")}},
		{name: "E_UNREACHABLE", want: "E_UNREACHABLE", changes: map[string]string{
			"hubs/h1.md":  doc("h1", "H1", "hub", "Hubs: [[h2|H2]]"),
			"hubs/h2.md":  doc("h2", "H2", "hub", "Hubs: [[h1|H1]]"),
			"notes/n2.md": n("Hubs: [[h1|H1]]\n\n[[n1|Leaders are unique]]")}},
		{name: "E_CITATION", want: "E_CITATION", changes: map[string]string{
			"notes/n2.md": n(hubLine + "\n\nClaim [2].\n\n## References\n\n1. [[s1|Source one]]")}},
		{name: "E_RAW_ORPHAN", want: "E_RAW_ORPHAN", changes: map[string]string{"raw/stray.txt": "x"}},
		{name: "E_SHA256", want: "E_SHA256", changes: map[string]string{"raw/s1-a.bin": "changed"}},
		{name: "a Hubs line that links a non-hub is E_HUBS_LINE", want: "E_HUBS_LINE", changes: map[string]string{
			"notes/n2.md": n("Hubs: [[n1|Leaders are unique]]\n\n[[n1|x]]")}},
	})
}

// TestLintCitationRules ports tests/test_vault.py test_lint_citation_rules.
func TestLintCitationRules(t *testing.T) {
	ref := "\n\n## References\n\n"
	s1 := "[[s1|Source one]]"
	note := func(body string) map[string]string {
		return map[string]string{"notes/n2.md": doc("n2", "Two", "note", hubLine+"\n\n"+body)}
	}
	runCodeCases(t, []codeCase{
		{name: "in order with a page locator", absent: "E_CITATION", changes: note("A [1], B [2, p. 9]." + ref + "1. " + s1 + "\n2. " + s1)},
		{name: "first-use order", want: "E_CITATION", changes: note("A [2], B [1]." + ref + "1. " + s1 + "\n2. " + s1)},
		{name: "entry never cited", want: "E_CITATION", changes: note("A [1]." + ref + "1. " + s1 + "\n2. " + s1)},
		{name: "References not last H2", want: "E_CITATION", changes: note("A [1]." + ref + "1. " + s1 + "\n\n## Later\n\ntext")},
		{name: "no References", want: "E_CITATION", changes: note("A [1].")},
		{name: "no evidence", absent: "E_CITATION", changes: note("Plain text, no evidence.")},
		{name: "markdown link and wiki link are not citations", absent: "E_CITATION", changes: note("A [link](http://x) and [[n1|Leaders are unique]]")},
	})
}

// TestLintWarnings ports tests/test_vault.py test_lint_warnings.
func TestLintWarnings(t *testing.T) {
	n2 := func(body string) map[string]string {
		return map[string]string{"notes/n2.md": doc("n2", "Two", "note", hubLine+"\n\n"+body)}
	}
	oldN3 := doc("n3", "Old", "note", hubLine+"\n\n[[n1|Leaders are unique]]", kv{"status", "superseded"}, kv{"superseded_by", "n1"})
	runCodeCases(t, []codeCase{
		{name: "W_STALE_ALIAS", want: "W_STALE_ALIAS", changes: n2("See [[n1|Old title]].")},
		{name: "W_DEAD_END", want: "W_DEAD_END", changes: n2("Nothing linked.")},
		{name: "W_MARKER", want: "W_MARKER", changes: n2("TODO: later. [[n1|Leaders are unique]]")},
		{name: "W_WORD_COUNT", want: "W_WORD_COUNT", changes: n2(strings.Repeat("word ", 500) + "[[n1|Leaders are unique]]")},
		{name: "W_SUPERSEDED_LINK", want: "W_SUPERSEDED_LINK", changes: map[string]string{
			"notes/n3.md": oldN3, "notes/n2.md": doc("n2", "Two", "note", hubLine+"\n\nSee [[n3|Old]].")}},
	})

	t.Run("Extract and code fences are not scanned", func(t *testing.T) {
		src := doc("s1", "Source one", "source",
			"Hubs: [[000000000000src0|Sources]]\n\n## Extract\n\nTODO: [[x]] [3]\n\n```\nGAP: [[y]]\n```",
			kv{"raw", "raw/s1-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})
		got := runIn(t, "dev", with_(t, map[string]string{"sources/s1.md": src}), "lint")
		if got.code != 0 || got.stdout != "0 errors, 0 warnings\n" {
			t.Errorf("got code %d stdout %q, want 0 \"0 errors, 0 warnings\\n\"", got.code, got.stdout)
		}
	})
}

// TestLintStaged ports tests/test_vault.py test_load_staged: the staged tree wins over the worktree.
func TestLintStaged(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Break a staged file and add an untracked raw file, both after staging.
	if err := os.WriteFile("notes/n1.md", []byte("changed after staging"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("raw/stray.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	staged := run(env, "lint", "--staged")
	if staged.code != 0 || staged.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("lint --staged: code %d stdout %q stderr %q, want 0 clean", staged.code, staged.stdout, staged.stderr)
	}
	plain := codesOf(t, run(env, "lint", "--json"))
	if !plain["E_FRONTMATTER"] || !plain["E_RAW_ORPHAN"] {
		t.Errorf("plain lint codes %v, want E_FRONTMATTER and E_RAW_ORPHAN", plain)
	}
}

func TestLintStagedOutsideGitRepo(t *testing.T) {
	got := runIn(t, "dev", baseVault(t), "lint", "--staged")
	if got.code != 2 || !strings.Contains(got.stderr, "git ls-files") {
		t.Errorf("got code %d stderr %q, want 2 and a git ls-files error", got.code, got.stderr)
	}
}

// TestFix ports tests/test_vault.py test_fix.
func TestFix(t *testing.T) {
	body := hubLine + "\n\nSee [[n1|%s]] and [[n1]] and [[nope|X]].\n\n```\n[[n1|Old title]]\n```\n\n## Extract\n\n[[n1|Old title]]"
	files := with_(t, map[string]string{"notes/n2.md": doc("n2", "Two", "note", fmt.Sprintf(body, "Old title"))})
	env := setupVault(t, "dev", files)
	want := doc("n2", "Two", "note", fmt.Sprintf(body, "Leaders are unique"))

	got := run(env, "fix")
	if got.code != 0 || got.stdout != "fixed notes/n2.md\n" {
		t.Errorf("first fix: code %d stdout %q stderr %q, want 0 \"fixed notes/n2.md\\n\"", got.code, got.stdout, got.stderr)
	}
	if b, err := os.ReadFile("notes/n2.md"); err != nil || string(b) != want {
		t.Errorf("notes/n2.md = %q (%v), want %q", b, err, want)
	}
	got = run(env, "fix")
	if got.code != 0 || got.stdout != "" {
		t.Errorf("second fix: code %d stdout %q, want 0 and no output", got.code, got.stdout)
	}
}

func TestFixJSON(t *testing.T) {
	files := with_(t, map[string]string{"notes/n2.md": doc("n2", "Two", "note", hubLine+"\n\nSee [[n1|Old title]].")})
	env := setupVault(t, "dev", files)
	for _, want := range []string{
		`{"ok":true,"fixed":["notes/n2.md"]}` + "\n",
		`{"ok":true,"fixed":[]}` + "\n",
	} {
		if got := run(env, "fix", "--json"); got.code != 0 || got.stdout != want {
			t.Errorf("fix --json: code %d stdout %q, want 0 %q", got.code, got.stdout, want)
		}
	}
}
