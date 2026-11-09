package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	reviews  = []string{"unreviewed", "reviewed", "disputed"}
	logTypes = []string{"decision", "meeting", "plan", "status"}
)

// adder records one finding.
type adder func(code, path string, line int, msg string)

// str returns the string value of a frontmatter key, or "" when it is absent or not a string.
func (d *doc) str(key string) string {
	s, _ := d.fm[key].(string)
	return s
}

// pyRepr prints a frontmatter value the way Python's repr does for the messages v1 printed.
func pyRepr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		if strings.Contains(x, "'") && !strings.Contains(x, `"`) {
			return `"` + x + `"`
		}
		return "'" + strings.ReplaceAll(x, "'", `\'`) + "'"
	}
	return fmt.Sprint(v)
}

func checkFrontmatter(d *doc, add adder) {
	fm := d.fm
	folder, _, _ := strings.Cut(d.path, "/")
	if d.path == IndexPath {
		folder = "hubs"
	}
	for _, key := range []string{"id", "title", "kind"} {
		if s, ok := fm[key].(string); !ok || strings.TrimSpace(s) == "" {
			add("E_FRONTMATTER", d.path, 1, key+" must be a non-empty string (quote it in YAML)")
		}
	}
	kind := d.str("kind")
	switch {
	case !slices.Contains(kindValues(), kind):
		add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("kind %s is not note, hub, source or log", pyRepr(fm["kind"])))
	case kind != folderKind[folder]:
		add("E_WRONG_FOLDER", d.path, 1, fmt.Sprintf("kind %s does not belong in %s/", kind, folder))
	}
	if _, has := fm["status"]; has {
		if kind == "hub" || kind == "log" {
			add("E_FRONTMATTER", d.path, 1, kind+" must not have status")
		} else if _, ok := fm["superseded_by"].(string); fm["status"] != "superseded" || !ok {
			add("E_FRONTMATTER", d.path, 1, "status must be 'superseded' with superseded_by: ID")
		}
	}
	if kind == "source" {
		for _, key := range []string{"raw", "sha256"} {
			if d.str(key) == "" {
				add("E_FRONTMATTER", d.path, 1, "source needs "+key+" (a string)")
			}
		}
		if !slices.Contains(reviews, d.str("review")) {
			add("E_FRONTMATTER", d.path, 1, "review must be one of "+strings.Join(reviews, ", "))
		}
	}
	if _, has := fm["type"]; has && kind == "log" && !slices.Contains(logTypes, d.str("type")) {
		add("E_FRONTMATTER", d.path, 1, "type must be one of "+strings.Join(logTypes, ", "))
	}
}

func kindValues() []string {
	out := make([]string, 0, len(folderKind))
	for _, k := range folderKind {
		out = append(out, k)
	}
	return out
}

// citeRe finds [3] and [3, p. 4]. Go has no lookaround, so citations() checks the neighbours.
var (
	citeRe  = regexp.MustCompile(`\[(\d+)(?:,[^\]\n]*)?\]`)
	entryRe = regexp.MustCompile(`\A(\d+)\. `)
)

// citations returns the numbers of the [N] citations in s. A match preceded by [ or
// followed by ] or ( is a wiki link or a markdown link, not a citation.
func citations(s string) []int {
	var out []int
	for pos := 0; pos < len(s); {
		m := citeRe.FindStringSubmatchIndex(s[pos:])
		if m == nil {
			break
		}
		start, end := pos+m[0], pos+m[1]
		if (start > 0 && s[start-1] == '[') || (end < len(s) && (s[end] == ']' || s[end] == '(')) {
			pos = start + 1
			continue
		}
		k, _ := strconv.Atoi(s[pos+m[2] : pos+m[3]])
		out = append(out, k)
		pos = end
	}
	return out
}

// checkCitations covers claim notes only: hubs make no claims, sources paraphrase, logs are exempt.
func checkCitations(d *doc, add adder) {
	type cite struct{ n, k int }
	var (
		refLines       []int
		lastH2         string
		cites, entries []cite
	)
	for _, l := range d.scanned() {
		if strings.HasPrefix(l.text, "## ") {
			lastH2 = strings.TrimSpace(l.text[3:])
			if lastH2 == "References" {
				refLines = append(refLines, l.n)
			}
		}
		if l.section != "References" {
			for _, k := range citations(l.text) {
				cites = append(cites, cite{l.n, k})
			}
		} else if m := entryRe.FindStringSubmatch(l.text); m != nil {
			k, _ := strconv.Atoi(m[1])
			entries = append(entries, cite{l.n, k})
		}
	}
	if len(refLines) > 0 && (len(refLines) > 1 || lastH2 != "References") {
		add("E_CITATION", d.path, refLines[0], "References must be one heading and the last H2")
	}
	if len(cites) > 0 && len(refLines) == 0 {
		add("E_CITATION", d.path, cites[0].n, fmt.Sprintf("citation [%d] but no ## References", cites[0].k))
	}
	listed, cited := map[int]bool{}, map[int]bool{}
	gap := false
	for i, e := range entries {
		listed[e.k] = true
		gap = gap || e.k != i+1
	}
	if gap {
		add("E_CITATION", d.path, entries[0].n, "References numbering must run 1, 2, 3... without gaps")
	}
	var first []int
	for _, c := range cites {
		if !cited[c.k] {
			cited[c.k] = true
			first = append(first, c.k)
		}
	}
	for i, k := range first {
		if k != i+1 {
			add("E_CITATION", d.path, cites[0].n, "citations must first appear in order 1, 2, 3...")
			break
		}
	}
	for _, c := range cites {
		if !listed[c.k] {
			add("E_CITATION", d.path, c.n, fmt.Sprintf("[%d] has no References entry", c.k))
		}
	}
	for _, e := range entries {
		if !cited[e.k] {
			add("E_CITATION", d.path, e.n, fmt.Sprintf("References entry %d is never cited", e.k))
		}
	}
}
