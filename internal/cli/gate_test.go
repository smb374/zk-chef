package cli

import (
	"os"
	"strings"
	"testing"
)

func vaultWith(minVersion string) map[string]string {
	return map[string]string{".zk-chef/config.toml": "word_count_warn = 400\nmin_version = " + minVersion + "\n"}
}

const (
	refused = "run zk-chef self-update"
	passed  = "" // the gate lets the verb run
)

func TestRunVersionGate(t *testing.T) {
	tests := []struct {
		name       string
		binVersion string
		files      map[string]string
		args       []string
		wantStderr string
	}{
		{"older binary refused", "1.0.0", vaultWith(`"1.2.0"`), []string{"lint"}, refused},
		{"older binary refused for init", "1.0.0", vaultWith(`"1.2.0"`), []string{"init"}, refused},
		{"equal passes", "1.2.0", vaultWith(`"1.2.0"`), []string{"lint"}, passed},
		{"newer passes", "1.3.0", vaultWith(`"1.2.0"`), []string{"lint"}, passed},
		{"compares numbers not text", "1.10.0", vaultWith(`"1.9.0"`), []string{"lint"}, passed},
		{"v prefix on binary", "v1.0.0", vaultWith(`"1.2.0"`), []string{"lint"}, refused},
		{"v prefix on pin", "1.2.0", vaultWith(`"v1.2.0"`), []string{"lint"}, passed},
		{"pre-release is older than its release", "1.2.0-rc.1", vaultWith(`"1.2.0"`), []string{"lint"}, refused},
		{"dev passes", "dev", vaultWith(`"99.0.0"`), []string{"lint"}, passed},
		{"absent key passes", "1.0.0", map[string]string{".zk-chef/config.toml": "word_count_warn = 400\n"}, []string{"lint"}, passed},
		{"no config file passes", "1.0.0", nil, []string{"lint"}, passed},
		{"invalid value refused", "1.0.0", vaultWith(`"banana"`), []string{"lint"}, "min_version"},
		{"non-string value refused", "1.0.0", vaultWith("3"), []string{"lint"}, "min_version"},
		{"invalid value refused for dev", "dev", vaultWith(`"banana"`), []string{"lint"}, "min_version"},
		{"self-update never gated", "1.0.0", vaultWith(`"1.2.0"`), []string{"self-update"}, passed},
		{"doctor never gated", "1.0.0", vaultWith(`"1.2.0"`), []string{"doctor"}, passed},
		{"self-update ignores invalid pin", "1.0.0", vaultWith(`"banana"`), []string{"self-update"}, passed},
		{"doctor ignores invalid pin", "1.0.0", vaultWith(`"banana"`), []string{"doctor"}, passed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, tt.binVersion, tt.files, tt.args...)
			gated := strings.Contains(got.stderr, refused) || strings.Contains(got.stderr, "min_version")
			if tt.wantStderr == passed {
				if gated {
					t.Errorf("stderr %q: gate refused the verb", got.stderr)
				}
				return
			}
			if got.code != 2 || !strings.Contains(got.stderr, tt.wantStderr) {
				t.Errorf("got code %d stderr %q, want 2 and %q", got.code, got.stderr, tt.wantStderr)
			}
		})
	}
}

func TestGateUsesRootFlag(t *testing.T) {
	tests := []struct {
		name, binVersion string
		cwdElsewhere     bool // run from another directory, with --root
		wantRefused      bool
	}{
		{"older binary refused through --root", "1.0.0", true, true},
		{"newer binary passes through --root", "1.3.0", true, false},
		{"older binary refused in cwd without --root", "1.0.0", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupVault(t, tt.binVersion, vaultWith(`"1.2.0"`))
			args := []string{"lint"}
			if tt.cwdElsewhere {
				root, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				t.Chdir(t.TempDir())
				args = append(args, "--root", root)
			}
			got := run(env, args...)
			if tt.wantRefused {
				if got.code != 2 || !strings.Contains(got.stderr, "min_version") {
					t.Errorf("got code %d stderr %q, want 2 and min_version", got.code, got.stderr)
				}
				return
			}
			if strings.Contains(got.stderr, refused) {
				t.Errorf("got code %d stderr %q, want the gate to let the verb run", got.code, got.stderr)
			}
		})
	}
}

// Run puts every token after "--" into the arguments, so a "--root" there must not move the
// vault after the gate ran: the verb is refused, by the gate or as bad usage.
func TestGateRootFlagAfterDoubleDash(t *testing.T) {
	tests := []struct {
		verb string
		args []string
	}{
		{"key", []string{"list"}},
		{"review", nil},
	}
	for _, tt := range tests {
		t.Run(tt.verb, func(t *testing.T) {
			env := setupVault(t, "1.0.0", vaultWith(`"1.2.0"`))
			root, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(t.TempDir())
			args := append([]string{tt.verb, "--"}, tt.args...)
			got := run(env, append(args, "--root", root)...)
			if got.code != 2 || got.stdout != "" {
				t.Errorf("got code %d stdout %q stderr %q, want 2 and no stdout", got.code, got.stdout, got.stderr)
			}
		})
	}
}

func TestRunVersionFlagIsNeverGated(t *testing.T) {
	got := runIn(t, "1.0.0", vaultWith(`"banana"`), "--version")
	if got.code != 0 || got.stdout != "1.0.0\n" {
		t.Errorf("got code %d stdout %q, want 0 \"1.0.0\\n\"", got.code, got.stdout)
	}
}
