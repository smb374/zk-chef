package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smb374/zk-chef/internal/version"
)

// result is what one Run call produced.
type result struct {
	code           int
	stdout, stderr string
}

// setupVault builds a temp-dir vault from files (path -> content), points HOME and
// GNUPGHOME at temp dirs, sets the binary version, and changes into the vault root.
// It returns the env slice for Run.
func setupVault(t *testing.T, binVersion string, files map[string]string) []string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	home, gnupg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GNUPGHOME", gnupg)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	oldAssets := Assets
	Assets = os.DirFS(repo)
	t.Cleanup(func() { Assets = oldAssets })
	t.Chdir(root)

	old := version.Version
	version.Version = binVersion
	t.Cleanup(func() { version.Version = old })
	return []string{"HOME=" + home, "GNUPGHOME=" + gnupg}
}

// run calls Run from the current directory.
func run(env []string, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(""), &stdout, &stderr, env)
	return result{code, stdout.String(), stderr.String()}
}

// runIn builds a temp-dir vault from files and runs Run from the vault root.
func runIn(t *testing.T, binVersion string, files map[string]string, args ...string) result {
	t.Helper()
	return run(setupVault(t, binVersion, files), args...)
}
