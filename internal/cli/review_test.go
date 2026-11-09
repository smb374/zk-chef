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
// source s1, makes it a git repo with one commit, and returns the env for Run.
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

func readNote(t *testing.T) string { return readFile(t, "sources/s1.md") }

func TestReviewSignsAndCommits(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	got := run(env, "review", "s1", "reviewed")
	if got.code != 0 || got.stdout != "signed s1: reviewed\n" {
		t.Fatalf("got code %d stdout %q stderr %q, want 0 \"signed s1: reviewed\\n\"", got.code, got.stdout, got.stderr)
	}
	note := readNote(t)
	if !strings.Contains(note, "review: reviewed\n") || strings.Count(note, "BEGIN PGP SIGNED MESSAGE") != 1 {
		t.Errorf("note %q, want review: reviewed and one signed block", note)
	}
	if lint := run(env, "lint"); lint.code != 0 || lint.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("lint after review: code %d stdout %q, want clean", lint.code, lint.stdout)
	}
	if subj := gitOut(t, "log", "-1", "--format=%s"); subj != "review: s1 reviewed" {
		t.Errorf("commit subject %q, want %q", subj, "review: s1 reviewed")
	}
	if st := gitOut(t, "status", "--porcelain"); st != "" {
		t.Errorf("git status %q, want clean", st)
	}
}
func TestReviewRefusesChangedRaw(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	writeFile(t, "raw/s1-a.bin", "changed bytes\n")
	before := readNote(t)
	head := gitOut(t, "rev-parse", "HEAD")
	// an unusable GNUPGHOME makes any gpg call fail with a different message
	env = []string{env[0], "GNUPGHOME=" + t.TempDir() + "/none"}
	got := run(env, "review", "s1", "reviewed")
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
		{"path as ID", []string{"review", "../notes/n1", "reviewed"}, "invalid source ID"},
		{"bad verdict", []string{"review", "s1", "maybe"}, "verdict must be reviewed or disputed"},
		{"ID only", []string{"review", "s1"}, "usage: zk-chef review"},
		{"too many args", []string{"review", "s1", "reviewed", "x"}, "usage: zk-chef review"},
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
	writeFile(t, "notes/n1.md", readFile(t, "notes/n1.md")+"\nstaged edit\n")
	gitOut(t, "add", "notes/n1.md")
	if got := run(env, "review", "s1", "reviewed"); got.code != 0 {
		t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
	}
	if files := gitOut(t, "show", "--name-only", "--format=", "HEAD"); files != "sources/s1.md" {
		t.Errorf("commit holds %q, want only sources/s1.md", files)
	}
	if staged := gitOut(t, "diff", "--cached", "--name-only"); staged != "notes/n1.md" {
		t.Errorf("still staged %q, want notes/n1.md", staged)
	}
}

func TestReviewSecondVerdictGoesFirst(t *testing.T) {
	s := newSigner(t)
	env := reviewVault(t, s, nil)
	if got := run(env, "review", "s1", "reviewed"); got.code != 0 {
		t.Fatalf("first: code %d stderr %q, want 0", got.code, got.stderr)
	}
	head := gitOut(t, "rev-parse", "HEAD")
	got := run(env, "review", "s1", "disputed", "--no-commit", "--json")
	want := `{"ok":true,"id":"s1","verdict":"disputed","path":"sources/s1.md","committed":false}` + "\n"
	if got.code != 0 || got.stdout != want {
		t.Fatalf("second: code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, want)
	}
	note := readNote(t)
	if n := strings.Count(note, "BEGIN PGP SIGNED MESSAGE"); n != 2 || !strings.Contains(note, "review: disputed\n") {
		t.Errorf("note %q, want two blocks and review: disputed", note)
	}
	if first := strings.Index(note, "verdict: disputed"); first < 0 || first > strings.Index(note, "verdict: reviewed") {
		t.Errorf("note %q, want the disputed block before the reviewed block", note)
	}
	if lint := run(env, "lint"); lint.code != 0 {
		t.Errorf("lint: code %d stdout %q, want clean", lint.code, lint.stdout)
	}
	if gitOut(t, "rev-parse", "HEAD") != head || gitOut(t, "status", "--porcelain") != "M sources/s1.md" {
		t.Error("--no-commit made a commit or left a different tree, want one modified note")
	}
}

// runStdin calls Run from the current directory with scripted stdin.
func runStdin(env []string, stdin string, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr, env)
	return result{code, stdout.String(), stderr.String()}
}

// extraSource returns the files of sources/<id>.md with raw/<id>-a.bin, in the given review state.
func extraSource(id, review string) map[string]string {
	raw := "raw of " + id + "\n"
	return map[string]string{
		"sources/" + id + ".md": doc(id, "Title "+id, "source", sourceHubs+"## Review\n\n", kv{"raw", "raw/" + id + "-a.bin"}, kv{"sha256", fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))}, kv{"review", review}),
		"raw/" + id + "-a.bin":  raw,
	}
}

// queueVault is the review fixture with unreviewed s1..s5 and an already reviewed r0.
func queueVault(t *testing.T, s signer) []string {
	t.Helper()
	changes := map[string]string{}
	for _, id := range []string{"s2", "s3", "s4", "s5"} {
		maps.Copy(changes, extraSource(id, "unreviewed"))
	}
	maps.Copy(changes, extraSource("r0", "reviewed"))
	return reviewVault(t, s, changes)
}

func TestReviewQueueKeys(t *testing.T) {
	s := newSigner(t)
	env := queueVault(t, s)
	abs, _ := filepath.Abs("raw/s4-a.bin")
	// s1 y, s2 d, s3 s, s4 o then unknown x then y, s5 q
	got := runStdin(env, "y\nd\ns\no\nx\ny\nq\n", "review")
	if got.code != 0 {
		t.Fatalf("code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{"s1: Source one\n", "raw: raw/s1-a.bin\n", "s2: Title s2\n", "signed s2: disputed\n", "\n" + abs + "\n", "reviewed 2, disputed 1, skipped 1\n"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want it to contain %q", got.stdout, want)
		}
	}
	if !strings.HasSuffix(got.stdout, "reviewed 2, disputed 1, skipped 1\n") {
		t.Errorf("stdout %q, want it to end with the summary", got.stdout)
	}
	if strings.Contains(got.stdout, "r0") || strings.Contains(got.stdout, "signed s5") {
		t.Errorf("stdout %q lists the reviewed source or signs s5", got.stdout)
	}
	for id, want := range map[string]string{"s1": "reviewed", "s2": "disputed", "s3": "unreviewed", "s4": "reviewed", "s5": "unreviewed"} {
		if note := readFile(t, "sources/"+id+".md"); !strings.Contains(note, "review: "+want+"\n") {
			t.Errorf("sources/%s.md review is not %s:\n%s", id, want, note)
		}
	}
	if subjects := gitOut(t, "log", "--format=%s", "-3"); subjects != "review: s4 reviewed\nreview: s2 disputed\nreview: s1 reviewed" {
		t.Errorf("commits %q, want three review commits, newest first", subjects)
	}
	if lint := run(env, "lint"); strings.Contains(lint.stdout, "sources/s") { // r0 is reviewed without a block on purpose
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
	if note := readFile(t, "sources/s2.md"); !strings.Contains(note, "review: unreviewed\n") {
		t.Errorf("s2 changed after EOF:\n%s", note)
	}
}

func TestReviewQueueHashMismatchSkips(t *testing.T) {
	s := newSigner(t)
	env := queueVault(t, s)
	writeFile(t, "raw/s1-a.bin", "tampered\n")
	got := runStdin(env, "y\ny\nq\n", "review")
	if got.code != 0 || !strings.HasSuffix(got.stdout, "reviewed 1, disputed 0, skipped 1\n") {
		t.Errorf("got code %d stdout %q, want 0 and reviewed 1, disputed 0, skipped 1", got.code, got.stdout)
	}
	if !strings.Contains(got.stderr, "raw file changed since ingest; inspect it, then update sha256") {
		t.Errorf("stderr %q, want the refusal", got.stderr)
	}
	if note := readFile(t, "sources/s1.md"); !strings.Contains(note, "review: unreviewed\n") {
		t.Errorf("s1 changed despite the mismatch:\n%s", note)
	}
	if note := readFile(t, "sources/s2.md"); !strings.Contains(note, "review: reviewed\n") {
		t.Errorf("s2 not reviewed after the skip:\n%s", note)
	}
}
