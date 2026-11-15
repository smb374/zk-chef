package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type statusSource struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type statusReport struct {
	OK            bool           `json:"ok"`
	New           []statusSource `json:"new"`
	ReReview      []statusSource `json:"re_review"`
	DisputedCited []struct {
		ID      string   `json:"id"`
		CitedBy []string `json:"cited_by"`
	} `json:"disputed_cited"`
	Lint struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
	} `json:"lint"`
}

func statusJSON(t *testing.T, env []string) statusReport {
	t.Helper()
	got := run(env, "status", "--json")
	var r statusReport
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil || got.code != 0 {
		t.Fatalf("code %d stdout %q stderr %q: %v, want 0 and a status object", got.code, got.stdout, got.stderr, err)
	}
	return r
}

func mustReview(t *testing.T, env []string, id, verdict string) {
	t.Helper()
	if got := run(env, "review", id, verdict, "--no-commit"); got.code != 0 {
		t.Fatalf("review %s %s: code %d stderr %q", id, verdict, got.code, got.stderr)
	}
}

func replaceInFile(t *testing.T, path, old, new string) {
	t.Helper()
	text := readFile(t, path)
	if !strings.Contains(text, old) {
		t.Fatalf("%s has no %q", path, old)
	}
	writeFile(t, path, strings.Replace(text, old, new, 1))
}

// TestStatusNewVersusReReview: the verified block's hash decides, not file times.
//   - src001: signed, then the raw bytes changed (old mtime) and the note reset to unreviewed: re-review
//   - src002: never signed: new
//   - src003: signed, then the block was edited so it no longer verifies: new
//   - src004: signed, then only the note was reset to unreviewed; the block still matches the raw bytes: new
//   - src005: signed and unchanged: not listed
func TestStatusNewVersusReReview(t *testing.T) {
	s := newSigner(t)
	changes := map[string]string{}
	for _, id := range []string{"src002", "src003", "src004", "src005"} {
		maps.Copy(changes, extraSource(id, "unreviewed"))
	}
	env := reviewVault(t, s, changes)
	for _, id := range []string{"src001", "src003", "src004", "src005"} {
		mustReview(t, env, id, "reviewed")
	}
	replaceInFile(t, "notes/src003.md", "verdict: reviewed", "verdict: disputed")
	replaceInFile(t, "notes/src003.md", "review:reviewed", "review:unreviewed")
	replaceInFile(t, "notes/src004.md", "review:reviewed", "review:unreviewed")
	newRaw := "new edition\n"
	writeFile(t, "raw/src001.bin", newRaw)
	if err := os.Chtimes("raw/src001.bin", time.Unix(1e9, 0), time.Unix(1e9, 0)); err != nil {
		t.Fatal(err)
	}
	replaceInFile(t, "notes/src001.md", "sha256: "+rawSHA, fmt.Sprintf("sha256: %x", sha256.Sum256([]byte(newRaw))))
	replaceInFile(t, "notes/src001.md", "review:reviewed", "review:unreviewed")

	r := statusJSON(t, env)
	wantNew := []statusSource{{"src002", "notes/src002.md"}, {"src003", "notes/src003.md"}, {"src004", "notes/src004.md"}}
	wantRe := []statusSource{{"src001", "notes/src001.md"}}
	if !r.OK || !slices.Equal(r.New, wantNew) || !slices.Equal(r.ReReview, wantRe) {
		t.Errorf("status %+v, want new %v and re_review %v", r, wantNew, wantRe)
	}
}

// TestStatusDisputedCited: only live notes count; a superseded note and a source's own text do not.
func TestStatusDisputedCited(t *testing.T) {
	s := newSigner(t)
	changes := map[string]string{
		"notes/old.md":    doc("Old", "note", hubLine+"\n\nSee [[src002]].", kv{"status", "superseded"}, kv{"superseded_by", "not001"}),
		"notes/log001.md": doc("Log", "log", hubLine+"\n\nSee [[src001]]."),
	}
	maps.Copy(changes, extraSource("src002", "unreviewed"))
	env := reviewVault(t, s, changes)
	mustReview(t, env, "src001", "disputed")
	mustReview(t, env, "src002", "disputed")

	r := statusJSON(t, env)
	if len(r.DisputedCited) != 1 || r.DisputedCited[0].ID != "src001" || !slices.Equal(r.DisputedCited[0].CitedBy, []string{"notes/log001.md", "notes/not001.md"}) {
		t.Errorf("disputed_cited %+v, want only src001 cited by notes/log001.md and notes/not001.md", r.DisputedCited)
	}
	got := run(env, "status")
	want := "disputed, still cited (1):\n  src001  cited by notes/log001.md, notes/not001.md\n"
	if got.code != 0 || !strings.Contains(got.stdout, want) {
		t.Errorf("code %d stdout %q, want it to contain %q", got.code, got.stdout, want)
	}
}

// lintCountsVault has one error (a note without frontmatter) and one warning (a TODO: marker).
func lintCountsVault(t *testing.T) map[string]string {
	return with_(t, map[string]string{
		"notes/bad.md":    "no frontmatter",
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nOne leader per term [1, p. 4]. TODO: check\n\n## References\n\n1. [[src001|Source one]]"),
	})
}

func TestStatusLintCountsText(t *testing.T) {
	got := runIn(t, "dev", lintCountsVault(t), "status")
	want := "new (1):\n  src001  notes/src001.md\nre-review (0):\ndisputed, still cited (0):\nlint: 1 errors, 1 warnings\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q, want 0 %q", got.code, got.stdout, want)
	}
}

func TestStatusLintCountsJSON(t *testing.T) {
	got := runIn(t, "dev", lintCountsVault(t), "status", "--json")
	want := `{"ok":true,"new":[{"id":"src001","path":"notes/src001.md"}],"re_review":[],"disputed_cited":[],"lint":{"errors":1,"warnings":1}}` + "\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q, want 0 %q", got.code, got.stdout, want)
	}
}

func TestStatusMissingConfig(t *testing.T) {
	got := runIn(t, "dev", map[string]string{"notes/x.md": "x"}, "status")
	if got.code != 2 || !strings.Contains(got.stderr, "no .zk-chef/config.toml; run zk-chef init first") {
		t.Errorf("code %d stderr %q, want 2 and the missing-config message", got.code, got.stderr)
	}
}
