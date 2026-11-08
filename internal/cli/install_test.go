package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pelletier/go-toml/v2"
)

var skillNames = []string{"zettel-writing", "zk-chef-ops"}

// installHome sets up a temp HOME with an empty PATH (so zk is missing) and returns the env and HOME.
func installHome(t *testing.T) (env []string, home string) {
	t.Helper()
	env = setupVault(t, "1.2.3", nil)
	t.Setenv("PATH", t.TempDir())
	return env, strings.TrimPrefix(env[0], "HOME=")
}

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "skills"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallSkills(t *testing.T) {
	// Paths are relative to internal/cli; setupVault changes the directory, so read the repo first.
	want := map[string]string{}
	for _, n := range skillNames {
		want[n] = repoFile(t, n, "SKILL.md")
	}
	env, home := installHome(t)
	skills := filepath.Join(home, ".agents", "skills")

	first := run(env, "install")
	if first.code != 0 {
		t.Fatalf("install exit %d, stderr %q", first.code, first.stderr)
	}
	for _, n := range skillNames {
		if !strings.Contains(first.stdout, "installed skill "+n+"\n") {
			t.Errorf("stdout %q lacks %q", first.stdout, "installed skill "+n)
		}
		if got := tree(t, filepath.Join(skills, n))["SKILL.md"]; got != want[n] {
			t.Errorf("%s/SKILL.md differs from the embedded copy", n)
		}
		if got := tree(t, filepath.Join(skills, n))[".zk-chef-owned"]; got != "1.2.3" {
			t.Errorf("%s marker = %q, want 1.2.3", n, got)
		}
	}
	before := tree(t, skills)
	second := run(env, "install")
	if second.code != 0 || second.stdout != first.stdout {
		t.Errorf("re-run: code %d stdout %q, want 0 %q", second.code, second.stdout, first.stdout)
	}
	if after := tree(t, skills); !equalTrees(before, after) {
		t.Errorf("re-run changed the skills: %v -> %v", keys(before), keys(after))
	}
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInstallKeepsUnmarkedSkill(t *testing.T) {
	env, home := installHome(t)
	own := filepath.Join(home, ".agents", "skills", "zk-chef-ops")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := run(env, "install")
	if got.code != 0 {
		t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "kept zk-chef-ops: not written by zk-chef\n") {
		t.Errorf("stdout %q lacks the kept line", got.stdout)
	}
	if files := tree(t, own); len(files) != 1 || files["SKILL.md"] != "mine" {
		t.Errorf("unmarked skill changed: %v", files)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "zettel-writing", ".zk-chef-owned")); err != nil {
		t.Errorf("other skill not installed: %v", err)
	}
}

func TestInstallReplacesMarkedSkill(t *testing.T) {
	env, home := installHome(t)
	dir := filepath.Join(home, ".agents", "skills", "zk-chef-ops")
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{".zk-chef-owned": "0.9.0", "stale.md": "x", "old/deep.md": "y", "SKILL.md": "old text"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := run(env, "install")
	if got.code != 0 {
		t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
	}
	files := tree(t, dir)
	if _, ok := files["stale.md"]; ok {
		t.Error("stale.md survived the replace")
	}
	if _, ok := files["old/deep.md"]; ok {
		t.Error("old/deep.md survived the replace")
	}
	if files[".zk-chef-owned"] != "1.2.3" || files["SKILL.md"] == "old text" {
		t.Errorf("skill not refreshed: marker %q", files[".zk-chef-owned"])
	}
	parent, err := os.ReadDir(filepath.Dir(dir))
	if err != nil || len(parent) != len(skillNames) {
		t.Errorf("skills dir has leftovers: %v %v", parent, err)
	}
}

func TestInstallKeepsExecBit(t *testing.T) {
	env, home := installHome(t)
	old := Assets
	Assets = fstest.MapFS{
		"skills/tool/run.sh":   {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
		"skills/tool/SKILL.md": {Data: []byte("hi"), Mode: 0o444},
	}
	t.Cleanup(func() { Assets = old })
	if got := run(env, "install"); got.code != 0 {
		t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
	}
	dir := filepath.Join(home, ".agents", "skills", "tool")
	for p, want := range map[string]os.FileMode{"run.sh": 0o755, "SKILL.md": 0o644, ".zk-chef-owned": 0o644} {
		st, err := os.Stat(filepath.Join(dir, p))
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: mode %v err %v, want %v", p, st, err, want)
		}
	}
}

const zkTag = "v0.15.1"

var zkBody = "#!/bin/sh\necho zk\n"

type tarEntry struct{ name, body string }

// zkTarball builds a .tar.gz with the given regular files.
func zkTarball(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeZK serves a release that lists the four supported zk assets, all backed by tarball
// with the given digest, and points zkReleaseAPI, HTTPClient and the host platform at it.
// downloads collects the asset names requested.
type fakeZK struct{ downloads []string }

func serveZK(t *testing.T, tarball []byte, digest string) *fakeZK {
	t.Helper()
	f := &fakeZK{}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		type asset struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		}
		var assets []asset
		for _, n := range []string{"linux-amd64", "linux-arm64", "macos-arm64", "macos-x86_64"} {
			name := "zk-" + zkTag + "-" + n + ".tar.gz"
			assets = append(assets, asset{name, srv.URL + "/dl/" + name, digest})
		}
		writeJSON(w, map[string]any{"tag_name": zkTag, "assets": assets})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.downloads = append(f.downloads, strings.TrimPrefix(r.URL.Path, "/dl/"))
		w.Write(tarball)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldAPI, oldClient, oldOS, oldArch := zkReleaseAPI, HTTPClient, hostOS, hostArch
	zkReleaseAPI, HTTPClient, hostOS, hostArch = srv.URL+"/release", srv.Client(), "linux", "amd64"
	t.Cleanup(func() { zkReleaseAPI, HTTPClient, hostOS, hostArch = oldAPI, oldClient, oldOS, oldArch })
	return f
}

func TestInstallZKDownload(t *testing.T) {
	tarball := zkTarball(t, tarEntry{"zk", zkBody})
	serveZK(t, tarball, sha256Digest(tarball))
	env, home := installHome(t)
	bin := filepath.Join(home, ".local", "bin")

	got := run(env, "install", "-y")
	if got.code != 0 {
		t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
	}
	if want := "installed zk " + zkTag + " to " + filepath.Join(bin, "zk") + "\n"; !strings.Contains(got.stdout, want) {
		t.Errorf("stdout %q lacks %q", got.stdout, want)
	}
	st, err := os.Stat(filepath.Join(bin, "zk"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("zk: %v %v, want mode 0755", st, err)
	}
	if files := tree(t, bin); len(files) != 1 || files["zk"] != zkBody {
		t.Errorf("bin dir holds %v, want only zk with the tarball's content", keys(files))
	}
	var state struct {
		ZK struct {
			Installed bool   `toml:"installed_by_zk_chef"`
			Version   string `toml:"version"`
			Path      string `toml:"path"`
		} `toml:"zk"`
	}
	b, err := os.ReadFile(filepath.Join(home, ".config", "zk-chef", "state.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if !state.ZK.Installed || state.ZK.Version != zkTag || state.ZK.Path != filepath.Join(bin, "zk") {
		t.Errorf("state = %+v", state.ZK)
	}
}

func TestInstallZKRefusesBadDigest(t *testing.T) {
	tarball := zkTarball(t, tarEntry{"zk", zkBody})
	tests := []struct {
		name, digest, wantErr string
	}{
		{"mismatch", "sha256:" + strings.Repeat("0", 64), "sha256 mismatch for zk-" + zkTag + "-linux-amd64.tar.gz"},
		{"missing", "", "no sha256 digest listed for zk-" + zkTag + "-linux-amd64.tar.gz; refusing to install zk"},
		{"other algorithm", "md5:abc", "no sha256 digest listed for zk-" + zkTag + "-linux-amd64.tar.gz; refusing to install zk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serveZK(t, tarball, tt.digest)
			env, home := installHome(t)
			got := run(env, "install", "-y")
			if got.code != 1 || !strings.Contains(got.stderr, tt.wantErr) {
				t.Errorf("got code %d stderr %q, want 1 and %q", got.code, got.stderr, tt.wantErr)
			}
			if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
				t.Errorf(".local exists after a refused download: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
				t.Errorf(".config exists after a refused download: %v", err)
			}
			if !strings.Contains(got.stdout, "installed skill zk-chef-ops") {
				t.Errorf("skills not installed: stdout %q", got.stdout)
			}
		})
	}
}

func TestInstallZKPrompt(t *testing.T) {
	const prompt = "zk not found; download to ~/.local/bin? [y/N]"
	tests := []struct {
		name      string
		args      []string
		stdin     string
		wantAsked bool
		wantZK    bool
	}{
		{"y", nil, "y\n", true, true},
		{"yes", nil, "yes\n", true, true},
		{"n", nil, "n\n", true, false},
		{"empty line", nil, "\n", true, false},
		{"other text", nil, "sure\n", true, false},
		{"EOF", nil, "", true, false},
		{"-y skips the prompt", []string{"-y"}, "n\n", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tarball := zkTarball(t, tarEntry{"zk", zkBody})
			f := serveZK(t, tarball, sha256Digest(tarball))
			env, home := installHome(t)
			got := runStdin(env, tt.stdin, append([]string{"install"}, tt.args...)...)
			if got.code != 0 {
				t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
			}
			if asked := strings.Contains(got.stdout+got.stderr, prompt); asked != tt.wantAsked {
				t.Errorf("prompt shown = %v, want %v (stdout %q stderr %q)", asked, tt.wantAsked, got.stdout, got.stderr)
			}
			_, err := os.Stat(filepath.Join(home, ".local", "bin", "zk"))
			if (err == nil) != tt.wantZK || (len(f.downloads) == 1) != tt.wantZK {
				t.Errorf("zk installed = %v, downloads %v, want installed = %v", err == nil, f.downloads, tt.wantZK)
			}
			if !strings.Contains(got.stdout, "installed skill zk-chef-ops") {
				t.Errorf("skills not installed: stdout %q", got.stdout)
			}
		})
	}
}

func TestInstallZKDownloadFailure(t *testing.T) {
	old := HTTPClient
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	HTTPClient, zkReleaseAPI = srv.Client(), srv.URL+"/release"
	t.Cleanup(func() { HTTPClient = old })
	env, home := installHome(t)
	got := run(env, "install", "-y")
	if got.code != 1 || !strings.Contains(got.stderr, "HTTP 404") {
		t.Errorf("got code %d stderr %q, want 1 and HTTP 404", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "installed skill zk-chef-ops") {
		t.Errorf("skills not installed: stdout %q", got.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Errorf(".local exists after a failed download: %v", err)
	}
}

func TestInstallZKAlreadyPresent(t *testing.T) {
	tests := []struct {
		name  string
		place func(home string) string // returns the dir to add to PATH, or ""
	}{
		{"on PATH", func(home string) string { return t.TempDir() }},
		{"in ~/.local/bin", func(home string) string { return "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := serveZK(t, nil, "")
			env, home := installHome(t)
			dir := filepath.Join(home, ".local", "bin")
			if d := tt.place(home); d != "" {
				dir = d
				t.Setenv("PATH", d)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "zk"), []byte("mine"), 0o755); err != nil {
				t.Fatal(err)
			}
			got := run(env, "install", "-y", "--json")
			if got.code != 0 || !strings.Contains(got.stdout, `"zk":{"status":"present","version":null}`) {
				t.Errorf("got code %d stdout %q stderr %q, want 0 and zk present", got.code, got.stdout, got.stderr)
			}
			if len(f.downloads) != 0 {
				t.Errorf("downloaded %v although zk is present", f.downloads)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "zk")); string(b) != "mine" {
				t.Errorf("zk was replaced: %q", b)
			}
			if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
				t.Errorf("state written although zk was present: %v", err)
			}
		})
	}
}

func TestInstallKeepsOtherStateKeys(t *testing.T) {
	tarball := zkTarball(t, tarEntry{"zk", zkBody})
	serveZK(t, tarball, sha256Digest(tarball))
	env, home := installHome(t)
	state := filepath.Join(home, ".config", "zk-chef", "state.toml")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("theme = \"dark\"\n[zk]\nnote = \"keep\"\nversion = \"old\"\n[other]\nn = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(env, "install", "-y"); got.code != 0 {
		t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
	}
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := toml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	zk, _ := got["zk"].(map[string]any)
	other, _ := got["other"].(map[string]any)
	if got["theme"] != "dark" || zk["note"] != "keep" || zk["version"] != zkTag || zk["installed_by_zk_chef"] != true || other["n"] != int64(3) {
		t.Errorf("state = %v", got)
	}
}

func TestInstallPathWarning(t *testing.T) {
	const warning = "warning: ~/.local/bin is not on PATH\n"
	tests := []struct {
		name     string
		path     func(home string) string
		wantWarn bool
	}{
		{"absent", func(home string) string { return t.TempDir() }, true},
		{"present", func(home string) string { return t.TempDir() + ":" + filepath.Join(home, ".local", "bin") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, home := installHome(t)
			t.Setenv("PATH", tt.path(home))
			got := run(env, "install")
			if got.code != 0 || strings.Contains(got.stderr, warning) != tt.wantWarn {
				t.Errorf("got code %d stderr %q, want warning = %v", got.code, got.stderr, tt.wantWarn)
			}
		})
	}
}

func TestInstallJSON(t *testing.T) {
	const skills = `"skills":[{"name":"zettel-writing","status":"installed"},{"name":"zk-chef-ops","status":"installed"}]`
	tarball := zkTarball(t, tarEntry{"zk", zkBody})
	tests := []struct {
		name  string
		args  []string
		stdin string
		setup func(t *testing.T)
		code  int
		want  string
	}{
		{"installed", []string{"-y"}, "", func(t *testing.T) { serveZK(t, tarball, sha256Digest(tarball)) }, 0,
			`{"ok":true,` + skills + `,"zk":{"status":"installed","version":"` + zkTag + `"}}`},
		{"declined", nil, "n\n", nil, 0,
			`{"ok":true,` + skills + `,"zk":{"status":"declined","version":null}}`},
		{"failed", []string{"-y"}, "", func(t *testing.T) { serveZK(t, tarball, "") }, 1,
			`{"ok":false,"error":"zk-chef install: no sha256 digest listed for zk-` + zkTag + `-linux-amd64.tar.gz; refusing to install zk",` + skills + `,"zk":{"status":"failed","version":null}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}
			env, _ := installHome(t)
			got := runStdin(env, tt.stdin, append([]string{"install", "--json"}, tt.args...)...)
			if got.code != tt.code || got.stdout != tt.want+"\n" {
				t.Errorf("got code %d stdout %q, want %d %q", got.code, got.stdout, tt.code, tt.want)
			}
		})
	}
}

func TestInstallPicksAssetPerPlatform(t *testing.T) {
	tests := []struct {
		goos, goarch string
		wantAsset    string // empty: no release for the platform
	}{
		{"linux", "amd64", "zk-" + zkTag + "-linux-amd64.tar.gz"},
		{"linux", "arm64", "zk-" + zkTag + "-linux-arm64.tar.gz"},
		{"darwin", "arm64", "zk-" + zkTag + "-macos-arm64.tar.gz"},
		{"darwin", "amd64", "zk-" + zkTag + "-macos-x86_64.tar.gz"},
		{"windows", "amd64", ""},
		{"linux", "386", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			tarball := zkTarball(t, tarEntry{"zk", zkBody})
			f := serveZK(t, tarball, sha256Digest(tarball))
			hostOS, hostArch = tt.goos, tt.goarch
			env, home := installHome(t)
			got := run(env, "install", "-y")
			if tt.wantAsset == "" {
				want := "no zk release for " + tt.goos + "/" + tt.goarch
				if got.code != 1 || !strings.Contains(got.stderr, want) || len(f.downloads) != 0 {
					t.Errorf("got code %d stderr %q downloads %v, want 1 and %q", got.code, got.stderr, f.downloads, want)
				}
				if !strings.Contains(got.stdout, "installed skill zk-chef-ops") {
					t.Errorf("skills not installed: stdout %q", got.stdout)
				}
				return
			}
			if got.code != 0 || len(f.downloads) != 1 || f.downloads[0] != tt.wantAsset {
				t.Errorf("got code %d stderr %q downloads %v, want 0 and [%s]", got.code, got.stderr, f.downloads, tt.wantAsset)
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", "zk")); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestInstallZKArchiveEntries(t *testing.T) {
	t.Run("traversal entries are ignored", func(t *testing.T) {
		tarball := zkTarball(t, tarEntry{"../zk", "evil"}, tarEntry{"/abs/zk", "evil"}, tarEntry{"zk-v1/zk", "good"})
		serveZK(t, tarball, sha256Digest(tarball))
		env, home := installHome(t)
		got := run(env, "install", "-y")
		if got.code != 0 {
			t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
		}
		if b, _ := os.ReadFile(filepath.Join(home, ".local", "bin", "zk")); string(b) != "good" {
			t.Errorf("zk = %q, want the local entry", b)
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "zk")); !os.IsNotExist(err) {
			t.Errorf("an entry escaped the bin dir: %v", err)
		}
	})
	t.Run("only a traversal entry", func(t *testing.T) {
		tarball := zkTarball(t, tarEntry{"../zk", "evil"})
		serveZK(t, tarball, sha256Digest(tarball))
		env, home := installHome(t)
		got := run(env, "install", "-y")
		if got.code != 1 || !strings.Contains(got.stderr, "zk binary not found in archive") {
			t.Errorf("got code %d stderr %q, want 1 and not found", got.code, got.stderr)
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "zk")); !os.IsNotExist(err) {
			t.Errorf("an entry escaped the bin dir: %v", err)
		}
	})
}

func TestInstallHome(t *testing.T) {
	t.Run("env HOME wins over the process HOME", func(t *testing.T) {
		_, processHome := installHome(t)
		other := t.TempDir()
		if got := run([]string{"HOME=" + other}, "install"); got.code != 0 {
			t.Fatalf("install exit %d, stderr %q", got.code, got.stderr)
		}
		if _, err := os.Stat(filepath.Join(other, ".agents", "skills", "zk-chef-ops", ownerMarker)); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(filepath.Join(processHome, ".agents")); !os.IsNotExist(err) {
			t.Errorf("process HOME was touched: %v", err)
		}
	})
	t.Run("no home", func(t *testing.T) {
		installHome(t)
		t.Setenv("HOME", "")
		got := run(nil, "install")
		if got.code != 2 || !strings.Contains(got.stderr, "no home directory") {
			t.Errorf("got code %d stderr %q, want 2 and no home directory", got.code, got.stderr)
		}
	})
}
