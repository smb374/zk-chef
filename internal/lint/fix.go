package lint

import (
	"os"
	"path/filepath"
	"strings"
)

// Fix rewrites stale [[ID|alias]] aliases in the worktree to the target's current title.
// It touches only scanned lines where aliasChecked holds, and leaves links without an alias and unknown IDs alone.
// It returns the changed paths, sorted.
func Fix(root string) ([]string, error) {
	files, err := LoadWorktree(root)
	if err != nil {
		return nil, err
	}
	var pages []*Page
	titles := map[string]string{}
	for _, p := range parsePages(files) {
		if p.err != "" {
			continue
		}
		pages = append(pages, p)
		if id, title := p.ID(), p.Title(); id != "" && title != "" {
			titles[id] = title
		}
	}
	current := func(link string) string {
		m := linkRe.FindStringSubmatch(link)
		title, known := titles[strings.TrimSpace(m[1])]
		if !known || !strings.Contains(link, "|") || m[2] == title {
			return link
		}
		return "[[" + m[1] + "|" + title + "]]"
	}
	var changed []string
	for _, p := range pages {
		text := string(files[p.path])
		lines := strings.Split(text, "\n")
		for _, l := range p.scanned() {
			if p.aliasChecked(l) {
				lines[l.n-1] = linkRe.ReplaceAllStringFunc(lines[l.n-1], current)
			}
		}
		if next := canonicalFrontmatter(strings.Join(lines, "\n")); next != text {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p.path)), []byte(next), 0o644); err != nil {
				return changed, err
			}
			changed = append(changed, p.path)
		}
	}
	return changed, nil
}
