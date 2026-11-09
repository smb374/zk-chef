package cli

import (
	"runtime/debug"
	"testing"

	"github.com/smb374/zk-chef/internal/version"
)

func TestRunVersionFlag(t *testing.T) {
	tests := []struct {
		name, binVersion, want string
	}{
		{"ldflags value", "1.2.3", "1.2.3\n"},
		{"no ldflags and no build info", "", "dev\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, tt.binVersion, nil, "--version")
			if got.code != 0 || got.stdout != tt.want {
				t.Errorf("got code %d stdout %q, want 0 %q", got.code, got.stdout, tt.want)
			}
		})
	}
}

func TestVersionFlagBuildInfo(t *testing.T) {
	tests := []struct {
		name, ldflags, buildInfo, want string
	}{
		{"pseudo-version", "", "v0.0.0-20261109011301-560572385a9f", "dev\n"},
		{"pseudo-version with +dirty", "", "v0.0.0-20261109011301-560572385a9f+dirty", "dev\n"},
		{"pseudo-version of a tagged base", "", "v1.2.4-0.20261109011301-560572385a9f", "dev\n"},
		{"pseudo-version of a pre-release base", "", "v1.2.4-rc.1.0.20261109011301-560572385a9f", "dev\n"},
		{"clean tag", "", "v1.2.3", "v1.2.3\n"},
		{"tag with +dirty", "", "v1.2.3+dirty", "dev\n"},
		{"devel", "", "(devel)", "dev\n"},
		{"ldflags beat pseudo-version", "1.0.0", "v0.0.0-20261109011301-560572385a9f", "1.0.0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupVault(t, tt.ldflags, nil)
			old := version.ReadBuildInfo
			version.ReadBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: tt.buildInfo}}, true
			}
			t.Cleanup(func() { version.ReadBuildInfo = old })
			got := run(env, "--version")
			if got.code != 0 || got.stdout != tt.want {
				t.Errorf("got code %d stdout %q, want 0 %q", got.code, got.stdout, tt.want)
			}
		})
	}
}
