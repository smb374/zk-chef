package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// readTree returns every regular file under dir (slash path -> content).
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExportLintErrorStopsExport(t *testing.T) {
	out := t.TempDir()
	files := with_(t, map[string]string{"notes/a.md": "no frontmatter"})
	got := runIn(t, "dev", files, "export", out)
	if got.code != 1 || !strings.Contains(got.stdout, "notes/a.md:1: E_FRONTMATTER missing frontmatter\n") {
		t.Errorf("got code %d stdout %q stderr %q, want 1 and the finding on stdout", got.code, got.stdout, got.stderr)
	}
	if tree := readTree(t, out); len(tree) != 0 {
		t.Errorf("OUT has files %v, want none", tree)
	}
}

// exportVault is the base vault plus a rich source, a project log and files that must not be exported.
func exportVault(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"sources/s1.md": doc("s1", "Source one", "source",
			"Hubs: [[000000000000src0|Sources]]\n\nWhy.\n\n## Review",
			kv{"raw", "raw/s1-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"},
			kv{"url", "https://example.com/x"}, kv{"citation", "Doe 2026"}, kv{"authors", "[Doe, Roe]"}, kv{"published", "2026-01-02"}),
		"projects/p1.md":       doc("p1", "Project one", "log", hubLine+"\n\nDecision."),
		"AGENTS.md":            "agent rules",
		"keys/k.asc":           "key",
		".githooks/pre-commit": "hook",
		".zk/config.toml":      "zk",
	})
}

func TestExportCleanVaultLayout(t *testing.T) {
	out := t.TempDir()
	files := exportVault(t)
	got := runIn(t, "dev", files, "export", out)
	if got.code != 0 {
		t.Fatalf("got code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	tree := readTree(t, out)
	want := map[string]string{
		"notes/n1.md":    "---\ntitle: Leaders are unique\nkind: note\n---\n" + hubLine + "\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [[s1|Source one]]\n",
		"sources/s1.md":  "---\ntitle: Source one\nkind: source\nreview: unreviewed\nurl: https://example.com/x\ncitation: Doe 2026\nauthors: [Doe, Roe]\npublished: 2026-01-02\n---\nHubs: [[000000000000src0|Sources]]\n\nWhy.\n\n## Review\n",
		"projects/p1.md": "---\ntitle: Project one\nkind: log\n---\n" + hubLine + "\n\nDecision.\n",
	}
	for p, w := range want {
		if tree[p] != w {
			t.Errorf("%s = %q, want %q", p, tree[p], w)
		}
	}
	for _, p := range []string{"index.md", "hubs/000000000000src0.md"} {
		page, ok := tree[p]
		if !ok || !strings.HasPrefix(page, "---\ntitle: ") || strings.Contains(page, "\nid: ") {
			t.Errorf("%s = %q (present %v), want a page with title first and no id", p, page, ok)
		}
	}
	for p := range tree {
		if strings.HasPrefix(p, "raw/") || strings.HasPrefix(p, "keys/") || strings.HasPrefix(p, ".") || p == "AGENTS.md" {
			t.Errorf("OUT holds %s, which must not be exported", p)
		}
	}
}
func TestExportRefusesBadOut(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	before := readTree(t, ".")
	for name, args := range map[string][]string{
		"missing DIR":     {"export"},
		"DIR is vault":    {"export", "."},
		"DIR in vault":    {"export", "site"},
		"DIR in folder":   {"export", "notes/site"},
		"DIR with --root": {"export", "--root", ".", "./"},
	} {
		got := run(env, args...)
		if got.code == 0 || got.stdout != "" || got.stderr == "" {
			t.Errorf("%s: got code %d stdout %q stderr %q, want nonzero, no stdout, an error", name, got.code, got.stdout, got.stderr)
		}
	}
	if after := readTree(t, "."); len(after) != len(before) {
		t.Errorf("vault has %d files after refused exports, want %d", len(after), len(before))
	}
}

func TestExportLeavesVaultUnchanged(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", exportVault(t))
	before := readTree(t, ".")
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("got code %d stderr %q, want 0", got.code, got.stderr)
	}
	after := readTree(t, ".")
	if len(after) != len(before) {
		t.Errorf("vault has %d files after export, want %d", len(after), len(before))
	}
	for p, b := range before {
		if after[p] != b {
			t.Errorf("%s changed by export", p)
		}
	}
}
func TestExportJSONReport(t *testing.T) {
	out := t.TempDir()
	got := runIn(t, "dev", exportVault(t), "export", "--json", out)
	var r struct {
		OK      bool     `json:"ok"`
		Written []string `json:"written"`
		Counts  struct {
			Written int `json:"written"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
		t.Fatalf("stdout %q stderr %q is not a report: %v", got.stdout, got.stderr, err)
	}
	var onDisk []string
	for p := range readTree(t, out) {
		onDisk = append(onDisk, p)
	}
	slices.Sort(onDisk)
	if got.code != 0 || !r.OK || !slices.Equal(r.Written, onDisk) || r.Counts.Written != len(onDisk) || len(onDisk) == 0 {
		t.Errorf("got code %d report %+v, want 0, ok, written = %v and a matching count", got.code, r, onDisk)
	}
}

func TestExportHelpListsVerb(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"export", "--help"}} {
		got := runIn(t, "dev", nil, args...)
		if got.code != 0 || !strings.Contains(got.stdout, "export") {
			t.Errorf("%v: got code %d stdout %q, want 0 and export listed", args, got.code, got.stdout)
		}
	}
}
