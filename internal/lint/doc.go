package lint

import (
	"fmt"
	"regexp"
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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
