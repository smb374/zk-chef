package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/smb374/zk-chef/internal/lint"
)

// writeJSON prints v as one line, without HTML escaping.
func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// fail prints msg to stderr (and a JSON error object with --json) and returns exit code 2.
func fail(inv *invocation, msg string) int {
	fmt.Fprintln(inv.stderr, msg)
	if inv.json {
		writeJSON(inv.stdout, struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}{false, msg})
	}
	return 2
}

func lintVerb(inv *invocation) int {
	root, err := filepath.Abs(inv.root)
	if err != nil {
		return fail(inv, "zk-chef lint: "+err.Error())
	}
	load := lint.LoadWorktree
	if inv.staged {
		load = lint.LoadStaged
	}
	files, err := load(root)
	if err != nil {
		return fail(inv, "zk-chef lint: "+err.Error())
	}
	if _, ok := files[lint.ConfigPath]; !ok {
		return fail(inv, root+": no "+lint.ConfigPath+"; run zk-chef init first")
	}
	found, err := lint.Lint(files)
	if err != nil {
		return fail(inv, "zk-chef lint: "+err.Error())
	}
	errors := 0
	for _, f := range found {
		if strings.HasPrefix(f.Code, "E_") {
			errors++
		}
	}
	warnings := len(found) - errors
	if inv.json {
		if found == nil {
			found = []lint.Finding{}
		}
		writeJSON(inv.stdout, struct {
			OK       bool           `json:"ok"`
			Errors   int            `json:"errors"`
			Warnings int            `json:"warnings"`
			Findings []lint.Finding `json:"findings"`
		}{errors == 0, errors, warnings, found})
	} else {
		for _, f := range found {
			fmt.Fprintf(inv.stdout, "%s:%d: %s %s\n", f.Path, f.Line, f.Code, f.Msg)
		}
		fmt.Fprintf(inv.stdout, "%d errors, %d warnings\n", errors, warnings)
	}
	if errors > 0 {
		return 1
	}
	return 0
}
