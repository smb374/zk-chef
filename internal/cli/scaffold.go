package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/smb374/zk-chef/internal/diff"
)

// hugoConfigNames are the names, in OUT, that Hugo reads its site config from (a file, or the config/ directory).
var hugoConfigNames = []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config.yaml", "config.yml", "config.json", "config"}

// scaffoldVendorDir is the folder, under the scaffold tree, of files that zk-chef owns.
const scaffoldVendorDir = "static/vendor/"

// scaffoldReport lists the slash paths, relative to OUT, that an export created, replaced with the current version, and kept as the user's edit. diffs maps each kept path to the unified diff of the user's file to the current version.
type scaffoldReport struct {
	created, updated, kept []string
	diffs                  map[string]string
}

// renderHugoToml returns the hugo.toml that template tmpl gives for title.
func renderHugoToml(tmpl, title string) []byte {
	return []byte(strings.Replace(tmpl, "@TITLE@", tomlString(title), 1))
}

// exportScaffoldHugo brings the Hugo site files in out up to the embedded scaffold, by the rule in docs/adr/0001-export-scaffold-updates-by-known-hashes.md:
// a missing file is created; a file that equals a released version (scaffoldHashes, hugoTomlTemplates) is replaced; any other file is kept.
// Files under static/vendor/ are replaced whenever they differ. hugo.toml is not created when another Hugo config exists.
func exportScaffoldHugo(out, title string) (scaffoldReport, error) {
	rep := scaffoldReport{[]string{}, []string{}, []string{}, map[string]string{}}
	tree, err := fs.Sub(Assets, exportScaffoldDir)
	if err != nil {
		return rep, err
	}
	configured := false
	for _, name := range hugoConfigNames[1:] { // [0] is hugo.toml, handled by the walk below
		if _, err := os.Lstat(filepath.Join(out, name)); err == nil {
			configured = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return rep, err
		}
	}
	err = fs.WalkDir(tree, ".", func(src string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, err := fs.ReadFile(tree, src)
		if err != nil {
			return err
		}
		rel := src
		shipped := func(have []byte) bool {
			sum := sha256.Sum256(have)
			return slices.Contains(scaffoldHashes[rel], hex.EncodeToString(sum[:]))
		}
		if src == "hugo.toml.tmpl" {
			rel = "hugo.toml"
			want = renderHugoToml(string(want), title)
			shipped = func(have []byte) bool {
				return slices.ContainsFunc(hugoTomlTemplates, func(tmpl string) bool { return bytes.Equal(have, renderHugoToml(tmpl, title)) })
			}
		}
		dst := filepath.Join(out, filepath.FromSlash(rel))
		have, err := os.ReadFile(dst)
		missing := errors.Is(err, fs.ErrNotExist)
		switch {
		case missing && rel == "hugo.toml" && configured:
			return nil
		case err != nil && !missing:
			return err
		case !missing && bytes.Equal(have, want):
			return nil
		case !missing && !strings.HasPrefix(rel, scaffoldVendorDir) && !shipped(have):
			rep.kept = append(rep.kept, rel)
			rep.diffs[rel] = diff.Unified(rel, string(have), string(want))
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return err
		}
		if missing {
			rep.created = append(rep.created, rel)
		} else {
			rep.updated = append(rep.updated, rel)
		}
		return nil
	})
	return rep, err
}
