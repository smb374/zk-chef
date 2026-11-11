package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/smb374/zk-chef/internal/lint"
	"github.com/smb374/zk-chef/internal/version"
	"io/fs"
)

const hooksFix = "run zk-chef init (or git config core.hooksPath .githooks)"

// doctorCheck is one doctor result. Fix is nil when the check passes.
type doctorCheck struct {
	Name   string  `json:"name"`
	OK     bool    `json:"ok"`
	Detail string  `json:"detail"`
	Fix    *string `json:"fix"`
}

func pass(name, detail string) doctorCheck { return doctorCheck{Name: name, OK: true, Detail: detail} }

func failing(name, detail, fix string) doctorCheck {
	return doctorCheck{Name: name, Detail: detail, Fix: &fix}
}

// doctorVerb runs every check, in order, and exits 1 when one fails. A failed check never stops the others.
func doctorVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef doctor: "+err.Error())
	}
	home, homeErr := homeDir(inv)
	vault := loadVaultConfig(root)
	checks := []doctorCheck{
		checkZK(home, homeErr),
		checkOnPath("git", "install git"),
		checkOnPath("gpg", "install gnupg"),
		checkHooksPath(inv, root),
		checkPinnedKey(vault, root),
		checkSkills(home, homeErr),
		checkPath(home, homeErr),
		checkMinVersion(vault),
		checkHugo(),
	}
	failed := 0
	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK     bool          `json:"ok"`
			Checks []doctorCheck `json:"checks"`
		}{failed == 0, checks})
	} else {
		for _, c := range checks {
			if c.OK {
				fmt.Fprintf(inv.stdout, "ok   %s  %s\n", c.Name, c.Detail)
				continue
			}
			fmt.Fprintf(inv.stdout, "FAIL %s  %s\n  fix: %s\n", c.Name, c.Detail, *c.Fix)
		}
		if failed == 0 {
			fmt.Fprintln(inv.stdout, "all checks passed")
		} else {
			fmt.Fprintf(inv.stdout, "%d checks failed\n", failed)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// checkZK looks for zk on PATH, then in home/.local/bin.
func checkZK(home string, homeErr error) doctorCheck {
	if p, err := exec.LookPath("zk"); err == nil {
		return pass("zk", p)
	}
	if homeErr == nil {
		p := filepath.Join(home, ".local", "bin", "zk")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return pass("zk", p)
		}
	}
	return failing("zk", "zk not found on PATH or in ~/.local/bin", "run zk-chef install")
}

// checkOnPath looks for the tool name on PATH.
func checkOnPath(name, fix string) doctorCheck {
	if p, err := exec.LookPath(name); err == nil {
		return pass(name, p)
	}
	return failing(name, name+" not found on PATH", fix)
}

// repoGit runs git in root with the verb's environment and returns trimmed stdout.
func repoGit(inv *invocation, root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), inv.env...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// checkHooksPath wants core.hooksPath of the repo at root to be .githooks.
func checkHooksPath(inv *invocation, root string) doctorCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return failing("hooks-path", "git not found on PATH", "install git")
	}
	if _, err := repoGit(inv, root, "rev-parse", "--git-dir"); err != nil {
		return failing("hooks-path", "not a git repository", "run zk-chef init")
	}
	// A non-zero exit with no output means the key is unset.
	got, _ := repoGit(inv, root, "config", "core.hooksPath")
	switch got {
	case ".githooks":
		return pass("hooks-path", "core.hooksPath is .githooks")
	case "":
		return failing("hooks-path", "core.hooksPath is not set", hooksFix)
	}
	return failing("hooks-path", fmt.Sprintf("core.hooksPath is %q, want .githooks", got), hooksFix)
}

// vaultConfig is the decoded vault config of a doctor run. problem is empty when it loaded.
type vaultConfig struct {
	cfg          map[string]any
	problem, fix string
}

func loadVaultConfig(root string) vaultConfig {
	if _, err := os.Stat(filepath.Join(root, configPath)); os.IsNotExist(err) {
		return vaultConfig{problem: "no " + configPath + "; run zk-chef init first", fix: "run zk-chef init"}
	}
	cfg, err := readConfig(root)
	if err != nil {
		return vaultConfig{problem: err.Error(), fix: "fix " + configPath}
	}
	return vaultConfig{cfg: cfg}
}

// checkPinnedKey wants one pinned fingerprint, at least, and a keys/<FPR>.asc file for every pin.
func checkPinnedKey(vault vaultConfig, root string) doctorCheck {
	const name = "pinned-key"
	if vault.problem != "" {
		return failing(name, vault.problem, vault.fix)
	}
	const fix = "run zk-chef key add"
	pins, err := pinnedOf(vault.cfg)
	if err != nil {
		return failing(name, err.Error(), "fix pinned_fingerprints in "+configPath)
	}
	if len(pins) == 0 {
		return failing(name, "no pinned fingerprint in "+configPath, fix)
	}
	entries, err := os.ReadDir(filepath.Join(root, "keys"))
	if err != nil && !os.IsNotExist(err) {
		return failing(name, err.Error(), fix)
	}
	have := map[string]bool{}
	for _, e := range entries {
		if fpr, ok := strings.CutSuffix(e.Name(), ".asc"); ok && e.Type().IsRegular() {
			have[lint.NormFpr(fpr)] = true
		}
	}
	var missing []string
	for _, p := range pins {
		if fpr := lint.NormFpr(p); !have[fpr] {
			missing = append(missing, "no keys/ file for "+fpr)
		}
	}
	if len(missing) > 0 {
		return failing(name, strings.Join(missing, "; "), fix)
	}
	return pass(name, fmt.Sprintf("%d pinned, each with a keys/ file", len(pins)))
}

// checkSkills wants every embedded skill in home/.agents/skills with an owner marker that
// holds the binary version.
func checkSkills(home string, homeErr error) doctorCheck {
	const name = "skills"
	if homeErr != nil {
		return failing(name, "no home directory: "+homeErr.Error(), "set HOME")
	}
	embedded, err := fs.Sub(Assets, "skills")
	if err != nil {
		return failing(name, err.Error(), "reinstall zk-chef")
	}
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return failing(name, err.Error(), "reinstall zk-chef")
	}
	want := version.Current()
	var missing, unmarked, outdated, fixes []string
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		dir := filepath.Join(home, ".agents", "skills", e.Name())
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			missing = append(missing, e.Name())
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ownerMarker))
		switch {
		case err != nil:
			unmarked = append(unmarked, e.Name())
		case strings.TrimSpace(string(b)) != want:
			outdated = append(outdated, fmt.Sprintf("%s (%s, want %s)", e.Name(), strings.TrimSpace(string(b)), want))
		}
	}
	var problems []string
	for _, g := range []struct {
		label string
		names []string
	}{{"missing", missing}, {"unmarked", unmarked}, {"outdated", outdated}} {
		if len(g.names) > 0 {
			problems = append(problems, g.label+": "+strings.Join(g.names, ", "))
		}
	}
	if len(problems) == 0 {
		return pass(name, fmt.Sprintf("%d skills at %s", n, want))
	}
	if len(missing)+len(outdated) > 0 {
		fixes = append(fixes, "run zk-chef install")
	}
	for _, k := range unmarked {
		fixes = append(fixes, "rename or remove your "+k+" skill, then run zk-chef install")
	}
	return failing(name, strings.Join(problems, "; "), strings.Join(fixes, "; "))
}

// checkPath wants home/.local/bin on the process PATH.
func checkPath(home string, homeErr error) doctorCheck {
	if homeErr != nil {
		return failing("path", "no home directory: "+homeErr.Error(), "set HOME")
	}
	dir := filepath.Join(home, ".local", "bin")
	if !onPath(dir) {
		return failing("path", dir+" is not on PATH", "add ~/.local/bin to PATH")
	}
	return pass("path", dir+" is on PATH")
}

// checkMinVersion wants the binary version to be at least the vault's min_version.
// An absent key and a dev binary pass.
func checkMinVersion(vault vaultConfig) doctorCheck {
	const name = "min_version"
	if vault.problem != "" {
		return failing(name, vault.problem, vault.fix)
	}
	raw, ok := vault.cfg["min_version"]
	if !ok {
		return pass(name, "no min_version in "+configPath)
	}
	minVersion, isString := raw.(string)
	if !isString || !version.Valid(minVersion) {
		return failing(name, fmt.Sprintf("min_version %v is not a semantic version", raw),
			"set min_version in "+configPath+" to a MAJOR.MINOR.PATCH version")
	}
	bin := version.Current()
	if bin == "dev" {
		return pass(name, "dev build passes every min_version")
	}
	cmp, err := version.Compare(bin, minVersion)
	if err != nil {
		return failing(name, fmt.Sprintf("binary version %q is not a semantic version", bin), "run zk-chef self-update")
	}
	if cmp < 0 {
		return failing(name, fmt.Sprintf("%s is older than min_version %s", bin, minVersion), "run zk-chef self-update")
	}
	return pass(name, fmt.Sprintf("%s satisfies min_version %s", bin, minVersion))
}

// checkHugo reports the hugo on PATH. Hugo is optional (only export -g hugo needs it), so no outcome fails the check.
func checkHugo() doctorCheck {
	const name = "hugo"
	p, err := exec.LookPath("hugo")
	if err != nil {
		return pass(name, "not installed (only needed for export -g hugo)")
	}
	v, ok := installedHugo()
	switch {
	case !ok:
		return pass(name, p+" (version not recognized)")
	case hugoBelowFloor(v):
		return pass(name, fmt.Sprintf("WARNING: %s is %s; export -g hugo needs Hugo %s or newer", p, v, hugoFloor))
	}
	return pass(name, fmt.Sprintf("%s is %s", p, v))
}
