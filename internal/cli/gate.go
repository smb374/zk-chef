package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/version"
)

const configPath = ".zk-chef/config.toml"

// ungated verbs must run on a binary older than the vault's min_version.
var ungated = map[string]bool{"self-update": true, "doctor": true}

// gate checks the vault's min_version against the binary version.
// It returns false, after printing the reason to stderr, when the verb must not run.
// The vault root is the working directory; no config file or no key means no gate.
func gate(stderr io.Writer) bool {
	b, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		fmt.Fprintf(stderr, "zk-chef: read %s: %v\n", configPath, err)
		return false
	}
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		fmt.Fprintf(stderr, "zk-chef: parse %s: %v\n", configPath, err)
		return false
	}
	raw, ok := cfg["min_version"]
	if !ok {
		return true
	}
	minVersion, isString := raw.(string)
	if !isString || !version.Valid(minVersion) {
		fmt.Fprintf(stderr, "zk-chef: %s: min_version %v is not a semantic version\n", configPath, raw)
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
		fmt.Fprintf(stderr, "zk-chef %s is older than min_version %s in %s\nrun zk-chef self-update\n", bin, minVersion, configPath)
		return false
	}
	return true
}
