package lint

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Pages maps the ID of each parsable note in files to its page.
func Pages(files map[string][]byte) map[string]*Page {
	out := map[string]*Page{}
	for _, p := range parsePages(files) {
		if p.err == "" {
			out[p.ID()] = p
		}
	}
	return out
}

// Children returns the paths of the parsable pages whose Hubs line names hub, sorted by path.
// It fails when hub is not the ID of a parsable hub page.
func Children(files map[string][]byte, hub string) ([]string, error) {
	all := parsePages(files)
	pages := map[string]*Page{}
	for _, p := range all {
		if p.err == "" {
			pages[p.ID()] = p
		}
	}
	if pages[hub] == nil {
		return nil, fmt.Errorf("no page with id %s", hub)
	}
	if pages[hub].Kind() != "hub" {
		return nil, fmt.Errorf("%s is not a hub", hub)
	}
	out := []string{}
	for _, p := range all {
		if hubs, _ := p.Links(); p.err == "" && slices.Contains(hubs, hub) {
			out = append(out, p.path)
		}
	}
	return out, nil
}

// Err reports why the frontmatter did not parse, or nil.
func (p *Page) Err() error {
	if p.err == "" {
		return nil
	}
	return errors.New(p.err)
}

// ID is the file stem.
func (p *Page) ID() string { return p.stem }

// Kind is the value of the page's single kind/ tag, or "" when the page has none or several.
func (p *Page) Kind() string {
	if k := p.tagValues("kind"); len(k) == 1 {
		return k[0]
	}
	return ""
}

// Title is the frontmatter title, or "" when it is absent or not a string.
func (p *Page) Title() string { return p.str("title") }

// Review is the value of the page's first review/ tag, or "" when it has none.
func (p *Page) Review() string {
	if r := p.tagValues("review"); len(r) > 0 {
		return r[0]
	}
	return ""
}

// Superseded reports whether the page has the tag status/superseded.
func (p *Page) Superseded() bool { return slices.Contains(p.tags, "status/superseded") }

// SupersededBy is the frontmatter superseded_by, or "" when it is absent or not a string.
func (p *Page) SupersededBy() string { return p.str("superseded_by") }

// Tags returns the page's tags, in file order.
func (p *Page) Tags() []string { return slices.Clone(p.tags) }

// Links returns the trimmed IDs of the links in the lines lint scans. IDs on a first body line
// that starts with "Hubs: " are in hubs; every other ID is in others.
func (p *Page) Links() (hubs, others []string) {
	for _, l := range p.scanned() {
		for _, m := range linkRe.FindAllStringSubmatch(l.text, -1) {
			id := strings.TrimSpace(m[1])
			if l.n == p.lines[0].n && strings.HasPrefix(l.text, "Hubs: ") {
				hubs = append(hubs, id)
			} else {
				others = append(others, id)
			}
		}
	}
	return hubs, others
}
