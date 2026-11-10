package cli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/smb374/zk-chef/internal/lint"
	"gopkg.in/yaml.v3"
)

// hugoShortcodes match a Hugo shortcode call, {{< ... >}} or {{% ... %}}, across lines.
var hugoShortcodes = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?s)\{\{<(.*?)>\}\}`), "{{</*${1}*/>}}"},
	{regexp.MustCompile(`(?s)\{\{%(.*?)%\}\}`), "{{%/*${1}*/%}}"},
}

// escapeHugoShortcodes rewrites every shortcode call in body so that Hugo shows it as text.
func escapeHugoShortcodes(body string) string {
	for _, s := range hugoShortcodes {
		body = s.re.ReplaceAllString(body, s.repl)
	}
	return body
}

// exportFolders are the vault folders whose pages go to the export, one level deep.
var exportFolders = []string{"notes", "hubs", "sources", "projects"}

// exportManifest is the file in OUT that lists, as slash paths relative to OUT, the files the last run wrote.
const exportManifest = ".zk-chef-export.json"

// exportKeys are the frontmatter fields an export keeps on every page; exportSourceKeys are the ones it keeps on sources only.
var (
	exportKeys       = []string{"title", "kind", "status", "superseded_by"}
	exportSourceKeys = []string{"review", "url", "citation", "authors", "published"}
)

// exportHugoKeys maps the exported keys that Hugo reserves to the names a Hugo export writes instead.
var exportHugoKeys = map[string]string{"kind": "zk_kind", "url": "source_url", "published": "source_published"}

// exportFrontmatter keeps only the exportKeys of the YAML mapping fm, and the exportSourceKeys too when source is set, in file order. When hugo is set it renames the keys in exportHugoKeys.
func exportFrontmatter(fm string, source, hugo bool) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
		return "", err
	}
	kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		if k := doc.Content[0].Content[i]; slices.Contains(exportKeys, k.Value) || source && slices.Contains(exportSourceKeys, k.Value) {
			if to, ok := exportHugoKeys[k.Value]; ok && hugo {
				k.Value = to
			}
			kept.Content = append(kept.Content, k, doc.Content[0].Content[i+1])
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(kept); err != nil {
		return "", err
	}
	return buf.String(), enc.Close()
}

// exportTarget is where a page goes in the export and the title its links show.
type exportTarget struct{ path, title string }

// exportHead is the frontmatter an export reads from each page.
type exportHead struct {
	Title        string
	Kind         string
	Status       string
	SupersededBy string `yaml:"superseded_by"`
}

// exportLinkText escapes the characters that end or start Markdown link text.
var exportLinkText = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`)

// exportLink is a Markdown link from page from to target t, showing text.
func exportLink(from string, t exportTarget, text string) (string, error) {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(t.path))
	if err != nil {
		return "", err
	}
	return "[" + exportLinkText.Replace(text) + "](" + filepath.ToSlash(rel) + ")", nil
}

// exportSection returns "\n## name\n\n" and one list entry per source page, sorted by title, for the pages in set; "" when set is empty.
func exportSection(name, from string, set map[string]bool, targets map[string]exportTarget) (string, error) {
	var items []exportTarget
	for id := range set {
		items = append(items, targets[id])
	}
	slices.SortFunc(items, func(a, b exportTarget) int {
		return cmp.Or(strings.Compare(a.title, b.title), strings.Compare(a.path, b.path))
	})
	var sb strings.Builder
	for _, t := range items {
		link, err := exportLink(from, t, t.title)
		if err != nil {
			return "", err
		}
		sb.WriteString("- " + link + "\n")
	}
	if sb.Len() == 0 {
		return "", nil
	}
	return "\n## " + name + "\n\n" + sb.String(), nil
}

// exportSourcePage is a vault page that goes to the export: its text, the frontmatter fields the export reads, and its id (the file stem).
type exportSourcePage struct {
	text string
	head exportHead
	id   string
}

// exportPages maps each page's path under the export to its exported text. Lint has passed, so every page parses. hugo selects the Hugo output: pages go under content/ and the root page is content/_index.md.
func exportPages(files map[string][]byte, hugo bool) (map[string]string, error) {
	src := map[string]exportSourcePage{}
	targets := map[string]exportTarget{} // page id (the file stem) -> target
	for p, b := range files {
		folder, name, _ := strings.Cut(p, "/")
		if p != lint.IndexPath && !(slices.Contains(exportFolders, folder) && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/")) {
			continue
		}
		fm, _, ok := lint.SplitFrontmatter(string(b))
		if !ok {
			return nil, fmt.Errorf("%s: missing frontmatter", p)
		}
		var head exportHead
		if err := yaml.Unmarshal([]byte(fm), &head); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		id := strings.TrimSuffix(path.Base(p), ".md")
		src[p] = exportSourcePage{string(b), head, id}
		targets[id] = exportTarget{p, head.Title}
	}
	// members[id] and linkedBy[id] hold the ids of the pages that name id in their Hubs line or link it elsewhere.
	members, linkedBy := map[string]map[string]bool{}, map[string]map[string]bool{}
	addEdge := func(into map[string]map[string]bool, to, from string) {
		if into[to] == nil {
			into[to] = map[string]bool{}
		}
		into[to][from] = true
	}
	for _, s := range src {
		hubs, others := lint.BodyLinks(s.text)
		for _, id := range hubs {
			addEdge(members, id, s.id)
		}
		for _, id := range others {
			addEdge(linkedBy, id, s.id)
		}
	}
	pages := map[string]string{}
	for p, s := range src {
		text, err := lint.RewriteLinks(s.text, func(id, alias string, aliased bool) (string, error) {
			t, ok := targets[id]
			if !ok {
				return "", fmt.Errorf("%s: link to unknown id %s", p, id)
			}
			if !aliased {
				alias = t.title
			}
			return exportLink(p, t, alias)
		})
		if err != nil {
			return nil, err
		}
		if s.head.Kind == "source" {
			text = lint.StripExtract(text)
		}
		fm, body, _ := lint.SplitFrontmatter(text)
		kept, err := exportFrontmatter(fm, s.head.Kind == "source", hugo)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if hugo && len(s.id) >= 12 {
			// The first 12 digits of an ID are its UTC minute; a stem that is not a valid date has no date.
			if d, err := time.Parse("200601021504", s.id[:12]); err == nil {
				kept += "date: " + d.Format("2006-01-02T15:04:00Z") + "\n"
			}
		}
		if s.head.Status == "superseded" {
			t := targets[s.head.SupersededBy]
			link, err := exportLink(p, t, t.title)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			body = "Superseded by " + link + ".\n\n" + body
		}
		backlinks := map[string]bool{}
		for id := range linkedBy[s.id] {
			if id != s.id && !members[s.id][id] {
				backlinks[id] = true
			}
		}
		membersText, err := exportSection("Members", p, members[s.id], targets)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		backlinksText, err := exportSection("Backlinks", p, backlinks, targets)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if tail := membersText + backlinksText; tail != "" {
			body = strings.TrimRight(body, "\n") + "\n" + tail
		}
		if hugo {
			body = escapeHugoShortcodes(body)
		}
		pages[p] = "---\n" + kept + "---\n" + body
	}
	if hugo {
		pages["_index.md"] = pages[lint.IndexPath]
		delete(pages, lint.IndexPath)
		site := make(map[string]string, len(pages))
		for p, text := range pages {
			site["content/"+p] = text
		}
		pages = site
	}
	return pages, nil
}

// exportClean deletes the files that the manifest in out lists and the directories they leave empty, and returns the entries it deleted.
// An entry that is not a local path or not a regular file is ignored. A missing manifest lists nothing; a corrupt one is an error and deletes nothing.
func exportClean(out string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(out, exportManifest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s in %s is corrupt (%w); fix or delete it", exportManifest, out, err)
	}
	var deleted []string
	for _, e := range m.Files {
		rel := filepath.FromSlash(e)
		if !filepath.IsLocal(rel) || rel == exportManifest {
			continue
		}
		file := filepath.Join(out, rel)
		if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(file); err != nil {
			return deleted, err
		}
		deleted = append(deleted, e)
		for dir := filepath.Dir(file); dir != out; dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil { // fails on a non-empty directory, which stays
				break
			}
		}
	}
	return deleted, nil
}

// exportScaffoldDir is the embedded tree of Hugo site files: hugo.toml.tmpl and layouts/.
const exportScaffoldDir = "export-scaffold/hugo"

// exportRootTitle returns the title of the root page, or "zk-chef vault" when it has none.
func exportRootTitle(files map[string][]byte) string {
	fm, _, _ := lint.SplitFrontmatter(string(files[lint.IndexPath]))
	var head exportHead
	if yaml.Unmarshal([]byte(fm), &head) != nil || strings.TrimSpace(head.Title) == "" {
		return "zk-chef vault"
	}
	return head.Title
}

// tomlString returns s as a TOML string: a literal string when it can be one, else a basic string.
func tomlString(s string) string {
	needsEscape := func(r rune) bool { return r == '\'' || (r != '\t' && (r < ' ' || r == 0x7f)) }
	if !strings.ContainsFunc(s, needsEscape) {
		return "'" + s + "'"
	}
	b, _ := json.Marshal(s) // a JSON string is a valid TOML basic string
	return string(b)
}

// hugoConfigNames are the names, in OUT, that Hugo reads its site config from (a file, or the config/ directory).
var hugoConfigNames = []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config.yaml", "config.yml", "config.json", "config"}

// exportScaffoldHugo creates the Hugo site files in out that do not exist yet and returns their slash paths, relative to out.
// It never changes an existing file, and it skips hugo.toml when any Hugo config exists.
func exportScaffoldHugo(out, title string) ([]string, error) {
	tree, err := fs.Sub(Assets, exportScaffoldDir)
	if err != nil {
		return nil, err
	}
	layouts, err := fs.Glob(tree, "layouts/*.html")
	if err != nil {
		return nil, err
	}
	configured := false
	for _, name := range hugoConfigNames {
		if _, err := os.Lstat(filepath.Join(out, name)); err == nil {
			configured = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	var rels []string
	if !configured {
		rels = append(rels, "hugo.toml")
	}
	created := []string{}
	for _, rel := range append(rels, layouts...) {
		src := rel
		if rel == "hugo.toml" {
			src = "hugo.toml.tmpl"
		}
		b, err := fs.ReadFile(tree, src)
		if err != nil {
			return created, err
		}
		if rel == "hugo.toml" {
			b = []byte(strings.Replace(string(b), "@TITLE@", tomlString(title), 1))
		}
		dst := filepath.Join(out, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return created, err
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // O_EXCL: never overwrite
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return created, err
		}
		_, err = f.Write(b)
		if err := errors.Join(err, f.Close()); err != nil {
			return created, err
		}
		created = append(created, rel)
	}
	return created, nil
}

// resolveExisting returns the absolute path p with the symlinks in its longest existing part resolved. A dangling symlink is followed to its missing target.
func resolveExisting(p string) (string, error) {
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if fi, lerr := os.Lstat(p); errors.Is(err, fs.ErrNotExist) && lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(p), target)
			}
			p = target
			continue
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return "", err
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func exportVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	if len(inv.args) != 1 {
		return fail(inv, "zk-chef export: need one argument, the output directory")
	}
	if inv.generator != "" && inv.generator != "hugo" {
		return fail(inv, "zk-chef export: unknown generator "+inv.generator+"; use hugo or omit --generator")
	}
	hugo := inv.generator == "hugo"
	out, err := filepath.Abs(inv.args[0])
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	realRoot, err := resolveExisting(root)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	// inVault reports whether p is the vault or inside it, after resolving the symlinks in the part of p that exists.
	inVault := func(p string) (bool, error) {
		resolved, err := resolveExisting(p)
		if err != nil {
			return false, err
		}
		rel, err := filepath.Rel(realRoot, resolved)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
	}
	dests := []string{""} // the paths in OUT that an export writes to or through
	if hugo {
		dests = append(dests, "content", "content/_index.md", "hugo.toml", "layouts")
		for _, folder := range exportFolders {
			dests = append(dests, "content/"+folder)
		}
	} else {
		dests = append(dests, "index.md", "_index.md")
		dests = append(dests, exportFolders...)
	}
	for _, dest := range dests {
		dst := filepath.Join(out, filepath.FromSlash(dest))
		inside, err := inVault(dst)
		if err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
		if inside {
			return fail(inv, "zk-chef export: "+dst+" is the vault or inside it ("+realRoot+"); pick a directory outside it")
		}
	}
	files, err := lint.LoadWorktree(root)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	if _, ok := files[lint.ConfigPath]; !ok {
		return fail(inv, root+": no "+lint.ConfigPath+"; run zk-chef init first")
	}
	found, err := lint.Lint(files)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	errCount, warnings := lint.Counts(found)
	if errCount > 0 {
		if inv.json {
			writeJSON(inv.stdout, struct {
				OK       bool           `json:"ok"`
				Errors   int            `json:"errors"`
				Warnings int            `json:"warnings"`
				Findings []lint.Finding `json:"findings"`
			}{false, errCount, warnings, found})
		} else {
			for _, f := range found {
				fmt.Fprintf(inv.stdout, "%s:%d: %s %s\n", f.Path, f.Line, f.Code, f.Msg)
			}
		}
		return 1
	}
	pages, err := exportPages(files, hugo)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	written := make([]string, 0, len(pages))
	for p := range pages {
		written = append(written, p)
	}
	slices.Sort(written)
	deleted, err := exportClean(out)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	removed := 0
	for _, p := range deleted {
		if _, kept := pages[p]; !kept {
			removed++
		}
	}
	for _, p := range written {
		dst := filepath.Join(out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
		if err := os.WriteFile(dst, []byte(pages[p]), 0o644); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
	}
	manifest, err := json.MarshalIndent(struct {
		Files []string `json:"files"`
	}{written}, "", "  ")
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	if err := os.WriteFile(filepath.Join(out, exportManifest), append(manifest, '\n'), 0o644); err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	scaffolded := []string{}
	if hugo {
		if scaffolded, err = exportScaffoldHugo(out, exportRootTitle(files)); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK         bool           `json:"ok"`
			Written    []string       `json:"written"`
			Scaffolded []string       `json:"scaffolded"`
			Counts     map[string]int `json:"counts"`
		}{true, written, scaffolded, map[string]int{"written": len(written), "removed": removed}})
	} else {
		fmt.Fprintf(inv.stdout, "wrote %d pages to %s, removed %d\n", len(written), out, removed)
		if len(scaffolded) > 0 {
			fmt.Fprintf(inv.stdout, "created %s\n", strings.Join(scaffolded, ", "))
		}
	}
	return 0
}
