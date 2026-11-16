package lint

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// EditFrontmatter parses the frontmatter of text into a yaml.v3 node tree, calls edit on its top-level mapping,
// and returns text with the frontmatter encoded again with a 2-space indent. It is the one writer of frontmatter (ADR 0005).
// When the frontmatter is missing or is not a valid YAML mapping, or edit fails, it returns an error and the caller keeps text.
func EditFrontmatter(text string, edit func(m *yaml.Node) error) (string, error) {
	fm, body, ok := SplitFrontmatter(text)
	if !ok {
		return "", errors.New("note has no frontmatter")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fm), &doc); err != nil {
		return "", fmt.Errorf("invalid YAML: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("frontmatter is not a mapping")
	}
	if err := edit(doc.Content[0]); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return "---\n" + buf.String() + "---\n" + body, nil
}

// SetKey sets key in mapping m to a copy of v's value and style. An existing key keeps its position and comments; a new key goes last.
func SetKey(m *yaml.Node, key string, v yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			v.HeadComment, v.LineComment, v.FootComment = old.HeadComment, old.LineComment, old.FootComment
			*old = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &v)
}

// SetReviewTag changes the item review/<state> in the tags list of m to review/verdict. Other items, such as topic/review/reviewed, stay.
func SetReviewTag(m *yaml.Node, verdict string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "tags" {
			continue
		}
		for _, item := range m.Content[i+1].Content {
			if state, ok := strings.CutPrefix(item.Value, "review/"); ok && item.Kind == yaml.ScalarNode && contains(Reviews, state) {
				item.Value = "review/" + verdict
			}
		}
	}
}
