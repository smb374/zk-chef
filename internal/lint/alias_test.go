package lint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// aliasBody has one stale alias per line type: Hubs line (6), Prose (8), a body H2 (12) and References (16).
const aliasBody = "Hubs: [[tgt001|Old]]\n\nProse [[tgt001|Old]].\n\n## See also\n\n[[tgt001|Old]]\n\n## References\n\n1. [[tgt001|Old]]"

var aliasLines = []int{6, 8, 12, 16}

// TestAliasScope checks which stale Aliases Fix rewrites and Lint warns about, by kind and line position.
func TestAliasScope(t *testing.T) {
	for _, tc := range []struct {
		name, kind, path, id string
		want                 []int // lines checked, out of aliasLines
		lintable             bool  // kind passes the frontmatter checks, so Lint reports it
	}{
		{"note", "note", "notes/xxx001.md", "xxx001", []int{6, 16}, true},
		{"hub", "hub", "notes/xxx001.md", "xxx001", aliasLines, true},
		{"log", "log", "notes/xxx001.md", "xxx001", aliasLines, true},
		{"root index", "hub", "index.md", "index", aliasLines, true},
		{"source", "source", "notes/xxx001.md", "xxx001", aliasLines, false},
		{"odd kind", "essay", "notes/xxx001.md", "xxx001", aliasLines, false},
		{"no kind", "", "notes/xxx001.md", "xxx001", aliasLines, false}, // Lint drops a doc with no kind
	} {
		t.Run(tc.name, func(t *testing.T) {
			tags := "[kind:" + tc.kind + "]"
			if tc.kind == "" {
				tags = "[]"
			}
			subject := "---\ntitle: X\ndate: 2026-01-02T03:04:05Z\ntags: " + tags + "\n---\n" + aliasBody + "\n"
			files := map[string]string{
				".zk-chef/config.toml": "",
				"index.md":             "---\ntitle: Index\ndate: 2026-01-02T03:04:05Z\ntags: [kind:hub]\n---\nIndex.\n",
				"notes/tgt001.md":      "---\ntitle: New\ndate: 2026-01-02T03:04:05Z\ntags: [kind:note]\n---\nHubs: [[index|Index]]\n",
				tc.path:                subject,
			}
			if tc.path == "index.md" {
				files["index.md"] = subject
			}
			bytes := map[string][]byte{}
			for p, s := range files {
				bytes[p] = []byte(s)
			}

			if tc.lintable {
				findings, err := Lint(bytes)
				if err != nil {
					t.Fatal(err)
				}
				var got []int
				for _, f := range findings {
					if f.Code == "W_STALE_ALIAS" && f.Path == tc.path {
						got = append(got, f.Line)
					}
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("W_STALE_ALIAS lines %v, want %v", got, tc.want)
				}
			}

			root := t.TempDir()
			for p, s := range files {
				full := filepath.Join(root, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Fix(root); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.path)))
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Split(subject, "\n")
			for _, n := range tc.want {
				want[n-1] = strings.Replace(want[n-1], "Old", "New", 1)
			}
			if got := string(b); got != strings.Join(want, "\n") {
				t.Errorf("after Fix:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
			}
		})
	}
}
