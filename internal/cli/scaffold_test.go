package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestScaffoldHashesCoverEmbeddedFiles is the release check of ADR 0001: the current version of each embedded scaffold file is in the known-hash list.
func TestScaffoldHashesCoverEmbeddedFiles(t *testing.T) {
	setupVault(t, "dev", nil) // points Assets at the repo
	tree, err := fs.Sub(Assets, exportScaffoldDir)
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, scaffoldVendorDir) {
			return err
		}
		b, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		if p == "hugo.toml.tmpl" {
			if !slices.Contains(hugoTomlTemplates, string(b)) {
				t.Errorf("%s: current template is not in hugoTomlTemplates; append it (docs/build-notes.md)", p)
			}
			return nil
		}
		sum := sha256.Sum256(b)
		if !slices.Contains(scaffoldHashes[p], hex.EncodeToString(sum[:])) {
			t.Errorf("%s: sha256 %x is not in scaffoldHashes; append it (docs/build-notes.md)", p, sum)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
