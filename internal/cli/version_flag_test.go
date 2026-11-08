package cli

import "testing"

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
