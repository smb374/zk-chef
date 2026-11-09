package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HTTPClient downloads ingest URLs. Tests replace it with a fake Transport.
var HTTPClient = &http.Client{Timeout: 60 * time.Second}

var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// ingestVerb makes a source note for a local file or URL and keeps the raw bytes in raw/.
func ingestVerb(inv *invocation) int {
	if inv.title == "" || len(inv.args) != 1 {
		return fail(inv, "usage: zk-chef ingest PATH|URL --title T [--root DIR] [--json]")
	}
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef ingest: "+err.Error())
	}
	src := inv.args[0]
	isURL := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://")
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
	zk, err := exec.LookPath("zk")
	if err != nil {
		return fail(inv, "zk not found; run zk-chef install")
	}

	rawDir := filepath.Join(root, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	var tmp, sum string
	if isURL {
		tmp, sum, err = stageURL(src, rawDir)
	} else {
		tmp, sum, err = stageFile(src, rawDir)
	}
	if err != nil {
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	defer os.Remove(tmp)

	cmd := exec.Command(zk, "new", "sources", "--title", inv.title, "--print-path", "--no-input")
	cmd.Dir = root
	cmd.Env = append(append(os.Environ(), inv.env...), "ZK_NOTEBOOK_DIR="+root)
	var zkOut, zkErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &zkOut, &zkErr
	if err := cmd.Run(); err != nil {
		return failCode(inv, 1, "zk new failed: "+err.Error()+"\n"+strings.TrimSpace(zkErr.String()))
	}
	notePath := strings.TrimSpace(zkOut.String())
	if !filepath.IsAbs(notePath) {
		notePath = filepath.Join(root, notePath)
	}
	id := strings.TrimSuffix(filepath.Base(notePath), ".md")
	raw := "raw/" + id + "-" + name

	text, err := os.ReadFile(notePath)
	if err != nil {
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	fm := map[string]string{"raw": raw, "sha256": sum, "review": "unreviewed"}
	order := []string{"raw", "sha256", "review"}
	if isURL {
		fm["url"] = strconv.Quote(src)
		order = append(order, "url")
	}
	edited, err := setFrontmatter(string(text), fm, order)
	if err != nil {
		os.Remove(notePath)
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	rawPath := filepath.Join(root, filepath.FromSlash(raw))
	if err := os.Link(tmp, rawPath); err != nil {
		os.Remove(notePath)
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	if err := os.WriteFile(notePath, []byte(edited), 0o644); err != nil {
		os.Remove(notePath)
		os.Remove(rawPath)
		return failCode(inv, 1, "zk-chef ingest: "+err.Error())
	}
	rel, err := filepath.Rel(root, notePath)
	if err != nil {
		rel = notePath
	}
	rel = filepath.ToSlash(rel)
	if inv.json {
		var u *string
		if isURL {
			u = &src
		}
		writeJSON(inv.stdout, struct {
			OK     bool    `json:"ok"`
			Path   string  `json:"path"`
			Raw    string  `json:"raw"`
			SHA256 string  `json:"sha256"`
			URL    *string `json:"url"`
		}{true, rel, raw, sum, u})
		return 0
	}
	fmt.Fprintln(inv.stdout, rel)
	return 0
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

// stageURL downloads rawURL to a new temp file in dir and returns its path and sha256 (hex).
func stageURL(rawURL, dir string) (path, sum string, err error) {
	resp, err := HTTPClient.Get(rawURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return stage(resp.Body, dir)
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
