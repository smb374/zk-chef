package cli

import (
	"strings"
	"testing"
)

func TestRunStubVerbs(t *testing.T) {
	verbs := []string{"review", "status", "key", "ingest", "upgrade", "install", "self-update", "doctor"}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			got := runIn(t, "dev", nil, verb)
			want := "zk-chef " + verb + ": not implemented\n"
			if got.code != 2 || got.stderr != want || got.stdout != "" {
				t.Errorf("got code %d stdout %q stderr %q, want 2 \"\" %q", got.code, got.stdout, got.stderr, want)
			}
		})
	}
}

func TestRunJSONFlag(t *testing.T) {
	got := runIn(t, "dev", nil, "review", "--json")
	want := `{"ok":false,"error":"not implemented"}` + "\n"
	if got.code != 2 || got.stdout != want {
		t.Errorf("got code %d stdout %q, want 2 %q", got.code, got.stdout, want)
	}
}

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
