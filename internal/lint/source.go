package lint

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// hasControl reports whether s has a control character, such as a newline.
func hasControl(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }

// HasRaw reports whether the source has a raw file with its hash.
func (s Source) HasRaw() bool { return s.Raw != "" && s.SHA256 != "" }

// Evidence returns the key and value pairs a review signs, and checkReview compares: path and sha256 when the source has a raw file
// (even if it has a url too), else url and accessed.
func (s Source) Evidence() [][2]string {
	if s.HasRaw() {
		return [][2]string{{"path", s.Raw}, {"sha256", s.SHA256}}
	}
	return [][2]string{{"url", s.URL}, {"accessed", s.Accessed}}
}

// hasURL reports whether the source has a URL with its access date.
func (s Source) hasURL() bool { return s.URL != "" && s.Accessed != "" }

var (
	yearRe = regexp.MustCompile(`\A\d{4}\z`)
	extRe  = regexp.MustCompile(`\A\.[A-Za-z0-9]+\z`)
)

// rawNameOK reports whether raw is raw/<stem> with an optional .<ext>.
func rawNameOK(raw, stem string) bool {
	rest, ok := strings.CutPrefix(raw, RawDir+"/"+stem)
	return ok && (rest == "" || extRe.MatchString(rest))
}

// validDate reports whether a frontmatter value is a YYYY-MM-DD date, or a YYYY year when year is set.
// YAML reads an unquoted date as a time, and an unquoted year as an int.
func validDate(v any, year bool) bool {
	switch x := v.(type) {
	case time.Time:
		return x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0
	case int:
		return year && x >= 1000 && x <= 9999
	case string:
		if year && yearRe.MatchString(x) {
			return true
		}
		_, err := time.Parse("2006-01-02", x)
		return err == nil
	}
	return false
}

// checkSourceEvidence applies the source evidence rule: url with accessed, or raw with sha256, or both;
// published is a year or a date when present.
func checkSourceEvidence(d *Page, add adder) {
	fail := func(msg string) { add("E_FRONTMATTER", d.path, 1, msg) }
	has := func(key string) bool { v := d.fm[key]; return v != nil && v != "" }
	for _, key := range []string{"url", "raw", "sha256"} {
		if has(key) && d.str(key) == "" {
			fail(key + " must be a string")
		}
	}
	url, accessed, raw, sha := has("url"), has("accessed"), has("raw"), has("sha256")
	if !url && !accessed && !raw && !sha {
		fail("source needs url with accessed, or raw with sha256")
	}
	if url && !accessed {
		fail("url needs accessed (YYYY-MM-DD)")
	}
	if accessed && !url {
		fail("accessed needs url")
	}
	for _, kv := range [][2]string{{"url", d.str("url")}, {"accessed", d.dateStr("accessed")}} {
		if hasControl(kv[1]) {
			fail(kv[0] + " must not contain control characters")
		}
	}
	if raw && !sha {
		fail("raw needs sha256")
	}
	if sha && !raw {
		fail("sha256 needs raw")
	}
	if accessed && !validDate(d.fm["accessed"], false) {
		fail("accessed must be YYYY-MM-DD")
	}
	if has("published") && !validDate(d.fm["published"], true) {
		fail("published must be YYYY or YYYY-MM-DD")
	}
}

// Source is a valid source note as review sees it. ID is the file name stem.
// A source has raw with SHA256, or URL with Accessed (YYYY-MM-DD), or both.
type Source struct {
	ID, Title, Path, Raw, SHA256, URL, Accessed, Review string
}

// dateStr returns a date frontmatter value as YYYY-MM-DD. YAML reads an unquoted date as a time.
func (p *Page) dateStr(key string) string {
	if t, ok := p.fm[key].(time.Time); ok {
		return t.Format("2006-01-02")
	}
	return p.str(key)
}

// source reads the source fields of the page, without validating them.
func (p *Page) source() Source {
	return Source{
		ID:       p.ID(),
		Title:    p.Title(),
		Path:     p.path,
		Raw:      p.str("raw"),
		SHA256:   p.str("sha256"),
		URL:      p.str("url"),
		Accessed: p.dateStr("accessed"),
		Review:   p.Review(),
	}
}

// ParseSource reads a source note. It fails when the note has no valid frontmatter,
// is not kind source, or has neither raw with sha256 nor url with accessed.
func ParseSource(p, text string) (Source, error) {
	d := ParsePage(p, text)
	if d.err != "" {
		return Source{}, errors.New(d.err)
	}
	if d.Kind() != "source" {
		return Source{}, errors.New("kind is not source")
	}
	s := d.source()
	hasRaw, hasURL := s.HasRaw(), s.hasURL()
	if hasControl(s.URL) || hasControl(s.Accessed) {
		return Source{}, errors.New("url and accessed must not contain control characters")
	}
	if !hasRaw && !hasURL {
		return Source{}, errors.New("source needs raw with sha256, or url with accessed (strings)")
	}
	return s, nil
}

// Sources returns the valid source notes among files, sorted by ID.
func Sources(files map[string][]byte) []Source {
	var out []Source
	for p, b := range files {
		if dir, name, _ := strings.Cut(p, "/"); dir == NotesDir && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/") {
			if s, err := ParseSource(p, strings.ToValidUTF8(string(b), "\ufffd")); err == nil {
				out = append(out, s)
			}
		}
	}
	slices.SortFunc(out, func(a, b Source) int { return strings.Compare(a.ID, b.ID) })
	return out
}
