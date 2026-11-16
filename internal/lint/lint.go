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
	linkRe   = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]*))?\]\]`)
	markerRe = regexp.MustCompile(`\b(?:TODO|CONTRADICTION):`)
	hubsRe   = regexp.MustCompile(`\AHubs: ` + linkRe.String() + `(?:, ` + linkRe.String() + `)*\z`)
)

type config struct {
	WordCountWarn int      `toml:"word_count_warn"`
	Pinned        []string `toml:"pinned_fingerprints"`
}

func parseConfig(files map[string][]byte) (config, error) {
	cfg := config{WordCountWarn: 400}
	if err := toml.Unmarshal(files[ConfigPath], &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	return cfg, nil
}

// parsePages parses index.md and every markdown file under notes/, subfolders included (lint reports them), sorted by path.
func parsePages(files map[string][]byte) []*Page {
	var pages []*Page
	for p, b := range files {
		if p == IndexPath || strings.HasPrefix(p, NotesDir+"/") && strings.HasSuffix(p, ".md") {
			pages = append(pages, ParsePage(p, strings.ToValidUTF8(string(b), "\ufffd")))
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].path < pages[j].path })
	return pages
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
	var good, parsed []*Page // parsed: no parse error; good: parsed with valid frontmatter
	pages := parsePages(files)
	firstOf := map[string]*Page{}
	for _, d := range pages {
		if first, dup := firstOf[d.stem]; dup {
			add("E_DUPLICATE_ID", d.path, 1, fmt.Sprintf("ID %s also used by %s", d.stem, first.path))
		} else {
			firstOf[d.stem] = d
		}
	}
	for _, d := range pages {
		if d.err != "" {
			add("E_FRONTMATTER", d.path, 1, d.err)
			continue
		}
		parsed = append(parsed, d)
		n := len(out)
		checkFrontmatter(d, add)
		if len(out) == n {
			good = append(good, d)
		}
		if text := string(files[d.path]); canonicalFrontmatter(text) != text {
			add("W_FRONTMATTER_FORMAT", d.path, 1, "frontmatter is not in canonical form; run zk-chef lint --fix")
		}
	}
	byID := map[string]*Page{}
	for _, d := range parsed {
		if byID[d.ID()] == nil {
			byID[d.ID()] = d
		}
	}
	for _, d := range parsed {
		checkKeys(d, add)
	}
	for _, d := range good {
		if v, has := d.fm["superseded_by"]; has {
			if s, isStr := v.(string); !isStr || byID[s] == nil {
				add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("superseded_by %v does not exist", v))
			}
		}
		if d.SupersededBy() != "" {
			if !d.Superseded() {
				add("E_FRONTMATTER", d.path, 1, "superseded_by needs status/superseded")
			} else {
				checkSupersedeLine(d, add)
			}
		}
	}

	if byID[indexStem] == nil {
		add("E_UNREACHABLE", IndexPath, 1, "index.md not found")
	}
	edges := map[string][]string{}             // hub id -> ids of the pages that name it in their Hubs line
	linked := map[string][]string{}            // page id -> ids its body links, past the Hubs line
	type hubLink struct{ child, parent *Page } // a hub and one parent hub on its Hubs line
	var hubLinks []hubLink
	for _, d := range parsed {
		live := !d.Superseded()
		words := 0
		for _, l := range d.scanned() {
			for _, m := range linkRe.FindAllStringSubmatch(l.text, -1) {
				target := byID[strings.TrimSpace(m[1])]
				if target == nil {
					add("E_BROKEN_LINK", d.path, l.n, "no note with id "+m[1])
					continue
				}
				// A link's ID cannot contain |, so | marks an alias, even an empty one.
				if title := target.Title(); d.aliasChecked(l) && strings.Contains(m[0], "|") && m[2] != title {
					add("W_STALE_ALIAS", d.path, l.n, fmt.Sprintf("alias %s differs from title %s", pyRepr(m[2]), pyRepr(title)))
				}
				if live && target.Superseded() {
					add("W_SUPERSEDED_LINK", d.path, l.n, fmt.Sprintf("links %s, superseded by %v", target.ID(), target.SupersededBy()))
				}
				if d.ID() == indexStem || l.n != d.lines[0].n { // index has no Hubs line
					linked[d.ID()] = append(linked[d.ID()], target.ID())
				}
			}
			if m := markerRe.FindString(l.text); m != "" {
				add("W_MARKER", d.path, l.n, m)
			}
			if l.section != "References" {
				words += len(strings.Fields(l.text))
			}
		}
		valid := slices.Contains(good, d) // kind checks need valid frontmatter
		if valid && d.Kind() == "note" {
			checkCitations(d, add)
			if words > cfg.WordCountWarn {
				add("W_WORD_COUNT", d.path, d.lines[0].n, fmt.Sprintf("%d words", words))
			}
		} else if valid && d.Kind() == "log" {
			checkLog(d, add)
		}
		if d.ID() == indexStem {
			continue
		}
		first := d.lines[0]
		text := strings.TrimRightFunc(first.text, unicode.IsSpace)
		hubs := linkRe.FindAllStringSubmatch(text, -1)
		if !hubsRe.MatchString(text) {
			add("E_HUBS_LINE", d.path, first.n, "first body line must be: Hubs: [[ID|Hub]], [[ID|Hub]]")
			continue
		}
		for _, m := range hubs {
			target := byID[strings.TrimSpace(m[1])]
			if target == nil {
				continue
			}
			if target.Kind() != "hub" || target.ID() == d.ID() {
				add("E_HUBS_LINE", d.path, first.n, m[1]+" is not another hub")
			} else {
				edges[target.ID()] = append(edges[target.ID()], d.ID())
				if d.Kind() == "hub" {
					hubLinks = append(hubLinks, hubLink{d, target})
				}
			}
		}
	}
	for _, h := range hubLinks {
		if child, parent := h.child, h.parent; !slices.Contains(linked[parent.ID()], child.ID()) {
			add("W_HUB_UNLISTED", child.path, child.lines[0].n, fmt.Sprintf("hub %s does not link %s in its body", parent.ID(), child.ID()))
		}
	}
	reach, todo := map[string]bool{indexStem: true}, []string{indexStem}
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
		if !reach[d.ID()] {
			add("E_UNREACHABLE", d.path, 1, "does not reach the Index hub through Hubs lines")
		}
	}

	rawPaths := map[string]bool{}
	for _, d := range parsed {
		if d.Kind() == "source" && d.source().HasRaw() {
			rawPaths[d.str("raw")] = true
		}
	}
	for _, d := range good {
		if d.Kind() != "source" {
			continue
		}
		raw, want, hasRaw := d.str("raw"), d.str("sha256"), d.source().HasRaw()
		if !slices.ContainsFunc(d.lines, func(l line) bool { return l.section == "Review" }) {
			add("E_REVIEW_HEADING", d.path, 1, "source needs a ## Review heading")
		}
		if hasRaw {
			if !rawNameOK(raw, d.ID()) {
				add("E_FRONTMATTER", d.path, 1, fmt.Sprintf("raw must be raw/%s or raw/%s.<ext>", d.ID(), d.ID()))
			}
			if b, found := files[raw]; !found {
				add("E_SHA256", d.path, 1, fmt.Sprintf("raw file %s is missing", raw))
			} else if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != want {
				add("E_SHA256", d.path, 1, raw+" does not match sha256 in the note")
			}
		}
		checkReview(d, files, v, add)
	}
	for p := range files {
		if strings.HasPrefix(p, RawDir+"/") && !strings.HasPrefix(path.Base(p), ".") && !rawPaths[p] {
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
