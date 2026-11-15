package lint

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// SourceRef names one source note.
type SourceRef struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// DisputedCite is a disputed source that live notes still cite.
type DisputedCite struct {
	ID      string   `json:"id"`
	CitedBy []string `json:"cited_by"`
}

// Status lists what needs a human review decision.
type Status struct {
	New           []SourceRef // unreviewed, no verified signed block (or a block for the current raw bytes)
	ReReview      []SourceRef // unreviewed, latest block verifies but signed other raw bytes
	DisputedCited []DisputedCite
}

// ComputeStatus classifies the sources among files. It fails only when the config is unusable.
//
// A live note is a non-superseded page that is not a source. It cites a source
// when a [[ID]] or [[ID|alias]] link to it sits in a line lint scans (not in Extract, Review or code fences).
func ComputeStatus(files map[string][]byte) (Status, error) {
	cfg, err := parseConfig(files)
	if err != nil {
		return Status{}, err
	}
	v := newVerifier(files, cfg.Pinned)
	defer v.close()
	var st Status
	disputed := map[string]*DisputedCite{}
	for _, src := range Sources(files) {
		ref := SourceRef{src.ID, src.Path}
		switch src.Review {
		case "disputed":
			disputed[src.ID] = &DisputedCite{ID: src.ID, CitedBy: []string{}}
		case "unreviewed":
			if fields, ok := latestVerified(v, src, files); ok && signedOther(src, fields, files) {
				st.ReReview = append(st.ReReview, ref)
			} else {
				st.New = append(st.New, ref)
			}
		}
	}
	for _, d := range parseDocs(files) {
		if d.err != "" || d.Kind() == "source" || d.Superseded() {
			continue
		}
		seen := map[string]bool{}
		for _, l := range d.scanned() {
			for _, m := range linkRe.FindAllStringSubmatch(l.text, -1) {
				if c := disputed[strings.TrimSpace(m[1])]; c != nil && !seen[c.ID] {
					seen[c.ID] = true
					c.CitedBy = append(c.CitedBy, d.path)
				}
			}
		}
	}
	for _, c := range disputed {
		if len(c.CitedBy) > 0 {
			st.DisputedCited = append(st.DisputedCited, *c)
		}
	}
	slices.SortFunc(st.DisputedCited, func(a, b DisputedCite) int { return strings.Compare(a.ID, b.ID) })
	return st, nil
}

// latestVerified returns the fields of the newest signed block of src when that block verifies.
func latestVerified(v *verifier, src Source, files map[string][]byte) (map[string]string, bool) {
	blocks := reviewBlocks(parseDoc(src.Path, string(files[src.Path])))
	if len(blocks) == 0 {
		return nil, false
	}
	text, errMsg := v.verify(blocks[0].text)
	if errMsg != "" {
		return nil, false
	}
	fields := parseMessage(text)
	return fields, fields != nil
}

// signedOther reports whether a verified block covers other evidence than the source holds now:
// other raw bytes for a source with raw and sha256, other url or accessed for a URL-only source.
func signedOther(src Source, fields map[string]string, files map[string][]byte) bool {
	if src.Raw != "" && src.SHA256 != "" {
		sum := sha256.Sum256(files[src.Raw])
		return fields["sha256"] != hex.EncodeToString(sum[:])
	}
	return fields["url"] != src.URL || fields["accessed"] != src.Accessed
}

// Counts returns the number of error findings (codes E_*) and warning findings (all others).
func Counts(found []Finding) (errors, warnings int) {
	for _, f := range found {
		if strings.HasPrefix(f.Code, "E_") {
			errors++
		}
	}
	return errors, len(found) - errors
}
