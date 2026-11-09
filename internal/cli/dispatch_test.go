package cli

import (
	"strings"
	"testing"
)

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no args", nil},
		{"unknown verb", []string{"frobnicate"}},
		{"unknown flag", []string{"lint", "--bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runIn(t, "dev", nil, tt.args...)
			if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "usage: zk-chef") {
				t.Errorf("got code %d stdout %q stderr %q, want 2, empty stdout, usage on stderr", got.code, got.stdout, got.stderr)
			}
		})
	}
}
