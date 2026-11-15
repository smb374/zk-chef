package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var reviews = []string{"unreviewed", "reviewed", "disputed"}

// adder records one finding.
type adder func(code, path string, line int, msg string)

// str returns the string value of a frontmatter key, or "" when it is absent or not a string.
func (d *Page) str(key string) string {
	s, _ := d.fm[key].(string)
	return s
}

// pyRepr prints a frontmatter value the way Python's repr does for the messages the earlier Python build printed.
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

// stemRe is the stem of every page but the root index: 6 lowercase letters and digits.
var stemRe = regexp.MustCompile(`\A[a-z0-9]{6}\z`)

// kindValues are the values of the kind: tag.
var kindValues = []string{"note", "hub", "source", "log"}

// Kebab words, joined by "/" for topic: tags. A type: tag is one kebab word.
var (
	kebabRe = regexp.MustCompile(`\A[a-z0-9]+(-[a-z0-9]+)*\z`)
	topicRe = regexp.MustCompile(`\A[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*\z`)
)

// tagKnown reports whether tag belongs to the namespaces the page's kind allows.
func tagKnown(tag, kind string) bool {
	ns, v, _ := strings.Cut(tag, ":")
	switch ns {
	case "kind":
		return slices.Contains(kindValues, v)
	case "review":
		return kind == "source" && slices.Contains(reviews, v)
	case "status":
		return v == "superseded"
	case "topic":
		return topicRe.MatchString(v)
	case "type":
		return kind == "log" && kebabRe.MatchString(v)
	}
	return false
}

// knownKeys are the frontmatter keys every kind lists; keysByKind adds the keys of one kind.
var (
	knownKeys  = []string{"title", "date", "tags", "aliases"}
	keysByKind = map[string][]string{
		"note":   {"superseded_by"},
		"source": {"superseded_by", "url", "accessed", "authors", "published", "raw", "sha256"},
	}
)

// checkKeys warns about each frontmatter key that the page's kind does not list, and each tag outside the namespaces it allows.
func checkKeys(d *Page, add adder) {
	var unknown []string
	for key := range d.fm {
		if !slices.Contains(knownKeys, key) && !slices.Contains(keysByKind[d.Kind()], key) {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	for _, key := range unknown {
		add("W_UNKNOWN_KEY", d.path, 1, fmt.Sprintf("key %s is not listed for kind %s", key, d.Kind()))
	}
	for _, tag := range d.tags {
		if !tagKnown(tag, d.Kind()) {
			add("W_UNKNOWN_TAG", d.path, 1, "tag "+tag+" is not allowed for kind "+d.Kind())
		}
	}
}

// checkSupersedeLine requires, for a superseded page, the first non-blank body line after the Hubs line to be
// "Superseded by [[superseded_by|Title]]".
func checkSupersedeLine(d *Page, add adder) {
	want := d.SupersededBy()
	for _, l := range d.lines[1:] {
		if strings.TrimSpace(l.text) == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(l.text, "Superseded by "); ok {
			if m := linkRe.FindStringSubmatch(rest); m != nil && strings.HasPrefix(rest, "[[") {
				if got := strings.TrimSpace(m[1]); got != want {
					add("E_FRONTMATTER", d.path, l.n, fmt.Sprintf("Superseded by line names %s, but superseded_by is %s", got, want))
				}
				return
			}
		}
		break
	}
	add("E_FRONTMATTER", d.path, d.lines[0].n, fmt.Sprintf("superseded page needs the line \"Superseded by [[%s|Title]]\" after the Hubs line", want))
}

func checkFrontmatter(d *Page, add adder) {
	if d.path != IndexPath {
		if strings.Contains(strings.TrimPrefix(d.path, NotesDir+"/"), "/") {
			add("E_FRONTMATTER", d.path, 1, "subfolder in "+NotesDir+"/; the folder is flat")
		}
		if !stemRe.MatchString(d.stem) {
			add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("stem %s must be 6 lowercase letters and digits", pyRepr(d.stem)))
		}
	}
	if strings.TrimSpace(d.Title()) == "" {
		add("E_FRONTMATTER", d.path, 1, "title must be a non-empty string (quote it in YAML)")
	}
	if v, has := d.fm["date"]; !has || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
		add("E_FRONTMATTER", d.path, 1, "date is required (UTC, such as 2026-01-02T03:04:05Z)")
	}
	if d.tagsErr != "" {
		add("E_FRONTMATTER", d.path, 1, d.tagsErr)
		return
	}
	kinds := d.tagValues("kind")
	switch {
	case len(kinds) != 1:
		add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("page needs exactly one kind: tag (note, hub, source or log), found %d", len(kinds)))
	case !slices.Contains(kindValues, kinds[0]):
		add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("kind %s is not note, hub, source or log", pyRepr(kinds[0])))
	}
	kind := d.Kind()
	if d.path == IndexPath && kind != "" && kind != "hub" {
		add("E_FRONTMATTER", d.path, 1, "index.md must be kind hub")
	}
	for _, s := range d.tagValues("status") {
		if s != "superseded" {
			add("E_FRONTMATTER", d.path, 1, "status tag must be status:superseded, found status:"+s)
		}
	}
	if d.Superseded() {
		if kind == "hub" || kind == "log" {
			add("E_FRONTMATTER", d.path, 1, kind+" must not be superseded")
		} else if _, ok := d.fm["superseded_by"].(string); !ok {
			add("E_FRONTMATTER", d.path, 1, "status:superseded needs superseded_by: STEM")
		}
	}
	if kind == "source" {
		checkSourceEvidence(d, add)
		if r := d.tagValues("review"); len(r) != 1 || !slices.Contains(reviews, r[0]) {
			add("E_FRONTMATTER", d.path, 1, "source needs exactly one review: tag, one of "+strings.Join(reviews, ", "))
		}
	} else if kind != "" && len(d.tagValues("review")) > 0 {
		add("E_FRONTMATTER", d.path, 1, "only a source has a review: tag")
	}
}

// checkLog warns when a log has no ## Updates section, an entry heading under it that is not a YYYY-MM-DD date,
// or entries that are not newest first.
func checkLog(d *Page, add adder) {
	updates, newest := false, ""
	for _, l := range d.scanned() {
		if l.section != "Updates" {
			continue
		}
		heading, isH3 := strings.CutPrefix(l.text, "### ")
		heading = strings.TrimSpace(heading)
		switch {
		case strings.TrimSpace(l.text) == "## Updates":
			updates = true
		case !isH3:
		case !validDate(strings.TrimSpace(heading), false):
			add("W_LOG_HEADING", d.path, l.n, fmt.Sprintf("entry heading %s is not a YYYY-MM-DD date", pyRepr(heading)))
		case newest != "" && heading > newest:
			add("W_LOG_HEADING", d.path, l.n, fmt.Sprintf("entry %s is newer than the one above it; list newest first", heading))
		default:
			newest = heading
		}
	}
	if !updates {
		add("W_LOG_HEADING", d.path, d.lines[0].n, "log needs a ## Updates section")
	}
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
func checkCitations(d *Page, add adder) {
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
