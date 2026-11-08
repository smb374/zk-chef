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

// runIn builds a temp-dir vault from files (path -> content), points HOME and
// GNUPGHOME at temp dirs, sets the binary version, and runs Run from the vault root.
func runIn(t *testing.T, binVersion string, files map[string]string, args ...string) result {
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
	t.Chdir(root)

	old := version.Version
	version.Version = binVersion
	t.Cleanup(func() { version.Version = old })

	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(""), &stdout, &stderr, []string{"HOME=" + home, "GNUPGHOME=" + gnupg})
	return result{code, stdout.String(), stderr.String()}
}
