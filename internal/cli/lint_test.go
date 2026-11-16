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
	tags := []string{"kind/" + kind}
	var fm strings.Builder
	for _, e := range extra {
		if e[0] == "review" || e[0] == "status" {
			tags = append(tags, e[0]+"/"+e[1])
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
		{"url with a newline", []kv{{"url", `"https://example.com/y\npath: raw/x"`}, accessed}, "url must not contain control characters"},
		{"accessed with a control character", []kv{url, {"accessed", `"2026-01-03\u0007"`}}, "accessed must not contain control characters"},
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
	valid := "title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind/note]\n"
	tests := []struct {
		name    string
		changes map[string]string
		want    []finding // exactly these findings; the message is checked by substring
	}{
		{"a valid page is clean", map[string]string{"notes/notaaa.md": note(valid)}, nil},
		{"no kind tag", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [topic/x]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"two kind tags", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind/note, kind/hub]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"unknown kind", map[string]string{"notes/notaaa.md": note("title: Other\ndate: 2026-01-02T03:04:05Z\ntags: [kind/essay]\n")},
			[]finding{{"E_FRONTMATTER", "notes/notaaa.md", 1, "kind"}}},
		{"no date", map[string]string{"notes/notaaa.md": note("title: Other\ntags: [kind/note]\n")},
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
		{"E_WRONG_FOLDER is gone: a hub sits in notes/", map[string]string{"notes/hubaaa.md": doc("Hub", "hub", "Hubs: [[index|Index]]\n\nA hub.")},
			[]finding{{"W_HUB_UNLISTED", "notes/hubaaa.md", 6, "hubaaa"}}}, // only the index does not link it
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

// TestLintFixRefusesStagedAndChildren: --fix with --staged or --children is a usage error.
func TestLintFixRefusesStagedAndChildren(t *testing.T) {
	vault := baseVault(t)
	for _, flag := range [][]string{{"--staged"}, {"--children", "hubsrc"}} {
		if got := runIn(t, "dev", vault, append([]string{"lint", "--fix"}, flag...)...); got.code != 2 {
			t.Errorf("lint --fix %v: code %d stderr %q, want 2", flag, got.code, got.stderr)
		}
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
	t.Run("a comma inside a hub title is clean", func(t *testing.T) {
		for _, f := range findingsOf(t, with_(t, page("Hubs: [[hubsrc|Parsing, lexing]], [[hubprj|Projects]]"))) {
			if f.Code == "E_HUBS_LINE" {
				t.Errorf("finding %+v, want no E_HUBS_LINE", f)
			}
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
	oldN3 := doc("Old", "note", hubLine+"\n\nSuperseded by [[not001|Leaders are unique]]", kv{"status", "superseded"}, kv{"superseded_by", "not001"})
	runCodeCases(t, []codeCase{
		{name: "W_STALE_ALIAS in a note's References", want: "W_STALE_ALIAS", changes: not002("A claim [1].\n\n## References\n\n1. [[src001|Old title]]")},
		{name: "W_STALE_ALIAS on a note's Hubs line", want: "W_STALE_ALIAS", changes: map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[index|Old hub]]\n\n[[not001|Leaders are unique]]")}},
		{name: "W_STALE_ALIAS on a hub's prose", want: "W_STALE_ALIAS", changes: hubMap("See [[nota01|Old title]].")},
		{name: "a stale alias in note Prose is the writer's wording", absent: "W_STALE_ALIAS", changes: not002("See [[not001|Old title]].")},
		{name: "a stale alias under a note H2 before References is Prose", absent: "W_STALE_ALIAS", changes: not002("## See also\n\n[[not001|Old title]]")},
		{name: "W_DEAD_END is removed: a note may link nothing besides its Hubs line", absent: "W_DEAD_END", changes: not002("Nothing linked.")},
		{name: "W_MARKER", want: "W_MARKER", changes: not002("TODO: later. [[not001|Leaders are unique]]")},
		{name: "W_MARKER no longer reports GAP:", absent: "W_MARKER", changes: not002("GAP: a note on retries. [[not001|Leaders are unique]]")},
		{name: "W_WORD_COUNT", want: "W_WORD_COUNT", changes: not002(strings.Repeat("word ", 500) + "[[not001|Leaders are unique]]")},
		{name: "W_SUPERSEDED_LINK", want: "W_SUPERSEDED_LINK", changes: map[string]string{
			"notes/not003.md": oldN3, "notes/not002.md": doc("Two", "note", hubLine+"\n\nSee [[not003|Old]].")}},
		{name: "W_HUB_MAP is removed: interlinked members need no walk or GAP: line", absent: "W_HUB_MAP", changes: hubMap("A short scope line.")},
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

// TestLintFix: lint --fix rewrites stale aliases on a note's Hubs line and in its References, leaves its Prose, code and Extract alone, then lints.
func TestLintFix(t *testing.T) {
	body := "Hubs: [[index|%s]]\n\nSee [[not001|Old title]] and [[not001]] [1].\n\n## See also\n\n[[not001|Old title]]\n\n```\n[[not001|Old title]]\n```\n\n## Extract\n\n[[not001|Old title]]\n\n## References\n\n1. [[not001|%s]]"
	orig := doc("Two", "note", fmt.Sprintf(body, "Old hub", "Old title"))
	// Only the Hubs line and References change; Prose, the H2 before References, code and Extract stay.
	want := doc("Two", "note", fmt.Sprintf(body, "Index", "Leaders are unique"))
	env := setupVault(t, "dev", with_(t, map[string]string{"notes/not002.md": orig}))

	if got := run(env, "lint"); !strings.Contains(got.stdout, "W_STALE_ALIAS") {
		t.Errorf("lint without --fix: stdout %q, want W_STALE_ALIAS", got.stdout)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != orig {
		t.Errorf("lint without --fix changed notes/not002.md: %q (%v)", b, err)
	}
	got := run(env, "lint", "--fix")
	if got.code != 0 || got.stdout != "fixed notes/not002.md\n0 errors, 0 warnings\n" || got.stderr != "" {
		t.Errorf("first lint --fix: code %d stdout %q stderr %q, want 0 \"fixed notes/not002.md\\n0 errors, 0 warnings\\n\"", got.code, got.stdout, got.stderr)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != want {
		t.Errorf("notes/not002.md = %q (%v), want %q", b, err, want)
	}
	got = run(env, "lint", "--fix")
	if got.code != 0 || got.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("second lint --fix: code %d stdout %q, want 0 \"0 errors, 0 warnings\\n\"", got.code, got.stdout)
	}
}

// TestLintFixThenLints: the findings that --fix cannot cure still show, with exit code 1.
func TestLintFixThenLints(t *testing.T) {
	files := with_(t, map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[index|Old hub]]\n\nSee [[nope|X]].")})
	got := runIn(t, "dev", files, "lint", "--fix")
	if got.code != 1 || !strings.HasPrefix(got.stdout, "fixed notes/not002.md\n") || !strings.Contains(got.stdout, "notes/not002.md:8: E_BROKEN_LINK no note with id nope\n") {
		t.Errorf("code %d stdout %q, want 1, the fixed path and the E_BROKEN_LINK finding", got.code, got.stdout)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || !strings.Contains(string(b), "Hubs: [[index|Index]]\n") {
		t.Errorf("notes/not002.md = %q (%v), want the Hubs alias fixed", b, err)
	}
}

// TestLintFixHubTitleWithComma: --fix writes a hub title that has a comma into the Hubs line, and lint stays clean.
func TestLintFixHubTitleWithComma(t *testing.T) {
	files := with_(t, map[string]string{
		"notes/hubprs.md": doc("Parsing, lexing", "hub", hubLine+"\n\nParsing."),
		"notes/not002.md": doc("Two", "note", "Hubs: [[hubprs|Old hub]], [[index|Index]]\n\nBody."),
	})
	got := runIn(t, "dev", files, "lint", "--fix")
	if got.code != 0 || !strings.HasPrefix(got.stdout, "fixed notes/not002.md\n") || strings.Contains(got.stdout, "not002.md:") {
		t.Errorf("code %d stdout %q, want 0 and no finding on notes/not002.md", got.code, got.stdout)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || !strings.Contains(string(b), "Hubs: [[hubprs|Parsing, lexing]], [[index|Index]]\n") {
		t.Errorf("notes/not002.md = %q (%v), want the comma title in the Hubs line", b, err)
	}
}

// TestLintFixAliasScope checks which stale aliases lint warns about and --fix rewrites, by kind and line position.
func TestLintFixAliasScope(t *testing.T) {
	// One stale alias per line type: Hubs line (6), Prose (8), a body H2 (12) and References (16).
	const aliasBody = "Hubs: [[not001|Old]]\n\nProse [[not001|Old]].\n\n## See also\n\n[[not001|Old]]\n\n## References\n\n1. [[not001|Old]]"
	all := []int{6, 8, 12, 16}
	for _, tc := range []struct {
		name, kind, path string
		want             []int // lines checked, out of all
		lintable         bool  // the page passes the frontmatter checks, so lint reports it
	}{
		{"note", "note", "notes/xxx001.md", []int{6, 16}, true},
		{"hub", "hub", "notes/xxx001.md", all, true},
		{"log", "log", "notes/xxx001.md", all, true},
		{"root index", "hub", "index.md", all, true},
		{"source", "source", "notes/xxx001.md", all, false},
		{"odd kind", "essay", "notes/xxx001.md", all, false},
		{"no kind", "", "notes/xxx001.md", all, false}, // lint drops a page with no kind
	} {
		t.Run(tc.name, func(t *testing.T) {
			tags := "[kind/" + tc.kind + "]"
			if tc.kind == "" {
				tags = "[]"
			}
			subject := "---\ntitle: X\ndate: 2026-01-02T03:04:05Z\ntags: " + tags + "\n---\n" + aliasBody + "\n"
			files := with_(t, map[string]string{tc.path: subject})
			if tc.lintable {
				var got []int
				for _, f := range findingsWith(t, files, "W_STALE_ALIAS") {
					if f.Path == tc.path {
						got = append(got, f.Line)
					}
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("W_STALE_ALIAS lines %v, want %v", got, tc.want)
				}
			}
			run(setupVault(t, "dev", files), "lint", "--fix")
			b, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Split(subject, "\n")
			for _, n := range tc.want {
				want[n-1] = strings.Replace(want[n-1], "Old", "Leaders are unique", 1)
			}
			if got := string(b); got != strings.Join(want, "\n") {
				t.Errorf("after lint --fix:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
			}
		})
	}
}

// TestLintFixProseOfOtherKinds checks that hub, source and log pages refresh aliases on every scanned line.
func TestLintFixProseOfOtherKinds(t *testing.T) {
	prose := func(old string) string { return "Hubs: [[index|Index]]\n\nSee [[not001|" + old + "]]." }
	src := func(old string) string {
		return doc("Source one", "source", prose(old)+"\n\n## Review", kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})
	}
	files := with_(t, map[string]string{
		"notes/hub001.md": doc("H1", "hub", prose("Old title")),
		"notes/log001.md": doc("P1", "log", prose("Old title")),
		"notes/src001.md": src("Old title"),
	})
	got := runIn(t, "dev", files, "lint", "--fix")
	if got.code != 0 || !strings.HasPrefix(got.stdout, "fixed notes/hub001.md\nfixed notes/log001.md\nfixed notes/src001.md\n") {
		t.Errorf("code %d stdout %q stderr %q, want 0 and the three pages fixed", got.code, got.stdout, got.stderr)
	}
	for p, want := range map[string]string{
		"notes/hub001.md": doc("H1", "hub", prose("Leaders are unique")),
		"notes/log001.md": doc("P1", "log", prose("Leaders are unique")),
		"notes/src001.md": src("Leaders are unique"),
	} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", p, b, err, want)
		}
	}
}

func TestLintFixJSON(t *testing.T) {
	files := with_(t, map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[index|Old hub]]\n\n[[not001|Leaders are unique]].")})
	env := setupVault(t, "dev", files)
	for _, want := range []string{
		`{"ok":true,"errors":0,"warnings":0,"findings":[],"fixed":["notes/not002.md"]}` + "\n",
		`{"ok":true,"errors":0,"warnings":0,"findings":[],"fixed":[]}` + "\n",
	} {
		if got := run(env, "lint", "--fix", "--json"); got.code != 0 || got.stdout != want {
			t.Errorf("lint --fix --json: code %d stdout %q, want 0 %q", got.code, got.stdout, want)
		}
	}
}

// TestLintChildren: --children lists the pages whose Hubs line names the hub, not the pages that only mention it.
func TestLintChildren(t *testing.T) {
	files := with_(t, map[string]string{
		"notes/not002.md": doc("Two", "note", "Hubs: [[hubprj|Projects]]\n\nSee [[hubsrc|Sources]] for more."),
		"notes/src002.md": srcTwo(kv{"url", "https://example.com/y"}, kv{"accessed", "2026-01-03"}),
		"notes/hub003.md": doc("H3", "hub", "Hubs: [[index|Index]]\n\nNo members yet."),
	})
	env := setupVault(t, "dev", files)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string // substring
	}{
		{"members of a hub", []string{"lint", "--children", "hubsrc"}, 0, "notes/src001.md\nnotes/src002.md\n", ""},
		{"a prose mention is not a member", []string{"lint", "--children", "hubprj"}, 0, "notes/not002.md\n", ""},
		{"the index has members", []string{"lint", "--children", "index"}, 0, "notes/hub003.md\nnotes/hubprj.md\nnotes/hubsrc.md\nnotes/not001.md\n", ""},
		{"json is a list", []string{"lint", "--children", "hubsrc", "--json"}, 0, `["notes/src001.md","notes/src002.md"]` + "\n", ""},
		{"json list of no members", []string{"lint", "--children", "hub003", "--json"}, 0, "[]\n", ""},
		{"no members", []string{"lint", "--children", "hub003"}, 0, "", ""},
		{"unknown hub", []string{"lint", "--children", "nope"}, 2, "", "nope"},
		{"a page that is not a hub", []string{"lint", "--children", "not001"}, 2, "", "not001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(env, tt.args...)
			if got.code != tt.wantCode || got.stdout != tt.wantStdout || !strings.Contains(got.stderr, tt.wantStderr) || (tt.wantStderr == "" && got.stderr != "") {
				t.Errorf("got code %d stdout %q stderr %q, want %d %q stderr containing %q", got.code, got.stdout, got.stderr, tt.wantCode, tt.wantStdout, tt.wantStderr)
			}
		})
	}
}

// TestFixVerbIsRemoved: fix is an unknown verb and changes nothing; lint --fix has its job.
func TestFixVerbIsRemoved(t *testing.T) {
	orig := doc("Two", "note", "Hubs: [[index|Old hub]]\n\n[[not001|Leaders are unique]].")
	got := runIn(t, "dev", with_(t, map[string]string{"notes/not002.md": orig}), "fix")
	if got.code != 2 || got.stdout != "" || !strings.HasPrefix(got.stderr, "usage: zk-chef <verb>") || strings.Contains(got.stderr, "fix") {
		t.Errorf("code %d stdout %q stderr %q, want 2, no stdout and the usage message without fix", got.code, got.stdout, got.stderr)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != orig {
		t.Errorf("notes/not002.md = %q (%v), want it unchanged", b, err)
	}
}

// findingsWith returns the findings of lint --json over files that carry code.
func findingsWith(t *testing.T, files map[string]string, code string) []finding {
	t.Helper()
	var out []finding
	for _, f := range findingsOf(t, files) {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

// TestLintUnknownKey: a frontmatter key that the page's kind does not list is W_UNKNOWN_KEY.
func TestLintUnknownKey(t *testing.T) {
	note := func(extra ...kv) map[string]string {
		return map[string]string{"notes/not002.md": doc("Two", "note", hubLine+"\n\nSee [[not001|Leaders are unique]].", extra...)}
	}
	t.Run("a valid vault is silent", func(t *testing.T) {
		if got := findingsWith(t, with_(t, note(kv{"aliases", "[Other]"}, kv{"superseded_by", "not001"})), "W_UNKNOWN_KEY"); len(got) != 0 {
			t.Errorf("findings %+v, want none", got)
		}
	})
	t.Run("a typo in a note key", func(t *testing.T) {
		got := findingsWith(t, with_(t, note(kv{"alises", "[Other]"})), "W_UNKNOWN_KEY")
		if len(got) != 1 || got[0].Path != "notes/not002.md" || !strings.Contains(got[0].Msg, "alises") {
			t.Errorf("findings %+v, want one W_UNKNOWN_KEY on notes/not002.md naming alises", got)
		}
	})
	t.Run("a source key on a note", func(t *testing.T) {
		got := findingsWith(t, with_(t, note(kv{"url", "https://example.com"})), "W_UNKNOWN_KEY")
		if len(got) != 1 || !strings.Contains(got[0].Msg, "url") {
			t.Errorf("findings %+v, want one W_UNKNOWN_KEY naming url", got)
		}
	})
	t.Run("superseded_by on a hub", func(t *testing.T) {
		files := map[string]string{"notes/hub003.md": doc("H3", "hub", "Hubs: [[index|Index]]\n\nA hub.", kv{"superseded_by", "not001"})}
		got := findingsWith(t, with_(t, files), "W_UNKNOWN_KEY")
		if len(got) != 1 || !strings.Contains(got[0].Msg, "superseded_by") {
			t.Errorf("findings %+v, want one W_UNKNOWN_KEY naming superseded_by", got)
		}
	})
	t.Run("the v0.2 citation key on a source", func(t *testing.T) {
		files := map[string]string{"notes/src001.md": doc("Source one", "source", "Hubs: [[hubsrc|Sources]]\n\n## Review",
			kv{"raw", "raw/src001.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"}, kv{"citation", "Doe 2026"})}
		got := findingsWith(t, with_(t, files), "W_UNKNOWN_KEY")
		if len(got) != 1 || !strings.Contains(got[0].Msg, "citation") {
			t.Errorf("findings %+v, want one W_UNKNOWN_KEY naming citation", got)
		}
	})
}

// taggedPage builds a page of notes/ with exactly these tags and the given body.
func taggedPage(title string, tags []string, body string) string {
	return "---\ntitle: " + title + "\ndate: 2026-01-02T03:04:05Z\ntags: [" + strings.Join(tags, ", ") + "]\n---\n" + body + "\n"
}

// TestLintUnknownTag: a tag without "/" or in an unknown namespace is W_UNKNOWN_TAG; a valid topic or log type tag is silent.
func TestLintUnknownTag(t *testing.T) {
	noteBody := hubLine + "\n\nSee [[not001|Leaders are unique]]."
	logBody := hubLine + "\n\n## Updates"
	tests := []struct {
		name string
		page string
		want string // the tag the message names; empty means silent
	}{
		{"topic with nested kebab segments", taggedPage("Two", []string{"kind/note", "topic/ml/rnn-2", "topic/x"}, noteBody), ""},
		{"type on a log", taggedPage("Two", []string{"kind/log", "type/decision"}, logBody), ""},
		{"a bare tag", taggedPage("Two", []string{"kind/note", "custom"}, noteBody), "custom"},
		{"an unknown namespace", taggedPage("Two", []string{"kind/note", "area/ml"}, noteBody), "area/ml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findingsWith(t, with_(t, map[string]string{"notes/not002.md": tt.page}), "W_UNKNOWN_TAG")
			switch {
			case tt.want == "" && len(got) != 0:
				t.Errorf("findings %+v, want none", got)
			case tt.want != "" && (len(got) != 1 || got[0].Path != "notes/not002.md" || !strings.Contains(got[0].Msg, tt.want)):
				t.Errorf("findings %+v, want one W_UNKNOWN_TAG on notes/not002.md naming %s", got, tt.want)
			}
		})
	}
	t.Run("the base vault tags are silent", func(t *testing.T) {
		if got := findingsWith(t, baseVault(t), "W_UNKNOWN_TAG"); len(got) != 0 {
			t.Errorf("findings %+v, want none", got)
		}
	})
}

// TestLintTagNamespaces: a bad value in a known namespace is E_FRONTMATTER only; an unknown namespace, a tag without "/" or an old ":" tag is W_UNKNOWN_TAG.
func TestLintTagNamespaces(t *testing.T) {
	noteBody := hubLine + "\n\nSee [[not001|Leaders are unique]]."
	tests := []struct {
		name      string
		tags      []string
		wantError string // substring of the E_FRONTMATTER message; empty means no error
		wantWarn  bool
	}{
		{"unknown kind value", []string{"kind/essay"}, "kind", false},
		{"bad status value", []string{"kind/note", "status/draft"}, "status", false},
		{"review on a note", []string{"kind/note", "review/maybe"}, "review", false},
		{"bad topic value", []string{"kind/note", "topic/ML"}, "topic", false},
		{"empty topic segment", []string{"kind/note", "topic/a//b"}, "topic", false},
		{"type with a slash", []string{"kind/log", "type/a/b"}, "type", false},
		{"type with capitals", []string{"kind/log", "type/Decision"}, "type", false},
		{"type on a note", []string{"kind/note", "type/decision"}, "type", false},
		{"a bare tag", []string{"kind/note", "custom"}, "", true},
		{"an unknown namespace", []string{"kind/note", "foo/bar"}, "", true},
		{"an old colon kind tag is no kind", []string{"kind:note"}, "kind", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := findingsOf(t, with_(t, map[string]string{"notes/not002.md": taggedPage("Two", tt.tags, noteBody)}))
			var errMsg string
			warn := false
			for _, f := range found {
				if f.Path != "notes/not002.md" {
					continue
				}
				if f.Code == "E_FRONTMATTER" {
					errMsg += f.Msg + "\n"
				}
				warn = warn || f.Code == "W_UNKNOWN_TAG"
			}
			if (tt.wantError == "") != (errMsg == "") || !strings.Contains(errMsg, tt.wantError) {
				t.Errorf("E_FRONTMATTER messages %q, want one containing %q", errMsg, tt.wantError)
			}
			if warn != tt.wantWarn {
				t.Errorf("W_UNKNOWN_TAG = %v, want %v", warn, tt.wantWarn)
			}
		})
	}
}

// TestLintSupersede: status/superseded needs superseded_by to an existing page and the body line
// "Superseded by [[stem|Title]]" after the Hubs line; a hub or a log cannot be superseded.
func TestLintSupersede(t *testing.T) {
	new := doc("New claim", "note", hubLine+"\n\nSee [[not001|Leaders are unique]].")
	superseded := func(kind, body string) map[string]string {
		return map[string]string{
			"notes/not002.md": doc("Old claim", kind, body, kv{"status", "superseded"}, kv{"superseded_by", "not003"}),
			"notes/not003.md": new,
		}
	}
	tests := []struct {
		name    string
		changes map[string]string
		want    string // part of the E_FRONTMATTER message on notes/not002.md; empty means no finding at all
	}{
		{"all three parts", superseded("note", hubLine+"\n\nSuperseded by [[not003|New claim]]\n\nOld."), ""},
		{"a source with all three parts", map[string]string{
			"notes/not002.md": doc("Old source", "source", hubLine+"\n\nSuperseded by [[not003|New claim]]\n\n## Review",
				kv{"url", "https://example.com/x"}, kv{"accessed", "2026-01-03"}, kv{"review", "unreviewed"}, kv{"status", "superseded"}, kv{"superseded_by", "not003"}),
			"notes/not003.md": new}, ""},
		{"no body line", superseded("note", hubLine+"\n\nOld."), "Superseded by [[not003"},
		{"body line names another stem", superseded("note", hubLine+"\n\nSuperseded by [[not001|Leaders are unique]]\n\nOld."), "not003"},
		{"superseded_by names no page", map[string]string{
			"notes/not002.md": doc("Old claim", "note", hubLine+"\n\nSuperseded by [[nope|X]]\n\nOld.", kv{"status", "superseded"}, kv{"superseded_by", "nope"})}, "nope"},
		{"a superseded hub", superseded("hub", hubLine+"\n\nSuperseded by [[not003|New claim]]"), "must not be superseded"},
		{"a superseded log", superseded("log", hubLine+"\n\nSuperseded by [[not003|New claim]]\n\n## Updates"), "must not be superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msgs []string
			for _, f := range findingsOf(t, with_(t, tt.changes)) {
				if f.Path == "notes/not002.md" && strings.HasPrefix(f.Code, "E_") {
					msgs = append(msgs, f.Code+" "+f.Msg)
				}
			}
			switch {
			case tt.want == "" && len(msgs) != 0:
				t.Errorf("errors %q, want none", msgs)
			case tt.want != "" && !slices.ContainsFunc(msgs, func(m string) bool { return strings.HasPrefix(m, "E_FRONTMATTER ") && strings.Contains(m, tt.want) }):
				t.Errorf("errors %q, want an E_FRONTMATTER containing %q", msgs, tt.want)
			}
		})
	}
}

// TestLintHubUnlisted: a hub whose parent's body does not link it is W_HUB_UNLISTED; the index counts as a parent.
func TestLintHubUnlisted(t *testing.T) {
	indexLinking := func(links string) string {
		return doc("Index", "hub", links)
	}
	tests := []struct {
		name    string
		changes map[string]string
		want    []string // paths of the hubs reported; none means silent
	}{
		{"the base vault lists its hubs in the index", nil, nil},
		{"the index does not link a hub", map[string]string{"index.md": indexLinking("- [[hubsrc|Sources]]")}, []string{"notes/hubprj.md"}},
		{"the index links a hub on its first body line", map[string]string{"index.md": indexLinking("[[hubprj|Projects]] [[hubsrc|Sources]]")}, nil},
		{"the parent names the child on its Hubs line only", map[string]string{
			"notes/hub001.md": doc("H1", "hub", "Hubs: [[hubsrc|Sources]]\n\nA child hub."),
			"notes/hubsrc.md": doc("Sources", "hub", "Hubs: [[index|Index]]\n\nOne source note per raw file."),
		}, []string{"notes/hub001.md"}},
		{"the parent links the child in its body", map[string]string{
			"notes/hub001.md": doc("H1", "hub", "Hubs: [[hubsrc|Sources]]\n\nA child hub."),
			"notes/hubsrc.md": doc("Sources", "hub", "Hubs: [[index|Index]]\n\nSee [[hub001|H1]]."),
		}, nil},
		{"a note under a hub needs no link from it", map[string]string{"notes/not002.md": doc("Two", "note", "Hubs: [[hubprj|Projects]]\n\nSee [[not001|Leaders are unique]].")}, nil},
		{"one of two parents does not link the hub", map[string]string{
			"notes/hub001.md": doc("H1", "hub", "Hubs: [[hubsrc|Sources]], [[hubprj|Projects]]\n\nA child hub."),
			"notes/hubsrc.md": doc("Sources", "hub", "Hubs: [[index|Index]]\n\nSee [[hub001|H1]]."),
		}, []string{"notes/hub001.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, f := range findingsWith(t, with_(t, tt.changes), "W_HUB_UNLISTED") {
				got = append(got, f.Path)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("W_HUB_UNLISTED on %v, want %v", got, tt.want)
			}
		})
	}
}

// TestLintLogHeading: a log needs ## Updates, whose ### headings are YYYY-MM-DD dates, newest first.
func TestLintLogHeading(t *testing.T) {
	log := func(body string) map[string]string {
		return map[string]string{"notes/log001.md": doc("Project one", "log", hubLine+"\n\n"+body)}
	}
	entry := func(h string) string { return "### " + h + "\n\nDecided.\n\n" }
	tests := []struct {
		name    string
		changes map[string]string
		want    int // the line of the one finding; 0 means silent
	}{
		{"entries newest first", log("## Updates\n\n" + entry("2026-02-01") + entry("2026-01-15")), 0},
		{"two entries on one day", log("## Updates\n\n" + entry("2026-02-01") + entry("2026-02-01")), 0},
		{"an empty Updates section", log("## Updates"), 0},
		{"free H2 sections before Updates", log("## Context\n\nWhy.\n\n## Updates\n\n" + entry("2026-02-01")), 0},
		{"a heading in a code fence is not an entry", log("## Updates\n\n```\n### not a date\n```"), 0},
		{"no Updates section", log("Decision."), 6},
		{"an entry heading that is not a date", log("## Updates\n\n" + entry("Later")), 10},
		{"an impossible date", log("## Updates\n\n" + entry("2026-13-40")), 10},
		{"a date with a time", log("## Updates\n\n" + entry("2026-02-01 10:00")), 10},
		{"oldest first", log("## Updates\n\n" + entry("2026-01-15") + entry("2026-02-01")), 14},
		{"a note needs no Updates section", map[string]string{"notes/not002.md": doc("Two", "note", hubLine+"\n\nSee [[not001|Leaders are unique]].")}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findingsWith(t, with_(t, tt.changes), "W_LOG_HEADING")
			switch {
			case tt.want == 0 && len(got) != 0:
				t.Errorf("findings %+v, want none", got)
			case tt.want != 0 && (len(got) != 1 || got[0].Path != "notes/log001.md" || got[0].Line != tt.want):
				t.Errorf("findings %+v, want one W_LOG_HEADING on notes/log001.md:%d", got, tt.want)
			}
		})
	}
}

// TestInitSeedsLogUpdates: the log template that init writes starts a log with a ## Updates section.
func TestInitSeedsLogUpdates(t *testing.T) {
	env := setupVault(t, "dev", nil)
	if got := run(env, "init", "v"); got.code != 0 {
		t.Fatalf("init exit %d, stderr %q", got.code, got.stderr)
	}
	b, err := os.ReadFile(filepath.Join("v", ".zk", "templates", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n## Updates\n") {
		t.Errorf("log template = %q, want a ## Updates section", b)
	}
}

// TestLintFrontmatterFormat: frontmatter that is not in the canonical form of the shared writer is W_FRONTMATTER_FORMAT, a warning.
func TestLintFrontmatterFormat(t *testing.T) {
	const tail = "---\nHubs: [[index|Index]]\n\nSee [[not001|Leaders are unique]].\n"
	const head = "---\ntitle: Two\ndate: 2026-01-02T03:04:05Z\n"
	index, err := os.ReadFile(filepath.Join("..", "..", "scaffold", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, page string
		want       bool
	}{
		{"extra spaces after the colon", "---\ntitle:   Two\ndate: 2026-01-02T03:04:05Z\ntags: [kind/note]\n" + tail, true},
		{"spaces inside a flow list", head + "tags: [ kind/note ,topic/a ]\n" + tail, true},
		{"column-0 block list", head + "tags:\n- kind/note\n- topic/a\n" + tail, true},
		{"canonical block list", head + "tags:\n  - kind/note\n  - topic/a\n" + tail, false},
		{"note template page", "---\ntitle: \"Two\"\ndate: 2026-01-02T03:04:05Z\ntags: [kind/note]\n" + tail, false},
		{"source template page", "---\ntitle: \"Two\"\ndate: 2026-01-02T03:04:05Z\ntags: [kind/source, review/unreviewed]\n" + tail, false},
		{"hub template page", "---\ntitle: \"Two\"\ndate: 2026-01-02T03:04:05Z\ntags: [kind/hub]\n" + tail, false},
		{"log template page", "---\ntitle: \"Two\"\ndate: 2026-01-02T03:04:05Z\ntags: [kind/log]\n" + tail, false},
		{"scaffold index.md", string(index), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := "notes/not002.md"
			if tt.name == "scaffold index.md" {
				p = "index.md"
			}
			got := runIn(t, "dev", with_(t, map[string]string{p: tt.page}), "lint", "--json")
			if got.code != 0 && !strings.Contains(tt.name, "source") { // a bare source page has no evidence
				t.Errorf("code %d stdout %q, want 0 (a warning does not fail lint)", got.code, got.stdout)
			}
			var r report
			if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
				t.Fatal(err)
			}
			has := slices.ContainsFunc(r.Findings, func(f finding) bool { return f.Code == "W_FRONTMATTER_FORMAT" && f.Path == p && f.Line == 1 })
			if has != tt.want {
				t.Errorf("W_FRONTMATTER_FORMAT on %s = %v, want %v; findings %+v", p, has, tt.want, r.Findings)
			}
		})
	}
}

// TestLintFixFrontmatterFormat: --fix writes the canonical frontmatter, keeps comments and the body, and a second --fix changes nothing.
func TestLintFixFrontmatterFormat(t *testing.T) {
	const body = "---\nHubs: [[index|Index]]\n\nSee   [[not001|Leaders are unique]].  \n"
	orig := "---\n# why\ntitle:   Two\ndate: 2026-01-02T03:04:05Z\ntags:\n- kind/note\n- topic/a # keep\n" + body
	want := "---\n# why\ntitle: Two\ndate: 2026-01-02T03:04:05Z\ntags:\n  - kind/note\n  - topic/a # keep\n" + body
	env := setupVault(t, "dev", with_(t, map[string]string{"notes/not002.md": orig}))
	got := run(env, "lint", "--fix")
	if got.code != 0 || got.stdout != "fixed notes/not002.md\n0 errors, 0 warnings\n" {
		t.Errorf("first lint --fix: code %d stdout %q, want 0 and the page fixed", got.code, got.stdout)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != want {
		t.Errorf("notes/not002.md = %q (%v), want %q", b, err, want)
	}
	if got = run(env, "lint", "--fix"); got.code != 0 || got.stdout != "0 errors, 0 warnings\n" {
		t.Errorf("second lint --fix: code %d stdout %q, want nothing fixed", got.code, got.stdout)
	}
}

// TestLintFixKeepsInvalidYAML: --fix leaves a page with invalid YAML as it is, and lint reports E_FRONTMATTER.
func TestLintFixKeepsInvalidYAML(t *testing.T) {
	orig := "---\ntitle:   Two\ntags: [kind/note\n---\nHubs: [[index|Index]]\n"
	env := setupVault(t, "dev", with_(t, map[string]string{"notes/not002.md": orig}))
	got := run(env, "lint", "--fix")
	if got.code != 1 || strings.Contains(got.stdout, "fixed") || !strings.Contains(got.stdout, "notes/not002.md:1: E_FRONTMATTER invalid YAML") {
		t.Errorf("code %d stdout %q, want 1, no fixed line and E_FRONTMATTER", got.code, got.stdout)
	}
	if b, err := os.ReadFile("notes/not002.md"); err != nil || string(b) != orig {
		t.Errorf("notes/not002.md = %q (%v), want it unchanged", b, err)
	}
}
