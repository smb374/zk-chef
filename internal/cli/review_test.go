package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Review tests port tests/test_vault.py test_review_and_signatures (review parts) and
// test_skill_dry_run step 2. They sign with real gpg and commit with real git.

// gitOut runs git in the current directory and returns trimmed stdout.
func gitOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// reviewVault builds a signed-vault fixture (signer pinned, keys/me.asc) with one unreviewed
// source src001, makes it a git repo with one commit, and returns the env for Run.
func reviewVault(t *testing.T, s signer, changes map[string]string) []string {
	t.Helper()
	env := setupVault(t, "dev", signedVault(t, s, s.fpr, changes))
	t.Setenv("GNUPGHOME", s.home)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-qm", "base"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return []string{env[0], "GNUPGHOME=" + s.home}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readNote(t *testing.T) string { return readFile(t, "notes/src001.md") }

func TestReviewSignsAndCommits(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	got := run(env, "review", "src001", "reviewed")
	if got.code != 0 || got.stdout != "signed src001: reviewed\n" {
		t.Fatalf("got code %d stdout %q stderr %q, want 0 \"signed src001: reviewed\\n\"", got.code, got.stdout, got.stderr)
	}
	note := readNote(t)
	if !strings.Contains(note, "\ntags: [kind:source, review:reviewed]\n") || strings.Contains(note, "\nreview:") || strings.Count(note, "BEGIN PGP SIGNED MESSAGE") != 1 {
		t.Errorf("note %q, want the tag review:reviewed (no review: field) and one signed block", note)
	}
	if lint := run(env, "lint"); lint.code != 0 || lint.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("lint after review: code %d stdout %q, want clean", lint.code, lint.stdout)
	}
	if subj := gitOut(t, "log", "-1", "--format=%s"); subj != "review: src001 reviewed" {
		t.Errorf("commit subject %q, want %q", subj, "review: src001 reviewed")
	}
	if st := gitOut(t, "status", "--porcelain"); st != "" {
		t.Errorf("git status %q, want clean", st)
	}
}
func TestReviewRefusesChangedRaw(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	writeFile(t, "raw/src001.bin", "changed bytes\n")
	before := readNote(t)
	head := gitOut(t, "rev-parse", "HEAD")
	// an unusable GNUPGHOME makes any gpg call fail with a different message
	env = []string{env[0], "GNUPGHOME=" + t.TempDir() + "/none"}
	got := run(env, "review", "src001", "reviewed")
	want := "raw file changed since ingest; inspect it, then update sha256"
	if got.code != 1 || !strings.Contains(got.stderr, want) || strings.Contains(got.stderr, "gpg") {
		t.Errorf("got code %d stderr %q, want 1 and %q without gpg output", got.code, got.stderr, want)
	}
	if readNote(t) != before || gitOut(t, "rev-parse", "HEAD") != head {
		t.Error("note or HEAD changed, want nothing written")
	}
}

func TestReviewUsageErrors(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	head := gitOut(t, "rev-parse", "HEAD")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown ID", []string{"review", "nope", "reviewed"}, "nope"},
		{"path as ID", []string{"review", "../notes/not001", "reviewed"}, "invalid source ID"},
		{"bad verdict", []string{"review", "src001", "maybe"}, "verdict must be reviewed or disputed"},
		{"ID only", []string{"review", "src001"}, "usage: zk-chef review"},
		{"too many args", []string{"review", "src001", "reviewed", "x"}, "usage: zk-chef review"},
		{"queue with --json", []string{"review", "--json"}, "--json needs SRC-ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(env, tt.args...)
			if got.code != 2 || !strings.Contains(got.stderr, tt.want) {
				t.Errorf("got code %d stderr %q, want 2 and %q", got.code, got.stderr, tt.want)
			}
		})
	}
	if gitOut(t, "rev-parse", "HEAD") != head || strings.Contains(readNote(t), "PGP") {
		t.Error("HEAD or note changed, want nothing written")
	}
}

func TestReviewCommitsOnlyTheNote(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	writeFile(t, "notes/not001.md", readFile(t, "notes/not001.md")+"\nstaged edit\n")
	gitOut(t, "add", "notes/not001.md")
	if got := run(env, "review", "src001", "reviewed"); got.code != 0 {
		t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
	}
	if files := gitOut(t, "show", "--name-only", "--format=", "HEAD"); files != "notes/src001.md" {
		t.Errorf("commit holds %q, want only notes/src001.md", files)
	}
	if staged := gitOut(t, "diff", "--cached", "--name-only"); staged != "notes/not001.md" {
		t.Errorf("still staged %q, want notes/not001.md", staged)
	}
}

func TestReviewSecondVerdictGoesFirst(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	if got := run(env, "review", "src001", "reviewed"); got.code != 0 {
		t.Fatalf("first: code %d stderr %q, want 0", got.code, got.stderr)
	}
	head := gitOut(t, "rev-parse", "HEAD")
	got := run(env, "review", "src001", "disputed", "--no-commit", "--json")
	want := `{"ok":true,"id":"src001","verdict":"disputed","path":"notes/src001.md","committed":false}` + "\n"
	if got.code != 0 || got.stdout != want {
		t.Fatalf("second: code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, want)
	}
	note := readNote(t)
	if n := strings.Count(note, "BEGIN PGP SIGNED MESSAGE"); n != 2 || !strings.Contains(note, "\ntags: [kind:source, review:disputed]\n") {
		t.Errorf("note %q, want two blocks and the tag review:disputed", note)
	}
	if first := strings.Index(note, "verdict: disputed"); first < 0 || first > strings.Index(note, "verdict: reviewed") {
		t.Errorf("note %q, want the disputed block before the reviewed block", note)
	}
	if lint := run(env, "lint"); lint.code != 0 {
		t.Errorf("lint: code %d stdout %q, want clean", lint.code, lint.stdout)
	}
	if gitOut(t, "rev-parse", "HEAD") != head || gitOut(t, "status", "--porcelain") != "M notes/src001.md" {
		t.Error("--no-commit made a commit or left a different tree, want one modified note")
	}
}

// runStdin calls Run from the current directory with scripted stdin.
func runStdin(env []string, stdin string, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr, env)
	return result{code, stdout.String(), stderr.String()}
}

// extraSource returns the files of notes/<id>.md with raw/<id>.bin, in the given review state.
func extraSource(id, review string) map[string]string {
	raw := "raw of " + id + "\n"
	return map[string]string{
		"notes/" + id + ".md": doc("Title "+id, "source", sourceHubs+"## Review\n\n", kv{"raw", "raw/" + id + ".bin"}, kv{"sha256", fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))}, kv{"review", review}),
		"raw/" + id + ".bin":  raw,
	}
}

// queueVault is the review fixture with unreviewed src001..s5 and an already reviewed srcr00.
func queueVault(t *testing.T, s signer) []string {
	t.Helper()
	changes := map[string]string{}
	for _, id := range []string{"src002", "src003", "src004", "src005"} {
		maps.Copy(changes, extraSource(id, "unreviewed"))
	}
	maps.Copy(changes, extraSource("srcr00", "reviewed"))
	return reviewVault(t, s, changes)
}

func TestReviewQueueKeys(t *testing.T) {
	s := newSigner(t)
	env := queueVault(t, s)
	abs, _ := filepath.Abs("raw/src004.bin")
	// src001 y, src002 d, src003 s, src004 o then unknown x then y, src005 q
	got := runStdin(env, "y\nd\ns\no\nx\ny\nq\n", "review")
	if got.code != 0 {
		t.Fatalf("code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{"src001: Source one\n", "raw: raw/src001.bin\n", "src002: Title src002\n", "signed src002: disputed\n", "\n" + abs + "\n", "reviewed 2, disputed 1, skipped 1\n"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want it to contain %q", got.stdout, want)
		}
	}
	if !strings.HasSuffix(got.stdout, "reviewed 2, disputed 1, skipped 1\n") {
		t.Errorf("stdout %q, want it to end with the summary", got.stdout)
	}
	if strings.Contains(got.stdout, "srcr00") || strings.Contains(got.stdout, "signed src005") {
		t.Errorf("stdout %q lists the reviewed source or signs src005", got.stdout)
	}
	for id, want := range map[string]string{"src001": "reviewed", "src002": "disputed", "src003": "unreviewed", "src004": "reviewed", "src005": "unreviewed"} {
		if note := readFile(t, "notes/"+id+".md"); !strings.Contains(note, "tags: [kind:source, review:"+want+"]\n") {
			t.Errorf("notes/%s.md review is not %s:\n%s", id, want, note)
		}
	}
	if subjects := gitOut(t, "log", "--format=%s", "-3"); subjects != "review: src004 reviewed\nreview: src002 disputed\nreview: src001 reviewed" {
		t.Errorf("commits %q, want three review commits, newest first", subjects)
	}
	if lint := run(env, "lint"); strings.Contains(lint.stdout, "notes/src00") { // srcr00 is reviewed without a block on purpose
		t.Errorf("lint: code %d stdout %q, want no errors", lint.code, lint.stdout)
	}
}

func TestReviewQueueEOFEndsLikeQuit(t *testing.T) {
	s := newSigner(t)
	env := queueVault(t, s)
	got := runStdin(env, "y", "review") // no newline, then EOF
	if got.code != 0 || !strings.HasSuffix(got.stdout, "reviewed 1, disputed 0, skipped 0\n") {
		t.Errorf("got code %d stdout %q, want 0 and reviewed 1, disputed 0, skipped 0", got.code, got.stdout)
	}
	if note := readFile(t, "notes/src002.md"); !strings.Contains(note, "review:unreviewed]\n") {
		t.Errorf("src002 changed after EOF:\n%s", note)
	}
}

func TestReviewQueueHashMismatchSkips(t *testing.T) {
	s := newSigner(t)
	env := queueVault(t, s)
	writeFile(t, "raw/src001.bin", "tampered\n")
	got := runStdin(env, "y\ny\nq\n", "review")
	if got.code != 0 || !strings.HasSuffix(got.stdout, "reviewed 1, disputed 0, skipped 1\n") {
		t.Errorf("got code %d stdout %q, want 0 and reviewed 1, disputed 0, skipped 1", got.code, got.stdout)
	}
	if !strings.Contains(got.stderr, "raw file changed since ingest; inspect it, then update sha256") {
		t.Errorf("stderr %q, want the refusal", got.stderr)
	}
	if note := readFile(t, "notes/src001.md"); !strings.Contains(note, "review:unreviewed]\n") {
		t.Errorf("src001 changed despite the mismatch:\n%s", note)
	}
	if note := readFile(t, "notes/src002.md"); !strings.Contains(note, "review:reviewed]\n") {
		t.Errorf("src002 not reviewed after the skip:\n%s", note)
	}
}
