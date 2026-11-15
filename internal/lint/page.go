package lint

import (
	"errors"
	"slices"
	"strings"
)

// ParsePage parses one markdown file. Check Err before you trust the facts the page returns.
func ParsePage(path, text string) *Page { return parseDoc(path, text) }

// Pages maps the ID of each parsable note in files to its page.
func Pages(files map[string][]byte) map[string]*Page {
	out := map[string]*Page{}
	for _, d := range parseDocs(files) {
		if d.err == "" {
			out[d.ID()] = d
		}
	}
	return out
}

// Err reports why the frontmatter did not parse, or nil.
func (d *Page) Err() error {
	if d.err == "" {
		return nil
	}
	return errors.New(d.err)
}

// ID is the file stem.
func (d *Page) ID() string { return d.stem }

// Kind is the value of the page's single kind: tag, or "" when the page has none or several.
func (d *Page) Kind() string {
	if k := d.tagValues("kind"); len(k) == 1 {
		return k[0]
	}
	return ""
}

// Title is the frontmatter title, or "" when it is absent or not a string.
func (d *Page) Title() string { return d.str("title") }

// Review is the value of the page's first review: tag, or "" when it has none.
func (d *Page) Review() string {
	if r := d.tagValues("review"); len(r) > 0 {
		return r[0]
	}
	return ""
}

// Superseded reports whether the page has the tag status:superseded.
func (d *Page) Superseded() bool { return slices.Contains(d.tags, "status:superseded") }

// SupersededBy is the frontmatter superseded_by, or "" when it is absent or not a string.
func (d *Page) SupersededBy() string { return d.str("superseded_by") }

// Tags returns the page's tags, in file order.
func (d *Page) Tags() []string { return slices.Clone(d.tags) }

// Links returns the trimmed IDs of the links in the lines lint scans. IDs on a first body line
// that starts with "Hubs: " are in hubs; every other ID is in others.
func (d *Page) Links() (hubs, others []string) {
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
