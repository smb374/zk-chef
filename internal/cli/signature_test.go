package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Signature tests port tests/test_vault.py test_review_and_signatures. They sign blocks with
// real gpg, the way v1 review builds the clearsigned message; review itself is ticket #7.

func requireGPG(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg not on PATH")
	}
}

// gpg runs gpg in a given GNUPGHOME and returns stdout.
func gpg(t *testing.T, home, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("gpg", append([]string{"--batch"}, args...)...)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+home)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpg %v: %v %s", args, err, err.(*exec.ExitError).Stderr)
	}
	return string(out)
}

// signer is a throwaway key in its own temp GNUPGHOME (never the real HOME).
type signer struct{ home, fpr, pub string }

func newSigner(t *testing.T) signer {
	t.Helper()
	requireGPG(t)
	home := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("gpgconf", "--homedir", home, "--kill", "all").Run() })
	gpg(t, home, "", "--passphrase", "", "--quick-gen-key", "zk-chef-test@example.com", "default", "default", "never")
	var fpr string
	for ln := range strings.SplitSeq(gpg(t, home, "", "--with-colons", "--list-keys"), "\n") {
		if strings.HasPrefix(ln, "fpr:") {
			fpr = strings.Split(ln, ":")[9]
			break
		}
	}
	return signer{home, fpr, gpg(t, home, "", "--armor", "--export", fpr)}
}

// fenced signs message and wraps it as v1 review does: a fenced block plus a blank line.
func (s signer) fenced(t *testing.T, message string) string {
	t.Helper()
	signed := gpg(t, s.home, message, "--clearsign", "--armor")
	return "```\n" + strings.TrimRight(signed, "\n") + "\n```\n\n"
}

// review signs a vault-review v1 message.
func (s signer) review(t *testing.T, path, sha, verdict string) string {
	t.Helper()
	return s.fenced(t, "vault-review v1\npath: "+path+"\nsha256: "+sha+"\nverdict: "+verdict+"\ndate: 2026-01-02\n")
}

const sourceHubs = "Hubs: [[000000000000src0|Sources]]\n\nWhy.\n\n## Extract\n\ntext\n\n"

// source builds sources/s1.md with the review blocks under ## Review, newest first.
func source(reviewState, sha string, blocks ...string) string {
	body := sourceHubs
	if len(blocks) > 0 {
		body += "## Review\n\n" + strings.Join(blocks, "")
	}
	return doc("s1", "Source one", "source", body, kv{"raw", "raw/s1-a.bin"}, kv{"sha256", sha}, kv{"review", reviewState})
}

// signedVault is the base vault with keys/me.asc, the signer pinned, and extra changes applied.
func signedVault(t *testing.T, s signer, pinned string, changes map[string]string) map[string]string {
	t.Helper()
	files := with_(t, nil)
	cfg := strings.Replace(files[".zk-chef/config.toml"], "pinned_fingerprints = []", `pinned_fingerprints = ["`+pinned+`"]`, 1)
	files[".zk-chef/config.toml"] = cfg
	files["keys/me.asc"] = s.pub
	for p, c := range changes {
		if c == "\x00delete" {
			delete(files, p)
		} else {
			files[p] = c
		}
	}
	return files
}

// fenceLine returns the 1-based line of the nth opening fence ("```") in text.
func fenceLine(text string, nth int) int {
	open := false
	for i, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "```") {
			open = !open
			if open {
				if nth--; nth == 0 {
					return i + 1
				}
			}
		}
	}
	return 0
}

func TestReviewSignatures(t *testing.T) {
	s := newSigner(t)
	const raw = "raw/s1-a.bin"
	newSHA := fmt.Sprintf("%x", sha256.Sum256([]byte("new"))) // hash of the updated raw file
	good := s.review(t, raw, rawSHA, "reviewed")
	disputed := s.review(t, raw, rawSHA, "disputed")
	zeros := strings.Repeat("0", 40)
	del := "\x00delete"

	tests := []struct {
		name     string
		pinned   string
		changes  map[string]string
		want     []string // codes that must be reported; none means lint must be clean
		wantLine int      // line of the first want code; 0 skips the check
		wantMsg  string   // substring of the message of the first want code
	}{
		{name: "valid signed review", changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, good)}},
		{name: "raw file changed after review",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, good), raw: "RAW bytes\n\nwith newlines\n"},
			want:    []string{"E_REVIEW_MISMATCH", "E_SHA256"}, wantLine: 1, wantMsg: "differs from the raw file"},
		{name: "signer not pinned", pinned: zeros,
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, good)},
			want:    []string{"E_SIGNATURE"}, wantLine: fenceLine(source("reviewed", rawSHA, good), 1), wantMsg: "is not pinned in .zk-chef/config.toml"},
		{name: "signer key not in keys/",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, good), "keys/me.asc": del},
			want:    []string{"E_SIGNATURE"}, wantLine: fenceLine(source("reviewed", rawSHA, good), 1), wantMsg: "signature does not verify against keys/"},
		{name: "signed text edited after signing",
			changes: map[string]string{"sources/s1.md": strings.Replace(source("reviewed", rawSHA, good), "verdict: reviewed", "verdict: disputed", 1)},
			want:    []string{"E_SIGNATURE"}, wantLine: fenceLine(source("reviewed", rawSHA, good), 1), wantMsg: "signature does not verify against keys/"},
		{name: "signed text is not a review message",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, s.fenced(t, "hello\n"))},
			want:    []string{"E_SIGNATURE"}, wantMsg: "signed text is not a vault-review v1 message"},
		{name: "note verdict differs from block",
			changes: map[string]string{"sources/s1.md": source("disputed", rawSHA, good)},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "latest block verdict 'reviewed' differs from note 'disputed'"},
		{name: "block path differs from note",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, s.review(t, "raw/s1-other.bin", rawSHA, "reviewed"))},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "latest block path 'raw/s1-other.bin' differs from note 'raw/s1-a.bin'"},
		{name: "block sha256 differs from note",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, s.review(t, raw, newSHA, "reviewed"))},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "latest block sha256 '" + newSHA + "' differs from note '" + rawSHA + "'"},
		{name: "same-name raw update, unreviewed, old block kept",
			changes: map[string]string{"sources/s1.md": source("unreviewed", newSHA, good), raw: "new"},
		},
		{name: "same-name raw update claims reviewed with a stale block",
			changes: map[string]string{"sources/s1.md": source("reviewed", newSHA, good), raw: "new"},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "latest block sha256"},
		{name: "reviewed with no signed block",
			changes: map[string]string{"sources/s1.md": source("reviewed", rawSHA, "```\njust text\n```\n\n")},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "review is reviewed but no signed block exists"},
		{name: "second review goes first",
			changes: map[string]string{"sources/s1.md": source("disputed", rawSHA, disputed, good)}},
		{name: "first block is the latest",
			changes: map[string]string{"sources/s1.md": source("disputed", rawSHA, good, disputed)},
			want:    []string{"E_REVIEW_MISMATCH"}, wantLine: 1, wantMsg: "latest block verdict 'reviewed' differs from note 'disputed'"},
		{name: "bad older block is reported at its fence",
			changes: map[string]string{"sources/s1.md": source("disputed", rawSHA, disputed, strings.Replace(good, "verdict: reviewed", "verdict: disputed", 1))},
			want:    []string{"E_SIGNATURE"}, wantLine: fenceLine(source("disputed", rawSHA, disputed, good), 2), wantMsg: "does not verify"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinned := tt.pinned
			if pinned == "" {
				pinned = s.fpr
			}
			got := runIn(t, "dev", signedVault(t, s, pinned, tt.changes), "lint", "--json")
			var r report
			if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
				t.Fatalf("stdout %q stderr %q: %v", got.stdout, got.stderr, err)
			}
			codes := codesOf(t, got)
			if len(tt.want) == 0 {
				if len(codes) != 0 {
					t.Errorf("findings %+v, want a clean lint", r.Findings)
				}
				return
			}
			for _, c := range tt.want {
				if !codes[c] {
					t.Errorf("findings %+v, want %s", r.Findings, c)
				}
			}
			var first *finding
			for i, f := range r.Findings {
				if f.Code == tt.want[0] {
					first = &r.Findings[i]
					break
				}
			}
			if first == nil {
				return
			}
			if tt.wantLine != 0 && first.Line != tt.wantLine {
				t.Errorf("%s at line %d, want %d", first.Code, first.Line, tt.wantLine)
			}
			if !strings.Contains(first.Msg, tt.wantMsg) {
				t.Errorf("%s message %q, want it to contain %q", first.Code, first.Msg, tt.wantMsg)
			}
		})
	}
}
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReviewSignaturesStaged: the commit hook runs lint --staged, so the staged note must be checked, not the worktree.
func TestReviewSignaturesStaged(t *testing.T) {
	s := newSigner(t)
	good := s.review(t, "raw/s1-a.bin", rawSHA, "reviewed")
	tampered := strings.Replace(good, "verdict: reviewed", "verdict: disputed", 1)
	env := setupVault(t, "dev", signedVault(t, s, s.fpr, map[string]string{"sources/s1.md": source("reviewed", rawSHA, tampered)}))
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile(t, "sources/s1.md", source("reviewed", rawSHA, good))

	if codes := codesOf(t, run(env, "lint", "--json")); len(codes) != 0 {
		t.Errorf("worktree lint codes %v, want none", codes)
	}
	staged := run(env, "lint", "--staged", "--json")
	if staged.code != 1 || !codesOf(t, staged)["E_SIGNATURE"] {
		t.Errorf("lint --staged: code %d stdout %q, want 1 and E_SIGNATURE", staged.code, staged.stdout)
	}
	if out, err := exec.Command("git", "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if staged := run(env, "lint", "--staged"); staged.code != 0 || staged.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("lint --staged after fix: code %d stdout %q, want clean", staged.code, staged.stdout)
	}
}

func TestReviewSignaturesGPGMissing(t *testing.T) {
	s := newSigner(t)
	good := s.review(t, "raw/s1-a.bin", rawSHA, "reviewed")
	files := signedVault(t, s, s.fpr, map[string]string{"sources/s1.md": source("reviewed", rawSHA, good)})
	t.Setenv("PATH", t.TempDir())
	got := runIn(t, "dev", files, "lint")
	want := "sources/s1.md:" + fmt.Sprint(fenceLine(source("reviewed", rawSHA, good), 1)) + ": E_SIGNATURE gpg not found\n1 errors, 0 warnings\n"
	if got.code != 1 || got.stdout != want {
		t.Errorf("got code %d stdout %q, want 1 %q", got.code, got.stdout, want)
	}
}

// TestReviewSignaturesIgnoresUserKeyring: a key in the caller's GNUPGHOME does not count; only keys/ does.
func TestReviewSignaturesIgnoresUserKeyring(t *testing.T) {
	s := newSigner(t)
	good := s.review(t, "raw/s1-a.bin", rawSHA, "reviewed")
	files := signedVault(t, s, s.fpr, map[string]string{"sources/s1.md": source("reviewed", rawSHA, good), "keys/me.asc": "\x00delete"})
	env := setupVault(t, "dev", files)
	t.Setenv("GNUPGHOME", s.home)
	got := run(env, "lint", "--json")
	if got.code != 1 || !codesOf(t, got)["E_SIGNATURE"] {
		t.Errorf("got code %d stdout %q, want 1 and E_SIGNATURE", got.code, got.stdout)
	}
}

// TestReviewSignaturesRemovesTempHome: the temporary GNUPGHOME is gone after the run.
func TestReviewSignaturesRemovesTempHome(t *testing.T) {
	s := newSigner(t)
	good := s.review(t, "raw/s1-a.bin", rawSHA, "reviewed")
	files := signedVault(t, s, s.fpr, map[string]string{"sources/s1.md": source("reviewed", rawSHA, good)})
	env := setupVault(t, "dev", files)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	got := run(env, "lint", "--json")
	if got.code != 0 {
		t.Fatalf("got code %d stdout %q, want a clean lint", got.code, got.stdout)
	}
	if left, err := os.ReadDir(tmp); err != nil || len(left) != 0 {
		t.Errorf("TMPDIR holds %v (%v) after lint, want it empty", left, err)
	}
}
