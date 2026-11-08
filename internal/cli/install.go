package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/smb374/zk-chef/internal/version"
)

// zkReleaseAPI is the GitHub release endpoint for the latest zk. Tests point it at httptest.
var zkReleaseAPI = "https://api.github.com/repos/zk-org/zk/releases/latest"

// hostOS and hostArch name the platform to download zk for. Tests override them.
var hostOS, hostArch = runtime.GOOS, runtime.GOARCH

// ownerMarker is the file that tells install a skill dir was written by zk-chef.
const ownerMarker = ".zk-chef-owned"

func installFlags(fs *flag.FlagSet, inv *invocation) {
	fs.BoolVar(&inv.yes, "y", false, "download zk without asking")
}

// homeDir is HOME from the verb's env, else the process env, else the OS user home.
func homeDir(inv *invocation) (string, error) {
	for i := len(inv.env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(inv.env[i], "HOME="); ok && v != "" {
			return v, nil
		}
	}
	if v := os.Getenv("HOME"); v != "" {
		return v, nil
	}
	return os.UserHomeDir()
}

type skillResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// installVerb writes the embedded skills to ~/.agents/skills and offers to download zk.
func installVerb(inv *invocation) int {
	home, err := homeDir(inv)
	if err != nil {
		return fail(inv, "zk-chef install: no home directory: "+err.Error())
	}
	skills, err := installSkills(home)
	if err != nil {
		return fail(inv, "zk-chef install: "+err.Error())
	}
	if !inv.json {
		for _, s := range skills {
			if s.Status == "installed" {
				fmt.Fprintf(inv.stdout, "installed skill %s\n", s.Name)
			} else {
				fmt.Fprintf(inv.stdout, "kept %s: not written by zk-chef\n", s.Name)
			}
		}
	}
	status, tag, zkErr := ensureZK(inv, home)
	if !onPath(filepath.Join(home, ".local", "bin")) {
		fmt.Fprintln(inv.stderr, "warning: ~/.local/bin is not on PATH")
	}
	zk := zkResult{Status: status}
	if status == "installed" {
		zk.Version = &tag
	}
	if zkErr != nil {
		msg := "zk-chef install: " + zkErr.Error()
		fmt.Fprintln(inv.stderr, msg)
		if inv.json {
			writeJSON(inv.stdout, struct {
				OK     bool          `json:"ok"`
				Error  string        `json:"error"`
				Skills []skillResult `json:"skills"`
				ZK     zkResult      `json:"zk"`
			}{false, msg, skills, zk})
		}
		return 1
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK     bool          `json:"ok"`
			Skills []skillResult `json:"skills"`
			ZK     zkResult      `json:"zk"`
		}{true, skills, zk})
	} else if status == "installed" {
		fmt.Fprintf(inv.stdout, "installed zk %s to %s\n", tag, filepath.Join(home, ".local", "bin", "zk"))
	}
	return 0
}

type zkResult struct {
	Status  string  `json:"status"`
	Version *string `json:"version"`
}

// onPath reports whether dir is an entry of the process PATH.
func onPath(dir string) bool {
	return slices.ContainsFunc(filepath.SplitList(os.Getenv("PATH")), func(e string) bool {
		return e != "" && filepath.Clean(e) == filepath.Clean(dir)
	})
}

// ensureZK reports "present" when zk is already installed, "installed" after a download,
// and "declined" when the download was not accepted.
func ensureZK(inv *invocation, home string) (status, tag string, err error) {
	if _, err := exec.LookPath("zk"); err == nil {
		return "present", "", nil
	}
	if st, err := os.Stat(filepath.Join(home, ".local", "bin", "zk")); err == nil && st.Mode().IsRegular() {
		return "present", "", nil
	}
	if !inv.yes {
		// The prompt goes to stderr so that --json keeps stdout to one object.
		fmt.Fprint(inv.stderr, "zk not found; download to ~/.local/bin? [y/N] ")
		line, _ := bufio.NewReader(inv.stdin).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return "declined", "", nil
		}
	}
	tag, err = downloadZK(home)
	if err != nil {
		return "failed", "", err
	}
	return "installed", tag, nil
}

// pickAsset returns the file name suffix of the zk release asset for a platform.
// The names follow zk's releases: macOS builds say "macos", and its Intel build says x86_64.
func pickAsset(goos, goarch string) (string, error) {
	suffix, ok := map[[2]string]string{
		{"linux", "amd64"}:  "-linux-amd64.tar.gz",
		{"linux", "arm64"}:  "-linux-arm64.tar.gz",
		{"darwin", "arm64"}: "-macos-arm64.tar.gz",
		{"darwin", "amd64"}: "-macos-x86_64.tar.gz",
	}[[2]string{goos, goarch}]
	if !ok {
		return "", fmt.Errorf("no zk release for %s/%s", goos, goarch)
	}
	return suffix, nil
}

type zkAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

// maxDownload caps every zk download read.
const maxDownload = 256 << 20

// httpGet returns the body of a 2xx response from HTTPClient.
func httpGet(url string) ([]byte, error) {
	resp, err := HTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// downloadZK installs the latest zk release to home/.local/bin/zk and records it in state.toml.
func downloadZK(home string) (tag string, err error) {
	suffix, err := pickAsset(hostOS, hostArch)
	if err != nil {
		return "", err
	}
	body, err := httpGet(zkReleaseAPI)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag    string    `json:"tag_name"`
		Assets []zkAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("zk release: %w", err)
	}
	i := slices.IndexFunc(release.Assets, func(a zkAsset) bool { return strings.HasSuffix(a.Name, suffix) })
	if i < 0 {
		return "", fmt.Errorf("zk release %s has no asset ending in %s", release.Tag, suffix)
	}
	asset := release.Assets[i]
	want, _ := strings.CutPrefix(asset.Digest, "sha256:")
	if want == asset.Digest || want == "" {
		return "", fmt.Errorf("no sha256 digest listed for %s; refusing to install zk", asset.Name)
	}
	archive, err := httpGet(asset.URL)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(archive); !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return "", fmt.Errorf("sha256 mismatch for %s", asset.Name)
	}
	binary, err := extractZK(archive)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := writeExecutable(bin, "zk", binary); err != nil {
		return "", err
	}
	if err := writeZKState(home, release.Tag, filepath.Join(bin, "zk")); err != nil {
		return "", err
	}
	return release.Tag, nil
}

// extractZK returns the contents of the file named zk in a .tar.gz. It skips entries whose
// path is not local to the archive root.
func extractZK(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("zk binary not found in archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.IsLocal(hdr.Name) && path.Base(hdr.Name) == "zk" {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// writeExecutable writes dir/name with mode 0755 through a temp file and a rename.
func writeExecutable(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+"-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o755)
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

// writeZKState sets [zk] in home/.config/zk-chef/state.toml and keeps every other key.
// ponytail: decode and re-encode drops comments and sorts keys; upgrade to an editing TOML library if that matters.
func writeZKState(home, tag, zkPath string) error {
	dir := filepath.Join(home, ".config", "zk-chef")
	file := filepath.Join(dir, "state.toml")
	state := map[string]any{}
	b, err := os.ReadFile(file)
	if err == nil {
		if err := toml.Unmarshal(b, &state); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	zk, _ := state["zk"].(map[string]any)
	if zk == nil {
		zk = map[string]any{}
	}
	zk["installed_by_zk_chef"], zk["version"], zk["path"] = true, tag, zkPath
	state["zk"] = zk
	out, err := toml.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, out, 0o644)
}

// installSkills writes every embedded skill dir to home/.agents/skills. A dir that exists
// without the owner marker is kept.
func installSkills(home string) ([]skillResult, error) {
	embedded, err := fs.Sub(Assets, "skills")
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return nil, err
	}
	parent := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	var results []skillResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		target := filepath.Join(parent, e.Name())
		if _, err := os.Stat(target); err == nil {
			if _, err := os.Stat(filepath.Join(target, ownerMarker)); err != nil {
				results = append(results, skillResult{e.Name(), "kept"})
				continue
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := replaceSkill(embedded, e.Name(), parent, target); err != nil {
			return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
		}
		results = append(results, skillResult{e.Name(), "installed"})
	}
	return results, nil
}

// replaceSkill builds the skill in a temp dir beside target, then swaps it in. A failure
// leaves the old copy.
func replaceSkill(embedded fs.FS, name, parent, target string) error {
	tmp, err := os.MkdirTemp(parent, "."+name+"-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src, err := fs.Sub(embedded, name)
	if err != nil {
		return err
	}
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// embed.FS reports every mode as 0444, so keep only the execute bits.
		return os.WriteFile(dst, data, 0o644|info.Mode()&0o111)
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ownerMarker), []byte(version.Current()), 0o644); err != nil {
		return err
	}
	old, err := os.MkdirTemp(parent, "."+name+"-old-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(old)
	if err := os.Rename(target, filepath.Join(old, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(filepath.Join(old, name), target)
		return err
	}
	return nil
}
