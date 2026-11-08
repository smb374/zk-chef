package cli

import (
	"cmp"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// doctorReport is the doctor --json report.
type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

const doctorFPR = "ABCDEF0123456789ABCDEF0123456789ABCDEF01"

// doctorHome is the HOME of an env built by healthyDoctor.
func doctorHome(env []string) string { return strings.TrimPrefix(env[0], "HOME=") }

// healthyDoctor builds a vault, HOME and PATH where every doctor check passes: fake zk and gpg
// and the real git in bin, one pinned key with its file, hooksPath set, the skills installed by
// the binary and ~/.local/bin on PATH. extraConfig is appended to the vault config last, so a
// min_version above binVersion does not gate the install step.
func healthyDoctor(t *testing.T, binVersion, extraConfig string) (env []string, bin string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(baseVault(t)[".zk-chef/config.toml"], "pinned_fingerprints = []", `pinned_fingerprints = ["`+doctorFPR+`"]`, 1)
	env = setupVault(t, binVersion, map[string]string{".zk-chef/config.toml": cfg, "keys/" + doctorFPR + ".asc": "key\n"})
	bin = t.TempDir()
	if err := os.Symlink(realGit, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zk", "gpg"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+filepath.Join(doctorHome(env), ".local", "bin"))
	for _, args := range [][]string{{"init", "-q", "."}, {"config", "core.hooksPath", ".githooks"}} {
		if out, err := exec.Command(realGit, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got := run(env, "install"); got.code != 0 {
		t.Fatalf("install: code %d stderr %q", got.code, got.stderr)
	}
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(extraConfig); err != nil {
		t.Fatal(err)
	}
	return env, bin
}

// doctorJSON runs doctor --json and decodes the report.
func doctorJSON(t *testing.T, env []string, args ...string) (int, doctorReport) {
	t.Helper()
	got := run(env, append([]string{"doctor", "--json"}, args...)...)
	var rep doctorReport
	if err := json.Unmarshal([]byte(got.stdout), &rep); err != nil {
		t.Fatalf("doctor --json: code %d stdout %q stderr %q: %v", got.code, got.stdout, got.stderr, err)
	}
	return got.code, rep
}

// checkNamed returns the report entry called name.
func checkNamed(t *testing.T, rep doctorReport, name string) doctorCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, rep.Checks)
	return doctorCheck{}
}

// doctorCase is one doctor scenario: break the healthy setup, then expect one check to pass or fail.
type doctorCase struct {
	name        string
	binVersion  string // default 1.2.3
	extraConfig string // appended to the vault config
	breakIt     func(t *testing.T, env []string, bin string)
	wantOK      bool
	wantDetail  string // substring of the detail
	wantFix     string // exact fix when the check fails
}

func runDoctorCases(t *testing.T, check string, cases []doctorCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			binVersion := cmp.Or(tt.binVersion, "1.2.3")
			env, bin := healthyDoctor(t, binVersion, tt.extraConfig)
			if tt.breakIt != nil {
				tt.breakIt(t, env, bin)
			}
			code, rep := doctorJSON(t, env)
			c := checkNamed(t, rep, check)
			if c.OK != tt.wantOK || !strings.Contains(c.Detail, tt.wantDetail) {
				t.Errorf("%s = %+v, want ok %v and detail containing %q", check, c, tt.wantOK, tt.wantDetail)
			}
			if tt.wantOK && c.Fix != nil || !tt.wantOK && (c.Fix == nil || *c.Fix != tt.wantFix) {
				t.Errorf("%s fix = %v, want %q", check, c.Fix, tt.wantFix)
			}
			if (code == 0) != rep.OK {
				t.Errorf("code %d with report ok = %v, want code 0 only when ok", code, rep.OK)
			}
		})
	}
}

func remove(path string) func(*testing.T, []string, string) {
	return func(t *testing.T, _ []string, _ string) {
		t.Helper()
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoctorZK(t *testing.T) {
	runDoctorCases(t, "zk", []doctorCase{
		{name: "on PATH", wantOK: true},
		{name: "in ~/.local/bin", wantOK: true, breakIt: func(t *testing.T, env []string, bin string) {
			remove(filepath.Join(bin, "zk"))(t, env, bin)
			local := filepath.Join(doctorHome(env), ".local", "bin")
			if err := os.MkdirAll(local, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(local, "zk"), []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing", wantOK: false, wantFix: "run zk-chef install", wantDetail: "not found",
			breakIt: func(t *testing.T, env []string, bin string) { remove(filepath.Join(bin, "zk"))(t, env, bin) }},
	})
}

func TestDoctorTextOutput(t *testing.T) {
	env, bin := healthyDoctor(t, "1.2.3", "")
	remove(filepath.Join(bin, "zk"))(t, env, bin)
	got := run(env, "doctor")
	if got.code != 1 {
		t.Errorf("code %d, want 1", got.code)
	}
	if want := "FAIL zk  zk not found on PATH or in ~/.local/bin\n  fix: run zk-chef install\n"; !strings.HasPrefix(got.stdout, want) {
		t.Errorf("stdout %q, want prefix %q", got.stdout, want)
	}
	if !strings.HasSuffix(got.stdout, "\n1 checks failed\n") {
		t.Errorf("stdout %q, want last line \"1 checks failed\"", got.stdout)
	}
}

func TestDoctorRootFlag(t *testing.T) {
	env, _ := healthyDoctor(t, "1.2.3", "")
	root, _ := os.Getwd()
	t.Chdir(t.TempDir())
	code, rep := doctorJSON(t, env, "--root", root)
	if code != 0 || !rep.OK {
		t.Errorf("code %d report %+v, want every check to pass for the vault named by --root", code, rep)
	}
}

func TestDoctorMinVersion(t *testing.T) {
	const fix = "run zk-chef self-update"
	const invalidFix = "set min_version in .zk-chef/config.toml to a MAJOR.MINOR.PATCH version"
	runDoctorCases(t, "min_version", []doctorCase{
		{name: "absent", wantOK: true},
		{name: "equal", wantOK: true, extraConfig: "min_version = \"1.2.3\"\n"},
		{name: "equal with a v prefix", wantOK: true, extraConfig: "min_version = \"v1.2.3\"\n"},
		{name: "binary newer", wantOK: true, extraConfig: "min_version = \"1.0.0\"\n"},
		{name: "binary older", wantOK: false, wantFix: fix, wantDetail: "1.2.3 is older than min_version 1.2.4",
			extraConfig: "min_version = \"1.2.4\"\n"},
		{name: "dev binary", wantOK: true, binVersion: "dev", extraConfig: "min_version = \"9.0.0\"\n"},
		{name: "invalid text", wantOK: false, wantFix: invalidFix, wantDetail: "min_version",
			extraConfig: "min_version = \"banana\"\n"},
		{name: "invalid type", wantOK: false, wantFix: invalidFix, wantDetail: "min_version",
			extraConfig: "min_version = 3\n"},
		{name: "invalid value on a dev binary", wantOK: false, wantFix: invalidFix, wantDetail: "min_version",
			binVersion: "dev", extraConfig: "min_version = \"banana\"\n"},
		{name: "no vault config", wantOK: false, wantFix: "run zk-chef init", wantDetail: "no .zk-chef/config.toml; run zk-chef init first",
			breakIt: func(t *testing.T, env []string, bin string) { remove(configPath)(t, env, bin) }},
	})
}

func TestDoctorMissingConfigKeepsOtherChecks(t *testing.T) {
	env, bin := healthyDoctor(t, "1.2.3", "")
	remove(configPath)(t, env, bin)
	code, rep := doctorJSON(t, env)
	failed := map[string]bool{}
	for _, c := range rep.Checks {
		failed[c.Name] = !c.OK
	}
	want := map[string]bool{"zk": false, "git": false, "gpg": false, "hooks-path": false, "pinned-key": true, "skills": false, "path": false, "min_version": true}
	if code != 1 || rep.OK || len(failed) != len(want) {
		t.Fatalf("code %d ok %v checks %v, want 1 false and 8 checks", code, rep.OK, rep.Checks)
	}
	for name, w := range want {
		if failed[name] != w {
			t.Errorf("check %s failed = %v, want %v", name, failed[name], w)
		}
	}
}

func TestDoctorPath(t *testing.T) {
	setPath := func(entries func(env []string, bin string) []string) func(*testing.T, []string, string) {
		return func(t *testing.T, env []string, bin string) {
			t.Setenv("PATH", strings.Join(entries(env, bin), string(filepath.ListSeparator)))
		}
	}
	runDoctorCases(t, "path", []doctorCase{
		{name: "on PATH", wantOK: true},
		{name: "on PATH with a trailing slash", wantOK: true, breakIt: setPath(func(env []string, bin string) []string {
			return []string{bin, filepath.Join(doctorHome(env), ".local", "bin") + "/"}
		})},
		{name: "not on PATH", wantOK: false, wantFix: "add ~/.local/bin to PATH", wantDetail: "not on PATH",
			breakIt: setPath(func(_ []string, bin string) []string { return []string{bin} })},
	})
}

func TestDoctorSkills(t *testing.T) {
	const install = "run zk-chef install"
	skill := func(env []string, name string, parts ...string) string {
		return filepath.Join(append([]string{doctorHome(env), ".agents", "skills", name}, parts...)...)
	}
	setMarker := func(content string) func(*testing.T, []string, string) {
		return func(t *testing.T, env []string, _ string) {
			t.Helper()
			if err := os.WriteFile(skill(env, "zk-chef-ops", ownerMarker), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	runDoctorCases(t, "skills", []doctorCase{
		{name: "current", wantOK: true},
		{name: "marker with a trailing newline", wantOK: true, breakIt: setMarker("1.2.3\n")},
		{name: "missing", wantOK: false, wantFix: install, wantDetail: "missing: zk-chef-ops",
			breakIt: func(t *testing.T, env []string, bin string) { remove(skill(env, "zk-chef-ops"))(t, env, bin) }},
		{name: "unmarked", wantOK: false, wantDetail: "unmarked: zk-chef-ops",
			wantFix: "rename or remove your zk-chef-ops skill, then run zk-chef install",
			breakIt: func(t *testing.T, env []string, bin string) {
				remove(skill(env, "zk-chef-ops", ownerMarker))(t, env, bin)
			}},
		{name: "outdated marker", wantOK: false, wantFix: install, wantDetail: "outdated: zk-chef-ops (0.9.0, want 1.2.3)",
			breakIt: setMarker("0.9.0")},
		{name: "missing and unmarked", wantOK: false, wantDetail: "missing: zettel-writing; unmarked: zk-chef-ops",
			wantFix: "run zk-chef install; rename or remove your zk-chef-ops skill, then run zk-chef install",
			breakIt: func(t *testing.T, env []string, bin string) {
				remove(skill(env, "zettel-writing"))(t, env, bin)
				remove(skill(env, "zk-chef-ops", ownerMarker))(t, env, bin)
			}},
	})
}

// setPins rewrites pinned_fingerprints in the vault config in the current directory.
func setPins(pins ...string) func(*testing.T, []string, string) {
	return func(t *testing.T, _ []string, _ string) {
		t.Helper()
		b, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		list, _ := json.Marshal(append([]string{}, pins...)) // never null
		out := strings.Replace(string(b), `pinned_fingerprints = ["`+doctorFPR+`"]`, "pinned_fingerprints = "+string(list), 1)
		if err := os.WriteFile(configPath, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoctorPinnedKey(t *testing.T) {
	const fix = "run zk-chef key add"
	other := strings.Repeat("0", 40)
	runDoctorCases(t, "pinned-key", []doctorCase{
		{name: "pinned with file", wantOK: true},
		{name: "pin spelled in lower case with spaces", wantOK: true,
			breakIt: setPins(strings.ToLower(doctorFPR[:20]) + " " + strings.ToLower(doctorFPR[20:]))},
		{name: "none pinned", wantOK: false, wantFix: fix, wantDetail: "no pinned fingerprint", breakIt: setPins()},
		{name: "pinned without file", wantOK: false, wantFix: fix, wantDetail: "no keys/ file for " + other,
			breakIt: setPins(doctorFPR, other)},
		{name: "file removed", wantOK: false, wantFix: fix, wantDetail: "no keys/ file for " + doctorFPR,
			breakIt: func(t *testing.T, env []string, bin string) { remove("keys/"+doctorFPR+".asc")(t, env, bin) }},
		{name: "no vault config", wantOK: false, wantFix: "run zk-chef init", wantDetail: "no .zk-chef/config.toml; run zk-chef init first",
			breakIt: func(t *testing.T, env []string, bin string) { remove(configPath)(t, env, bin) }},
	})
}

func TestDoctorHooksPath(t *testing.T) {
	const fix = "run zk-chef init (or git config core.hooksPath .githooks)"
	gitIn := func(t *testing.T, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	runDoctorCases(t, "hooks-path", []doctorCase{
		{name: "set", wantOK: true},
		{name: "set without a vault config", wantOK: true,
			breakIt: func(t *testing.T, env []string, bin string) { remove(configPath)(t, env, bin) }},
		{name: "unset", wantOK: false, wantFix: fix, wantDetail: "core.hooksPath is not set",
			breakIt: func(t *testing.T, _ []string, _ string) { gitIn(t, "config", "--unset", "core.hooksPath") }},
		{name: "other value", wantOK: false, wantFix: fix, wantDetail: `core.hooksPath is "hooks"`,
			breakIt: func(t *testing.T, _ []string, _ string) { gitIn(t, "config", "core.hooksPath", "hooks") }},
		{name: "not a git repository", wantOK: false, wantFix: "run zk-chef init", wantDetail: "not a git repository",
			breakIt: func(t *testing.T, env []string, bin string) { remove(".git")(t, env, bin) }},
	})
}

func TestDoctorAllPass(t *testing.T) {
	env, _ := healthyDoctor(t, "1.2.3", "")
	got := run(env, "doctor")
	if got.code != 0 || !strings.HasSuffix(got.stdout, "\nall checks passed\n") || strings.Contains(got.stdout, "FAIL") {
		t.Errorf("code %d stdout %q stderr %q, want 0 and all checks passed", got.code, got.stdout, got.stderr)
	}
	code, rep := doctorJSON(t, env)
	var names []string
	for _, c := range rep.Checks {
		names = append(names, c.Name)
		if !c.OK || c.Fix != nil {
			t.Errorf("check %+v, want ok with null fix", c)
		}
	}
	want := "zk git gpg hooks-path pinned-key skills path min_version"
	if code != 0 || !rep.OK || strings.Join(names, " ") != want {
		t.Errorf("code %d ok %v checks %v, want 0 true [%s]", code, rep.OK, names, want)
	}
}

func TestDoctorRunsBelowMinVersion(t *testing.T) {
	env, _ := healthyDoctor(t, "1.2.3", "min_version = \"9.0.0\"\n")
	if got := run(env, "lint"); got.code != 2 || !strings.Contains(got.stderr, "older than min_version") {
		t.Fatalf("lint: code %d stderr %q, want the version gate", got.code, got.stderr)
	}
	code, rep := doctorJSON(t, env)
	if c := checkNamed(t, rep, "zk"); !c.OK {
		t.Errorf("zk = %+v, want ok", c)
	}
	if code != 1 || rep.OK {
		t.Errorf("code %d ok %v, want 1 false for the min_version failure", code, rep.OK)
	}
}

func TestDoctorGit(t *testing.T) {
	runDoctorCases(t, "git", []doctorCase{
		{name: "on PATH", wantOK: true},
		{name: "missing", wantOK: false, wantFix: "install git", wantDetail: "not found",
			breakIt: func(t *testing.T, env []string, bin string) { remove(filepath.Join(bin, "git"))(t, env, bin) }},
	})
}

func TestDoctorGPG(t *testing.T) {
	runDoctorCases(t, "gpg", []doctorCase{
		{name: "on PATH", wantOK: true},
		{name: "missing", wantOK: false, wantFix: "install gnupg", wantDetail: "not found",
			breakIt: func(t *testing.T, env []string, bin string) { remove(filepath.Join(bin, "gpg"))(t, env, bin) }},
	})
}
