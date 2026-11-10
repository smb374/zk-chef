package cli

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExportHugoScaffoldFreshOut(t *testing.T) {
	tree := exportHugoOK(t, baseVault(t))
	for _, want := range []string{
		"baseURL = '/'\n",
		"title = 'Index'\n",
		"refLinksErrorLevel = 'ERROR'\n",
		"disableKinds = ['taxonomy', 'term', 'RSS', 'sitemap']\n",
		"[markup.goldmark.renderHooks.link]\n  useEmbedded = 'always'\n",
		"[module.hugoVersion]\n  min = '0.146.0'\n",
	} {
		if !strings.Contains(tree["hugo.toml"], want) {
			t.Errorf("hugo.toml = %q, want it to contain %q", tree["hugo.toml"], want)
		}
	}
	for p, wants := range map[string][]string{
		"layouts/baseof.html": {"<style>", `href="{{ "/" | relURL }}"`, "{{ block \"main\" . }}"},
		"layouts/home.html":   {"{{ .Title }}", "{{ .Content }}", "range .Pages"},
		"layouts/list.html":   {"{{ .Title }}", "{{ .Content }}", "range .Pages"},
		"layouts/single.html": {"{{ .Title }}", "{{ with .Date }}", "{{ with .Params.source_url }}", "{{ .Content }}"},
	} {
		for _, want := range wants {
			if !strings.Contains(tree[p], want) {
				t.Errorf("%s = %q, want it to contain %q", p, tree[p], want)
			}
		}
	}
}

func TestExportHugoScaffoldTitleIsQuotedForToml(t *testing.T) {
	tree := exportHugoOK(t, with_(t, map[string]string{
		"index.md": doc("index", `"It's a \"vault\""`, "hub", "- [[000000000000src0|Sources]]"),
	}))
	if want := `title = "It's a \"vault\""` + "\n"; !strings.Contains(tree["hugo.toml"], want) {
		t.Errorf("hugo.toml = %q, want it to contain %q", tree["hugo.toml"], want)
	}
}

func TestExportHugoReportsScaffoldedFiles(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", baseVault(t))
	want := []string{"hugo.toml", "layouts/baseof.html", "layouts/home.html", "layouts/list.html", "layouts/single.html"}
	got := run(env, "export", "--json", "--generator", "hugo", out)
	var r struct {
		Scaffolded []string `json:"scaffolded"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil || got.code != 0 || !slices.Equal(r.Scaffolded, want) {
		t.Errorf("code %d stdout %q (%v), want scaffolded %v", got.code, got.stdout, err, want)
	}
	text := run(env, "export", "--generator", "hugo", t.TempDir())
	if !strings.Contains(text.stdout, "created hugo.toml, layouts/baseof.html, layouts/home.html, layouts/list.html, layouts/single.html") {
		t.Errorf("stdout %q, want it to name the created scaffold files", text.stdout)
	}
}

func TestExportGenericReportsNoScaffold(t *testing.T) {
	files := baseVault(t) // before runIn changes the directory
	got := runIn(t, "dev", files, "export", "--json", t.TempDir())
	if !strings.Contains(got.stdout, `"scaffolded": []`) && !strings.Contains(got.stdout, `"scaffolded":[]`) {
		t.Errorf("stdout %q, want an empty scaffolded array", got.stdout)
	}
	if text := runIn(t, "dev", files, "export", t.TempDir()); strings.Contains(text.stdout, "created") {
		t.Errorf("stdout %q, want no scaffold mention", text.stdout)
	}
}

// exportHugoScaffolded runs a hugo export to out and returns the scaffolded paths from the --json report.
func exportHugoScaffolded(t *testing.T, env []string, out string) []string {
	t.Helper()
	got := run(env, "export", "--json", "--generator", "hugo", out)
	var r struct {
		Scaffolded []string `json:"scaffolded"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil || got.code != 0 || r.Scaffolded == nil {
		t.Fatalf("code %d stdout %q stderr %q (%v), want 0 and a report with scaffolded", got.code, got.stdout, got.stderr, err)
	}
	return r.Scaffolded
}

// writeFiles writes files (slash path -> content) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportHugoScaffoldKeepsExistingConfig(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	for _, existing := range []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config.yaml", "config.yml", "config.json", "config/_default/hugo.toml"} {
		out := t.TempDir()
		writeFiles(t, out, map[string]string{existing: "mine"})
		scaffolded := exportHugoScaffolded(t, env, out)
		tree := readTree(t, out)
		if tree[existing] != "mine" {
			t.Errorf("%s: %s = %q, want it kept", existing, existing, tree[existing])
		}
		if _, ok := tree["hugo.toml"]; ok && existing != "hugo.toml" {
			t.Errorf("%s: OUT has hugo.toml, want none beside the existing config", existing)
		}
		if slices.Contains(scaffolded, "hugo.toml") || !slices.Contains(scaffolded, "layouts/baseof.html") {
			t.Errorf("%s: scaffolded %v, want the layouts and no hugo.toml", existing, scaffolded)
		}
	}
}

func TestExportHugoScaffoldIsWriteOnce(t *testing.T) {
	out := t.TempDir()
	writeFiles(t, out, map[string]string{"layouts/single.html": "my single"})
	env := setupVault(t, "dev", baseVault(t))
	if scaffolded := exportHugoScaffolded(t, env, out); slices.Contains(scaffolded, "layouts/single.html") || len(scaffolded) != 4 {
		t.Errorf("first run scaffolded %v, want 4 files and not layouts/single.html", scaffolded)
	}
	writeFiles(t, out, map[string]string{"hugo.toml": "edited", "layouts/baseof.html": "edited base"})
	before := readTree(t, out)
	if scaffolded := exportHugoScaffolded(t, env, out); len(scaffolded) != 0 {
		t.Errorf("second run scaffolded %v, want none", scaffolded)
	}
	after := readTree(t, out)
	for _, p := range []string{"hugo.toml", "layouts/baseof.html", "layouts/single.html", "layouts/home.html", "layouts/list.html"} {
		if after[p] != before[p] {
			t.Errorf("%s = %q after the second run, want %q", p, after[p], before[p])
		}
	}
	if after["layouts/single.html"] != "my single" {
		t.Errorf("layouts/single.html = %q, want the existing file kept", after["layouts/single.html"])
	}
	if text := run(env, "export", "--generator", "hugo", out); strings.Contains(text.stdout, "created") {
		t.Errorf("stdout %q, want no scaffold mention when nothing was created", text.stdout)
	}
}

func TestExportHugoSwitchKeepsScaffold(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", baseVault(t))
	for i, args := range [][]string{{"export", "--generator", "hugo", out}, {"export", out}, {"export", "--generator", "hugo", out}} {
		if got := run(env, args...); got.code != 0 {
			t.Fatalf("run %d: code %d stderr %q, want 0", i+1, got.code, got.stderr)
		}
		tree := readTree(t, out)
		for _, p := range []string{"hugo.toml", "layouts/baseof.html"} {
			if _, ok := tree[p]; !ok {
				t.Errorf("run %d: OUT lacks %s, want the scaffold kept", i+1, p)
			}
		}
		for p := range tree {
			hugoPage := strings.HasPrefix(p, "content/")
			scaffold := p == "hugo.toml" || strings.HasPrefix(p, "layouts/") || p == exportManifestName
			if hugo := i%2 == 0; hugo && !hugoPage && !scaffold {
				t.Errorf("run %d (hugo): OUT has %s, want no page outside content/", i+1, p)
			} else if !hugo && hugoPage {
				t.Errorf("run %d (generic): OUT has %s, want the hugo pages gone", i+1, p)
			}
		}
	}
}

func TestExportRefusesOutThroughSymlinkIntoVault(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	links := t.TempDir()
	for name, target := range map[string]string{"root": root, "notes": filepath.Join(root, "notes")} {
		if err := os.Symlink(target, filepath.Join(links, name)); err != nil {
			t.Fatal(err)
		}
	}
	elsewhere := t.TempDir() // OUT with a content directory that links into the vault
	if err := os.Symlink(filepath.Join(root, "notes"), filepath.Join(elsewhere, "content")); err != nil {
		t.Fatal(err)
	}
	before := readTree(t, root)
	for name, args := range map[string][]string{
		"link to vault":           {"export", filepath.Join(links, "root")},
		"link to vault folder":    {"export", filepath.Join(links, "notes")},
		"new dir below link":      {"export", filepath.Join(links, "root", "site")},
		"hugo link to vault":      {"export", "--generator", "hugo", filepath.Join(links, "root")},
		"hugo content link":       {"export", "--generator", "hugo", elsewhere},
		"hugo new dir below link": {"export", "--generator", "hugo", filepath.Join(links, "notes", "site", "deep")},
	} {
		got := run(env, args...)
		if got.code != 2 || got.stdout != "" || got.stderr == "" {
			t.Errorf("%s: got code %d stdout %q stderr %q, want 2, no stdout and an error", name, got.code, got.stdout, got.stderr)
		}
	}
	if after := readTree(t, root); !maps.Equal(before, after) {
		t.Errorf("vault changed by refused exports: %v vs %v", before, after)
	}
}

func TestExportRefusesOutThroughDanglingSymlinkIntoVault(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "site")
	if err := os.Symlink(filepath.Join(root, "newdir"), link); err != nil { // the target does not exist
		t.Fatal(err)
	}
	before := readTree(t, root)
	for _, args := range [][]string{{"export", link}, {"export", "--generator", "hugo", link}} {
		got := run(env, args...)
		if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "the vault or inside it") {
			t.Errorf("%v: got code %d stdout %q stderr %q, want 2, no stdout and the vault guard error", args, got.code, got.stdout, got.stderr)
		}
	}
	if after := readTree(t, root); !maps.Equal(before, after) {
		t.Errorf("vault changed by refused exports: %v vs %v", before, after)
	}
}

func TestExportHugoSiteBuilds(t *testing.T) {
	hugoBin, err := exec.LookPath("hugo")
	if err != nil {
		t.Skipf("hugo not on PATH: %v", err)
	}
	files := exportVault(t)
	files["notes/202611091044tnn3.md"] = doc("202611091044tnn3", "Dated note", "note", hubLine+"\n\nSee [[n1]] and [[s1|Reading]].\n\n{{< figure src=\"x\" >}}")
	out := t.TempDir()
	if got := runIn(t, "dev", files, "export", "--generator", "hugo", out); got.code != 0 {
		t.Fatalf("export: code %d stderr %q, want 0", got.code, got.stderr)
	}
	public := t.TempDir()
	cmd := exec.Command(hugoBin, "--source", out, "--destination", public)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hugo: %v\n%s", err, b)
	}
	for _, p := range []string{"index.html", "notes/n1/index.html", "sources/s1/index.html", "notes/202611091044tnn3/index.html"} {
		if _, err := os.Stat(filepath.Join(public, filepath.FromSlash(p))); err != nil {
			t.Errorf("hugo output lacks %s: %v", p, err)
		}
	}
}
