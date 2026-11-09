package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
	"gopkg.in/yaml.v3"
)

// needTools skips the test when a required host tool is missing.
func needTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not found", n)
		}
	}
}

func needSHA256(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sha256sum"); err == nil {
		return
	}
	needTools(t, "shasum")
}

func TestBuildScript(t *testing.T) {
	needTools(t, "sh", "go")
	needSHA256(t)
	out := filepath.Join(t.TempDir(), "dist")
	cmd := exec.Command("sh", ".github/scripts/build.sh", out, "v9.9.9")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, b)
	}

	want := []string{
		"checksums.txt",
		"zk-chef_darwin_amd64", "zk-chef_darwin_arm64",
		"zk-chef_linux_amd64", "zk-chef_linux_arm64",
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("files = %v, want %v", got, want)
	}

	sums, err := os.ReadFile(filepath.Join(out, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 4 {
		t.Fatalf("checksums.txt has %d lines, want 4:\n%s", len(lines), sums)
	}
	for _, name := range want[1:] {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		line := hex.EncodeToString(h[:]) + "  " + name
		found := false
		for _, l := range lines {
			found = found || l == line
		}
		if !found {
			t.Errorf("checksums.txt lacks line %q", line)
		}
	}

	host := "zk-chef_" + runtime.GOOS + "_" + runtime.GOARCH
	if _, err := os.Stat(filepath.Join(out, host)); err != nil {
		t.Skipf("no release asset for host %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ver, err := exec.Command(filepath.Join(out, host), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(ver) != "v9.9.9\n" {
		t.Errorf("--version = %q, want %q", ver, "v9.9.9\n")
	}
}
func TestReleaseWorkflowParses(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.On.Push.Tags) != 1 || wf.On.Push.Tags[0] != "v*" {
		t.Errorf("push tags = %v, want [v*]", wf.On.Push.Tags)
	}
	if wf.Permissions["contents"] != "write" {
		t.Errorf("permissions = %v, want contents: write", wf.Permissions)
	}
	var runs []string
	for _, j := range wf.Jobs {
		for _, s := range j.Steps {
			runs = append(runs, s.Run)
		}
	}
	all := strings.Join(runs, "\n")
	for _, want := range []string{"go vet ./...", "go test ./...", "build.sh", "gh release create"} {
		if !strings.Contains(all, want) {
			t.Errorf("run steps lack %q", want)
		}
	}
}

// runInstall runs install.sh against a fake release server with the given stdin (nil
// means /dev/null). It returns the exit code, stderr, and the bin dir.
func runInstall(t *testing.T, files map[string]string, stdin io.Reader, extraEnv ...string) (int, string, string) {
	t.Helper()
	needTools(t, "sh", "curl")
	needSHA256(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(tmp, "home"),
		"ZK_CHEF_BASE_URL=" + srv.URL,
		"ZK_CHEF_BIN_DIR=" + bin,
	}, extraEnv...)
	cmd.Stdin = stdin
	var stderr strings.Builder
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return code, stderr.String(), bin
}

func TestInstallScript(t *testing.T) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if (goos != "linux" && goos != "darwin") || (goarch != "amd64" && goarch != "arm64") {
		t.Skipf("unsupported host %s/%s", goos, goarch)
	}
	asset := "zk-chef_" + goos + "_" + goarch
	sum := func(body string) string {
		h := sha256.Sum256([]byte(body))
		return hex.EncodeToString(h[:])
	}
	// The fake binary appends its args to the file named by $RECORD, set below per run,
	// and writes its stdin to $RECORD.stdin unless stdin is a terminal (a `go test` child can
	// inherit one through /dev/tty, and reading it would block).
	fake := func(record string) string {
		return "#!/bin/sh\necho \"$*\" >> '" + record + "'\n[ -t 0 ] || cat > '" + record + ".stdin'\nexit 0\n"
	}
	// runCase builds the server files after the record path is known, so the fake can embed it.
	runCase := func(t *testing.T, sums func(body string) string, stdin io.Reader, extraEnv ...string) (int, string, string, string) {
		t.Helper()
		record := filepath.Join(t.TempDir(), "record")
		body := fake(record)
		code, stderr, bin := runInstall(t, map[string]string{
			asset:           body,
			"checksums.txt": sums(body),
		}, stdin, extraEnv...)
		return code, stderr, bin, record
	}
	good := func(body string) string {
		return sum("other") + "  " + asset + ".sig\n" + sum(body) + "  " + asset + "\n"
	}

	t.Run("installs and runs install", func(t *testing.T) {
		code, stderr, bin, record := runCase(t, good, nil)
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		fi, err := os.Stat(filepath.Join(bin, "zk-chef"))
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Fatalf("zk-chef not installed executable: %v", err)
		}
		if entries, _ := os.ReadDir(bin); len(entries) != 1 {
			t.Errorf("bin dir has %d entries, want 1", len(entries))
		}
		if b, _ := os.ReadFile(record); string(b) != "install\n" {
			t.Errorf("recorded args = %q, want %q", b, "install\n")
		}
		if !strings.Contains(stderr, bin) {
			t.Errorf("stderr lacks PATH hint for %s: %q", bin, stderr)
		}
	})
	t.Run("passes ZK_CHEF_INSTALL_ARGS", func(t *testing.T) {
		code, stderr, _, record := runCase(t, good, nil, "ZK_CHEF_INSTALL_ARGS=--a --b")
		if b, _ := os.ReadFile(record); code != 0 || string(b) != "install --a --b\n" {
			t.Errorf("exit %d, recorded %q, stderr %q", code, b, stderr)
		}
	})
	t.Run("reads stdin from the terminal path when stdin is piped", func(t *testing.T) {
		tty := filepath.Join(t.TempDir(), "tty")
		if err := os.WriteFile(tty, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, stderr, _, record := runCase(t, good, strings.NewReader("piped"), "ZK_CHEF_TTY="+tty)
		if b, _ := os.ReadFile(record + ".stdin"); code != 0 || string(b) != "y" {
			t.Errorf("exit %d, recorded stdin %q, want %q, stderr %q", code, b, "y", stderr)
		}
	})
	t.Run("keeps piped stdin when the terminal path is missing", func(t *testing.T) {
		tty := filepath.Join(t.TempDir(), "no-such-tty")
		code, stderr, _, record := runCase(t, good, strings.NewReader("piped"), "ZK_CHEF_TTY="+tty)
		if b, _ := os.ReadFile(record + ".stdin"); code != 0 || string(b) != "piped" {
			t.Errorf("exit %d, recorded stdin %q, want %q, stderr %q", code, b, "piped", stderr)
		}
	})
	t.Run("runs with nil stdin and default terminal path", func(t *testing.T) {
		if code, stderr, _, _ := runCase(t, good, nil); code != 0 {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	refusals := []struct {
		name    string
		sums    func(body string) string
		env     []string
		message string
	}{
		{"bad checksum", func(string) string { return sum("wrong") + "  " + asset + "\n" }, nil, "checksum mismatch for " + asset},
		{"no checksum line", func(string) string { return sum("x") + "  " + asset + ".sig\n" }, nil, "no checksum listed for " + asset},
		{"unsupported arch", good, []string{"PATH=" + fakeUname(t) + ":" + os.Getenv("PATH")}, "unsupported arch"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			code, stderr, bin, record := runCase(t, tt.sums, nil, tt.env...)
			if code == 0 || !strings.Contains(stderr, tt.message) {
				t.Errorf("exit %d, stderr %q, want non-zero with %q", code, stderr, tt.message)
			}
			if _, err := os.Stat(filepath.Join(bin, "zk-chef")); err == nil {
				t.Error("zk-chef was installed")
			}
			if _, err := os.Stat(record); err == nil {
				t.Error("fake binary ran")
			}
		})
	}
}

// fakeUname returns a dir holding a uname that reports riscv64.
func fakeUname(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in -m) echo riscv64;; *) echo Linux;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGoInstallCarriesEmbeddedFiles builds the module zip that the Go proxy would serve
// and checks that the embedded files are in it. It stands in for
// `go install github.com/smb374/zk-chef@latest`, which needs a pushed repo.
func TestGoInstallCarriesEmbeddedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mod.zip")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	mv := module.Version{Path: "github.com/smb374/zk-chef", Version: "v0.0.1"}
	if err := modzip.CreateFromDir(out, mv, "."); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	have := map[string]bool{}
	for _, f := range r.File {
		have[f.Name] = true
	}
	for _, p := range []string{
		"scaffold/.zk-chef/config.toml",
		"scaffold/.githooks/pre-commit",
		"scaffold/.zk/config.toml",
		"skills/zk-chef-ops/SKILL.md",
		"skills/zettel-writing/SKILL.md",
	} {
		if !have[mv.Path+"@"+mv.Version+"/"+p] {
			t.Errorf("module zip lacks %s", p)
		}
	}
}
