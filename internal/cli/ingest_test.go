package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ingestVault is an init-ed vault in the current directory. It skips the test when zk is missing.
func ingestVault(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("zk"); err != nil {
		t.Skip("zk not installed")
	}
	env := setupVault(t, "dev", nil)
	if got := run(env, "init", "-y"); got.code != 0 {
		t.Fatalf("init exit %d: %s", got.code, got.stderr)
	}
	return env
}

// writeTemp writes content to name in a new temp dir and returns the path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var notePathRe = regexp.MustCompile(`^notes/([a-z0-9]{6})\.md\n$`)

// runIngest runs Run and adds --hub index when args name no hub; ingest needs at least one.
func runIngest(env []string, args ...string) result {
	if !slices.Contains(args, "--hub") {
		args = append(args, "--hub", "index")
	}
	return run(env, args...)
}

func TestIngestFile(t *testing.T) {
	env := ingestVault(t)
	const content = "some raw bytes\n"
	src := writeTemp(t, "My File.TXT", content)
	got := runIngest(env, "ingest", src, "--title", "My Title")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	m := notePathRe.FindStringSubmatch(got.stdout)
	if m == nil {
		t.Fatalf("stdout %q is not exactly one notes/<id>.md path", got.stdout)
	}
	id := m[1]
	rawPath := "raw/" + id + ".txt"
	if b := readFile(t, rawPath); b != content {
		t.Errorf("%s = %q, want %q", rawPath, b, content)
	}
	note := readFile(t, "notes/"+id+".md")
	for _, want := range []string{
		"raw: " + rawPath + "\n",
		fmt.Sprintf("sha256: %x\n", sha256.Sum256([]byte(content))),
		"tags: [kind:source, review:unreviewed]\n",
		"title: \"My Title\"\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "url:") || strings.Contains(note, "accessed:") {
		t.Errorf("file ingest set url or accessed:\n%s", note)
	}
	if strings.Count(note, "raw:") != 1 || strings.Count(note, "sha256:") != 1 || strings.Count(note, "review:") != 1 {
		t.Errorf("template keys duplicated:\n%s", note)
	}
}

func TestIngestFileExtension(t *testing.T) {
	tests := []struct{ name, file, wantExt string }{
		{"lowercased", "A.TXT", ".txt"},
		{"last extension only", "notes.tar.GZ", ".gz"},
		{"spaces in name", "My File.pdf", ".pdf"},
		{"no extension", "Makefile", ""},
		{"extension with a dash", "odd.t-x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			got := runIngest(env, "ingest", writeTemp(t, tt.file, "bytes\n"), "--title", "T")
			m := notePathRe.FindStringSubmatch(got.stdout)
			if got.code != 0 || m == nil {
				t.Fatalf("exit %d, stdout %q, stderr %q", got.code, got.stdout, got.stderr)
			}
			rawPath := "raw/" + m[1] + tt.wantExt
			if names := listDir(t, "raw"); !slices.Equal(names, []string{m[1] + tt.wantExt}) {
				t.Errorf("raw/ holds %q, want only %s", names, rawPath)
			}
			if note := readFile(t, "notes/"+m[1]+".md"); !strings.Contains(note, "raw: "+rawPath+"\n") {
				t.Errorf("note lacks raw: %s:\n%s", rawPath, note)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeHTTP replaces HTTPClient with one that answers every request with status, body and, when it is not empty, the Content-Type.
func fakeHTTP(t *testing.T, status int, body, contentType string) {
	t.Helper()
	old := HTTPClient
	HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h, Request: r}, nil
	})}
	t.Cleanup(func() { HTTPClient = old })
}

// listDir returns the names in dir.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// wantAccessedToday checks that note has an accessed: line with the UTC date of start or of now.
func wantAccessedToday(t *testing.T, note string, start time.Time) {
	t.Helper()
	for _, d := range []time.Time{start, time.Now()} {
		if strings.Contains(note, "accessed: "+d.UTC().Format("2006-01-02")+"\n") {
			return
		}
	}
	t.Errorf("note lacks accessed: <today, UTC>:\n%s", note)
}

func TestIngestURL(t *testing.T) {
	tests := []struct {
		name, url, wantExt string
	}{
		{"last path segment, query stripped", "https://example.com/files/paper.pdf?x=1&y=2", ".pdf"},
		{"extension lowercased", "https://example.com/files/Paper.PDF", ".pdf"},
		{"unsafe characters replaced", "http://example.com/a%20b$c.txt", ".txt"},
		{"empty path", "https://example.com/", ""},
		{"host only", "https://example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			const body = "downloaded bytes\n"
			fakeHTTP(t, 200, body, "")
			start := time.Now()
			got := runIngest(env, "ingest", tt.url, "--title", "From Web")
			if got.code != 0 {
				t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
			}
			m := notePathRe.FindStringSubmatch(got.stdout)
			if m == nil {
				t.Fatalf("stdout %q is not exactly one notes/<id>.md path", got.stdout)
			}
			rawPath := "raw/" + m[1] + tt.wantExt
			if b := readFile(t, rawPath); b != body {
				t.Errorf("%s = %q, want %q", rawPath, b, body)
			}
			note := readFile(t, "notes/"+m[1]+".md")
			for _, want := range []string{
				"raw: " + rawPath + "\n",
				fmt.Sprintf("sha256: %x\n", sha256.Sum256([]byte(body))),
				"tags: [kind:source, review:unreviewed]\n",
				"url: " + strconv.Quote(tt.url) + "\n",
			} {
				if !strings.Contains(note, want) {
					t.Errorf("note lacks %q:\n%s", want, note)
				}
			}
			wantAccessedToday(t, note, start)
			if names := listDir(t, "raw"); len(names) != 1 {
				t.Errorf("raw/ holds %q, want only %s", names, rawPath)
			}
		})
	}
}

func TestIngestURLContentTypeExtension(t *testing.T) {
	tests := []struct {
		name, url, contentType, wantExt string
	}{
		{"pdf", "https://example.com/get?id=7", "application/pdf", ".pdf"},
		{"parameters ignored", "https://example.com/", "application/pdf; charset=binary", ".pdf"},
		{"html is .html, not .htm", "https://example.com/", "text/html; charset=utf-8", ".html"},
		{"name beats Content-Type", "https://example.com/a.txt", "application/pdf", ".txt"},
		{"unknown type", "https://example.com/get", "application/x-zk-chef-test", ""},
		{"no Content-Type", "https://example.com/get", "", ""},
		{"bad Content-Type", "https://example.com/get", "///;;", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			fakeHTTP(t, 200, "bytes\n", tt.contentType)
			got := runIngest(env, "ingest", tt.url, "--title", "T")
			m := notePathRe.FindStringSubmatch(got.stdout)
			if got.code != 0 || m == nil {
				t.Fatalf("exit %d, stdout %q, stderr %q", got.code, got.stdout, got.stderr)
			}
			if names := listDir(t, "raw"); !slices.Equal(names, []string{m[1] + tt.wantExt}) {
				t.Errorf("raw/ holds %q, want only %s", names, m[1]+tt.wantExt)
			}
			if note := readFile(t, "notes/"+m[1]+".md"); !strings.Contains(note, "raw: raw/"+m[1]+tt.wantExt+"\n") {
				t.Errorf("note lacks raw: raw/%s%s:\n%s", m[1], tt.wantExt, note)
			}
		})
	}
}

// noHTTP replaces HTTPClient with one that fails the test on any request.
func noHTTP(t *testing.T) {
	t.Helper()
	old := HTTPClient
	HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected HTTP request to %s", r.URL)
		return nil, errors.New("no network in this test")
	})}
	t.Cleanup(func() { HTTPClient = old })
}

func TestIngestNoRaw(t *testing.T) {
	env := ingestVault(t)
	noHTTP(t)
	start := time.Now()
	got := runIngest(env, "ingest", "https://example.com/p.pdf", "--no-raw", "--title", "Web page")
	m := notePathRe.FindStringSubmatch(got.stdout)
	if got.code != 0 || m == nil {
		t.Fatalf("exit %d, stdout %q, stderr %q", got.code, got.stdout, got.stderr)
	}
	note := readFile(t, "notes/"+m[1]+".md")
	for _, want := range []string{"url: \"https://example.com/p.pdf\"\n", "tags: [kind:source, review:unreviewed]\n"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	wantAccessedToday(t, note, start)
	if strings.Contains(note, "raw:") || strings.Contains(note, "sha256:") {
		t.Errorf("--no-raw note has raw or sha256:\n%s", note)
	}
	if names := listDir(t, "raw"); len(names) != 0 {
		t.Errorf("raw/ holds %q, want nothing", names)
	}
}

func TestIngestNoRawWithPathIsUsageError(t *testing.T) {
	env := ingestVault(t)
	got := runIngest(env, "ingest", writeTemp(t, "a.txt", "x"), "--no-raw", "--title", "T")
	if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "--no-raw is valid only with a URL") {
		t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, the --no-raw error", got.code, got.stdout, got.stderr)
	}
	for _, dir := range []string{"notes", "raw"} {
		if names := listDir(t, dir); len(names) != 0 {
			t.Errorf("%s/ holds %q, want nothing", dir, names)
		}
	}
}

// fakeZk puts a zk on PATH that counts its calls in the file COUNT_FILE. Its first calls that clash print the path of notes/old001.md, which already exists;
// the next call writes notes/new001.md and prints that path. It returns the count file.
func fakeZk(t *testing.T, clashes int) string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
n=$(cat "$COUNT_FILE" 2>/dev/null || echo 0)
echo $((n + 1)) > "$COUNT_FILE"
if [ "$n" -lt "$CLASHES" ]; then echo notes/old001.md; exit 0; fi
printf -- '---\ntitle: "T"\ndate: 2026-01-02T03:04:05Z\ntags: [kind:source, review:unreviewed]\n---\nHubs: ' > notes/new001.md
echo notes/new001.md
`
	if err := os.WriteFile(filepath.Join(bin, "zk"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(t.TempDir(), "count")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("COUNT_FILE", count)
	t.Setenv("CLASHES", strconv.Itoa(clashes))
	return count
}

func TestIngestRetriesWhenZkReturnsExistingFile(t *testing.T) {
	const old = "---\ntitle: Old\n---\nold note\n"
	tests := []struct {
		name      string
		clashes   int
		wantCode  int
		wantCalls string
	}{
		{"no clash", 0, 0, "1\n"},
		{"two clashes, third call is new", 2, 0, "3\n"},
		{"three clashes give up", 3, 1, "3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			writeFile(t, "notes/old001.md", old)
			count := fakeZk(t, tt.clashes)
			got := runIngest(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T")
			if got.code != tt.wantCode {
				t.Fatalf("exit %d, stdout %q, stderr %q, want %d", got.code, got.stdout, got.stderr, tt.wantCode)
			}
			if b, _ := os.ReadFile(count); string(b) != tt.wantCalls {
				t.Errorf("zk ran %q times, want %q", b, tt.wantCalls)
			}
			if b := readFile(t, "notes/old001.md"); b != old {
				t.Errorf("notes/old001.md = %q, want it unchanged", b)
			}
			if tt.wantCode == 0 {
				if got.stdout != "notes/new001.md\n" {
					t.Errorf("stdout %q, want notes/new001.md", got.stdout)
				}
				return
			}
			if got.stdout != "" || !strings.Contains(got.stderr, "notes/old001.md") {
				t.Errorf("stdout %q stderr %q, want empty stdout and a message that names notes/old001.md", got.stdout, got.stderr)
			}
			if names := listDir(t, "notes"); !slices.Contains(names, "old001.md") || slices.Contains(names, "new001.md") {
				t.Errorf("notes/ holds %q, want old001.md and no new001.md", names)
			}
			if names := listDir(t, "raw"); len(names) != 0 {
				t.Errorf("raw/ holds %q, want nothing", names)
			}
		})
	}
}

func TestIngestURLFailureLeavesNothing(t *testing.T) {
	env := ingestVault(t)
	fakeHTTP(t, 404, "not found", "")
	got := runIngest(env, "ingest", "https://example.com/gone.pdf", "--title", "Gone")
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "404") {
		t.Errorf("got code %d stdout %q stderr %q, want 1, empty stdout, 404 on stderr", got.code, got.stdout, got.stderr)
	}
	for _, dir := range []string{"notes", "raw"} {
		if names := listDir(t, dir); len(names) != 0 {
			t.Errorf("%s/ holds %q, want nothing", dir, names)
		}
	}
}
func TestIngestUsageErrors(t *testing.T) {
	env := ingestVault(t)
	src := writeTemp(t, "a.txt", "x")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing title", []string{"ingest", src}, "usage: zk-chef ingest"},
		{"missing argument", []string{"ingest", "--title", "T"}, "usage: zk-chef ingest"},
		{"missing file", []string{"ingest", filepath.Join(t.TempDir(), "nope.txt"), "--title", "T"}, "not a regular file"},
		{"directory", []string{"ingest", t.TempDir(), "--title", "T"}, "not a regular file"},
		{"URL without host", []string{"ingest", "https:///x", "--title", "T"}, "no host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIngest(env, tt.args...)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, tt.want) {
				t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, %q on stderr", got.code, got.stdout, got.stderr, tt.want)
			}
			if names := listDir(t, "notes"); len(names) != 0 {
				t.Errorf("notes/ holds %q, want nothing", names)
			}
		})
	}
}

func TestIngestZkMissing(t *testing.T) {
	env := ingestVault(t)
	t.Setenv("PATH", t.TempDir())
	got := runIngest(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T")
	if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "zk not found; run zk-chef install") {
		t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, zk hint", got.code, got.stdout, got.stderr)
	}
	if names := listDir(t, "raw"); len(names) != 0 {
		t.Errorf("raw/ holds %q, want nothing", names)
	}
}
func TestIngestJSON(t *testing.T) {
	env := ingestVault(t)
	const body = "json bytes\n"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	t.Run("file", func(t *testing.T) {
		got := runIngest(env, "ingest", writeTemp(t, "j.txt", body), "--title", "J", "--json")
		if got.code != 0 {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		var out struct {
			OK       bool    `json:"ok"`
			Path     string  `json:"path"`
			Raw      string  `json:"raw"`
			SHA256   string  `json:"sha256"`
			Accessed *string `json:"accessed"`
			URL      *string `json:"url"`
		}
		if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
			t.Fatalf("stdout is not one JSON object: %v\n%s", err, got.stdout)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(out.Path, "notes/"), ".md")
		if !out.OK || !notePathRe.MatchString(out.Path+"\n") || out.Raw != "raw/"+id+".txt" || out.SHA256 != sum || out.URL != nil || out.Accessed != nil {
			t.Errorf("unexpected JSON: %+v", out)
		}
		if !strings.Contains(got.stdout, `"url":null`) || !strings.Contains(got.stdout, `"accessed":null`) {
			t.Errorf("stdout %q lacks \"url\":null or \"accessed\":null", got.stdout)
		}
	})
	t.Run("URL", func(t *testing.T) {
		fakeHTTP(t, 200, body, "")
		got := runIngest(env, "ingest", "https://example.com/j.txt", "--title", "J", "--json")
		if got.code != 0 {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, `"url":"https://example.com/j.txt"}`) || !strings.Contains(got.stdout, `"sha256":"`+sum+`"`) || !strings.Contains(got.stdout, `"accessed":"`) {
			t.Errorf("stdout %q", got.stdout)
		}
	})
	t.Run("URL with --no-raw", func(t *testing.T) {
		noHTTP(t)
		got := runIngest(env, "ingest", "https://example.com/j.txt", "--no-raw", "--title", "J", "--json")
		if got.code != 0 {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, `"raw":null,"sha256":null,"accessed":"`) || !strings.Contains(got.stdout, `"url":"https://example.com/j.txt"}`) {
			t.Errorf("stdout %q", got.stdout)
		}
	})
}
func TestIngestedSourcePassesLint(t *testing.T) {
	for _, tt := range []struct{ name, arg, flag string }{
		{"file", "", ""},
		{"URL", "https://example.com/p.pdf?a=b: c", ""},
		{"URL with --no-raw", "https://example.com/p.pdf?a=b: c", "--no-raw"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			arg := tt.arg
			if arg == "" {
				arg = writeTemp(t, "p.txt", "raw\n")
			} else {
				fakeHTTP(t, 200, "raw\n", "")
			}
			args := []string{"ingest", arg, "--title", "A Source: with colon"}
			if tt.flag != "" {
				args = append(args, tt.flag)
			}
			got := runIngest(env, args...)
			if got.code != 0 {
				t.Fatalf("ingest exit %d: %s", got.code, got.stderr)
			}
			path := strings.TrimSpace(got.stdout)
			body := "\nWhy this source matters.\n\n## Extract\n\nSome extracted text.\n\n## Review\n"
			text := readFile(t, path)
			if err := os.WriteFile(path, []byte(text+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if l := run(env, "lint"); l.code != 0 {
				t.Errorf("lint exit %d:\n%s%s", l.code, l.stdout, l.stderr)
			}
		})
	}
}

const (
	projectsHub = "[[hubprj|Projects]]"
	indexHub    = "[[index|Index]]"
)

// ingestHubLine ingests a file with args and returns the Hubs line of the new note.
func ingestHubLine(t *testing.T, env []string, args ...string) string {
	t.Helper()
	got := runIngest(env, append([]string{"ingest", writeTemp(t, "a.txt", "x"), "--title", "T"}, args...)...)
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	_, body, _ := strings.Cut(readFile(t, strings.TrimSpace(got.stdout)), "\n---\n")
	line, _, _ := strings.Cut(body, "\n")
	return line
}

// withHub writes the hub notes/hubprj.md, which a Hubs line can name besides index.
func withHub(t *testing.T) {
	t.Helper()
	writeFile(t, "notes/hubprj.md", doc("Projects", "hub", hubLine+"\n\nProjects."))
}

func TestIngestHub(t *testing.T) {
	env := ingestVault(t)
	withHub(t)
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"one hub", []string{"--hub", "hubprj"}, "Hubs: " + projectsHub},
		{"two hubs in flag order", []string{"--hub", "hubprj", "--hub", "index"}, "Hubs: " + projectsHub + ", " + indexHub},
		{"reversed", []string{"--hub", "index", "--hub", "hubprj"}, "Hubs: " + indexHub + ", " + projectsHub},
		{"duplicate hub once", []string{"--hub", "hubprj", "--hub", "index", "--hub", "hubprj"}, "Hubs: " + projectsHub + ", " + indexHub},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ingestHubLine(t, env, tt.args...); got != tt.want {
				t.Errorf("Hubs line %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIngestHubTitleWithComma: a hub title with a comma leaves the Hubs line lint-clean.
func TestIngestHubTitleWithComma(t *testing.T) {
	env := ingestVault(t)
	writeFile(t, "notes/hubprs.md", doc("Parsing, lexing", "hub", hubLine+"\n\nParsing."))
	if got, want := ingestHubLine(t, env, "--hub", "hubprs", "--hub", "index"), "Hubs: [[hubprs|Parsing, lexing]], [[index|Index]]"; got != want {
		t.Errorf("Hubs line %q, want %q", got, want)
	}
	if got := run(env, "lint"); strings.Contains(got.stdout, "E_HUBS_LINE") || strings.Contains(got.stdout, "W_STALE_ALIAS") {
		t.Errorf("lint stdout %q, want no Hubs line finding", got.stdout)
	}
}

func TestIngestHubRejected(t *testing.T) {
	env := ingestVault(t)
	writeFile(t, "notes/not001.md", doc("N", "note", hubLine))
	for _, tt := range []struct{ name, id, want string }{
		{"unknown ID", "nope", "nope"},
		{"not a hub", "not001", "not001"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := run(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T", "--hub", "index", "--hub", tt.id)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, tt.want) || !strings.Contains(got.stderr, "hub") {
				t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, a hub error naming %q", got.code, got.stdout, got.stderr, tt.want)
			}
			if names := listDir(t, "notes"); !slices.Equal(names, []string{"not001.md"}) {
				t.Errorf("notes/ holds %q, want only not001.md", names)
			}
			if names := listDir(t, "raw"); len(names) != 0 {
				t.Errorf("raw/ holds %q, want nothing", names)
			}
		})
	}
}

// ingest needs at least one --hub, so that a new source always has a valid Hubs line.
func TestIngestWithoutHubWritesNothing(t *testing.T) {
	env := ingestVault(t)
	got := run(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T")
	if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "--hub") {
		t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout and a message that names --hub", got.code, got.stdout, got.stderr)
	}
	for _, dir := range []string{"notes", "raw"} {
		if names := listDir(t, dir); len(names) != 0 {
			t.Errorf("%s/ holds %q, want nothing", dir, names)
		}
	}
}

func TestIngestShortTitle(t *testing.T) {
	env := ingestVault(t)
	got := runIngest(env, "ingest", writeTemp(t, "a.txt", "x"), "-t", "Short Title")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	if note := readFile(t, strings.TrimSpace(got.stdout)); !strings.Contains(note, "title: \"Short Title\"\n") {
		t.Errorf("note lacks the -t title:\n%s", note)
	}
}
