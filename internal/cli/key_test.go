package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// keyVault builds the base vault and points GNUPGHOME at the signer's home, so key add finds its key.
func keyVault(t *testing.T, s signer, changes map[string]string) []string {
	t.Helper()
	env := setupVault(t, "dev", with_(t, changes))
	t.Setenv("GNUPGHOME", s.home)
	return []string{env[0], "GNUPGHOME=" + s.home}
}

// pinnedIn returns pinned_fingerprints from the vault config in the current directory.
func pinnedIn(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(".zk-chef/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Pinned []string `toml:"pinned_fingerprints"`
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Pinned
}

func TestKeyAddExplicitFingerprint(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	got := run(env, "key", "add", s.fpr)
	if got.code != 0 {
		t.Fatalf("code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	file := "keys/" + strings.ToUpper(s.fpr) + ".asc"
	b, err := os.ReadFile(file)
	if err != nil || !strings.HasPrefix(string(b), "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Errorf("%s: %q (%v), want an armored public key", file, b, err)
	}
	if pins := pinnedIn(t); len(pins) != 1 || pins[0] != strings.ToUpper(s.fpr) {
		t.Errorf("pinned %v, want [%s]", pins, strings.ToUpper(s.fpr))
	}
}
func TestKeyAddDefaultKey(t *testing.T) {
	s := newSigner(t)
	got := run(keyVault(t, s, nil), "key", "add")
	if got.code != 0 {
		t.Fatalf("code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	if pins := pinnedIn(t); len(pins) != 1 || pins[0] != strings.ToUpper(s.fpr) {
		t.Errorf("pinned %v, want [%s]", pins, strings.ToUpper(s.fpr))
	}
}

func TestKeyAddNoSecretKey(t *testing.T) {
	requireGPG(t)
	env := setupVault(t, "dev", with_(t, nil)) // GNUPGHOME is an empty temp dir
	got := run(env, "key", "add")
	if got.code != 2 || !strings.Contains(got.stderr, "no secret key; pass a fingerprint") {
		t.Errorf("code %d stderr %q, want 2 and \"no secret key; pass a fingerprint\"", got.code, got.stderr)
	}
}
func TestKeyAddTwiceChangesNothing(t *testing.T) {
	s := newSigner(t)
	other := strings.Repeat("0", 40)
	cfg := strings.Replace(baseVault(t)[".zk-chef/config.toml"], "pinned_fingerprints = []", `pinned_fingerprints = ["`+other+`"]`, 1)
	env := keyVault(t, s, map[string]string{".zk-chef/config.toml": cfg})
	want := []string{other, strings.ToUpper(s.fpr)}
	var cfgAfter, keyAfter []string
	for range 2 {
		if got := run(env, "key", "add", s.fpr); got.code != 0 {
			t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
		}
		c, _ := os.ReadFile(".zk-chef/config.toml")
		k, _ := os.ReadFile("keys/" + strings.ToUpper(s.fpr) + ".asc")
		cfgAfter, keyAfter = append(cfgAfter, string(c)), append(keyAfter, string(k))
	}
	if cfgAfter[0] != cfgAfter[1] || keyAfter[0] != keyAfter[1] {
		t.Errorf("second add changed the files:\n%s\n---\n%s", cfgAfter[0], cfgAfter[1])
	}
	if pins := pinnedIn(t); !slices.Equal(pins, want) {
		t.Errorf("pinned %v, want %v", pins, want)
	}
}
func TestKeyAddUnknownFingerprintWritesNothing(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	before, _ := os.ReadFile(".zk-chef/config.toml")
	got := run(env, "key", "add", strings.Repeat("A", 40))
	if got.code != 2 || got.stderr == "" {
		t.Errorf("code %d stderr %q, want 2 and a message", got.code, got.stderr)
	}
	after, _ := os.ReadFile(".zk-chef/config.toml")
	if string(before) != string(after) {
		t.Errorf("config changed:\n%s", after)
	}
	if _, err := os.Stat("keys"); err == nil {
		entries, _ := os.ReadDir("keys")
		if len(entries) != 0 {
			t.Errorf("keys/ holds %v, want it empty", entries)
		}
	}
}

func TestKeyAddKeepsOtherConfigKeys(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	if got := run(env, "key", "add", s.fpr); got.code != 0 {
		t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
	}
	b, _ := os.ReadFile(".zk-chef/config.toml")
	var cfg struct {
		WordCountWarn int    `toml:"word_count_warn"`
		IndexID       string `toml:"index_id"`
		ProjectsID    string `toml:"projects_id"`
		SourcesID     string `toml:"sources_id"`
	}
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	want := struct {
		n                int
		idx, prj, source string
	}{400, "000000000000idx0", "000000000000prj0", "000000000000src0"}
	if cfg.WordCountWarn != want.n || cfg.IndexID != want.idx || cfg.ProjectsID != want.prj || cfg.SourcesID != want.source {
		t.Errorf("config %+v after add, want the scaffold values %+v", cfg, want)
	}
}
func TestKeyRmKeepsFileAndUnpins(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	if got := run(env, "key", "add", s.fpr); got.code != 0 {
		t.Fatalf("add: code %d stderr %q", got.code, got.stderr)
	}
	lower := strings.ToLower(s.fpr) // rm compares normalized
	if got := run(env, "key", "rm", lower); got.code != 0 {
		t.Fatalf("rm: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if pins := pinnedIn(t); len(pins) != 0 {
		t.Errorf("pinned %v, want none", pins)
	}
	if _, err := os.Stat("keys/" + strings.ToUpper(s.fpr) + ".asc"); err != nil {
		t.Errorf("key file removed: %v", err)
	}
}

func TestKeyRmUnknownFails(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	got := run(env, "key", "rm", s.fpr)
	if want := "not pinned: " + strings.ToUpper(s.fpr); got.code != 2 || !strings.Contains(got.stderr, want) {
		t.Errorf("code %d stderr %q, want 2 and %q", got.code, got.stderr, want)
	}
}

// listVault pins a key with no file, adds the signer's key, and drops a stray unpinned file in keys/.
func listVault(t *testing.T, s signer) (env []string, zeros string) {
	t.Helper()
	zeros = strings.Repeat("0", 40)
	cfg := strings.Replace(baseVault(t)[".zk-chef/config.toml"], "pinned_fingerprints = []", `pinned_fingerprints = ["`+zeros+`"]`, 1)
	env = keyVault(t, s, map[string]string{".zk-chef/config.toml": cfg, "keys/stray.asc": "not a key\n"})
	if got := run(env, "key", "add", s.fpr); got.code != 0 {
		t.Fatalf("add: code %d stderr %q", got.code, got.stderr)
	}
	return env, zeros
}

func TestKeyListMarksMismatches(t *testing.T) {
	s := newSigner(t)
	env, zeros := listVault(t, s)
	fpr := strings.ToUpper(s.fpr)
	got := run(env, "key", "list")
	want := "pinned  " + zeros + "  (no file)\npinned  " + fpr + "  keys/" + fpr + ".asc\nunpinned file keys/stray.asc\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q, want 0 %q", got.code, got.stdout, want)
	}
}
func TestKeyAddJSON(t *testing.T) {
	s := newSigner(t)
	fpr := strings.ToUpper(s.fpr)
	got := run(keyVault(t, s, nil), "key", "add", s.fpr, "--json")
	want := `{"ok":true,"fingerprint":"` + fpr + `","file":"keys/` + fpr + ".asc\"}\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, want)
	}
}

func TestKeyListJSON(t *testing.T) {
	s := newSigner(t)
	env, zeros := listVault(t, s)
	fpr := strings.ToUpper(s.fpr)
	got := run(env, "key", "list", "--json")
	want := `{"ok":true,"pinned":[{"fingerprint":"` + zeros + `","file":null},{"fingerprint":"` + fpr + `","file":"keys/` + fpr + `.asc"}],"unpinned_files":["keys/stray.asc"]}` + "\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, want)
	}
}

func TestKeyRmJSON(t *testing.T) {
	s := newSigner(t)
	env, zeros := listVault(t, s)
	got := run(env, "key", "rm", zeros, "--json")
	want := `{"ok":true,"fingerprint":"` + zeros + "\"}\n"
	if got.code != 0 || got.stdout != want {
		t.Errorf("code %d stdout %q stderr %q, want 0 %q", got.code, got.stdout, got.stderr, want)
	}
	got = run(env, "key", "rm", zeros, "--json")
	want = `{"ok":false,"error":"not pinned: ` + zeros + "\"}\n"
	if got.code != 2 || got.stdout != want {
		t.Errorf("rm again: code %d stdout %q, want 2 %q", got.code, got.stdout, want)
	}
}
func TestKeyAddMakesSignedSourceLintClean(t *testing.T) {
	s := newSigner(t)
	good := s.review(t, "raw/s1-a.bin", rawSHA, "reviewed")
	env := keyVault(t, s, map[string]string{"sources/s1.md": source("reviewed", rawSHA, good)})
	if !codesOf(t, run(env, "lint", "--json"))["E_SIGNATURE"] {
		t.Fatal("lint before key add has no E_SIGNATURE, want one")
	}
	if got := run(env, "key", "add"); got.code != 0 {
		t.Fatalf("add: code %d stderr %q", got.code, got.stderr)
	}
	if got := run(env, "lint", "--json"); got.code != 0 || len(codesOf(t, got)) != 0 {
		t.Errorf("lint after key add: code %d stdout %q, want a clean lint", got.code, got.stdout)
	}
}

func TestKeyMissingConfig(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	if err := os.Remove(".zk-chef/config.toml"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range [][]string{{"add", s.fpr}, {"list"}, {"rm", s.fpr}} {
		got := run(env, append([]string{"key"}, sub...)...)
		lintGot := run(env, "lint")
		if got.code != 2 || got.stderr != lintGot.stderr {
			t.Errorf("key %v: code %d stderr %q, want 2 and lint's %q", sub, got.code, got.stderr, lintGot.stderr)
		}
	}
}

func TestKeyRootFlag(t *testing.T) {
	s := newSigner(t)
	env := keyVault(t, s, nil)
	root, _ := os.Getwd()
	t.Chdir(t.TempDir())
	if got := run(env, "key", "add", s.fpr, "--root", root); got.code != 0 {
		t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
	}
	if got := run(env, "key", "list", "--root", root); !strings.Contains(got.stdout, strings.ToUpper(s.fpr)) {
		t.Errorf("list stdout %q, want the added fingerprint", got.stdout)
	}
}
