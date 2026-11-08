package lint

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ConfigPath is the vault config, relative to the vault root.
const ConfigPath = ".zk-chef/config.toml"

// folderKind maps each note folder to the kind it holds.
var folderKind = map[string]string{"notes": "note", "hubs": "hub", "sources": "source", "projects": "log"}

// loaded lists every folder lint reads; raw/ and keys/ are read as bytes only.
var loaded = []string{"notes", "hubs", "sources", "projects", "raw", "keys"}

// LoadWorktree reads the lint inputs under root into a map of slash paths to bytes.
func LoadWorktree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, folder := range loaded {
		err := filepath.WalkDir(filepath.Join(root, folder), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			files[filepath.ToSlash(rel)] = b
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, ConfigPath)); err == nil {
		files[ConfigPath] = b
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return files, nil
}

// LoadStaged reads the lint inputs from the git index, not the worktree.
func LoadStaged(root string) (map[string][]byte, error) {
	ls := exec.Command("git", "-C", root, "ls-files", "-z")
	listing, err := ls.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", gitErr(err))
	}
	var names []string
	seen := map[string]bool{}
	for _, n := range strings.Split(string(listing), "\x00") {
		top := strings.SplitN(n, "/", 2)[0]
		if (n == ConfigPath || contains(loaded, top)) && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	var req bytes.Buffer
	for _, n := range names {
		req.WriteString(":" + n + "\n")
	}
	cat := exec.Command("git", "-C", root, "cat-file", "--batch")
	cat.Stdin = &req
	data, err := cat.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file: %w", gitErr(err))
	}
	files := map[string][]byte{}
	for _, n := range names {
		eol := bytes.IndexByte(data, '\n')
		if eol < 0 {
			return nil, fmt.Errorf("git cat-file: truncated output at %s", n)
		}
		header := strings.Fields(string(data[:eol]))
		if len(header) != 3 {
			return nil, fmt.Errorf("git cat-file: %s: %s", n, data[:eol])
		}
		size, err := strconv.Atoi(header[2])
		if err != nil || eol+1+size >= len(data) {
			return nil, fmt.Errorf("git cat-file: bad header %q for %s", data[:eol], n)
		}
		files[n] = data[eol+1 : eol+1+size]
		data = data[eol+1+size+1:]
	}
	return files, nil
}

// gitErr adds git's stderr to an exec error.
func gitErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
