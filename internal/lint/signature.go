package lint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const signedBegin = "-----BEGIN PGP SIGNED MESSAGE-----"

// messageKeys are the fields every signed review message must carry.
var messageKeys = []string{"path", "sha256", "verdict", "date"}

// NormFpr strips spaces and upper-cases a fingerprint.
func NormFpr(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, " ", ""))
}

// verifier checks clearsigned blocks with gpg, in a temporary GNUPGHOME that holds only the keys/ files.
type verifier struct {
	files  map[string][]byte
	pinned map[string]bool
	home   string
	setup  string // non-empty when the temporary home could not be prepared
}

func newVerifier(files map[string][]byte, pinned []string) *verifier {
	v := &verifier{files: files, pinned: map[string]bool{}}
	for _, f := range pinned {
		v.pinned[NormFpr(f)] = true
	}
	return v
}

func (v *verifier) env() []string {
	return append(os.Environ(), "GNUPGHOME="+v.home)
}

// prepare creates the temporary home and imports keys/ on first use.
func (v *verifier) prepare() {
	if v.home != "" || v.setup != "" {
		return
	}
	if _, err := exec.LookPath("gpg"); err != nil {
		v.setup = "gpg not found"
		return
	}
	home, err := os.MkdirTemp("", "zk-chef-gpg-")
	if err != nil {
		v.setup = "cannot create a temporary GNUPGHOME: " + err.Error()
		return
	}
	v.home = home
	var keys []string
	for p := range v.files {
		if strings.HasPrefix(p, "keys/") {
			keys = append(keys, p)
		}
	}
	sort.Strings(keys)
	for _, p := range keys {
		imp := exec.Command("gpg", "--batch", "--no-tty", "--import")
		imp.Stdin = bytes.NewReader(v.files[p])
		imp.Env = v.env()
		_ = imp.Run() // a bad key file only leaves its signatures unverifiable
	}
}

// verify returns the signed message text, or an error message.
func (v *verifier) verify(block string) (string, string) {
	v.prepare()
	if v.setup != "" {
		return "", v.setup
	}
	cmd := exec.Command("gpg", "--batch", "--no-tty", "--status-fd", "2", "--decrypt")
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = strings.NewReader(block), &stdout, &stderr, v.env()
	runErr := cmd.Run()
	var valid []string
	for _, ln := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(ln, "[GNUPG:] VALIDSIG") {
			valid = strings.Fields(ln)
			break
		}
	}
	if runErr != nil || len(valid) < 3 {
		return "", "signature does not verify against keys/"
	}
	if !v.pinned[NormFpr(valid[2])] && !v.pinned[NormFpr(valid[len(valid)-1])] {
		return "", fmt.Sprintf("signer %s is not pinned in %s", valid[len(valid)-1], ConfigPath)
	}
	return stdout.String(), ""
}

// close kills the gpg agent that verify started and removes the temporary home.
func (v *verifier) close() {
	if v.home == "" {
		return
	}
	_ = exec.Command("gpgconf", "--homedir", v.home, "--kill", "all").Run()
	_ = os.RemoveAll(v.home)
}

// reviewBlock is one fenced block of the ## Review section.
type reviewBlock struct {
	n    int // line of the opening fence
	text string
}

// reviewBlocks returns the signed blocks of the ## Review section, newest (first) first.
func reviewBlocks(d *doc) []reviewBlock {
	var blocks []reviewBlock
	open := false
	var cur reviewBlock
	var body []string
	for _, l := range d.lines {
		if l.section != "Review" {
			continue
		}
		if strings.HasPrefix(l.text, "```") || strings.HasPrefix(l.text, "~~~") {
			if !open {
				cur, body, open = reviewBlock{n: l.n}, nil, true
			} else {
				cur.text = strings.Join(body, "\n") + "\n"
				if strings.Contains(cur.text, signedBegin) {
					blocks = append(blocks, cur)
				}
				open = false
			}
		} else if open {
			body = append(body, l.text)
		}
	}
	return blocks
}

// parseMessage returns the fields of a vault-review v1 message, or nil.
func parseMessage(text string) map[string]string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if lines[0] != "vault-review v1" {
		return nil
	}
	fields := map[string]string{}
	for _, ln := range lines[1:] {
		if k, v, ok := strings.Cut(strings.TrimRight(ln, "\r"), ": "); ok {
			fields[k] = v
		}
	}
	for _, k := range messageKeys {
		if _, ok := fields[k]; !ok {
			return nil
		}
	}
	return fields
}

// checkReview verifies every signed block of a source note, then compares the latest one with the note.
func checkReview(d *doc, files map[string][]byte, v *verifier, add adder) {
	var messages []map[string]string
	for _, b := range reviewBlocks(d) {
		var fields map[string]string
		text, errMsg := v.verify(b.text)
		if errMsg == "" {
			fields = parseMessage(text)
			if fields == nil {
				errMsg = "signed text is not a vault-review v1 message"
			}
		}
		if errMsg != "" {
			add("E_SIGNATURE", d.path, b.n, errMsg)
		}
		messages = append(messages, fields)
	}
	review := d.str("review")
	if review == "unreviewed" {
		return // older blocks may cover older hashes
	}
	if len(messages) == 0 {
		add("E_REVIEW_MISMATCH", d.path, 1, fmt.Sprintf("review is %s but no signed block exists", review))
		return
	}
	latest := messages[0]
	if latest == nil {
		return // a bad latest block is already E_SIGNATURE
	}
	raw := d.str("raw")
	want := [][2]string{{"path", raw}, {"sha256", d.str("sha256")}, {"verdict", review}}
	for _, kv := range want {
		if latest[kv[0]] != kv[1] {
			add("E_REVIEW_MISMATCH", d.path, 1, fmt.Sprintf("latest block %s %s differs from note %s", kv[0], pyRepr(latest[kv[0]]), pyRepr(kv[1])))
		}
	}
	if b, found := files[raw]; found {
		if sum := sha256.Sum256(b); latest["sha256"] != hex.EncodeToString(sum[:]) {
			add("E_REVIEW_MISMATCH", d.path, 1, "latest block sha256 differs from the raw file")
		}
	}
}
