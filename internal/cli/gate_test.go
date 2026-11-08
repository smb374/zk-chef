package cli

import (
	"strings"
	"testing"
)

func vaultWith(minVersion string) map[string]string {
	return map[string]string{".zk-chef/config.toml": "word_count_warn = 400\nmin_version = " + minVersion + "\n"}
}

const (
	refused = "run zk-chef self-update"
	passed  = "not implemented"
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
			if got.code != 2 || !strings.Contains(got.stderr, tt.wantStderr) {
				t.Errorf("got code %d stderr %q, want 2 and %q", got.code, got.stderr, tt.wantStderr)
			}
			if (tt.wantStderr == passed) != strings.Contains(got.stderr, "not implemented") {
				t.Errorf("stderr %q: gate outcome differs from want %q", got.stderr, tt.wantStderr)
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
