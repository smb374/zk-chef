package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// hugoVendoredFiles are the files the Export scaffold writes under static/, in the order a walk of the embedded tree visits them.
var hugoVendoredFiles = []string{
	"static/vendored/tufte/LICENSE-et-book",
	"static/vendored/tufte/LICENSE-tufte-css",
	"static/vendored/tufte/et-book/et-book-bold-line-figures/et-book-bold-line-figures.woff",
	"static/vendored/tufte/et-book/et-book-display-italic-old-style-figures/et-book-display-italic-old-style-figures.woff",
	"static/vendored/tufte/et-book/et-book-roman-line-figures/et-book-roman-line-figures.woff",
	"static/vendored/tufte/et-book/et-book-roman-old-style-figures/et-book-roman-old-style-figures.woff",
	"static/vendored/tufte/et-book/et-book-semi-bold-old-style-figures/et-book-semi-bold-old-style-figures.woff",
	"static/vendored/tufte/tufte.css",
}

func TestExportHugoScaffoldFreshOut(t *testing.T) {
	tree := exportHugoOK(t, baseVault(t))
	for _, want := range []string{
		"baseURL = '/'\n",
		"title = 'Index'\n",
		"refLinksErrorLevel = 'ERROR'\n",
		"disableKinds = ['taxonomy', 'term', 'RSS', 'sitemap']\n",
		"[markup.goldmark.renderHooks.link]\n  useEmbedded = 'always'\n",
		"[module.hugoVersion]\n  min = '0.169.0'\n",
	} {
		if !strings.Contains(tree["hugo.toml"], want) {
			t.Errorf("hugo.toml = %q, want it to contain %q", tree["hugo.toml"], want)
		}
	}
	for p, wants := range map[string][]string{
		"layouts/baseof.html": {`this site needs Hugo 0.169.0 or newer, found`, "<style>", `href="{{ "/" | relURL }}"`, `{{ "vendored/tufte/tufte.css" | relURL }}`, "#members", "#backlinks", ".status-superseded", "{{ block \"main\" . }}"},
		"layouts/home.html":   {"{{ .Title }}", "{{ .Content }}", "range .Pages", "<article", "<section>"},
		"layouts/list.html":   {"{{ .Title }}", "{{ .Content }}", "range .Pages", "<article", "<section>"},
		"layouts/single.html": {"{{ .Title }}", "{{ with .Date }}", "{{ with .Params.source_url }}", "{{ .Content }}", "<article", "<section>", ".Params.review", ".Params.citation", ".Params.authors", ".Params.source_published", "status-"},
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
	want := append([]string{"hugo.toml", "layouts/baseof.html", "layouts/home.html", "layouts/list.html", "layouts/single.html"}, hugoVendoredFiles...)
	got := run(env, "export", "--json", "--generator", "hugo", out)
	var r struct {
		Scaffolded []string `json:"scaffolded"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil || got.code != 0 || !slices.Equal(r.Scaffolded, want) {
		t.Errorf("code %d stdout %q (%v), want scaffolded %v", got.code, got.stdout, err, want)
	}
	text := run(env, "export", "--generator", "hugo", t.TempDir())
	if !strings.Contains(text.stdout, "created "+strings.Join(want, "\ncreated ")+"\n") {
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

// hugoReport is the --json report of a hugo export.
type hugoReport struct {
	Scaffolded []string `json:"scaffolded"`
	Updated    []string `json:"updated"`
	Kept       []string `json:"kept"`
}

// exportHugoReport runs a hugo export to out and returns its --json report.
func exportHugoReport(t *testing.T, env []string, out string) hugoReport {
	t.Helper()
	got := run(env, "export", "--json", "--generator", "hugo", out)
	var r hugoReport
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil || got.code != 0 || r.Scaffolded == nil || r.Updated == nil || r.Kept == nil {
		t.Fatalf("code %d stdout %q stderr %q (%v), want 0 and a report with scaffolded, updated and kept", got.code, got.stdout, got.stderr, err)
	}
	return r
}

// shipOld records old as a released version of the scaffold file rel until the test ends; for hugo.toml, old is a template with @TITLE@.
func shipOld(t *testing.T, rel, old string) {
	t.Helper()
	oldHashes, oldTemplates := scaffoldHashes, hugoTomlTemplates
	t.Cleanup(func() { scaffoldHashes, hugoTomlTemplates = oldHashes, oldTemplates })
	if rel == "hugo.toml" {
		hugoTomlTemplates = append(slices.Clone(oldTemplates), old)
		return
	}
	sum := sha256.Sum256([]byte(old))
	scaffoldHashes = maps.Clone(oldHashes)
	scaffoldHashes[rel] = append(slices.Clone(oldHashes[rel]), hex.EncodeToString(sum[:]))
}

// withScaffold replaces the embedded export scaffold with a copy of the repo's, plus files (slash path under export-scaffold/hugo -> content), until the test ends. Call it after setupVault.
func withScaffold(t *testing.T, files map[string]string) {
	t.Helper()
	tree := fstest.MapFS{}
	repo := Assets
	err := fs.WalkDir(repo, exportScaffoldDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(repo, p)
		tree[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range files {
		tree[exportScaffoldDir+"/"+p] = &fstest.MapFile{Data: []byte(c)}
	}
	Assets = tree // setupVault's cleanup restores the original
}

func TestExportHugoScaffoldUpdatesShippedFile(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	shipOld(t, "layouts/single.html", "old single\n")
	out := t.TempDir()
	writeFiles(t, out, map[string]string{"layouts/single.html": "old single\n"})
	r := exportHugoReport(t, env, out)
	if !slices.Equal(r.Updated, []string{"layouts/single.html"}) || len(r.Kept) != 0 || slices.Contains(r.Scaffolded, "layouts/single.html") {
		t.Errorf("report %+v, want updated [layouts/single.html], none kept, and it not scaffolded", r)
	}
	if got := readTree(t, out)["layouts/single.html"]; got == "old single\n" || !strings.Contains(got, "{{ .Content }}") {
		t.Errorf("layouts/single.html = %q, want the current layout", got)
	}
	if again := exportHugoReport(t, env, out); len(again.Updated)+len(again.Kept)+len(again.Scaffolded) != 0 {
		t.Errorf("second run report %+v, want nothing to do", again)
	}
	writeFiles(t, out, map[string]string{"layouts/single.html": "old single\n"})
	if text := run(env, "export", "--generator", "hugo", out); !strings.Contains(text.stdout, "updated layouts/single.html\n") {
		t.Errorf("stdout %q, want the line \"updated layouts/single.html\"", text.stdout)
	}
}

func TestExportHugoScaffoldKeepsEditedFile(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	out := t.TempDir()
	writeFiles(t, out, map[string]string{"layouts/single.html": "my single\n"})
	got := run(env, "export", "--generator", "hugo", out)
	for _, want := range []string{"kept your layouts/single.html\n", "--- a/layouts/single.html\n", "-my single\n"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout %q, want it to contain %q", got.stdout, want)
		}
	}
	if tree := readTree(t, out); tree["layouts/single.html"] != "my single\n" {
		t.Errorf("layouts/single.html = %q, want the edited file kept", tree["layouts/single.html"])
	}
	jsonOut := t.TempDir()
	writeFiles(t, jsonOut, map[string]string{"layouts/single.html": "my single\n"})
	asJSON := run(env, "export", "--json", "--generator", "hugo", jsonOut)
	if strings.Contains(asJSON.stdout, "my single") || strings.Contains(asJSON.stdout, "---") {
		t.Errorf("--json stdout %q, want the path only, no diff", asJSON.stdout)
	}
	if r := exportHugoReport(t, env, jsonOut); !slices.Equal(r.Kept, []string{"layouts/single.html"}) || len(r.Updated) != 0 {
		t.Errorf("report %+v, want kept [layouts/single.html]", r)
	}
}

func TestExportHugoScaffoldRecreatesMissingFile(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	out := t.TempDir()
	exportHugoScaffolded(t, env, out)
	if err := os.Remove(filepath.Join(out, "layouts", "list.html")); err != nil {
		t.Fatal(err)
	}
	if r := exportHugoReport(t, env, out); !slices.Equal(r.Scaffolded, []string{"layouts/list.html"}) || len(r.Updated)+len(r.Kept) != 0 {
		t.Errorf("report %+v, want scaffolded [layouts/list.html] only", r)
	}
	if _, err := os.Stat(filepath.Join(out, "layouts", "list.html")); err != nil {
		t.Errorf("layouts/list.html: %v, want it created", err)
	}
}

func TestExportHugoScaffoldUpdatesPastHugoToml(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	shipOld(t, "hugo.toml", "title = @TITLE@\nold = true\n")
	out := t.TempDir()
	writeFiles(t, out, map[string]string{"hugo.toml": "title = 'Index'\nold = true\n"}) // the old template with the root title of baseVault
	if r := exportHugoReport(t, env, out); !slices.Equal(r.Updated, []string{"hugo.toml"}) {
		t.Errorf("report %+v, want updated [hugo.toml]", r)
	}
	if got := readTree(t, out)["hugo.toml"]; !strings.Contains(got, "title = 'Index'\nrefLinksErrorLevel") {
		t.Errorf("hugo.toml = %q, want the current template", got)
	}
	// An edit, or another title than the root title, is not a shipped version.
	for _, mine := range []string{"title = 'Index'\nold = true\n# mine\n", "title = 'Other'\nold = true\n"} {
		writeFiles(t, out, map[string]string{"hugo.toml": mine})
		if r := exportHugoReport(t, env, out); !slices.Equal(r.Kept, []string{"hugo.toml"}) || len(r.Updated) != 0 {
			t.Errorf("hugo.toml %q: report %+v, want kept [hugo.toml]", mine, r)
		}
		if got := readTree(t, out)["hugo.toml"]; got != mine {
			t.Errorf("hugo.toml = %q, want %q kept", got, mine)
		}
	}
}

func TestExportHugoScaffoldOwnsVendoredFiles(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	withScaffold(t, map[string]string{"static/vendored/tufte.css": "new css\n"})
	out := t.TempDir()
	if r := exportHugoReport(t, env, out); !slices.Contains(r.Scaffolded, "static/vendored/tufte.css") {
		t.Errorf("report %+v, want static/vendored/tufte.css scaffolded", r)
	}
	writeFiles(t, out, map[string]string{"static/vendored/tufte.css": "my css\n"})
	if r := exportHugoReport(t, env, out); !slices.Equal(r.Updated, []string{"static/vendored/tufte.css"}) || len(r.Kept) != 0 {
		t.Errorf("report %+v, want updated [static/vendored/tufte.css] and none kept", r)
	}
	if got := readTree(t, out)["static/vendored/tufte.css"]; got != "new css\n" {
		t.Errorf("static/vendored/tufte.css = %q, want it overwritten", got)
	}
	if r := exportHugoReport(t, env, out); len(r.Scaffolded)+len(r.Updated)+len(r.Kept) != 0 {
		t.Errorf("report %+v, want nothing to do when the vendored file is current", r)
	}
}

func TestExportHugoScaffoldReportsEmptyUpdatedAndKept(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	exportHugoReport(t, env, t.TempDir()) // fails unless updated and kept are arrays
}

func TestExportRefusesHugoStaticLinkIntoVault(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "notes"), filepath.Join(out, "static")); err != nil {
		t.Fatal(err)
	}
	before := readTree(t, root)
	got := run(env, "export", "--generator", "hugo", out)
	if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "the vault or inside it") {
		t.Errorf("got code %d stdout %q stderr %q, want 2, no stdout and the vault guard error", got.code, got.stdout, got.stderr)
	}
	if after := readTree(t, root); !maps.Equal(before, after) {
		t.Errorf("vault changed by a refused export: %v vs %v", before, after)
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
			scaffold := p == "hugo.toml" || strings.HasPrefix(p, "layouts/") || strings.HasPrefix(p, "static/") || p == exportManifestName
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
	files["notes/n9.md"] = doc("n9", "Old claim", "note", hubLine+"\n\nOld.", kv{"status", "superseded"}, kv{"superseded_by", "n1"})
	files["sources/s1.md"] = doc("s1", "Source one", "source",
		"Hubs: [[000000000000src0|Sources]]\n\nWhy.\n\n## Review",
		kv{"raw", "raw/s1-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"},
		kv{"url", "https://example.com/x"}, kv{"citation", "Doe 2026, https://example.com/paper (see https://example.org/b?x=1&y=2)."}, kv{"authors", "[Doe, Roe]"}, kv{"published", "2026-01-02"})
	out := t.TempDir()
	if got := runIn(t, "dev", files, "export", "--generator", "hugo", out); got.code != 0 {
		t.Fatalf("export: code %d stderr %q, want 0", got.code, got.stderr)
	}
	// Lint rejects a URL source without a citation, so write the page by hand.
	noCitation := "---\ntitle: No citation\nzk_kind: source\nreview: unreviewed\nsource_url: https://example.com/z\nauthors: [Doe, Roe]\nsource_published: 2026-01-02\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(out, "content", "sources", "s3.md"), []byte(noCitation), 0o644); err != nil {
		t.Fatal(err)
	}
	public := t.TempDir()
	cmd := exec.Command(hugoBin, "--source", out, "--destination", public)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hugo: %v\n%s", err, b)
	}
	for _, p := range hugoVendoredFiles {
		rel := strings.TrimPrefix(p, "static/")
		if _, err := os.Stat(filepath.Join(public, filepath.FromSlash(rel))); err != nil {
			t.Errorf("hugo output lacks %s: %v", rel, err)
		}
	}
	css, err := os.ReadFile(filepath.Join(public, "vendored", "tufte", "tufte.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{".eot", ".ttf", ".svg"} {
		if strings.Contains(string(css), gone) {
			t.Errorf("public/vendored/tufte/tufte.css names %s, a font file the scaffold does not ship", gone)
		}
	}
	for p, wants := range map[string][]string{
		"index.html":                       {`href="/vendored/tufte/tufte.css"`, "<article>", "<section>"},
		"notes/n1/index.html":              {`href="/vendored/tufte/tufte.css"`, "<article", "<section>"},
		"hubs/000000000000src0/index.html": {`id="members"`, `id="backlinks"`},
		"sources/s1/index.html":            {`<p class="source-meta">`, `<span class="review review-unreviewed">unreviewed</span>`, `<p class="citation">Doe 2026, <a href="https://example.com/paper">https://example.com/paper</a> (see <a href="https://example.org/b?x=1&amp;y=2">https://example.org/b?x=1&amp;y=2</a>).</p>`},
		"sources/s3/index.html":            {`<p><a href="https://example.com/z">https://example.com/z</a></p>`, `<span class="authors">Doe, Roe</span>`, `<span class="published">2026-01-02</span>`},
		"notes/n9/index.html":              {`<article class="status-superseded">`, "<p>Superseded by "},
	} {
		page, err := os.ReadFile(filepath.Join(public, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("hugo output lacks %s: %v", p, err)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(string(page), want) {
				t.Errorf("%s = %q, want it to contain %q", p, page, want)
			}
		}
	}
	for p, denies := range map[string][]string{
		"sources/s1/index.html": {`<p><a href="https://example.com/x">`, `href="https://example.com/x"`, `class="authors"`, `class="published"`},
		"sources/s3/index.html": {`class="citation"`},
	} {
		page, err := os.ReadFile(filepath.Join(public, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("hugo output lacks %s: %v", p, err)
			continue
		}
		for _, deny := range denies {
			if strings.Contains(string(page), deny) {
				t.Errorf("%s = %q, want it not to contain %q", p, page, deny)
			}
		}
	}
	for _, p := range []string{"index.html", "notes/n1/index.html", "sources/s1/index.html", "notes/202611091044tnn3/index.html"} {
		if _, err := os.Stat(filepath.Join(public, filepath.FromSlash(p))); err != nil {
			t.Errorf("hugo output lacks %s: %v", p, err)
		}
	}
}

// fakeHugo puts a hugo script that prints out on a PATH of its own until the test ends. An empty out puts no hugo on PATH.
func fakeHugo(t *testing.T, out string) {
	t.Helper()
	bin := t.TempDir()
	if out != "" {
		if err := os.WriteFile(filepath.Join(bin, "hugo"), []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func TestExportWarnsOldHugo(t *testing.T) {
	const warning = "needs Hugo 0.169.0 or newer"
	files := baseVault(t) // before setupVault changes the directory
	for name, args := range map[string][]string{"text": {"export", "-g", "hugo"}, "json": {"export", "--json", "-g", "hugo"}} {
		env := setupVault(t, "dev", files)
		fakeHugo(t, "hugo v0.146.0+extended linux/amd64")
		got := run(env, append(args, t.TempDir())...)
		if got.code != 0 || !strings.Contains(got.stderr, warning) || strings.Contains(got.stdout, warning) {
			t.Errorf("%s: code %d stdout %q stderr %q, want 0 and the warning on stderr only", name, got.code, got.stdout, got.stderr)
		}
	}
}

func TestExportNoWarnCurrentMissingOrUnparsableHugo(t *testing.T) {
	files := baseVault(t) // before setupVault changes the directory
	for name, out := range map[string]string{"current": "hugo v0.169.0+extended linux/amd64", "newer": "hugo v1.0.0 linux/amd64", "unparsable": "hugo banana", "missing": ""} {
		env := setupVault(t, "dev", files)
		fakeHugo(t, out)
		got := run(env, "export", "-g", "hugo", t.TempDir())
		if got.code != 0 || got.stderr != "" {
			t.Errorf("%s: code %d stderr %q, want 0 and no warning", name, got.code, got.stderr)
		}
	}
}
