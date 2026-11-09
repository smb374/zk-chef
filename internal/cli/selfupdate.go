package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// selfUpdateBase is the release download URL that holds checksums.txt and the binaries.
// Tests point it at httptest.
var selfUpdateBase = "https://github.com/smb374/zk-chef/releases/latest/download"

// selfExecutable names the binary to replace. Tests override it.
var selfExecutable = os.Executable

// selfUpdateVerb replaces the running binary with the latest release for this platform.
func selfUpdateVerb(inv *invocation) int {
	asset := "zk-chef_" + hostOS + "_" + hostArch
	sums, err := httpGet(selfUpdateBase + "/checksums.txt")
	if err != nil {
		return fail(inv, "zk-chef self-update: "+err.Error())
	}
	want := checksumFor(string(sums), asset)
	if want == "" {
		return fail(inv, "zk-chef self-update: no checksum listed for "+asset)
	}
	binary, err := httpGet(selfUpdateBase + "/" + asset)
	if err != nil {
		return fail(inv, "zk-chef self-update: "+err.Error())
	}
	if sum := sha256.Sum256(binary); !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return fail(inv, "zk-chef self-update: checksum mismatch for "+asset)
	}
	target, err := selfExecutable()
	if err != nil {
		return fail(inv, "zk-chef self-update: "+err.Error())
	}
	if err := writeExecutable(filepath.Dir(target), filepath.Base(target), binary); err != nil {
		return fail(inv, "zk-chef self-update: "+err.Error())
	}
	home, err := homeDir(inv)
	if err != nil {
		return fail(inv, "zk-chef self-update: no home directory: "+err.Error())
	}
	// The running binary embeds the old skills, so the new binary writes them.
	// It runs in home: `install` is gated by the cwd's vault, and a bad pin there would stop it.
	cmd := exec.Command(target, "install", "--skills-only")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), inv.env...)
	cmd.Stdout, cmd.Stderr = io.Discard, inv.stderr
	if !inv.json {
		cmd.Stdout = inv.stdout
	}
	if err := cmd.Run(); err != nil {
		return fail(inv, "zk-chef self-update: install --skills-only: "+err.Error())
	}
	zk := zkResult{Status: "skipped"}
	ours, err := zkInstalledByUs(home)
	if err != nil {
		return fail(inv, "zk-chef self-update: "+err.Error())
	}
	if ours {
		tag, err := downloadZK(home)
		if err != nil {
			return fail(inv, "zk-chef self-update: "+err.Error())
		}
		zk = zkResult{"updated", &tag}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK   bool     `json:"ok"`
			Path string   `json:"path"`
			ZK   zkResult `json:"zk"`
		}{true, target, zk})
		return 0
	}
	fmt.Fprintf(inv.stdout, "updated zk-chef at %s\n", target)
	if zk.Version != nil {
		fmt.Fprintf(inv.stdout, "updated zk %s\n", *zk.Version)
	}
	return 0
}

// zkInstalledByUs reports whether state.toml says zk-chef installed zk.
func zkInstalledByUs(home string) (bool, error) {
	file := filepath.Join(home, ".config", "zk-chef", "state.toml")
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		ZK struct {
			Installed bool `toml:"installed_by_zk_chef"`
		} `toml:"zk"`
	}
	if err := toml.Unmarshal(b, &state); err != nil {
		return false, fmt.Errorf("%s: %w", file, err)
	}
	return state.ZK.Installed, nil
}

// checksumFor returns the digest listed for name in a sha256sum-style file, or "".
func checksumFor(sums, name string) string {
	for line := range strings.SplitSeq(sums, "\n") {
		if f := strings.Fields(line); len(f) == 2 && (f[1] == name || f[1] == "*"+name) {
			return f[0]
		}
	}
	return ""
}
