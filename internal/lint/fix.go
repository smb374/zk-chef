package lint

import (
	"os"
	"path/filepath"
	"strings"
)

// Fix rewrites stale [[ID|alias]] aliases in the worktree to the target's current title.
// It touches only scanned lines, and leaves links without an alias and unknown IDs alone.
// It returns the changed paths, sorted.
func Fix(root string) ([]string, error) {
	files, err := LoadWorktree(root)
	if err != nil {
		return nil, err
	}
	var docs []*doc
	titles := map[string]string{}
	for _, d := range parseDocs(files) {
		if d.err != "" {
			continue
		}
		docs = append(docs, d)
		id, idOK := d.fm["id"].(string)
		title, titleOK := d.fm["title"].(string)
		if idOK && titleOK {
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
	for _, d := range docs {
		text := string(files[d.path])
		lines := strings.Split(text, "\n")
		for _, l := range d.scanned() {
			lines[l.n-1] = linkRe.ReplaceAllStringFunc(lines[l.n-1], current)
		}
		if next := strings.Join(lines, "\n"); next != text {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(d.path)), []byte(next), 0o644); err != nil {
				return changed, err
			}
			changed = append(changed, d.path)
		}
	}
	return changed, nil
}
