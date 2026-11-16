package lint

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// skipSections hold unsigned or signed text from outside; they are not scanned.
var skipSections = []string{"Extract", "Review"}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---(?:\n|\z)`)

// line is one body line. scan is false inside code fences and skipped sections.
type line struct {
	n       int
	text    string
	scan    bool
	section string
}

// Page is one markdown file: frontmatter plus body lines. It is the one model every verb reads
// a page's ID (the file stem), kind, title, review state, supersede state and Hubs-line links through.
// The kind, the review state and the supersede state are tags.
type Page struct {
	path    string
	stem    string
	fm      map[string]any
	tags    []string
	tagsErr string // non-empty when the tags key is missing or is not a list of strings
	err     string
	lines   []line
}

// ParsePage parses one markdown file into frontmatter and body lines. Check Err before you trust the facts the page returns.
//
// yaml.v3 follows YAML 1.2, PyYAML follows YAML 1.1. Three differences matter here;
// the cli test TestLintYAMLDifferences covers each:
//   - an unquoted numeric id is a non-string in both, so E_FRONTMATTER is the same;
//   - yes, no, on and off are strings in yaml.v3 but booleans in PyYAML;
//   - a duplicate key is an error in yaml.v3, while PyYAML keeps the last value.
//
// A mapping with non-string keys is accepted, with the keys printed as text.
func ParsePage(path, text string) *Page {
	d := &Page{path: path, stem: strings.TrimSuffix(filepath.Base(path), ".md")}
	m := frontmatterRe.FindStringSubmatchIndex(text)
	if m == nil {
		d.err = "missing frontmatter"
		return d
	}
	var v any
	if err := yaml.Unmarshal([]byte(text[m[2]:m[3]]), &v); err != nil {
		d.err = "invalid YAML: " + strings.SplitN(err.Error(), "\n", 2)[0]
	} else {
		switch x := v.(type) {
		case map[string]any:
			d.fm = x
		case map[any]any:
			d.fm = make(map[string]any, len(x))
			for k, val := range x {
				d.fm[fmt.Sprint(k)] = val
			}
		default:
			d.err = "frontmatter is not a mapping"
		}
	}
	d.readTags()
	start := strings.Count(text[:m[1]], "\n") + 1
	fenced, section := false, ""
	for i, l := range strings.Split(text[m[1]:], "\n") {
		if strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~") {
			fenced = !fenced
			d.lines = append(d.lines, line{start + i, l, false, section})
			continue
		}
		if !fenced && strings.HasPrefix(l, "## ") {
			section = strings.TrimSpace(l[3:])
		}
		scan := !fenced && !slices.Contains(skipSections, section)
		d.lines = append(d.lines, line{start + i, l, scan, section})
	}
	return d
}

// readTags fills tags from the tags key, or sets tagsErr.
func (p *Page) readTags() {
	raw, ok := p.fm["tags"]
	if !ok {
		p.tagsErr = "tags is required (a list such as [kind/note])"
		return
	}
	list, isList := raw.([]any)
	if !isList {
		p.tagsErr = "tags must be a list of strings"
		return
	}
	for _, v := range list {
		s, isStr := v.(string)
		if !isStr {
			p.tagsErr = "tags must be a list of strings"
			p.tags = nil
			return
		}
		p.tags = append(p.tags, s)
	}
}

// tagValues returns the values of the tags in namespace ns ("kind" gives "note" for kind/note), in file order.
func (p *Page) tagValues(ns string) []string {
	var out []string
	for _, t := range p.tags {
		if v, ok := strings.CutPrefix(t, ns+"/"); ok {
			out = append(out, v)
		}
	}
	return out
}

// SplitFrontmatter returns the YAML between the opening and closing --- lines and the body after them.
// ok is false when text has no frontmatter.
func SplitFrontmatter(text string) (fm, body string, ok bool) {
	m := frontmatterRe.FindStringSubmatchIndex(text)
	if m == nil {
		return "", "", false
	}
	return text[m[2]:m[3]], text[m[1]:], true
}

// StripExtract returns text without its Extract section: the "## Extract" heading and every line up to the next H2 or the end of the file.
// Headings in code fences do not count. text must have frontmatter.
func StripExtract(text string) string {
	m := frontmatterRe.FindStringSubmatchIndex(text)
	if m == nil {
		return text
	}
	var kept []string
	for _, l := range ParsePage("", text).lines {
		if l.section != "Extract" {
			kept = append(kept, l.text)
		}
	}
	return text[:m[1]] + strings.Join(kept, "\n")
}

// scanned returns the lines checks may read.
func (p *Page) scanned() []line {
	var out []line
	for _, l := range p.lines {
		if l.scan {
			out = append(out, l)
		}
	}
	return out
}

// aliasChecked reports whether l, a scanned line, is one where a stale Alias is fixed and warned about.
// In a note the Hubs line (first body line) and the References section qualify; the rest is Prose,
// where the writer's wording stays. Every other kind is checked on all scanned lines.
func (p *Page) aliasChecked(l line) bool {
	return p.Kind() != "note" || l.n == p.lines[0].n || l.section == "References"
}

// RewriteLinks replaces each [[ID]] or [[ID|alias]] in the lines lint scans, so links in code fences,
// Extract and Review stay as written. It calls repl with the trimmed ID, the alias, and whether an alias is present.
func RewriteLinks(text string, repl func(id, alias string, aliased bool) (string, error)) (string, error) {
	_, body, ok := SplitFrontmatter(text)
	if !ok {
		return "", fmt.Errorf("missing frontmatter")
	}
	lines := strings.Split(body, "\n")
	for i, l := range ParsePage("", text).lines {
		if !l.scan {
			continue
		}
		var out strings.Builder
		last := 0
		for _, m := range linkRe.FindAllStringSubmatchIndex(l.text, -1) {
			alias := ""
			if m[4] >= 0 {
				alias = l.text[m[4]:m[5]]
			}
			link, err := repl(strings.TrimSpace(l.text[m[2]:m[3]]), alias, m[4] >= 0)
			if err != nil {
				return "", err
			}
			out.WriteString(l.text[last:m[0]] + link)
			last = m[1]
		}
		out.WriteString(l.text[last:])
		lines[i] = out.String()
	}
	return text[:len(text)-len(body)] + strings.Join(lines, "\n"), nil
}
