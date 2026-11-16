package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/smb374/zk-chef/internal/diff"
)

// hugoConfigNames are the names, in OUT, that Hugo reads its site config from (a file, or the config/ directory).
var hugoConfigNames = []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config.yaml", "config.yml", "config.json", "config"}

// scaffoldVendoredDir is the folder, under the scaffold tree, of files that zk-chef owns.
const scaffoldVendoredDir = "static/vendored/"

// scaffoldVendoredTufte is the folder, in OUT, of the vendored Tufte files; the Style plain deletes it.
const scaffoldVendoredTufte = scaffoldVendoredDir + "tufte"

// siteStateFile is the file in OUT that records the Style of the site, as {"style": "<name>"}.
const siteStateFile = ".zk-chef-site.json"

// exportStyleDirs maps each Style to its embedded tree of layouts and static files. hugo.toml.tmpl is shared by the Styles and lives in exportScaffoldDir.
var exportStyleDirs = map[string]string{"plain": "export-scaffold/hugo-plain", "tufte": exportScaffoldDir}

// exportStyleNames lists the Styles, for messages.
const exportStyleNames = "plain or tufte"

// resolveStyle returns the Style of a Hugo export to out: flag when set, else the one in out's state file, else tufte when out has static/vendored/tufte/ and plain otherwise.
// An unknown flag or a corrupt state file is an error. It writes nothing.
func resolveStyle(out, flag string) (string, error) {
	if _, ok := exportStyleDirs[flag]; !ok && flag != "" {
		return "", fmt.Errorf("unknown style %s; use %s", flag, exportStyleNames)
	}
	b, err := os.ReadFile(filepath.Join(out, siteStateFile))
	var state struct {
		Style string `json:"style"`
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", err
	default:
		if err := json.Unmarshal(b, &state); err != nil {
			return "", fmt.Errorf("%s in %s is corrupt (%w); fix or delete it", siteStateFile, out, err)
		}
		if _, ok := exportStyleDirs[state.Style]; !ok {
			return "", fmt.Errorf("%s in %s is corrupt (unknown style %q; use %s); fix or delete it", siteStateFile, out, state.Style, exportStyleNames)
		}
	}
	if flag != "" {
		return flag, nil
	}
	if state.Style != "" {
		return state.Style, nil
	}
	if fi, err := os.Stat(filepath.Join(out, filepath.FromSlash(scaffoldVendoredTufte))); err == nil && fi.IsDir() {
		return "tufte", nil
	}
	return "plain", nil
}

// writeStyle records style in the state file in out.
func writeStyle(out, style string) error {
	q, err := json.Marshal(style)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, siteStateFile), []byte(`{"style": `+string(q)+"}\n"), 0o644)
}

// scaffoldReport lists the slash paths, relative to OUT, that an export created, replaced with the current version, and kept as the user's edit. diffs maps each kept path to the unified diff of the user's file to the current version.
type scaffoldReport struct {
	created, updated, kept []string
	diffs                  map[string]string
}

// renderHugoToml returns the hugo.toml that template tmpl gives for title.
func renderHugoToml(tmpl, title string) []byte {
	return []byte(strings.Replace(tmpl, "@TITLE@", tomlString(title), 1))
}

// exportScaffoldHugo brings the Hugo site files in out up to the embedded scaffold of style, by the rule in docs/adr/0001-export-scaffold-updates-by-known-hashes.md:
// a missing file is created; a file that equals a released version (scaffoldHashes, hugoTomlTemplates) is replaced; any other file is kept.
// Files under static/vendored/ are replaced whenever they differ, and the style plain deletes static/vendored/tufte/. hugo.toml is not created when another Hugo config exists.
func exportScaffoldHugo(out, title, style string) (scaffoldReport, error) {
	rep := scaffoldReport{[]string{}, []string{}, []string{}, map[string]string{}}
	tree, err := fs.Sub(Assets, exportStyleDirs[style])
	if err != nil {
		return rep, err
	}
	tmpl, err := fs.ReadFile(Assets, exportScaffoldDir+"/hugo.toml.tmpl")
	if err != nil {
		return rep, err
	}
	configured := false
	for _, name := range hugoConfigNames[1:] { // [0] is hugo.toml, handled with the template below
		if _, err := os.Lstat(filepath.Join(out, name)); err == nil {
			configured = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return rep, err
		}
	}
	// apply writes want to rel in out, or keeps the file there; shipped reports whether have is a released version of rel.
	apply := func(rel string, want []byte, shipped func(have []byte) bool) error {
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
		case !missing && !strings.HasPrefix(rel, scaffoldVendoredDir) && !shipped(have):
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
	}
	err = apply("hugo.toml", renderHugoToml(string(tmpl), title), func(have []byte) bool {
		return slices.ContainsFunc(hugoTomlTemplates, func(t string) bool { return bytes.Equal(have, renderHugoToml(t, title)) })
	})
	if err != nil {
		return rep, err
	}
	err = fs.WalkDir(tree, ".", func(src string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || src == "hugo.toml.tmpl" {
			return err
		}
		want, err := fs.ReadFile(tree, src)
		if err != nil {
			return err
		}
		return apply(src, want, func(have []byte) bool {
			sum := sha256.Sum256(have)
			return slices.Contains(scaffoldHashes[src], hex.EncodeToString(sum[:]))
		})
	})
	if err == nil && style == "plain" {
		err = os.RemoveAll(filepath.Join(out, filepath.FromSlash(scaffoldVendoredTufte)))
	}
	return rep, err
}
