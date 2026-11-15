package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/smb374/zk-chef/internal/lint"
)

// HTTPClient downloads ingest URLs. Tests replace it with a fake Transport.
var HTTPClient = &http.Client{Timeout: 60 * time.Second}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// rawExtRe is an extension ingest keeps for a raw file: a dot and lowercase letters and digits.
var rawExtRe = regexp.MustCompile(`\A\.[a-z0-9]+\z`)

// fileExt is the lowercased extension of name, or "" when it has none that rawExtRe accepts.
func fileExt(name string) string {
	if ext := strings.ToLower(filepath.Ext(name)); rawExtRe.MatchString(ext) {
		return ext
	}
	return ""
}

// ingestVerb makes a source note for a local file or URL. It keeps the raw bytes in raw/, except for a URL with --no-raw.
func ingestVerb(inv *invocation) int {
	if inv.title == "" || len(inv.args) != 1 {
		return fail(inv, "usage: zk-chef ingest PATH|URL --title|-t T --hub ID [--hub ID]... [--no-raw] [--root DIR] [--json]")
	}
	if len(inv.hubs) == 0 {
		return fail(inv, "zk-chef ingest: need at least one --hub ID, so that the source has a Hubs line")
	}
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef ingest: "+err.Error())
	}
	src := inv.args[0]
	isURL := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
	if inv.noRaw && !isURL {
		return fail(inv, "zk-chef ingest: --no-raw is valid only with a URL")
	}
	var name string
	if isURL {
		if name, err = urlName(src); err != nil {
			return fail(inv, src+": "+err.Error())
		}
	} else {
		st, err := os.Stat(src)
		if err != nil || !st.Mode().IsRegular() {
			return fail(inv, src+": not a regular file")
		}
		name = unsafeNameRe.ReplaceAllString(filepath.Base(src), "-")
	}
	files, err := lint.LoadWorktree(root)
	if err != nil {
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	pages := lint.Pages(files)
	for _, id := range inv.hubs {
		if p, ok := pages[id]; !ok || p.Kind() != "hub" {
			return fail(inv, "zk-chef ingest: --hub "+id+": no hub with that ID")
		}
	}
	zk, err := exec.LookPath("zk")
	if err != nil {
		return fail(inv, "zk not found; run zk-chef install")
	}

	var tmp, sum, contentType string
	if !inv.noRaw {
		rawDir := filepath.Join(root, "raw")
		if err := os.MkdirAll(rawDir, 0o755); err != nil {
			return failCode(inv, 1, "zk-chef ingest: "+err.Error())
		}
		if isURL {
			tmp, sum, contentType, err = stageURL(src, rawDir)
		} else {
			tmp, sum, err = stageFile(src, rawDir)
		}
		if err != nil {
			return failCode(inv, 1, "zk-chef ingest: "+err.Error())
		}
		defer os.Remove(tmp)
	}

	notePath, err := zkNewSource(zk, root, inv)
	if err != nil {
		return failCode(inv, 1, err.Error())
	}
	id := strings.TrimSuffix(filepath.Base(notePath), ".md")
	var raw string
	if !inv.noRaw {
		ext := fileExt(name)
		if ext == "" {
			ext = contentTypeExt(contentType)
		}
		raw = "raw/" + id + ext
	}

	text, err := os.ReadFile(notePath)
	if err != nil {
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	fm := map[string]string{}
	var order []string
	accessed := ""
	if isURL {
		accessed = time.Now().UTC().Format("2006-01-02")
		fm["url"], fm["accessed"] = strconv.Quote(src), accessed
		order = append(order, "url", "accessed")
	}
	if !inv.noRaw {
		fm["raw"], fm["sha256"] = raw, sum
		order = append(order, "raw", "sha256")
	}
	edited, err := setFrontmatter(string(text), fm, order)
	if err != nil {
		os.Remove(notePath)
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	if edited, err = addHubs(edited, inv.hubs, pages); err != nil {
		os.Remove(notePath)
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	rawPath := ""
	if !inv.noRaw {
		rawPath = filepath.Join(root, filepath.FromSlash(raw))
		if err := os.Link(tmp, rawPath); err != nil {
			os.Remove(notePath)
			return failCode(inv, 1, "zk-chef ingest: "+err.Error())
		}
	}
	if err := os.WriteFile(notePath, []byte(edited), 0o644); err != nil {
		os.Remove(notePath)
		if rawPath != "" {
			os.Remove(rawPath)
		}
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	rel, err := filepath.Rel(root, notePath)
	if err != nil {
		rel = notePath
	}
	rel = filepath.ToSlash(rel)
	if inv.json {
		var u, acc, r, s *string
		if isURL {
			u, acc = &src, &accessed
		}
		if !inv.noRaw {
			r, s = &raw, &sum
		}
		writeJSON(inv.stdout, struct {
			OK       bool    `json:"ok"`
			Path     string  `json:"path"`
			Raw      *string `json:"raw"`
			SHA256   *string `json:"sha256"`
			Accessed *string `json:"accessed"`
			URL      *string `json:"url"`
		}{true, rel, r, s, acc, u})
		return 0
	}
	fmt.Fprintln(inv.stdout, rel)
	return 0
}

// zkNewSource runs zk new with the source template and returns the absolute path of the new note.
// When zk returns a note that was there before the call, it tries again; after 3 tries it fails and leaves that note alone.
func zkNewSource(zk, root string, inv *invocation) (string, error) {
	var clash string
	for range 3 {
		before := map[string]bool{}
		if es, err := os.ReadDir(filepath.Join(root, lint.NotesDir)); err == nil {
			for _, e := range es {
				before[filepath.Join(root, lint.NotesDir, e.Name())] = true
			}
		}
		cmd := exec.Command(zk, "new", lint.NotesDir, "--template", "source.md", "--title", inv.title, "--print-path", "--no-input")
		cmd.Dir = root
		cmd.Env = append(append(os.Environ(), inv.env...), "ZK_NOTEBOOK_DIR="+root)
		var zkOut, zkErr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &zkOut, &zkErr
		if err := cmd.Run(); err != nil {
			return "", errors.New("zk new failed: " + err.Error() + "\n" + strings.TrimSpace(zkErr.String()))
		}
		notePath := strings.TrimSpace(zkOut.String())
		if !filepath.IsAbs(notePath) {
			notePath = filepath.Join(root, notePath)
		}
		if !before[filepath.Clean(notePath)] {
			return notePath, nil
		}
		clash = notePath
	}
	rel, err := filepath.Rel(root, clash)
	if err != nil {
		rel = clash
	}
	return "", fmt.Errorf("zk new returned the existing note %s 3 times; nothing written", filepath.ToSlash(rel))
}

// urlName is the safe raw file name for rawURL: its last path segment, or "download".
func urlName(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", errors.New("URL has no host")
	}
	name := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if name == "" {
		return "download", nil
	}
	return unsafeNameRe.ReplaceAllString(name, "-"), nil
}

// stageURL downloads rawURL to a new temp file in dir and returns its path, sha256 (hex) and Content-Type.
func stageURL(rawURL, dir string) (path, sum, contentType string, err error) {
	resp, err := HTTPClient.Get(rawURL)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", "", fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	path, sum, err = stage(resp.Body, dir)
	return path, sum, resp.Header.Get("Content-Type"), err
}

// contentTypeExt is the first extension the standard MIME table gives for contentType, or ".html" when it lists that one
// (the table sorts .htm first). It is "" when the table has none or rawExtRe refuses it.
func contentTypeExt(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	exts, _ := mime.ExtensionsByType(mediaType)
	if slices.Contains(exts, ".html") {
		return ".html"
	}
	if len(exts) > 0 && rawExtRe.MatchString(exts[0]) {
		return exts[0]
	}
	return ""
}

// stageFile copies src to a new temp file in dir and returns its path and sha256 (hex).
func stageFile(src, dir string) (path, sum string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	return stage(in, dir)
}

func stage(in io.Reader, dir string) (path, sum string, err error) {
	out, err := os.CreateTemp(dir, ".ingest-*")
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", "", err
	}
	return out.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// addHubs appends a link to each hub in ids, in order, to the Hubs line of text, joined with ", ". A hub the line already links is skipped.
func addHubs(text string, ids []string, pages map[string]*lint.Page) (string, error) {
	_, body, ok := lint.SplitFrontmatter(text)
	if !ok || !strings.HasPrefix(body, "Hubs: ") {
		return "", errors.New("note has no Hubs line")
	}
	named, _ := lint.ParsePage("", text).Links()
	end := strings.IndexByte(body, '\n')
	if end < 0 {
		end = len(body)
	}
	var add strings.Builder
	for _, id := range ids {
		if !slices.Contains(named, id) {
			named = append(named, id)
			if strings.HasSuffix(strings.TrimRight(body[:end], " ")+add.String(), "]]") {
				add.WriteString(", ")
			}
			add.WriteString("[[" + id + "|" + pages[id].Title() + "]]")
		}
	}
	at := len(text) - len(body) + end
	return text[:at] + add.String() + text[at:], nil
}

// setFrontmatter replaces the line of each key in order that the frontmatter already has,
// and adds the missing ones before the closing ---. Other lines stay as they are.
func setFrontmatter(text string, values map[string]string, order []string) (string, error) {
	lines := strings.Split(text, "\n")
	if lines[0] != "---" {
		return "", fmt.Errorf("note has no frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", fmt.Errorf("note frontmatter is not closed")
	}
	var add []string
	for _, k := range order {
		line := k + ": " + values[k]
		found := false
		for i := 1; i < end; i++ {
			if strings.HasPrefix(lines[i], k+":") {
				lines[i], found = line, true
				break
			}
		}
		if !found {
			add = append(add, line)
		}
	}
	out := append(append(append([]string{}, lines[:end]...), add...), lines[end:]...)
	return strings.Join(out, "\n"), nil
}
