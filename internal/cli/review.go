package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/smb374/zk-chef/internal/lint"
	"gopkg.in/yaml.v3"
)

// errRawChanged is the refusal when the raw file no longer matches the note's sha256.
var errRawChanged = errors.New("raw file changed since ingest; inspect it, then update sha256")

// failCode is fail with a chosen exit code.
func failCode(inv *invocation, code int, msg string) int {
	fail(inv, msg)
	return code
}

var reviewHeadRe = regexp.MustCompile(`(?m)^## Review[ \t]*\n\n?`)

// withReviewTag sets the note's review/ tag. It fails when the note then has another review state than verdict.
func withReviewTag(text, verdict string) (string, error) {
	text, err := lint.EditFrontmatter(text, func(m *yaml.Node) error { lint.SetReviewTag(m, verdict); return nil })
	if err != nil {
		return "", err
	}
	if got := lint.ParsePage("", text).Review(); got != verdict {
		return "", fmt.Errorf("review tag is %q after the edit, want %q", got, verdict)
	}
	return text, nil
}

// withReviewBlock puts block first in ## Review, as the earlier Python build did.
func withReviewBlock(text, block string) string {
	if loc := reviewHeadRe.FindStringIndex(text); loc != nil {
		return text[:loc[0]] + "## Review\n\n" + block + text[loc[1]:]
	}
	return strings.TrimRight(text, "\n") + "\n\n## Review\n\n" + block
}

// commandEnv is the process environment plus the verb's env.
func commandEnv(inv *invocation) []string { return append(os.Environ(), inv.env...) }

// clearsign signs message with gpg. It runs without --batch so the agent and pinentry can prompt.
func clearsign(inv *invocation, key, message string) (string, error) {
	args := []string{"--clearsign", "--armor"}
	if key != "" {
		args = append(args, "--local-user", key)
	}
	cmd := exec.Command("gpg", args...)
	var stdout bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = strings.NewReader(message), &stdout, inv.stderr, commandEnv(inv)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gpg --clearsign: %w", err)
	}
	return stdout.String(), nil
}

// gitRun runs git in root and returns its combined output on failure.
func gitRun(inv *invocation, root string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = commandEnv(inv)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// noteRel is the slash path of note id, relative to the vault root.
func noteRel(id string) string { return lint.NotesDir + "/" + id + ".md" }

// signSource signs a verdict on the raw file of notes/<id>.md, or on its URL when it has no raw file, and records it in the note.
func signSource(inv *invocation, root, id, verdict string) error {
	rel := noteRel(id)
	notePath := filepath.Join(root, rel)
	b, err := os.ReadFile(notePath)
	if err != nil {
		return err
	}
	src, err := lint.ParseSource(rel, string(b))
	if err != nil {
		return fmt.Errorf("%s is not a valid source note: %w", rel, err)
	}
	tagged, err := withReviewTag(string(b), verdict)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	// A source with raw and sha256 signs the raw form, even when it has a url too.
	if src.HasRaw() {
		raw, err := os.ReadFile(filepath.Join(root, src.Raw))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		sha := hex.EncodeToString(sum[:])
		if sha != src.SHA256 {
			return errRawChanged
		}
	}
	var evidence []string
	for _, kv := range src.Evidence() {
		evidence = append(evidence, kv[0]+": "+kv[1])
	}
	cfg, err := readConfig(root)
	if err != nil {
		return err
	}
	pins, err := pinnedOf(cfg)
	if err != nil {
		return err
	}
	if len(pins) == 0 {
		return fmt.Errorf("no key pinned. Run: zk-chef key add")
	}
	var key string
	if len(pins) == 1 {
		key = pins[0]
	}
	message := fmt.Sprintf("vault-review v2\n%s\nverdict: %s\ndate: %s\n", strings.Join(evidence, "\n"), verdict, time.Now().UTC().Format("2006-01-02"))
	signed, err := clearsign(inv, key, message)
	if err != nil {
		return err
	}
	block := "```\n" + strings.TrimRight(signed, " \t\r\n") + "\n```\n\n"
	if err := os.WriteFile(notePath, []byte(withReviewBlock(tagged, block)), 0o644); err != nil {
		return err
	}
	if inv.noCommit {
		return nil
	}
	// A failed commit must not leave the signed block in the note: a retry would add another.
	undo := func(err error) error {
		if werr := os.WriteFile(notePath, b, 0o644); werr != nil {
			return errors.Join(err, werr)
		}
		_ = gitRun(inv, root, "reset", "-q", "--", rel)
		return err
	}
	if err := gitRun(inv, root, "add", rel); err != nil {
		return undo(err)
	}
	if err := gitRun(inv, root, "commit", "-m", "review: "+id+" "+verdict, "--", rel); err != nil {
		return undo(err)
	}
	return nil
}

// reviewQueue walks the unreviewed sources by ID and signs the verdict the user types for each.
func reviewQueue(inv *invocation, root string) int {
	files, err := lint.LoadWorktree(root)
	if err != nil {
		return fail(inv, "zk-chef review: "+err.Error())
	}
	in := bufio.NewReader(inv.stdin)
	verdicts := map[string]string{"y": "reviewed", "d": "disputed"}
	counts := map[string]*int{"reviewed": new(int), "disputed": new(int)}
	var skipped, code int
queue:
	for _, src := range lint.Sources(files) {
		if src.Review != "unreviewed" {
			continue
		}
		// The queue shows the evidence that review signs: the raw file, else the URL.
		shown, open := "url: "+src.URL+" (accessed "+src.Accessed+")", src.URL
		if src.HasRaw() {
			shown, open = "raw: "+src.Raw, filepath.Join(root, src.Raw)
		}
		fmt.Fprintf(inv.stdout, "%s: %s\n%s\n", src.ID, src.Title, shown)
	prompt:
		for {
			fmt.Fprintln(inv.stdout, "y reviewed, d disputed, s skip, o raw path or url, q quit?")
			line, readErr := in.ReadString('\n')
			if line == "" && readErr != nil {
				break queue // EOF ends the queue like q
			}
			key := strings.ToLower(strings.TrimSpace(line))
			switch verdict := verdicts[key]; {
			case key == "o":
				fmt.Fprintln(inv.stdout, open)
			case key == "q":
				break queue
			case key == "s":
				skipped++
				break prompt
			case verdict != "":
				err := signSource(inv, root, src.ID, verdict)
				if err != nil {
					fmt.Fprintln(inv.stderr, "zk-chef review: "+src.ID+": "+err.Error())
				}
				switch {
				case errors.Is(err, errRawChanged):
					skipped++
				case err != nil:
					code = 1
					break queue
				default:
					*counts[verdict]++
					fmt.Fprintf(inv.stdout, "signed %s: %s\n", src.ID, verdict)
				}
				break prompt
			}
		}
	}
	fmt.Fprintf(inv.stdout, "reviewed %d, disputed %d, skipped %d\n", *counts["reviewed"], *counts["disputed"], skipped)
	return code
}

func reviewVerb(inv *invocation) int {
	if len(inv.args) == 0 {
		if inv.json {
			return fail(inv, "zk-chef review: --json needs SRC-ID")
		}
		root, err := filepath.Abs(inv.root)
		if err != nil {
			return fail(inv, "zk-chef review: "+err.Error())
		}
		return reviewQueue(inv, root)
	}
	if len(inv.args) != 2 {
		return fail(inv, "usage: zk-chef review [SRC-ID reviewed|disputed] [--root DIR] [--json] [--no-commit]")
	}
	id, verdict := inv.args[0], inv.args[1]
	if id == "." || id == ".." || filepath.Base(id) != id {
		return fail(inv, "zk-chef review: invalid source ID "+id)
	}
	if verdict != "reviewed" && verdict != "disputed" {
		return fail(inv, "zk-chef review: verdict must be reviewed or disputed")
	}
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef review: "+err.Error())
	}
	if _, err := os.Stat(filepath.Join(root, lint.NotesDir, id+".md")); err != nil {
		return fail(inv, "zk-chef review: no source note "+noteRel(id))
	}
	if err := signSource(inv, root, id, verdict); err != nil {
		return failCode(inv, 1, "zk-chef review: "+err.Error())
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK        bool   `json:"ok"`
			ID        string `json:"id"`
			Verdict   string `json:"verdict"`
			Path      string `json:"path"`
			Committed bool   `json:"committed"`
		}{true, id, verdict, noteRel(id), !inv.noCommit})
		return 0
	}
	fmt.Fprintf(inv.stdout, "signed %s: %s\n", id, verdict)
	return 0
}
