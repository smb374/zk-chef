package cli

import (
	"fmt"
	"path/filepath"

	"github.com/smb374/zk-chef/internal/lint"
)

func fixVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef fix: "+err.Error())
	}
	fixed, err := lint.Fix(root)
	if err != nil {
		return fail(inv, "zk-chef fix: "+err.Error())
	}
	if inv.json {
		if fixed == nil {
			fixed = []string{}
		}
		writeJSON(inv.stdout, struct {
			OK    bool     `json:"ok"`
			Fixed []string `json:"fixed"`
		}{true, fixed})
		return 0
	}
	for _, p := range fixed {
		fmt.Fprintf(inv.stdout, "fixed %s\n", p)
	}
	return 0
}
