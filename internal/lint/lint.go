// Package lint checks a vault's notes against its invariants and rewrites stale link aliases.
package lint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

// Finding is one lint result. Codes starting E_ are errors, W_ are warnings.
type Finding struct {
	Code string `json:"code"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

var (
	linkRe     = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]*))?\]\]`)
	markerRe   = regexp.MustCompile(`\b(?:TODO|GAP|CONTRADICTION):`)
	linkFullRe = regexp.MustCompile(`\A` + linkRe.String() + `\z`)
)

type config struct {
	IndexID       string   `toml:"index_id"`
	WordCountWarn int      `toml:"word_count_warn"`
	Pinned        []string `toml:"pinned_fingerprints"`
}

func parseConfig(files map[string][]byte) (config, error) {
	cfg := config{WordCountWarn: 400}
	if err := toml.Unmarshal(files[ConfigPath], &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	if cfg.IndexID == "" {
		return cfg, fmt.Errorf("%s: index_id is missing", ConfigPath)
	}
	return cfg, nil
}

// parseDocs parses every markdown file that sits directly in a note folder, sorted by path.
func parseDocs(files map[string][]byte) []*doc {
	var docs []*doc
	for p, b := range files {
		folder, name, ok := strings.Cut(p, "/")
		if _, isNoteFolder := folderKind[folder]; ok && isNoteFolder && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/") {
			docs = append(docs, parseDoc(p, strings.ToValidUTF8(string(b), "\ufffd")))
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].path < docs[j].path })
	return docs
}

// Lint checks files (slash path -> bytes, as LoadWorktree returns) and returns the findings
// sorted by path, line and code. It fails only when the config is unusable.
func Lint(files map[string][]byte) ([]Finding, error) {
	cfg, err := parseConfig(files)
	if err != nil {
		return nil, err
	}
	v := newVerifier(files, cfg.Pinned)
	defer v.close()
	var out []Finding
	add := func(code, path string, line int, msg string) {
		out = append(out, Finding{code, path, line, msg})
	}
	var good []*doc
	for _, d := range parseDocs(files) {
		if d.err != "" {
			add("E_FRONTMATTER", d.path, 1, d.err)
			continue
		}
		n := len(out)
		checkFrontmatter(d, add)
		if len(out) == n {
			good = append(good, d)
		}
	}
	byID := map[string]*doc{}
	for _, d := range good {
		id := d.str("id")
		stem := strings.TrimSuffix(path.Base(d.path), ".md")
		if id != stem {
			add("E_DUPLICATE_ID", d.path, 1, fmt.Sprintf("id %s differs from filename %s", id, stem))
		}
		if first, dup := byID[id]; dup {
			add("E_DUPLICATE_ID", d.path, 1, fmt.Sprintf("id %s also used by %s", id, first.path))
		} else {
			byID[id] = d
		}
	}
	for _, d := range good {
		if v, has := d.fm["superseded_by"]; has {
			if s, isStr := v.(string); !isStr || byID[s] == nil {
				add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("superseded_by %v does not exist", v))
			}
		}
	}

	if byID[cfg.IndexID] == nil {
		add("E_UNREACHABLE", ConfigPath, 1, fmt.Sprintf("index hub %s not found", cfg.IndexID))
	}
	edges := map[string][]string{} // hub id -> ids of the docs that name it in their Hubs line
	for _, d := range good {
		live := d.fm["status"] != "superseded"
		bodyLinks, words := 0, 0
		for _, l := range d.scanned() {
			for _, m := range linkRe.FindAllStringSubmatch(l.text, -1) {
				target := byID[strings.TrimSpace(m[1])]
				if target == nil {
					add("E_BROKEN_LINK", d.path, l.n, "no note with id "+m[1])
					continue
				}
				// A link's ID cannot contain |, so | marks an alias, even an empty one.
				if title := target.str("title"); strings.Contains(m[0], "|") && m[2] != title {
					add("W_STALE_ALIAS", d.path, l.n, fmt.Sprintf("alias %s differs from title %s", pyRepr(m[2]), pyRepr(title)))
				}
				if live && target.fm["status"] == "superseded" {
					add("W_SUPERSEDED_LINK", d.path, l.n, fmt.Sprintf("links %s, superseded by %v", target.str("id"), target.fm["superseded_by"]))
				}
				if l.n != d.lines[0].n {
					bodyLinks++
				}
			}
			if m := markerRe.FindString(l.text); m != "" {
				add("W_MARKER", d.path, l.n, m)
			}
			if l.section != "References" {
				words += len(strings.Fields(l.text))
			}
		}
		if d.str("kind") == "note" {
			if bodyLinks == 0 {
				add("W_DEAD_END", d.path, d.lines[0].n, "links nothing besides its Hubs line")
			}
			checkCitations(d, add)
			if words > cfg.WordCountWarn {
				add("W_WORD_COUNT", d.path, d.lines[0].n, fmt.Sprintf("%d words", words))
			}
		}
		if d.str("id") == cfg.IndexID {
			continue
		}
		first := d.lines[0]
		text := strings.TrimRightFunc(first.text, unicode.IsSpace)
		var hubs [][]string
		ok := strings.HasPrefix(text, "Hubs: ")
		if ok {
			for _, part := range strings.Split(text[len("Hubs: "):], " · ") {
				m := linkFullRe.FindStringSubmatch(part)
				if m == nil {
					ok = false
					break
				}
				hubs = append(hubs, m)
			}
		}
		if !ok {
			add("E_HUBS_LINE", d.path, first.n, "first body line must be: Hubs: [[ID|Hub]] · [[ID|Hub]]")
			continue
		}
		for _, m := range hubs {
			target := byID[strings.TrimSpace(m[1])]
			if target == nil {
				continue
			}
			if target.str("kind") != "hub" || target.str("id") == d.str("id") {
				add("E_HUBS_LINE", d.path, first.n, m[1]+" is not another hub")
			} else {
				edges[target.str("id")] = append(edges[target.str("id")], d.str("id"))
			}
		}
	}
	reach, todo := map[string]bool{cfg.IndexID: true}, []string{cfg.IndexID}
	for len(todo) > 0 {
		hub := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, member := range edges[hub] {
			if !reach[member] {
				reach[member] = true
				todo = append(todo, member)
			}
		}
	}
	for _, d := range good {
		if !reach[d.str("id")] {
			add("E_UNREACHABLE", d.path, 1, "does not reach the Index hub through Hubs lines")
		}
	}

	rawPaths := map[string]bool{}
	for _, d := range good {
		if d.str("kind") != "source" {
			continue
		}
		raw, want := d.str("raw"), d.str("sha256")
		rawPaths[raw] = true
		if !strings.HasPrefix(raw, "raw/"+d.str("id")+"-") {
			add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("raw must be raw/%s-<name>", d.str("id")))
		}
		if b, found := files[raw]; !found {
			add("E_SHA256", d.path, 1, fmt.Sprintf("raw file %s is missing", raw))
		} else if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != want {
			add("E_SHA256", d.path, 1, raw+" does not match sha256 in the note")
		}
		checkReview(d, files, v, add)
	}
	for p := range files {
		if strings.HasPrefix(p, "raw/") && !strings.HasPrefix(path.Base(p), ".") && !rawPaths[p] {
			add("E_RAW_ORPHAN", p, 1, "no source note names this file")
		}
	}
	slices.SortStableFunc(out, func(a, b Finding) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Code, b.Code)
	})
	return out, nil
}
