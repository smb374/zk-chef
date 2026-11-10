package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/smb374/zk-chef/internal/lint"
	"gopkg.in/yaml.v3"
)

// exportFolders are the vault folders whose pages go to the export, one level deep.
var exportFolders = []string{"notes", "hubs", "sources", "projects"}

// exportKeys are the frontmatter fields an export keeps.
var exportKeys = []string{"title", "kind", "status", "superseded_by", "review", "url", "citation", "authors", "published"}

// exportFrontmatter keeps only the exportKeys of the YAML mapping fm, in file order.
func exportFrontmatter(fm string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
		return "", err
	}
	kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		if k := doc.Content[0].Content[i]; slices.Contains(exportKeys, k.Value) {
			kept.Content = append(kept.Content, k, doc.Content[0].Content[i+1])
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(kept); err != nil {
		return "", err
	}
	return buf.String(), enc.Close()
}

// exportPages maps each page's path under the export to its exported text. Lint has passed, so every page parses.
func exportPages(files map[string][]byte) (map[string]string, error) {
	pages := map[string]string{}
	for p, b := range files {
		out := p
		folder, name, _ := strings.Cut(p, "/")
		if p != lint.IndexPath && !(slices.Contains(exportFolders, folder) && strings.HasSuffix(name, ".md") && !strings.Contains(name, "/")) {
			continue
		}
		fm, body, ok := lint.SplitFrontmatter(string(b))
		if !ok {
			return nil, fmt.Errorf("%s: missing frontmatter", p)
		}
		kept, err := exportFrontmatter(fm)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		pages[out] = "---\n" + kept + "---\n" + body
	}
	return pages, nil
}

func exportVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	if len(inv.args) != 1 {
		return fail(inv, "zk-chef export: need one argument, the output directory")
	}
	out, err := filepath.Abs(inv.args[0])
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	// ponytail: compares cleaned absolute paths only; a symlink into the vault gets through. Upgrade: filepath.EvalSymlinks on the nearest existing parent.
	if rel, err := filepath.Rel(root, out); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail(inv, "zk-chef export: "+out+" is the vault or inside it; pick a directory outside "+root)
	}
	files, err := lint.LoadWorktree(root)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	if _, ok := files[lint.ConfigPath]; !ok {
		return fail(inv, root+": no "+lint.ConfigPath+"; run zk-chef init first")
	}
	found, err := lint.Lint(files)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	errors, warnings := 0, 0
	for _, f := range found {
		if strings.HasPrefix(f.Code, "E_") {
			errors++
		} else {
			warnings++
		}
	}
	if errors > 0 {
		if inv.json {
			writeJSON(inv.stdout, struct {
				OK       bool           `json:"ok"`
				Errors   int            `json:"errors"`
				Warnings int            `json:"warnings"`
				Findings []lint.Finding `json:"findings"`
			}{false, errors, warnings, found})
		} else {
			for _, f := range found {
				fmt.Fprintf(inv.stdout, "%s:%d: %s %s\n", f.Path, f.Line, f.Code, f.Msg)
			}
		}
		return 1
	}
	pages, err := exportPages(files)
	if err != nil {
		return fail(inv, "zk-chef export: "+err.Error())
	}
	written := make([]string, 0, len(pages))
	for p := range pages {
		written = append(written, p)
	}
	slices.Sort(written)
	for _, p := range written {
		dst := filepath.Join(out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
		if err := os.WriteFile(dst, []byte(pages[p]), 0o644); err != nil {
			return fail(inv, "zk-chef export: "+err.Error())
		}
	}
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK      bool           `json:"ok"`
			Written []string       `json:"written"`
			Counts  map[string]int `json:"counts"`
		}{true, written, map[string]int{"written": len(written)}})
	} else {
		fmt.Fprintf(inv.stdout, "wrote %d pages to %s\n", len(written), out)
	}
	return 0
}
