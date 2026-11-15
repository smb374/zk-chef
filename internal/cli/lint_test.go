package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Fixtures ported from tests/test_vault.py (IDX, HUB, doc, RAW, SHA, base, codes, with_), in the v0.3 format.
const (
	idxID    = "index"
	hubLine  = "Hubs: [[" + idxID + "|Index]]"
	rawBytes = "raw bytes\n\nwith newlines\n"
)

var rawSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(rawBytes)))

// kv is one extra frontmatter line, in order. doc turns the keys review and status into tags.
type kv [2]string

// doc builds a v0.3 page: title, date and a tags list that starts with the kind. The ID is the file stem, so doc takes none.
func doc(title, kind, body string, extra ...kv) string {
	tags := []string{"kind:" + kind}
	var fm strings.Builder
	for _, e := range extra {
		if e[0] == "review" || e[0] == "status" {
			tags = append(tags, e[0]+":"+e[1])
			continue
		}
		fm.WriteString(e[0] + ": " + e[1] + "\n")
	}
	return "---\ntitle: " + title + "\ndate: 2026-01-02T03:04:05Z\ntags: [" + strings.Join(tags, ", ") + "]\n" + fm.String() + "---\n" + body + "\n"
}

// baseVault is the scaffold config, the root index, a Sources hub (hubsrc), a Projects hub (hubprj), one source and one note.
func baseVault(t *testing.T) map[string]string {
	t.Helper()
	cfg, err := os.ReadFile(filepath.Join("..", "..", "scaffold", ".zk-chef", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".zk-chef/config.toml": string(cfg),
		"index.md":             doc("Index", "hub", "Root of the vault. Every note reaches this hub through Hubs lines.\n\n- [[hubprj|Projects]]\n- [[hubsrc|Sources]]"),
		"notes/hubprj.md":      doc("Projects", "hub", "Hubs: [[index|Index]]\n\nOne project hub per project sits under this hub."),
		"notes/hubsrc.md":      doc("Sources", "hub", "Hubs: [[index|Index]]\n\nOne source note per raw file."),
	}
	files["notes/src001.md"] = doc("Source one", "source",
		"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review",
		kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})
	files["raw/src001.bin"] = rawBytes
	files["notes/not001.md"] = doc("Leaders are unique", "note",
		hubLine+"\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [[src001|Source one]]")
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
	linksN1 := hubLine + "\n\n[[not001|Leaders are unique]]"
	tests := []struct {
		name     string
		file     string
		wantMsg  string // substring of one E_FRONTMATTER message; empty means no E_FRONTMATTER at all
		wantPath string
	}{
		{"unquoted numeric title is not a string", doc("123", "note", linksN1),
			"title must be a non-empty string (quote it in YAML)", "notes/not002.md"},
		{"yes is a string in YAML 1.2, a bool in PyYAML", doc("yes", "note", linksN1), "", ""},
		{"non-string key is accepted, as in PyYAML", doc("Two", "note", linksN1, kv{"1", "x"}), "", ""},
		{"duplicate key is an error, PyYAML keeps the last", doc("Two", "note", linksN1, kv{"title", "Three"}),
			"invalid YAML: ", "notes/not002.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, "dev", with_(t, map[string]string{"notes/not002.md": tt.file}), "lint", "--json")
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
	n := func(body string) string { return doc("Two", "note", body) }
	runCodeCases(t, []codeCase{
		{name: "E_BROKEN_LINK", want: "E_BROKEN_LINK", changes: map[string]string{
			"notes/not002.md": n(hubLine + "\n\nSee [[nope|X]] and [[not001|Leaders are unique]].")}},
		{name: "E_FRONTMATTER", want: "E_FRONTMATTER", changes: map[string]string{"notes/not002.md": "no frontmatter"}},
		{name: "E_DUPLICATE_ID", want: "E_DUPLICATE_ID", changes: map[string]string{"notes/sub/not001.md": doc("Two", "note", hubLine)}},
		{name: "E_HUBS_LINE", want: "E_HUBS_LINE", changes: map[string]string{
			"notes/not002.md": n("Intro\n\n[[not001|Leaders are unique]]")}},
		{name: "E_UNREACHABLE", want: "E_UNREACHABLE", changes: map[string]string{
			"notes/hub001.md": doc("H1", "hub", "Hubs: [[hub002|H2]]"),
			"notes/hub002.md": doc("H2", "hub", "Hubs: [[hub001|H1]]"),
			"notes/not002.md": n("Hubs: [[hub001|H1]]\n\n[[not001|Leaders are unique]]")}},
		{name: "E_CITATION", want: "E_CITATION", changes: map[string]string{
			"notes/not002.md": n(hubLine + "\n\nClaim [2].\n\n## References\n\n1. [[src001|Source one]]")}},
		{name: "E_RAW_ORPHAN", want: "E_RAW_ORPHAN", changes: map[string]string{"raw/stray.txt": "x"}},
		{name: "E_SHA256", want: "E_SHA256", changes: map[string]string{"raw/src001.bin": "changed"}},
		{name: "a Hubs line that links a non-hub is E_HUBS_LINE", want: "E_HUBS_LINE", changes: map[string]string{
			"notes/not002.md": n("Hubs: [[not001|Leaders are unique]]\n\n[[not001|x]]")}},
		{name: "E_REVIEW_HEADING", want: "E_REVIEW_HEADING", changes: map[string]string{"notes/src001.md": doc("Source one", "source", "Hubs: [[hubsrc|Sources]]\n\nWhy.", kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})}},
	})
}

// srcTwo builds notes/src002.md: a source with the review tag, a Review heading and the given extra frontmatter.
func srcTwo(extra ...kv) string {
	return doc("Source two", "source", "Hubs: [[hubsrc|Sources]]\n\n## Review", append(extra, kv{"review", "unreviewed"})...)
}

// TestLintSourceEvidenceAccepted covers the source evidence rule: url with accessed, raw with sha256, or both; authors and published are optional.
func TestLintSourceEvidenceAccepted(t *testing.T) {
	url, accessed := kv{"url", "https://example.com/y"}, kv{"accessed", "2026-01-03"}
	raw, sha := kv{"raw", "raw/src002.pdf"}, kv{"sha256", rawSHA}
	tests := []struct {
		name  string
		extra []kv
		raws  map[string]string
	}{
		{"url and accessed", []kv{url, accessed}, nil},
		{"quoted accessed", []kv{url, {"accessed", `"2026-01-03"`}}, nil},
		{"raw and sha256 with extension", []kv{raw, sha}, map[string]string{"raw/src002.pdf": rawBytes}},
		{"raw and sha256 without extension", []kv{{"raw", "raw/src002"}, sha}, map[string]string{"raw/src002": rawBytes}},
		{"both", []kv{url, accessed, raw, sha}, map[string]string{"raw/src002.pdf": rawBytes}},
		{"authors and a date published", []kv{url, accessed, {"authors", "[Doe, Roe]"}, {"published", "2026-01-02"}}, nil},
		{"year published", []kv{url, accessed, {"published", "2026"}}, nil},
		{"quoted date published", []kv{url, accessed, {"published", `"2026-01-02"`}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := map[string]string{"notes/src002.md": srcTwo(tt.extra...)}
			maps.Copy(changes, tt.raws)
			if got := findingsOf(t, with_(t, changes)); len(got) != 0 {
				t.Errorf("findings %+v, want none", got)
			}
		})
	}
}

// TestLintSourceEvidenceRejected covers what the source evidence rule refuses.
func TestLintSourceEvidenceRejected(t *testing.T) {
	url, accessed := kv{"url", "https://example.com/y"}, kv{"accessed", "2026-01-03"}
	tests := []struct {
		name  string
		extra []kv
		want  string // part of the E_FRONTMATTER message
	}{
		{"neither url nor raw", nil, "source needs url with accessed, or raw with sha256"},
		{"url without accessed", []kv{url}, "url needs accessed"},
		{"accessed without url", []kv{accessed}, "accessed needs url"},
		{"raw without sha256", []kv{{"raw", "raw/src002"}}, "raw needs sha256"},
		{"sha256 without raw", []kv{{"sha256", rawSHA}}, "sha256 needs raw"},
		{"bad accessed", []kv{url, {"accessed", "2026-13-01"}}, "accessed must be YYYY-MM-DD"},
		{"published with one digit month", []kv{url, accessed, {"published", `"2026-1-2"`}}, "published must be YYYY or YYYY-MM-DD"},
		{"published not a date", []kv{url, accessed, {"published", "2026-13-01"}}, "published must be YYYY or YYYY-MM-DD"},
		{"published two digit year", []kv{url, accessed, {"published", "26"}}, "published must be YYYY or YYYY-MM-DD"},
		{"published text", []kv{url, accessed, {"published", "last spring"}}, "published must be YYYY or YYYY-MM-DD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, f := range findingsOf(t, with_(t, map[string]string{"notes/src002.md": srcTwo(tt.extra...)})) {
				found = found || (f.Code == "E_FRONTMATTER" && f.Path == "notes/src002.md" && strings.Contains(f.Msg, tt.want))
			}
			if !found {
				t.Errorf("no E_FRONTMATTER on notes/src002.md containing %q", tt.want)
			}
		})
	}
}

// TestLintSourceRawName covers the name of a raw copy: raw/<stem> with an optional .<ext>.
func TestLintSourceRawName(t *testing.T) {
	for _, raw := range []string{"raw/src002-a.bin", "raw/other.bin", "raw/src002.", "raw/src0021.bin", "raw/sub/src002.bin", "src002.bin"} {
		t.Run(raw, func(t *testing.T) {
			files := with_(t, map[string]string{
				"notes/src002.md": srcTwo(kv{"raw", raw}, kv{"sha256", rawSHA}),
				raw:               rawBytes,
			})
			found := false
			for _, f := range findingsOf(t, files) {
				found = found || (f.Code == "E_FRONTMATTER" && f.Path == "notes/src002.md" && strings.Contains(f.Msg, "raw must be raw/src002 or raw/src002.<ext>"))
			}
			if !found {
				t.Errorf("no E_FRONTMATTER on notes/src002.md that names the raw/src002 form")
			}
		})
	}
}

// findingsOf runs lint --json over files and returns the findings.
func findingsOf(t *testing.T, files map[string]string) []finding {
	t.Helper()
	got := runIn(t, "dev", files, "lint", "--json")
	var r report
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
		t.Fatalf("stdout %q stderr %q is not a lint report: %v", got.stdout, got.stderr, err)
	}
	return r.Findings
}

// TestLintPageFormat covers the v0.3 page rules: the stem is the ID, the kind is one tag, date and tags are required, notes/ is flat.
func TestLintPageFormat(t *testing.T) {
	note := func(fm string) string {
		return "---\n" + fm + "---\nHubs: [[hubsrc|Sources]]\n\nSee [[not001|Leaders are unique]].\n"
	}
	valid := "title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind:note]\n"
	tests := []struct {
		name    string
		changes map[string]string
		want    []finding // exactly these findings; the message is checked by substring
	}{
		{"a valid page is clean", map[string]string{"notes/notaaa.md": note(valid)}, nil},
		{"no kind tag", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [topic:x]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"two kind tags", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind:note, kind:hub]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"unknown kind", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind:essay]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"no date", map[string]string{"notes/notaaa.md": note("title: Other\ntags: [kind:note]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "date"}}},
		{"no tags", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "tags"}}},
		{"stem with capitals", map[string]string{"notes/NotAaa.md": note(valid)},
			[]finding{{"E_FRONTMATTER", "notes/NotAaa.md", 1, "stem"}}},
		{"stem too short", map[string]string{"notes/short.md": note(valid)},
			[]finding{{"E_FRONTMATTER", "notes/short.md", 1, "stem"}}},
		{"stem too long", map[string]string{"notes/notaaaa.md": note(valid)},
			[]finding{{"E_FRONTMATTER", "notes/notaaaa.md", 1, "stem"}}},
		{"index is a stem only at the root", map[string]string{"notes/index.md": note(valid)},
			[]finding{{"E_DUPLICATE_ID", "notes/index.md", 1, "index"}, {"E_FRONTMATTER", "notes/index.md", 1, "stem"}}},
		{"subfolder in notes/", map[string]string{"notes/sub/notaaa.md": note(valid)},
			[]finding{{"E_FRONTMATTER", "notes/sub/notaaa.md", 1, "subfolder"}}},
		{"duplicate stem", map[string]string{"notes/sub/not001.md": note(valid)},
			[]finding{{"E_DUPLICATE_ID", "notes/sub/not001.md", 1, "not001"}, {"E_FRONTMATTER", "notes/sub/not001.md", 1, "subfolder"}}},
		{"E_WRONG_FOLDER is gone: a hub sits in notes/", map[string]string{"notes/hubaaa.md": doc("Hub", "hub", "Hubs: [[index|Index]]\n\nA hub.")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findingsOf(t, with_(t, tt.changes))
			if len(got) != len(tt.want) {
				t.Fatalf("findings %+v, want %d like %+v", got, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				if g := got[i]; g.Code != w.Code || g.Path != w.Path || g.Line != w.Line || !strings.Contains(g.Msg, w.Msg) {
					t.Errorf("finding %d = %+v, want code %s path %s line %d, message containing %q", i, g, w.Code, w.Path, w.Line, w.Msg)
				}
			}
		})
	}
}

// TestLintHubsLineSeparator: the Hubs line joins links with ", "; the v0.2 " · " is E_HUBS_LINE.
func TestLintHubsLineSeparator(t *testing.T) {
	page := func(hubs string) map[string]string {
		return map[string]string{"notes/notaaa.md": doc("Other", "note", hubs+"\n\nSee [[not001|Leaders are unique]].")}
	}
	t.Run("comma separator is clean", func(t *testing.T) {
		if got := findingsOf(t, with_(t, page("Hubs: [[hubsrc|Sources]], [[hubprj|Projects]]"))); len(got) != 0 {
			t.Errorf("findings %+v, want none", got)
		}
	})
	t.Run("middle dot separator is E_HUBS_LINE", func(t *testing.T) {
		got := findingsOf(t, with_(t, page("Hubs: [[hubsrc|Sources]] · [[hubprj|Projects]]")))
		if !slices.ContainsFunc(got, func(f finding) bool { return f.Code == "E_HUBS_LINE" && f.Path == "notes/notaaa.md" && f.Line == 6 }) {
			t.Errorf("findings %+v, want E_HUBS_LINE at notes/notaaa.md:6", got)
		}
	})
}

// TestLintCitationRules ports tests/test_vault.py test_lint_citation_rules.
func TestLintCitationRules(t *testing.T) {
	ref := "\n\n## References\n\n"
	src001 := "[[src001|Source one]]"
	note := func(body string) map[string]string {
		return map[string]string{"notes/not002.md": doc("Two", "note", hubLine+"\n\n"+body)}
	}
	runCodeCases(t, []codeCase{
		{name: "in order with a page locator", absent: "E_CITATION", changes: note("A [1], B [2, p. 9]." + ref + "1. " + src001 + "\n2. " + src001)},
		{name: "first-use order", want: "E_CITATION", changes: note("A [2], B [1]." + ref + "1. " + src001 + "\n2. " + src001)},
		{name: "entry never cited", want: "E_CITATION", changes: note("A [1]." + ref + "1. " + src001 + "\n2. " + src001)},
		{name: "References not last H2", want: "E_CITATION", changes: note("A [1]." + ref + "1. " + src001 + "\n\n## Later\n\ntext")},
		{name: "no References", want: "E_CITATION", changes: note("A [1].")},
		{name: "no evidence", absent: "E_CITATION", changes: note("Plain text, no evidence.")},
		{name: "markdown link and wiki link are not citations", absent: "E_CITATION", changes: note("A [link](http://x) and [[not001|Leaders are unique]]")},
	})
}

// TestLintWarnings ports tests/test_vault.py test_lint_warnings.
func TestLintWarnings(t *testing.T) {
	not002 := func(body string) map[string]string {
		return map[string]string{"notes/not002.md": doc("Two", "note", hubLine+"\n\n"+body)}
	}
	// hubMap builds hub hub001 under the Index, with members nota01 and nota02 that link each other.
	hubMap := func(body string) map[string]string {
		m := "Hubs: [[hub001|H1]]\n\n"
		return map[string]string{
			"notes/hub001.md": doc("H1", "hub", "Hubs: [[index|Index]]\n\n"+body),
			"notes/nota01.md": doc("A", "note", m+"[[nota02|B]] refines it."),
			"notes/nota02.md": doc("B", "note", m+"[[nota01|A]] comes first."),
		}
	}
	oldN3 := doc("Old", "note", hubLine+"\n\n[[not001|Leaders are unique]]", kv{"status", "superseded"}, kv{"superseded_by", "not001"})
	runCodeCases(t, []codeCase{
		{name: "W_STALE_ALIAS in a note's References", want: "W_STALE_ALIAS", changes: not002("A claim [1].\n\n## References\n\n1. [[src001|Old title]]")},
		{name: "W_STALE_ALIAS on a note's Hubs line", want: "W_STALE_ALIAS", changes: map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[index|Old hub]]\n\n[[not001|Leaders are unique]]")}},
		{name: "W_STALE_ALIAS on a hub's prose", want: "W_STALE_ALIAS", changes: hubMap("See [[nota01|Old title]].")},
		{name: "a stale alias in note Prose is the writer's wording", absent: "W_STALE_ALIAS", changes: not002("See [[not001|Old title]].")},
		{name: "a stale alias under a note H2 before References is Prose", absent: "W_STALE_ALIAS", changes: not002("## See also\n\n[[not001|Old title]]")},
		{name: "W_DEAD_END", want: "W_DEAD_END", changes: not002("Nothing linked.")},
		{name: "W_MARKER", want: "W_MARKER", changes: not002("TODO: later. [[not001|Leaders are unique]]")},
		{name: "W_WORD_COUNT", want: "W_WORD_COUNT", changes: not002(strings.Repeat("word ", 500) + "[[not001|Leaders are unique]]")},
		{name: "W_SUPERSEDED_LINK", want: "W_SUPERSEDED_LINK", changes: map[string]string{
			"notes/not003.md": oldN3, "notes/not002.md": doc("Two", "note", hubLine+"\n\nSee [[not003|Old]].")}},
		{name: "W_HUB_MAP", want: "W_HUB_MAP", changes: hubMap("A short scope line.")},
		{name: "a hub map with a GAP: line is clean", absent: "W_HUB_MAP", changes: hubMap("GAP: a note on retries.")},
		{name: "a hub map with a walk is clean", absent: "W_HUB_MAP", changes: hubMap("1. [[nota01|A]]\n2. [[nota02|B]] follows A.")},
	})

	t.Run("Extract and code fences are not scanned", func(t *testing.T) {
		src := doc("Source one", "source",
			"Hubs: [[hubsrc|Sources]]\n\n## Extract\n\nTODO: [[x]] [3]\n\n```\nGAP: [[y]]\n```\n\n## Review",
			kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})
		got := runIn(t, "dev", with_(t, map[string]string{"notes/src001.md": src}), "lint")
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
	if err := os.WriteFile("notes/not001.md", []byte("changed after staging"), 0o644); err != nil {
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
	body := "Hubs: [[index|%s]]\n\nSee [[not001|Old title]] and [[not001]] and [[nope|X]].\n\n## See also\n\n[[not001|Old title]]\n\n```\n[[not001|Old title]]\n```\n\n## Extract\n\n[[not001|Old title]]\n\n## References\n\n1. [[not001|%s]]"
	files := with_(t, map[string]string{"notes/not002.md": doc("Two", "note", fmt.Sprintf(body, "Old hub", "Old title"))})
	env := setupVault(t, "dev", files)
	// Only the Hubs line and References change; Prose, the H2 before References, code and Extract stay.
	want := doc("Two", "note", fmt.Sprintf(body, "Index", "Leaders are unique"))

	got := run(env, "fix")
	if got.code != 0 || got.stdout != "fixed notes/not002.md\n" {
		t.Errorf("first fix: code %d stdout %q stderr %q, want 0 \"fixed notes/not002.md\\n\"", got.code, got.stdout, got.stderr)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != want {
		t.Errorf("notes/not002.md = %q (%v), want %q", b, err, want)
	}
	got = run(env, "fix")
	if got.code != 0 || got.stdout != "" {
		t.Errorf("second fix: code %d stdout %q, want 0 and no output", got.code, got.stdout)
	}
}

// TestFixProseOfOtherKinds checks that hub, source and log docs refresh Aliases on every scanned line.
func TestFixProseOfOtherKinds(t *testing.T) {
	prose := func(old string) string { return "Hubs: [[index|Index]]\n\nSee [[not001|" + old + "]]." }
	files := with_(t, map[string]string{
		"notes/hub001.md": doc("H1", "hub", prose("Old title")),
		"notes/log001.md": doc("P1", "log", prose("Old title")),
		"notes/src001.md": doc("Source one", "source", prose("Old title")+"\n\n## Review",
			kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"}),
	})
	got := runIn(t, "dev", files, "fix")
	if got.code != 0 || got.stdout != "fixed notes/hub001.md\nfixed notes/log001.md\nfixed notes/src001.md\n" {
		t.Errorf("code %d stdout %q stderr %q, want 0 and the three docs fixed", got.code, got.stdout, got.stderr)
	}
	for p, want := range map[string]string{
		"notes/hub001.md": doc("H1", "hub", prose("Leaders are unique")),
		"notes/log001.md": doc("P1", "log", prose("Leaders are unique")),
	} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", p, b, err, want)
		}
	}
}

func TestFixJSON(t *testing.T) {
	files := with_(t, map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[index|Old hub]]\n\n[[not001|Leaders are unique]].")})
	env := setupVault(t, "dev", files)
	for _, want := range []string{
		`{"ok":true,"fixed":["notes/not002.md"]}` + "\n",
		`{"ok":true,"fixed":[]}` + "\n",
	} {
		if got := run(env, "fix", "--json"); got.code != 0 || got.stdout != want {
			t.Errorf("fix --json: code %d stdout %q, want 0 %q", got.code, got.stdout, want)
		}
	}
}
