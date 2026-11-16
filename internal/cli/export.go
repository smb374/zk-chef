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

// exportManifest is the file in OUT that lists, as slash paths relative to OUT, the files the last run wrote.
const exportManifest = ".zk-chef-export.json"

// exportSourceKeys are the fields an export keeps from the frontmatter of a source page, after the keys it unpacks from the tags.
var exportSourceKeys = []string{"url", "accessed", "authors", "published"}

// exportHugoKeys maps the exported keys that Hugo reserves to the names a Hugo export writes instead.
var exportHugoKeys = map[string]string{"kind": "zk_kind", "url": "source_url", "published": "source_published"}

// exportFrontmatter builds the frontmatter of an exported page from the YAML mapping fm and the page model, in this order:
// title and date from the fields; kind, review and status unpacked from the tags; superseded_by; the topic/ and type/ tags as tags
// (aliases and every other tag stay behind); and the exportSourceKeys for a source. When hugo is set it renames the keys in exportHugoKeys.
func exportFrontmatter(fm string, page *lint.Page, hugo bool) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
		return "", err
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		fields[doc.Content[0].Content[i].Value] = doc.Content[0].Content[i+1]
	}
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	put := func(key string, v *yaml.Node) {
		if v == nil {
			return
		}
		if to, ok := exportHugoKeys[key]; ok && hugo {
			key = to
		}
		kept.Content = append(kept.Content, str(key), v)
	}
	source := page.Kind() == "source"
	put("title", fields["title"])
	put("date", fields["date"])
	put("kind", str(page.Kind()))
	if source {
		put("review", str(page.Review()))
	}
	if page.Superseded() {
		put("status", str("superseded"))
	}
	put("superseded_by", fields["superseded_by"])
	tags := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, tag := range page.Tags() {
		if strings.HasPrefix(tag, "topic/") || strings.HasPrefix(tag, "type/") {
			tags.Content = append(tags.Content, str(tag))
		}
	}
	if len(tags.Content) > 0 {
		put("tags", tags)
	}
	if source {
		for _, key := range exportSourceKeys {
			put(key, fields[key])
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

// exportSourcePage is a vault page that goes to the export: its text and its page model.
type exportSourcePage struct {
	text string
	page *lint.Page
}

// exportPages maps each page's path under the export to its exported text. Lint has passed, so every page parses. hugo selects the Hugo output: pages go under content/ and the root page is content/_index.md.
func exportPages(files map[string][]byte, hugo bool) (map[string]string, error) {
	src := map[string]exportSourcePage{}
	targets := map[string]exportTarget{} // page id (the file stem) -> target
	for p, b := range files {
		folder, name, _ := strings.Cut(p, "/")
		if p != lint.IndexPath && !(folder == lint.NotesDir && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/")) {
			continue
		}
		page := lint.ParsePage(p, string(b))
		if err := page.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		src[p] = exportSourcePage{string(b), page}
		targets[page.ID()] = exportTarget{p, page.Title()}
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
		hubs, others := s.page.Links()
		for _, id := range hubs {
			addEdge(members, id, s.page.ID())
		}
		for _, id := range others {
			addEdge(linkedBy, id, s.page.ID())
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
		if s.page.Kind() == "source" {
			text = lint.StripExtract(text)
		}
		fm, body, _ := lint.SplitFrontmatter(text)
		kept, err := exportFrontmatter(fm, s.page, hugo)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		backlinks := map[string]bool{}
		for id := range linkedBy[s.page.ID()] {
			if id != s.page.ID() && !members[s.page.ID()][id] {
				backlinks[id] = true
			}
		}
		membersText, err := exportSection("Members", p, members[s.page.ID()], targets)
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

// exportScaffoldDir is the embedded tree of Hugo site files: hugo.toml.tmpl, which every Style shares, and the layouts/ and static/ of the Style tufte. exportStyleDirs names the tree of each Style.
const exportScaffoldDir = "export-scaffold/hugo"

// exportRootTitle returns the title of the root page, or "zk-chef vault" when it has none.
func exportRootTitle(files map[string][]byte) string {
	page := lint.ParsePage(lint.IndexPath, string(files[lint.IndexPath]))
	if page.Err() != nil || strings.TrimSpace(page.Title()) == "" {
		return "zk-chef vault"
	}
	return page.Title()
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
	if inv.style != "" && !hugo {
		return fail(inv, "zk-chef export: --style needs --generator hugo")
	}
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
	dests := []string{"", exportManifest} // the paths in OUT that an export writes to or through
	if hugo {
		dests = append(dests, "content", "content/_index.md", "hugo.toml", "layouts", "static", siteStateFile)
		dests = append(dests, "content/"+lint.NotesDir)
	} else {
		dests = append(dests, "index.md", "_index.md")
		dests = append(dests, lint.NotesDir)
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
	style := ""
	if hugo {
		if style, err = resolveStyle(out, inv.style); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
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
	scaffold := scaffoldReport{[]string{}, []string{}, []string{}, nil}
	if hugo {
		if v, ok := installedHugo(); ok && hugoBelowFloor(v) {
			fmt.Fprintf(inv.stderr, "zk-chef export: warning: the hugo on PATH is %s; the site needs Hugo %s or newer\n", v, hugoFloor)
		}
		if scaffold, err = exportScaffoldHugo(out, exportRootTitle(files), style); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
		if err := writeStyle(out, style); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK         bool           `json:"ok"`
			Written    []string       `json:"written"`
			Scaffolded []string       `json:"scaffolded"`
			Updated    []string       `json:"updated"`
			Kept       []string       `json:"kept"`
			Counts     map[string]int `json:"counts"`
		}{true, written, scaffold.created, scaffold.updated, scaffold.kept, map[string]int{"written": len(written), "removed": removed}})
	} else {
		fmt.Fprintf(inv.stdout, "wrote %d pages to %s, removed %d\n", len(written), out, removed)
		for _, p := range scaffold.created {
			fmt.Fprintf(inv.stdout, "created %s\n", p)
		}
		for _, p := range scaffold.updated {
			fmt.Fprintf(inv.stdout, "updated %s\n", p)
		}
		for _, p := range scaffold.kept {
			fmt.Fprintf(inv.stdout, "kept your %s\n%s", p, scaffold.diffs[p])
		}
	}
	return 0
}
