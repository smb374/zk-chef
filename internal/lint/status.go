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
// A live note is a non-superseded note in notes/, hubs/ or projects/. It cites a source
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
			if blockSHA, ok := latestVerifiedSHA(v, src, files); !ok {
				st.New = append(st.New, ref)
			} else if sum := sha256.Sum256(files[src.Raw]); blockSHA != hex.EncodeToString(sum[:]) {
				st.ReReview = append(st.ReReview, ref)
			} else {
				st.New = append(st.New, ref)
			}
		}
	}
	for _, d := range parseDocs(files) {
		if folder, _, _ := strings.Cut(d.path, "/"); d.err != "" || folder == "sources" || d.fm["status"] == "superseded" {
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

// latestVerifiedSHA returns the sha256 in the newest signed block of src when that block verifies.
func latestVerifiedSHA(v *verifier, src Source, files map[string][]byte) (string, bool) {
	blocks := reviewBlocks(parseDoc(src.Path, string(files[src.Path])))
	if len(blocks) == 0 {
		return "", false
	}
	text, errMsg := v.verify(blocks[0].text)
	if errMsg != "" {
		return "", false
	}
	fields := parseMessage(text)
	if fields == nil {
		return "", false
	}
	return fields["sha256"], true
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
