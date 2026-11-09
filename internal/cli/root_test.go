package cli

import (
	"strings"
	"testing"
)

func TestVerbHelp(t *testing.T) {
	for _, v := range verbs {
		got := runIn(t, "dev", nil, v.name, "--help")
		if got.code != 0 || !strings.Contains(got.stdout, "zk-chef "+v.name) || !strings.Contains(got.stdout, "--json") {
			t.Errorf("%s --help: code %d stdout %q", v.name, got.code, got.stdout)
		}
		if v.root && !strings.Contains(got.stdout, "--root") {
			t.Errorf("%s --help does not list --root: %q", v.name, got.stdout)
		}
	}
}

// TestRootSelection: --root beats ZK_CHEF_ROOT, which beats the working directory.
func TestRootSelection(t *testing.T) {
	env := setupVault(t, "dev", with_(t, nil))
	empty := t.TempDir()
	got := run(append(env, "ZK_CHEF_ROOT="+empty), "lint")
	if got.code != 2 || !strings.Contains(got.stderr, empty) {
		t.Errorf("env root: code %d stderr %q, want 2 naming %s", got.code, got.stderr, empty)
	}
	if got := run(append(env, "ZK_CHEF_ROOT="+empty), "lint", "--root", "."); got.code != 0 {
		t.Errorf("--root over env: code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	if got := run(env, "lint"); got.code != 0 {
		t.Errorf("cwd root: code %d stdout %q, want 0", got.code, got.stdout)
	}
}

// TestHelpHintNamesOnlyVerbs: an unknown topic gets the root hint, not "help --help".
func TestHelpHintNamesOnlyVerbs(t *testing.T) {
	got := runIn(t, "dev", nil, "help", "bogus")
	if got.code != 2 || !strings.Contains(got.stderr, "run zk-chef --help") || strings.Contains(got.stderr, "help --help") {
		t.Errorf("help bogus: code %d stderr %q, want 2 with the root hint", got.code, got.stderr)
	}
}

// TestYesShortFlag: init, install and upgrade list -y.
func TestYesShortFlag(t *testing.T) {
	for _, name := range []string{"init", "install", "upgrade"} {
		got := runIn(t, "dev", nil, name, "--help")
		if got.code != 0 || !strings.Contains(got.stdout, "-y") {
			t.Errorf("%s --help: code %d stdout %q, want -y listed", name, got.code, got.stdout)
		}
	}
}
