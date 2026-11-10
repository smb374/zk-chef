package lint

import (
	"fmt"
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

// doc is one markdown file: frontmatter plus body lines.
type doc struct {
	path  string
	fm    map[string]any
	err   string
	lines []line
}

// parseDoc splits text into frontmatter and body lines.
//
// yaml.v3 follows YAML 1.2, PyYAML follows YAML 1.1. Three differences matter here;
// the cli test TestLintYAMLDifferences covers each:
//   - an unquoted numeric id is a non-string in both, so E_FRONTMATTER is the same;
//   - yes, no, on and off are strings in yaml.v3 but booleans in PyYAML;
//   - a duplicate key is an error in yaml.v3, while PyYAML keeps the last value.
//
// A mapping with non-string keys is accepted, with the keys printed as text.
func parseDoc(path, text string) *doc {
	d := &doc{path: path}
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
		scan := !fenced && !contains(skipSections, section)
		d.lines = append(d.lines, line{start + i, l, scan, section})
	}
	return d
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

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

// scanned returns the lines checks may read.
func (d *doc) scanned() []line {
	var out []line
	for _, l := range d.lines {
		if l.scan {
			out = append(out, l)
		}
	}
	return out
}

// RewriteLinks replaces each [[ID]] or [[ID|alias]] in the lines lint scans, so links in code fences,
// Extract and Review stay as written. It calls repl with the trimmed ID, the alias, and whether an alias is present.
func RewriteLinks(text string, repl func(id, alias string, aliased bool) (string, error)) (string, error) {
	_, body, ok := SplitFrontmatter(text)
	if !ok {
		return "", fmt.Errorf("missing frontmatter")
	}
	lines := strings.Split(body, "\n")
	for i, l := range parseDoc("", text).lines {
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

// BodyLinks returns the trimmed IDs of the links in the lines lint scans. IDs on a first body line
// that starts with "Hubs: " are in hubs; every other ID is in others.
func BodyLinks(text string) (hubs, others []string) {
	d := parseDoc("", text)
	for _, l := range d.scanned() {
		for _, m := range linkRe.FindAllStringSubmatch(l.text, -1) {
			id := strings.TrimSpace(m[1])
			if l.n == d.lines[0].n && strings.HasPrefix(l.text, "Hubs: ") {
				hubs = append(hubs, id)
			} else {
				others = append(others, id)
			}
		}
	}
	return hubs, others
}
