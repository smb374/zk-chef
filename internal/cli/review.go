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
)

// errRawChanged is the refusal when the raw file no longer matches the note's sha256.
var errRawChanged = errors.New("raw file changed since ingest; inspect it, then update sha256")

// failCode is fail with a chosen exit code.
func failCode(inv *invocation, code int, msg string) int {
	fail(inv, msg)
	return code
}

var (
	reviewLineRe = regexp.MustCompile(`(?m)^review:.*$`)
	reviewHeadRe = regexp.MustCompile(`(?m)^## Review[ \t]*\n\n?`)
)

// withReview sets the note's review: line and puts block first in ## Review, as v1 does.
func withReview(text, verdict, block string) string {
	if loc := reviewLineRe.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + "review: " + verdict + text[loc[1]:]
	}
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

// signSource signs a verdict on the raw file of sources/<id>.md and records it in the note.
func signSource(inv *invocation, root, id, verdict string) error {
	rel := "sources/" + id + ".md"
	notePath := filepath.Join(root, rel)
	b, err := os.ReadFile(notePath)
	if err != nil {
		return err
	}
	src, err := lint.ParseSource(rel, string(b))
	if err != nil {
		return fmt.Errorf("%s is not a valid source note: %w", rel, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, src.Raw))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	if sha != src.SHA256 {
		return errRawChanged
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
	message := fmt.Sprintf("vault-review v1\npath: %s\nsha256: %s\nverdict: %s\ndate: %s\n", src.Raw, sha, verdict, time.Now().UTC().Format("2006-01-02"))
	signed, err := clearsign(inv, key, message)
	if err != nil {
		return err
	}
	block := "```\n" + strings.TrimRight(signed, " \t\r\n") + "\n```\n\n"
	if err := os.WriteFile(notePath, []byte(withReview(string(b), verdict, block)), 0o644); err != nil {
		return err
	}
	if inv.noCommit {
		return nil
	}
	if err := gitRun(inv, root, "add", rel); err != nil {
		return err
	}
	return gitRun(inv, root, "commit", "-m", "review: "+id+" "+verdict, "--", rel)
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
		fmt.Fprintf(inv.stdout, "%s: %s\nraw: %s\n", src.ID, src.Title, src.Raw)
	prompt:
		for {
			fmt.Fprintln(inv.stdout, "y reviewed, d disputed, s skip, o raw path, q quit?")
			line, readErr := in.ReadString('\n')
			if line == "" && readErr != nil {
				break queue // EOF ends the queue like q
			}
			key := strings.ToLower(strings.TrimSpace(line))
			switch verdict := verdicts[key]; {
			case key == "o":
				fmt.Fprintln(inv.stdout, filepath.Join(root, src.Raw))
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
	if _, err := os.Stat(filepath.Join(root, "sources", id+".md")); err != nil {
		return fail(inv, "zk-chef review: no source note sources/"+id+".md")
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
		}{true, id, verdict, "sources/" + id + ".md", !inv.noCommit})
		return 0
	}
	fmt.Fprintf(inv.stdout, "signed %s: %s\n", id, verdict)
	return 0
}
