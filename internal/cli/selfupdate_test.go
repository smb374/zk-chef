package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	selfAsset  = "zk-chef_linux_amd64"
	oldBinary  = "old binary"
	zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"
)

// release describes what the fake release server serves.
type release struct {
	asset     string // body of the asset
	checksums string // body of checksums.txt
	truncate  bool   // send a Content-Length larger than the asset body, then cut the connection
}

func hexSum(b string) string {
	sum := sha256.Sum256([]byte(b))
	return hex.EncodeToString(sum[:])
}

// serveRelease serves rel, points selfUpdateBase, HTTPClient and the host platform at it, and
// makes self-update target a fresh old binary (mode 0750). It returns the binary path and a
// counter of asset requests.
func serveRelease(t *testing.T, rel release) (target string, assetHits *int) {
	t.Helper()
	assetHits = new(int)
	mux := http.NewServeMux()
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rel.checksums))
	})
	mux.HandleFunc("/"+selfAsset, func(w http.ResponseWriter, r *http.Request) {
		*assetHits++
		if rel.truncate {
			w.Header().Set("Content-Length", "100000")
			w.Write([]byte(rel.asset))
			w.(http.Flusher).Flush() // send the headers and the partial body before the cut
			panic(http.ErrAbortHandler)
		}
		w.Write([]byte(rel.asset))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	target = filepath.Join(t.TempDir(), "zk-chef")
	if err := os.WriteFile(target, []byte(oldBinary), 0o750); err != nil {
		t.Fatal(err)
	}
	oldBase, oldExe, oldClient, oldOS, oldArch := selfUpdateBase, selfExecutable, HTTPClient, hostOS, hostArch
	selfUpdateBase, selfExecutable, HTTPClient, hostOS, hostArch = srv.URL, func() (string, error) { return target, nil }, srv.Client(), "linux", "amd64"
	t.Cleanup(func() {
		selfUpdateBase, selfExecutable, HTTPClient, hostOS, hostArch = oldBase, oldExe, oldClient, oldOS, oldArch
	})
	return target, assetHits
}

// assertOldBinary fails unless target still holds the old bytes and mode and is alone in its dir.
func assertOldBinary(t *testing.T, target string) {
	t.Helper()
	b, err := os.ReadFile(target)
	st, serr := os.Stat(target)
	if err != nil || serr != nil || string(b) != oldBinary || st.Mode().Perm() != 0o750 {
		t.Errorf("old binary changed: %q %v %v", b, st, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(target)); err != nil || len(entries) != 1 {
		t.Errorf("dir holds %v (%v), want only the binary", entries, err)
	}
}

func TestSelfUpdateBadChecksum(t *testing.T) {
	env := setupVault(t, "1.0.0", nil)
	target, _ := serveRelease(t, release{asset: "new binary", checksums: zeroDigest + "  " + selfAsset + "\n"})
	got := run(env, "self-update")
	if got.code == 0 || !strings.Contains(got.stderr, "checksum mismatch for "+selfAsset) {
		t.Errorf("got code %d stderr %q, want nonzero and a checksum mismatch", got.code, got.stderr)
	}
	assertOldBinary(t, target)
}

func TestSelfUpdateInterrupted(t *testing.T) {
	env := setupVault(t, "1.0.0", nil)
	target, assetHits := serveRelease(t, release{asset: "new binary", checksums: hexSum("new binary") + "  " + selfAsset + "\n", truncate: true})
	got := run(env, "self-update")
	if *assetHits != 1 {
		t.Fatalf("asset requested %d times, want 1 (the cut download)", *assetHits)
	}
	if got.code == 0 || !strings.Contains(got.stderr, "unexpected EOF") {
		t.Errorf("got code %d stderr %q, want nonzero and unexpected EOF", got.code, got.stderr)
	}
	assertOldBinary(t, target)
}

// newBinary is a shell script that stands in for the release binary and exits 0.
const newBinary = "#!/bin/sh\nexit 0\n"

func TestSelfUpdateReplacesBinary(t *testing.T) {
	env := setupVault(t, "1.0.0", nil)
	target, _ := serveRelease(t, release{asset: newBinary, checksums: hexSum(newBinary) + "  " + selfAsset + "\n"})
	got := run(env, "self-update")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	b, err := os.ReadFile(target)
	st, serr := os.Stat(target)
	if err != nil || serr != nil || string(b) != newBinary || st.Mode().Perm() != 0o755 {
		t.Errorf("binary = %q %v %v, want the new bytes with mode 0755", b, st, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(target)); err != nil || len(entries) != 1 {
		t.Errorf("dir holds %v (%v), want only the binary", entries, err)
	}
}

func TestSelfUpdateMissingChecksumEntry(t *testing.T) {
	env := setupVault(t, "1.0.0", nil)
	target, assetHits := serveRelease(t, release{asset: newBinary, checksums: hexSum(newBinary) + "  zk-chef_darwin_arm64\n"})
	got := run(env, "self-update")
	if got.code == 0 || !strings.Contains(got.stderr, "no checksum listed for "+selfAsset) {
		t.Errorf("got code %d stderr %q, want nonzero and a missing checksum", got.code, got.stderr)
	}
	if *assetHits != 0 {
		t.Errorf("asset downloaded %d times, want 0", *assetHits)
	}
	assertOldBinary(t, target)
}

func TestSelfUpdateWritesSkills(t *testing.T) {
	env := setupVault(t, "1.0.0", nil)
	record := filepath.Join(t.TempDir(), "record")
	script := "#!/bin/sh\necho \"$@\" >" + record + "\necho \"$HOME\" >>" + record + "\n"
	serveRelease(t, release{asset: script, checksums: hexSum(script) + "  " + selfAsset + "\n"})
	got := run(env, "self-update")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	b, err := os.ReadFile(record)
	if want := "install --skills-only\n" + strings.TrimPrefix(env[0], "HOME=") + "\n"; err != nil || string(b) != want {
		t.Errorf("new binary was run as %q (%v), want %q", b, err, want)
	}
}

// The new binary runs `install`, which the version gate checks against the cwd's vault.
// A vault with a bad pin must not stop the skills from being written.
func TestSelfUpdateRunsNewBinaryInHome(t *testing.T) {
	env := setupVault(t, "1.0.0", vaultWith(`"banana"`))
	record := filepath.Join(t.TempDir(), "record")
	script := "#!/bin/sh\npwd -P >" + record + "\n"
	serveRelease(t, release{asset: script, checksums: hexSum(script) + "  " + selfAsset + "\n"})
	got := run(env, "self-update")
	if got.code != 0 {
		t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
	}
	home, err := filepath.EvalSymlinks(strings.TrimPrefix(env[0], "HOME="))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if want := home + "\n"; err != nil || string(b) != want {
		t.Errorf("new binary ran in %q (%v), want %q", b, err, want)
	}
}

func TestInstallSkillsOnly(t *testing.T) {
	tarball := zkTarball(t, tarEntry{"zk", zkBody})
	zk := serveZK(t, tarball, sha256Digest(tarball))
	env, home := installHome(t)
	got := runStdin(env, "y\n", "install", "--skills-only")
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "installed skill zk-chef-ops") {
		t.Errorf("got code %d stdout %q stderr %q, want skills written and nothing on stderr", got.code, got.stdout, got.stderr)
	}
	if len(zk.downloads) != 0 {
		t.Errorf("zk downloads %v, want none", zk.downloads)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Errorf(".local exists after a skills-only install: %v", err)
	}
}

func TestSelfUpdateZKOnlyWhenInstalledByUs(t *testing.T) {
	const plainSkipped = "updated zk-chef at %s\n"
	tests := []struct {
		name    string
		state   string   // state.toml content; "" writes no file
		args    []string // after self-update
		want    bool     // zk is downloaded
		wantOut string   // stdout; %s is the binary path
	}{
		{"installed by zk-chef", "[zk]\ninstalled_by_zk_chef = true\nversion = \"v0.1.0\"\n", nil, true, plainSkipped + "updated zk " + zkTag + "\n"},
		{"installed by zk-chef, json", "[zk]\ninstalled_by_zk_chef = true\n", []string{"--json"}, true, `{"ok":true,"path":"%s","zk":{"status":"updated","version":"` + zkTag + `"}}` + "\n"},
		{"flag false", "[zk]\ninstalled_by_zk_chef = false\n", nil, false, plainSkipped},
		{"no zk table", "theme = \"dark\"\n", nil, false, plainSkipped},
		{"no state file", "", nil, false, plainSkipped},
		{"no state file, json", "", []string{"--json"}, false, `{"ok":true,"path":"%s","zk":{"status":"skipped","version":null}}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tarball := zkTarball(t, tarEntry{"zk", zkBody})
			zk := serveZK(t, tarball, sha256Digest(tarball))
			env := setupVault(t, "1.0.0", nil)
			home := strings.TrimPrefix(env[0], "HOME=")
			if tt.state != "" {
				dir := filepath.Join(home, ".config", "zk-chef")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "state.toml"), []byte(tt.state), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			target, _ := serveRelease(t, release{asset: newBinary, checksums: hexSum(newBinary) + "  " + selfAsset + "\n"})
			got := run(env, append([]string{"self-update"}, tt.args...)...)
			if want := strings.ReplaceAll(tt.wantOut, "%s", target); got.code != 0 || got.stdout != want {
				t.Fatalf("got code %d stdout %q stderr %q, want 0 and %q", got.code, got.stdout, got.stderr, want)
			}
			_, err := os.Stat(filepath.Join(home, ".local", "bin", "zk"))
			if (err == nil) != tt.want || (len(zk.downloads) == 1) != tt.want {
				t.Errorf("zk installed = %v, downloads %v, want installed = %v", err == nil, zk.downloads, tt.want)
			}
		})
	}
}
