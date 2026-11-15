package cli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExportHugoStyleWritesMatchingLayouts(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	plainOut, tufteOut := t.TempDir(), t.TempDir()
	if got := run(env, "export", "-g", "hugo", "-s", "plain", plainOut); got.code != 0 {
		t.Fatalf("plain: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if got := run(env, "export", "-g", "hugo", "--style", "tufte", tufteOut); got.code != 0 {
		t.Fatalf("tufte: code %d stderr %q, want 0", got.code, got.stderr)
	}
	plain, tufte := readTree(t, plainOut), readTree(t, tufteOut)
	if base := plain["layouts/baseof.html"]; strings.Contains(base, "tufte") || !strings.Contains(base, "this site needs Hugo 0.169.0 or newer") {
		t.Errorf("plain layouts/baseof.html = %q, want the floor check and no Tufte", base)
	}
	if single := plain["layouts/single.html"]; !strings.Contains(single, ".Params.review") || strings.Contains(single, "status-") {
		t.Errorf("plain layouts/single.html = %q, want an unstyled review line", single)
	}
	if base := tufte["layouts/baseof.html"]; !strings.Contains(base, "vendored/tufte/tufte.css") {
		t.Errorf("tufte layouts/baseof.html = %q, want the Tufte stylesheet", base)
	}
	for _, p := range hugoVendoredFiles {
		if _, ok := plain[p]; ok {
			t.Errorf("plain export wrote %s, want no vendored files", p)
		}
		if _, ok := tufte[p]; !ok {
			t.Errorf("tufte export lacks %s", p)
		}
	}
}
func TestExportHugoStyleStateFileIsReused(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	out := t.TempDir()
	if got := run(env, "export", "-g", "hugo", "-s", "tufte", out); got.code != 0 {
		t.Fatalf("tufte: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if got := readTree(t, out)[siteStateFile]; got != "{\"style\": \"tufte\"}\n" {
		t.Errorf(".zk-chef-site.json = %q, want the tufte style", got)
	}
	if got := run(env, "export", "-g", "hugo", out); got.code != 0 || strings.Contains(got.stdout, "kept") {
		t.Fatalf("no style: code %d stdout %q stderr %q, want 0 and nothing kept", got.code, got.stdout, got.stderr)
	}
	if tree := readTree(t, out); !strings.Contains(tree["layouts/baseof.html"], "tufte.css") || tree[siteStateFile] != "{\"style\": \"tufte\"}\n" {
		t.Errorf("tree %v, want the state file and the layouts to stay tufte", slices.Collect(maps.Keys(tree)))
	}
	if got := run(env, "export", "-g", "hugo", "-s", "plain", out); got.code != 0 {
		t.Fatalf("plain: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if got := run(env, "export", "-g", "hugo", out); got.code != 0 {
		t.Fatalf("no style after plain: code %d stderr %q, want 0", got.code, got.stderr)
	}
	tree := readTree(t, out)
	if tree[siteStateFile] != "{\"style\": \"plain\"}\n" || strings.Contains(tree["layouts/baseof.html"], "tufte.css") {
		t.Errorf("state %q baseof %q, want plain", tree[".zk-chef-site.json"], tree["layouts/baseof.html"])
	}
	for _, p := range hugoVendoredFiles {
		if _, ok := tree[p]; ok {
			t.Errorf("plain site has %s, want static/vendored/tufte/ deleted", p)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "static", "vendored", "tufte")); !os.IsNotExist(err) {
		t.Errorf("static/vendored/tufte: %v, want it deleted", err)
	}
}

func TestExportHugoStyleFallsBackWithoutStateFile(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	old, fresh := t.TempDir(), t.TempDir()
	writeFiles(t, old, map[string]string{"static/vendored/tufte/tufte.css": "old css\n"})
	for out, want := range map[string]string{old: "tufte", fresh: "plain"} {
		if got := run(env, "export", "-g", "hugo", out); got.code != 0 {
			t.Fatalf("%s: code %d stderr %q, want 0", want, got.code, got.stderr)
		}
		tree := readTree(t, out)
		if tree[siteStateFile] != "{\"style\": \""+want+"\"}\n" || strings.Contains(tree["layouts/baseof.html"], "tufte.css") != (want == "tufte") {
			t.Errorf("want %s: state %q baseof %q", want, tree[".zk-chef-site.json"], tree["layouts/baseof.html"])
		}
	}
}

func TestExportHugoStyleSwitchKeepsEditedLayout(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	out := t.TempDir()
	if got := run(env, "export", "-g", "hugo", "-s", "tufte", out); got.code != 0 {
		t.Fatalf("tufte: code %d stderr %q, want 0", got.code, got.stderr)
	}
	writeFiles(t, out, map[string]string{"layouts/single.html": "my single\n"})
	got := run(env, "export", "-g", "hugo", "-s", "plain", out)
	for _, want := range []string{"kept your layouts/single.html\n", "--- a/layouts/single.html\n", "-my single\n", "updated layouts/baseof.html\n"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want it to contain %q", got.stdout, want)
		}
	}
	tree := readTree(t, out)
	if tree["layouts/single.html"] != "my single\n" || strings.Contains(tree["layouts/baseof.html"], "tufte.css") {
		t.Errorf("single %q baseof %q, want the edit kept and the unedited layout replaced", tree["layouts/single.html"], tree["layouts/baseof.html"])
	}
	// The edit is not a shipped version, so a switch back keeps it too, and --json lists it in kept.
	asJSON := run(env, "export", "--json", "-g", "hugo", "-s", "tufte", out)
	var r hugoReport
	if err := json.Unmarshal([]byte(asJSON.stdout), &r); err != nil || asJSON.code != 0 || !slices.Equal(r.Kept, []string{"layouts/single.html"}) {
		t.Errorf("code %d stdout %q (%v), want kept [layouts/single.html]", asJSON.code, asJSON.stdout, err)
	}
	if strings.Contains(asJSON.stdout, "my single") {
		t.Errorf("--json stdout %q, want the path only, no diff", asJSON.stdout)
	}
	if tree := readTree(t, out); !strings.Contains(tree["layouts/baseof.html"], "tufte.css") {
		t.Errorf("baseof %q, want it replaced with the tufte layout", tree["layouts/baseof.html"])
	}
}

func TestExportHugoStyleRefusalsWriteNothing(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	for name, c := range map[string]struct {
		args  []string
		state string // content of the state file in OUT, if any
	}{
		"style without hugo":   {args: []string{"export", "-s", "plain"}},
		"unknown style":        {args: []string{"export", "-g", "hugo", "-s", "fancy"}},
		"corrupt state file":   {args: []string{"export", "-g", "hugo"}, state: "{not json"},
		"unknown state style":  {args: []string{"export", "-g", "hugo"}, state: "{\"style\": \"fancy\"}\n"},
		"corrupt, style given": {args: []string{"export", "-g", "hugo", "-s", "plain"}, state: "{not json"},
	} {
		out := t.TempDir()
		if c.state != "" {
			writeFiles(t, out, map[string]string{siteStateFile: c.state})
		}
		before := readTree(t, out)
		got := run(env, append(c.args, out)...)
		if got.code != 2 || got.stdout != "" || got.stderr == "" {
			t.Errorf("%s: code %d stdout %q stderr %q, want 2, no stdout and an error", name, got.code, got.stdout, got.stderr)
		}
		if after := readTree(t, out); !maps.Equal(before, after) {
			t.Errorf("%s: OUT changed from %v to %v, want nothing written", name, before, after)
		}
	}
}
