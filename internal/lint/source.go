package lint

import (
	"errors"
	"path"
	"slices"
	"strings"
)

// Source is a valid source note as review sees it. ID is the file name stem.
type Source struct {
	ID, Title, Path, Raw, SHA256, Review string
}

// ParseSource reads a source note. It fails when the note has no valid frontmatter,
// is not kind source, or lacks a string raw or sha256.
func ParseSource(p, text string) (Source, error) {
	d := parseDoc(p, text)
	if d.err != "" {
		return Source{}, errors.New(d.err)
	}
	if d.str("kind") != "source" {
		return Source{}, errors.New("kind is not source")
	}
	s := Source{
		ID:     strings.TrimSuffix(path.Base(p), ".md"),
		Title:  d.str("title"),
		Path:   p,
		Raw:    d.str("raw"),
		SHA256: d.str("sha256"),
		Review: d.str("review"),
	}
	if s.Raw == "" || s.SHA256 == "" {
		return Source{}, errors.New("source needs raw and sha256 (strings)")
	}
	return s, nil
}

// Sources returns the valid source notes among files, sorted by ID.
func Sources(files map[string][]byte) []Source {
	var out []Source
	for p, b := range files {
		if dir, name, _ := strings.Cut(p, "/"); dir == "sources" && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/") {
			if s, err := ParseSource(p, strings.ToValidUTF8(string(b), "\ufffd")); err == nil {
				out = append(out, s)
			}
		}
	}
	slices.SortFunc(out, func(a, b Source) int { return strings.Compare(a.ID, b.ID) })
	return out
}
