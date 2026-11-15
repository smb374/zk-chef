package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/version"
)

const configPath = ".zk-chef/config.toml"

// firstTagVersion is the first release that reads the tag-based vault format; a vault with an older min_version is a v0.2.x vault.
const firstTagVersion = "0.3.0"

// ungated verbs must run on a binary older than the vault's min_version.
var ungated = map[string]bool{"self-update": true, "doctor": true}

// gate checks the vault's min_version against the binary version.
// It returns false, after printing the reason to stderr, when the verb must not run.
// The config is root/.zk-chef/config.toml; an empty root means the working directory (verbs
// without --root). No config file or no key means no gate.
func gate(root string, stderr io.Writer) bool {
	path := filepath.Join(root, configPath)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		fmt.Fprintf(stderr, "zk-chef: read %s: %v\n", path, err)
		return false
	}
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		fmt.Fprintf(stderr, "zk-chef: parse %s: %v\n", path, err)
		return false
	}
	raw, ok := cfg["min_version"]
	if !ok {
		return true
	}
	minVersion, isString := raw.(string)
	if !isString || !version.Valid(minVersion) {
		fmt.Fprintf(stderr, "zk-chef: %s: min_version %v is not a semantic version\n", path, raw)
		return false
	}
	if cmp, err := version.Compare(minVersion, firstTagVersion); err == nil && cmp < 0 {
		fmt.Fprintf(stderr, "zk-chef: %s: min_version %s is a v0.2.x vault; zk-chef %s reads v0.3 vaults only\nrun zk-chef init in a new directory; there is no migration\n", path, minVersion, firstTagVersion)
		return false
	}
	bin := version.Current()
	if bin == "dev" {
		return true
	}
	cmp, err := version.Compare(bin, minVersion)
	if err != nil {
		fmt.Fprintf(stderr, "zk-chef: binary version %q is not a semantic version\n", bin)
		return false
	}
	if cmp < 0 {
		fmt.Fprintf(stderr, "zk-chef %s is older than min_version %s in %s\nrun zk-chef self-update\n", bin, minVersion, path)
		return false
	}
	return true
}
