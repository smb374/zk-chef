package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
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

var notePathRe = regexp.MustCompile(`^sources/([0-9]{12}[a-z0-9]{4})\.md\n$`)

func TestIngestFile(t *testing.T) {
	env := ingestVault(t)
	const content = "some raw bytes\n"
	src := writeTemp(t, "My File.txt", content)
	got := run(env, "ingest", src, "--title", "My Title")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	m := notePathRe.FindStringSubmatch(got.stdout)
	if m == nil {
		t.Fatalf("stdout %q is not exactly one sources/<id>.md path", got.stdout)
	}
	id := m[1]
	rawPath := "raw/" + id + "-My-File.txt"
	if b := readFile(t, rawPath); b != content {
		t.Errorf("%s = %q, want %q", rawPath, b, content)
	}
	note := readFile(t, "sources/"+id+".md")
	for _, want := range []string{
		"raw: " + rawPath + "\n",
		fmt.Sprintf("sha256: %x\n", sha256.Sum256([]byte(content))),
		"review: unreviewed\n",
		"title: \"My Title\"\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "url:") {
		t.Errorf("file ingest set url:\n%s", note)
	}
	if strings.Count(note, "raw:") != 1 || strings.Count(note, "sha256:") != 1 || strings.Count(note, "review:") != 1 {
		t.Errorf("template keys duplicated:\n%s", note)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeHTTP replaces HTTPClient with one that answers every request with status and body.
func fakeHTTP(t *testing.T, status int, body string) {
	t.Helper()
	old := HTTPClient
	HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
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

func TestIngestURL(t *testing.T) {
	tests := []struct {
		name, url, wantName string
	}{
		{"last path segment, query stripped", "https://example.com/files/paper.pdf?x=1&y=2", "paper.pdf"},
		{"unsafe characters replaced", "http://example.com/a%20b$c.txt", "a-b-c.txt"},
		{"empty path", "https://example.com/", "download"},
		{"host only", "https://example.com", "download"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			const body = "downloaded bytes\n"
			fakeHTTP(t, 200, body)
			got := run(env, "ingest", tt.url, "--title", "From Web")
			if got.code != 0 {
				t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
			}
			m := notePathRe.FindStringSubmatch(got.stdout)
			if m == nil {
				t.Fatalf("stdout %q is not exactly one sources/<id>.md path", got.stdout)
			}
			rawPath := "raw/" + m[1] + "-" + tt.wantName
			if b := readFile(t, rawPath); b != body {
				t.Errorf("%s = %q, want %q", rawPath, b, body)
			}
			note := readFile(t, "sources/"+m[1]+".md")
			for _, want := range []string{
				"raw: " + rawPath + "\n",
				fmt.Sprintf("sha256: %x\n", sha256.Sum256([]byte(body))),
				"review: unreviewed\n",
				"url: " + strconv.Quote(tt.url) + "\n",
			} {
				if !strings.Contains(note, want) {
					t.Errorf("note lacks %q:\n%s", want, note)
				}
			}
			if names := listDir(t, "raw"); len(names) != 1 {
				t.Errorf("raw/ holds %q, want only %s", names, rawPath)
			}
		})
	}
}

func TestIngestURLFailureLeavesNothing(t *testing.T) {
	env := ingestVault(t)
	fakeHTTP(t, 404, "not found")
	got := run(env, "ingest", "https://example.com/gone.pdf", "--title", "Gone")
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "404") {
		t.Errorf("got code %d stdout %q stderr %q, want 1, empty stdout, 404 on stderr", got.code, got.stdout, got.stderr)
	}
	for _, dir := range []string{"sources", "raw"} {
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
			got := run(env, tt.args...)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, tt.want) {
				t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, %q on stderr", got.code, got.stdout, got.stderr, tt.want)
			}
			if names := listDir(t, "sources"); len(names) != 0 {
				t.Errorf("sources/ holds %q, want nothing", names)
			}
		})
	}
}

func TestIngestZkMissing(t *testing.T) {
	env := ingestVault(t)
	t.Setenv("PATH", t.TempDir())
	got := run(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T")
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
		got := run(env, "ingest", writeTemp(t, "j.txt", body), "--title", "J", "--json")
		if got.code != 0 {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		var out struct {
			OK     bool    `json:"ok"`
			Path   string  `json:"path"`
			Raw    string  `json:"raw"`
			SHA256 string  `json:"sha256"`
			URL    *string `json:"url"`
		}
		if err := json.Unmarshal([]byte(got.stdout), &out); err != nil {
			t.Fatalf("stdout is not one JSON object: %v\n%s", err, got.stdout)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(out.Path, "sources/"), ".md")
		if !out.OK || !notePathRe.MatchString(out.Path+"\n") || out.Raw != "raw/"+id+"-j.txt" || out.SHA256 != sum || out.URL != nil {
			t.Errorf("unexpected JSON: %+v", out)
		}
		if !strings.Contains(got.stdout, `"url":null`) {
			t.Errorf("stdout %q lacks \"url\":null", got.stdout)
		}
	})
	t.Run("URL", func(t *testing.T) {
		fakeHTTP(t, 200, body)
		got := run(env, "ingest", "https://example.com/j.txt", "--title", "J", "--json")
		if got.code != 0 {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		if !strings.Contains(got.stdout, `"url":"https://example.com/j.txt"}`) || !strings.Contains(got.stdout, `"sha256":"`+sum+`"`) {
			t.Errorf("stdout %q", got.stdout)
		}
	})
}
func TestIngestedSourcePassesLint(t *testing.T) {
	for _, tt := range []struct{ name, arg string }{
		{"file", ""},
		{"URL", "https://example.com/p.pdf?a=b: c"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := ingestVault(t)
			arg := tt.arg
			if arg == "" {
				arg = writeTemp(t, "p.txt", "raw\n")
			} else {
				fakeHTTP(t, 200, "raw\n")
			}
			got := run(env, "ingest", arg, "--title", "A Source: with colon")
			if got.code != 0 {
				t.Fatalf("ingest exit %d: %s", got.code, got.stderr)
			}
			path := strings.TrimSpace(got.stdout)
			body := "\nWhy this source matters.\n\n## Extract\n\nSome extracted text.\n\n## Review\n"
			text := readFile(t, path)
			if tt.name == "URL" {
				text = strings.Replace(text, "\nurl:", "\ncitation: \"A. Author, T.\"\nurl:", 1)
			}
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
	sourcesHub  = "[[000000000000src0|Sources]]"
	projectsHub = "[[000000000000prj0|Projects]]"
	indexHub    = "[[index|Index]]"
)

// ingestHubLine ingests a file with args and returns the Hubs line of the new note.
func ingestHubLine(t *testing.T, env []string, args ...string) string {
	t.Helper()
	got := run(env, append([]string{"ingest", writeTemp(t, "a.txt", "x"), "--title", "T"}, args...)...)
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	_, body, _ := strings.Cut(readFile(t, strings.TrimSpace(got.stdout)), "\n---\n")
	line, _, _ := strings.Cut(body, "\n")
	return line
}

func TestIngestHub(t *testing.T) {
	env := ingestVault(t)
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"one hub", []string{"--hub", "000000000000prj0"}, "Hubs: " + sourcesHub + " · " + projectsHub},
		{"two hubs in flag order", []string{"--hub", "000000000000prj0", "--hub", "index"}, "Hubs: " + sourcesHub + " · " + projectsHub + " · " + indexHub},
		{"reversed", []string{"--hub", "index", "--hub", "000000000000prj0"}, "Hubs: " + sourcesHub + " · " + indexHub + " · " + projectsHub},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ingestHubLine(t, env, tt.args...); got != tt.want {
				t.Errorf("Hubs line %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngestHubDuplicateAndSources(t *testing.T) {
	env := ingestVault(t)
	got := ingestHubLine(t, env, "--hub", "000000000000prj0", "--hub", "000000000000src0", "--hub", "000000000000prj0")
	if want := "Hubs: " + sourcesHub + " · " + projectsHub; got != want {
		t.Errorf("Hubs line %q, want %q", got, want)
	}
}

func TestIngestHubRejected(t *testing.T) {
	env := ingestVault(t)
	if err := os.WriteFile("notes/n1.md", []byte("---\nid: n1\ntitle: N\nkind: note\n---\nHubs: "+indexHub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, id, want string }{
		{"unknown ID", "nope", "nope"},
		{"not a hub", "n1", "n1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := run(env, "ingest", writeTemp(t, "a.txt", "x"), "--title", "T", "--hub", "index", "--hub", tt.id)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, tt.want) || !strings.Contains(got.stderr, "hub") {
				t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, a hub error naming %q", got.code, got.stdout, got.stderr, tt.want)
			}
			for _, dir := range []string{"sources", "raw"} {
				if names := listDir(t, dir); len(names) != 0 {
					t.Errorf("%s/ holds %q, want nothing", dir, names)
				}
			}
		})
	}
}

func TestIngestShortTitle(t *testing.T) {
	env := ingestVault(t)
	got := run(env, "ingest", writeTemp(t, "a.txt", "x"), "-t", "Short Title")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	if note := readFile(t, strings.TrimSpace(got.stdout)); !strings.Contains(note, "title: \"Short Title\"\n") {
		t.Errorf("note lacks the -t title:\n%s", note)
	}
}
