package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/smb374/zk-chef/internal/lint"
)

// lintCounts is the lint summary in the status JSON.
type lintCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

func statusVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef status: "+err.Error())
	}
	files, err := lint.LoadWorktree(root)
	if err != nil {
		return fail(inv, "zk-chef status: "+err.Error())
	}
	if _, ok := files[lint.ConfigPath]; !ok {
		return fail(inv, root+": no "+lint.ConfigPath+"; run zk-chef init first")
	}
	st, err := lint.ComputeStatus(files)
	if err != nil {
		return fail(inv, "zk-chef status: "+err.Error())
	}
	if st.New == nil {
		st.New = []lint.SourceRef{}
	}
	if st.ReReview == nil {
		st.ReReview = []lint.SourceRef{}
	}
	if st.DisputedCited == nil {
		st.DisputedCited = []lint.DisputedCite{}
	}
	found, err := lint.Lint(files)
	if err != nil {
		return fail(inv, "zk-chef status: "+err.Error())
	}
	errors, warnings := lint.Counts(found)
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK            bool                `json:"ok"`
			New           []lint.SourceRef    `json:"new"`
			ReReview      []lint.SourceRef    `json:"re_review"`
			DisputedCited []lint.DisputedCite `json:"disputed_cited"`
			Lint          lintCounts          `json:"lint"`
		}{OK: true, New: st.New, ReReview: st.ReReview, DisputedCited: st.DisputedCited, Lint: lintCounts{errors, warnings}})
		return 0
	}
	for _, sec := range []struct {
		title string
		refs  []lint.SourceRef
	}{{"new", st.New}, {"re-review", st.ReReview}} {
		fmt.Fprintf(inv.stdout, "%s (%d):\n", sec.title, len(sec.refs))
		for _, r := range sec.refs {
			fmt.Fprintf(inv.stdout, "  %s  %s\n", r.ID, r.Path)
		}
	}
	fmt.Fprintf(inv.stdout, "disputed, still cited (%d):\n", len(st.DisputedCited))
	for _, c := range st.DisputedCited {
		fmt.Fprintf(inv.stdout, "  %s  cited by %s\n", c.ID, strings.Join(c.CitedBy, ", "))
	}
	fmt.Fprintf(inv.stdout, "lint: %d errors, %d warnings\n", errors, warnings)
	return 0
}
